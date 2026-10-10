package httpapi

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGroupScanPreservesCapturedPaidTarget(t *testing.T) {
	for _, mode := range []string{"retag", "delete", "worker_and_scan"} {
		t.Run(mode, func(t *testing.T) {
			s, e, p, account, plan := renewalFixture(t)
			ctx := context.Background()
			in := purchaseInput(plan)
			in.Action = "renew"
			order, err := s.createPurchaseOrder(ctx, account, uuid.New(), in)
			if err != nil {
				t.Fatal(err)
			}
			fields := purchaseNotice(s, order.OrderId, uuid.NewString(), "90071992547409.00", "90071992547409.93")
			fields.Set("datetime", s.now().Add(time.Minute).UTC().Format(time.RFC3339))
			fields.Set("sign", yooMoneySignature(fields, s.cfg.Payments.YooMoneyNotificationSecret))
			if s.receiveYooMoney(ctx, fields) != nil || s.fulfillPurchase(ctx, order.OrderId) != nil {
				t.Fatal("owned paid renewal did not capture its access target")
			}
			prepared, err := s.purchaseOrder(ctx, account, order.OrderId)
			if err != nil || prepared.AccessOperationId == nil {
				t.Fatal("frozen paid operation absent", err)
			}
			snapshot := func() [32]byte {
				t.Helper()
				var raw string
				if e.Pool.QueryRow(ctx, `SELECT jsonb_build_array(p.quote,p.funding_operation_id,p.payment_status,o.target,
				 a.assigned_panel_id,a.vpn_id,a.sub_id,a.panel_key,
				 (SELECT jsonb_agg(to_jsonb(r) ORDER BY operation_id) FROM purchase_receipts r WHERE r.order_id=p.id))::text
				 FROM purchase_orders p JOIN access_operations o ON o.id=p.access_operation_id
				 JOIN accounts a ON a.id=p.account_id WHERE p.id=$1`, order.OrderId).Scan(&raw) != nil {
					t.Fatal("paid target/money/identity snapshot unavailable")
				}
				return sha256.Sum256([]byte(raw))
			}
			before := snapshot()
			operations, jobs := count(t, e, "access_operations"), count(t, e, "river_job")
			writes := p.adds + p.updates + p.attaches + p.detaches + p.resets + p.disables
			if mode != "worker_and_scan" {
				p.mu.Lock()
				p.inboundRows = []map[string]any{{"id": 2, "enable": true, "tag": "regular-second"}, {"id": 3, "enable": true, "tag": "euru"}, {"id": 9, "enable": true, "tag": "unlimited"}, {"id": 99, "enable": true, "tag": "foreign"}}
				if mode == "retag" {
					p.inboundRows = append(p.inboundRows, map[string]any{"id": 1, "enable": true, "tag": "manual-only"})
				}
				p.mu.Unlock()
			}
			if id, err := s.vpn.PrepareGroupReconciliation(ctx, account); err != nil || id != uuid.Nil || count(t, e, "access_operations") != operations || count(t, e, "river_job") != jobs {
				t.Fatal("group scan changed a pending paid target or queued another executor", err)
			}
			if mode == "worker_and_scan" {
				blocked, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				p.beforeRead = func() { once.Do(func() { close(blocked); <-release }) }
				done := make(chan error, 1)
				go func() { done <- s.applyAccess(ctx, *prepared.AccessOperationId) }()
				select {
				case <-blocked:
				case <-time.After(5 * time.Second):
					close(release)
					t.Fatal("paid worker did not own the account")
				}
				id, scanErr := s.vpn.PrepareGroupReconciliation(ctx, account)
				close(release)
				if err = <-done; err != nil || scanErr != nil || id != uuid.Nil {
					t.Fatal("simultaneous scan competed with the paid worker", err, scanErr)
				}
			} else if err = s.applyAccess(ctx, *prepared.AccessOperationId); err != nil {
				t.Fatal(err)
			}
			final, err := s.purchaseOrder(ctx, account, order.OrderId)
			want := "needs_review"
			if mode == "worker_and_scan" {
				want = "applied"
			}
			p.mu.Lock()
			afterWrites := p.adds + p.updates + p.attaches + p.detaches + p.resets + p.disables
			p.mu.Unlock()
			if err != nil || string(final.FulfillmentStatus) != want || snapshot() != before || count(t, e, "access_operations") != operations {
				t.Fatal("paid capture was repriced, rewritten, duplicated or applied to changed inbounds", err)
			}
			if mode != "worker_and_scan" && afterWrites != writes {
				t.Fatal("retagged/deleted frozen inbound allowed a paid panel write")
			}
			if id, err := s.vpn.PrepareGroupReconciliation(ctx, account); err != nil || id != uuid.Nil || snapshot() != before {
				t.Fatal("scan retired or replaced a paid operation needing review", err)
			}
		})
	}
}
