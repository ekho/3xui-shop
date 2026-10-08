package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Re-entering the pool to project refund status would deadlock a caller that
// already holds its only connection for a transaction or an inbox row stream.
func TestPaymentResolutionSingleConnection(t *testing.T) {
	s, e, account, plan, actor := manualFixture(t)
	order := manualOrder(t, s, account, plan)
	cfg := e.Pool.Config().Copy()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	single := newRegressionFixture(pool, e.Redis, s.queue, *s.cfg)
	single.now = e.Clock
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reported, err := single.payments.ReportManualPayment(ctx, account, order.OrderId, uuid.New())
	if err != nil || reported.ManualPayment == nil || reported.ManualPayment.State != "pending" || reported.FullyRefunded {
		t.Fatal("single-connection report/refund projection blocked", err)
	}
	page, err := single.payments.ManualPaymentRequests(ctx, actor, nil)
	if err != nil || len(page.Items) != 1 || page.Items[0].Order.OrderId != order.OrderId || page.Items[0].Order.FullyRefunded {
		t.Fatal("single-connection inbox/refund projection blocked", err)
	}
}

// Removing the selected-case/refund guard would expose another owner's money or
// permit a second payout/issuance after an operator-confirmed full refund.
func TestPaymentResolutionHTTP(t *testing.T) {
	h, e, cfg := httpFixture(t)
	client := supportLogin(t, h, e, cfg, "resolution@example.test")
	operator := supportLogin(t, h, e, cfg, "resolution-operator@example.test")
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, operator.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	id, newer := uuid.New(), uuid.New()
	quote := `{"amount_minor":"9007199254740993","currency":"RUB","devices":2,"period_days":30,"plan_id":"00000000-0000-4000-8000-000000000123","profile":"regular","revision":1,"traffic_gb":15}`
	for i, order := range []uuid.UUID{id, newer} {
		created := e.Clock().Add(time.Duration(i) * time.Minute)
		if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,payment_status,fulfillment_status,active,created_at,expires_at,paid_at) VALUES($1,$2,$3,$4,$5,9007199254740993,'AC','paid','queued',false,$6,$7,$6)`, order, client.id, uuid.New(), []byte{1, 2, 3}, quote, created, created.Add(30*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,created_at) VALUES('resolution-original',$1,$2,9007199254740993,9007199254740900,'643','p2p-incoming',false,false,$2);`, id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE purchase_orders SET funding_operation_id='resolution-original' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/operator/clients/" + client.id.String() + "/orders/" + id.String()
	read := func(session *supportSession, path string, body any, status int) wire.PaymentCase {
		t.Helper()
		r := supportRequest(h, session, "POST", path, "application/json", mustJSON(t, body), cfg.HTTP.CabinetOrigin, uuid.Nil)
		if r.Code != status {
			t.Fatalf("case: got %d, want %d", r.Code, status)
		}
		var out wire.PaymentCase
		if status == 200 {
			if json.Unmarshal(r.Body.Bytes(), &out) != nil || r.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("invalid/private case response")
			}
		}
		return out
	}
	before := read(&operator, base+"/payment-case", map[string]any{}, 200)
	if before.Order.OrderId != id || before.Receipt == nil || before.Receipt.GrossMinor != "9007199254740993" || before.Refund != nil || !before.CanConfirmRefund || !before.FinancialReviewOpen {
		t.Fatal("selected old money/availability lost")
	}
	read(nil, base+"/payment-case", map[string]any{}, 401)
	read(&client, base+"/payment-case", map[string]any{}, 403)
	read(&operator, strings.Replace(base, client.id.String(), operator.id.String(), 1)+"/payment-case", map[string]any{}, 404)
	read(&operator, base+"/payment-case", map[string]any{"receipt_operation_id": "another-receipt"}, 404)
	read(&operator, base+"/payment-case", map[string]any{"account_id": operator.id.String()}, 400)
	input := map[string]any{"receipt_operation_id": "resolution-original", "reference": "external-return-1", "reason": "Operator verified full return", "confirm_full": true, "keep_access": true}
	key := uuid.New()
	write := func(session *supportSession, body any, k uuid.UUID, status int) wire.PaymentRefund {
		t.Helper()
		r := supportRequest(h, session, "POST", base+"/refunds", "application/json", mustJSON(t, body), cfg.HTTP.CabinetOrigin, k)
		if r.Code != status {
			t.Fatalf("refund: got %d, want %d", r.Code, status)
		}
		var out wire.PaymentRefund
		if status == 201 && json.Unmarshal(r.Body.Bytes(), &out) != nil {
			t.Fatal("invalid refund response")
		}
		return out
	}
	write(&client, input, uuid.New(), 403)
	noCSRF := operator
	noCSRF.csrf = ""
	write(&noCSRF, input, uuid.New(), 403)
	if r := supportRequest(h, &operator, "POST", base+"/refunds", "application/json", mustJSON(t, input), "https://attacker.example", uuid.New()); r.Code != 403 {
		t.Fatal("foreign Origin refund accepted")
	}
	for _, bad := range []map[string]any{
		{"receipt_operation_id": "resolution-original", "reference": "external-return-1", "reason": "x", "confirm_full": false, "keep_access": true},
		{"receipt_operation_id": "resolution-original", "reference": "external-return-1", "reason": "x", "confirm_full": true, "keep_access": false},
		{"receipt_operation_id": "resolution-original", "reference": "https://secret.example/token", "reason": "x", "confirm_full": true, "keep_access": true},
		{"receipt_operation_id": "resolution-original", "reference": "external-return-1", "reason": "x", "confirm_full": true, "keep_access": true, "returned_currency": "USD"},
	} {
		write(&operator, bad, uuid.New(), 400)
	}
	refund := write(&operator, input, key, 201)
	if refund.RefundId == uuid.Nil || refund.OrderId != id || refund.Source != "operator" || (refund.OperatorAccountId == nil || *refund.OperatorAccountId != operator.id) || refund.ReturnedAmount != "90071992547409.93" || refund.ReturnedCurrency != "RUB" || refund.ReceiptGrossMinor != "9007199254740993" {
		t.Fatal("refund fabricated, rounded or attributed incorrectly")
	}
	if again := write(&operator, input, key, 201); again.RefundId != refund.RefundId {
		t.Fatal("lost response duplicated refund")
	}
	write(&operator, input, uuid.New(), 409)
	changed := map[string]any{}
	for k, v := range input {
		changed[k] = v
	}
	changed["reason"] = "Changed payload"
	write(&operator, changed, key, 409)
	for _, invalidReceipt := range []struct {
		id, currency          string
		net                   int64
		protected, unaccepted bool
	}{
		{"protected-receipt", "643", 90, true, false}, {"unaccepted-receipt", "643", 90, false, true}, {"zero-net-receipt", "643", 0, false, false}, {"unknown-currency-receipt", "840", 90, false, false},
	} {
		if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,created_at) VALUES($1,$2,$3,100,$4,$5,'p2p-incoming',$6,$7,$3)`, invalidReceipt.id, newer, e.Clock(), invalidReceipt.net, invalidReceipt.currency, invalidReceipt.protected, invalidReceipt.unaccepted); err != nil {
			t.Fatal(err)
		}
		other := strings.Replace(base, id.String(), newer.String(), 1)
		if c := read(&operator, other+"/payment-case", map[string]any{"receipt_operation_id": invalidReceipt.id}, 200); c.CanConfirmRefund {
			t.Fatal("uncredited/protected money presented as refundable")
		}
		bad := map[string]any{}
		for k, v := range input {
			bad[k] = v
		}
		bad["receipt_operation_id"] = invalidReceipt.id
		if r := supportRequest(h, &operator, "POST", other+"/refunds", "application/json", mustJSON(t, bad), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 409 {
			t.Fatal("uncredited/protected money was returned", r.Code)
		}
	}
	after := read(&operator, base+"/payment-case", map[string]any{}, 200)
	if after.Refund == nil || after.Refund.RefundId != refund.RefundId || after.CanConfirmRefund || after.FinancialReviewOpen || after.Order.FullyRefunded == nil || !*after.Order.FullyRefunded || after.Order.PaymentStatus != "paid" || after.Order.AccessOperationId != nil {
		t.Fatal("refund changed paid/access facts or did not close the financial case")
	}
	var n, audits int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM purchase_refunds WHERE order_id=$1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE account_id=$1 AND action='purchase_refund_confirmed'`, client.id).Scan(&audits); err != nil || n != 1 || audits != 1 {
		t.Fatal("refund/audit not atomic or duplicated", err)
	}
	var status, funding string
	var gross, net int64
	var hash []byte
	if err := e.Pool.QueryRow(ctx, `SELECT p.payment_status,p.funding_operation_id,p.body_hash,r.gross_minor,r.net_minor FROM purchase_orders p JOIN purchase_receipts r ON r.operation_id=p.funding_operation_id WHERE p.id=$1`, id).Scan(&status, &funding, &hash, &gross, &net); err != nil || status != "paid" || funding != "resolution-original" || fmt.Sprint(hash) != "[1 2 3]" || gross != 9007199254740993 || net != 9007199254740900 {
		t.Fatal("original financial facts rewritten", err)
	}
	for _, query := range []string{`UPDATE purchase_refunds SET reason='replace' WHERE order_id=$1`, `DELETE FROM purchase_refunds WHERE order_id=$1`} {
		if _, err := e.Pool.Exec(ctx, query, id); err == nil {
			t.Fatal("refund journal is mutable")
		}
	}
	for _, session := range []*supportSession{&client, &operator} {
		path := "/api/v1/payment-history"
		if session == &operator {
			path = "/api/v1/operator/clients/" + client.id.String() + "/payment-history"
		}
		r := supportRequest(h, session, "POST", path, "application/json", []byte(`{"kind":"refunds"}`), cfg.HTTP.CabinetOrigin, uuid.Nil)
		var page wire.PaymentHistoryPage
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || page.Refunds == nil || len(*page.Refunds) != 1 || (*page.Refunds)[0].RefundId != refund.RefundId {
			t.Fatal("owner refund history missing")
		}
	}
	if _, err := e.Pool.Exec(ctx, `DELETE FROM operator_accounts WHERE account_id=$1`, operator.id); err != nil {
		t.Fatal(err)
	}
	write(&operator, input, key, 403)
}

// A missing UUID cursor or stable account filter would duplicate/lose refunds
// on tied dates, or disclose another client's immutable financial history.
func TestPaymentResolutionRefundHistoryCursor(t *testing.T) {
	h, e, cfg := httpFixture(t)
	client := supportLogin(t, h, e, cfg, "refund-page@example.test")
	other := supportLogin(t, h, e, cfg, "refund-page-other@example.test")
	ctx := context.Background()
	order := uuid.New()
	quote := `{"amount_minor":"100","currency":"RUB","devices":1,"period_days":30,"plan_id":"00000000-0000-4000-8000-000000000123","profile":"regular","revision":1,"traffic_gb":15}`
	if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,active,created_at,expires_at) VALUES($1,$2,$3,$4,$5,100,'AC',false,$6,$7)`, order, client.id, uuid.New(), []byte{1}, quote, e.Clock(), e.Clock().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 51; i++ {
		id := uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
		receipt := fmt.Sprintf("refund-page-%02d", i)
		if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,created_at) VALUES($1,$2,$3,100,100,'643','p2p-incoming',false,false,$3)`, receipt, order, e.Clock()); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_refunds(id,order_id,receipt_operation_id,payment_method,reference,returned_amount,returned_currency,reason,operator_account_id,created_at) VALUES($1,$2,$3,'yoomoney',$3,'1.00','RUB','Verified full return',$4,$5)`, id, order, receipt, other.id, e.Clock()); err != nil {
			t.Fatal(err)
		}
	}
	read := func(session *supportSession, in any) wire.PaymentHistoryPage {
		t.Helper()
		r := supportRequest(h, session, "POST", "/api/v1/payment-history", "application/json", mustJSON(t, in), cfg.HTTP.CabinetOrigin, uuid.Nil)
		var out wire.PaymentHistoryPage
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.Refunds == nil {
			t.Fatal("refund page missing")
		}
		return out
	}
	first := read(&client, map[string]any{"kind": "refunds"})
	if len(*first.Refunds) != 50 || !first.HasMore || (*first.Refunds)[0].RefundId.String() != "00000000-0000-4000-8000-000000000051" {
		t.Fatal("tied-date refund ordering/page broken")
	}
	last := (*first.Refunds)[49]
	next := read(&client, map[string]any{"kind": "refunds", "before_id": last.RefundId.String(), "before_created_at": last.CreatedAt})
	if next.HasMore || len(*next.Refunds) != 1 || (*next.Refunds)[0].RefundId.String() != "00000000-0000-4000-8000-000000000001" {
		t.Fatal("refund cursor duplicated/skipped tied record")
	}
	if own := read(&other, map[string]any{"kind": "refunds"}); len(*own.Refunds) != 0 {
		t.Fatal("refund history leaked another account")
	}
}

// A refunded funding proof must not survive a worker restart, while refunding
// extra money must not cancel the one legitimate target or reset its traffic.
func TestPaymentResolutionWorkers(t *testing.T) {
	for _, state := range []string{"unprepared", "pending", "needs_review", "applied", "extra"} {
		t.Run(state, func(t *testing.T) {
			s, e, account, order := paidPurchase(t)
			ctx := context.Background()
			p := panelFixture(t, s)
			actor := verified(t, s, e, "refund-worker-operator@example.test")
			if err := s.changeOperatorRole(ctx, actor, true); err != nil {
				t.Fatal(err)
			}
			var receipt string
			if err := e.Pool.QueryRow(ctx, `SELECT funding_operation_id FROM purchase_orders WHERE id=$1`, order.OrderId).Scan(&receipt); err != nil {
				t.Fatal(err)
			}
			var operation *uuid.UUID
			if state != "unprepared" {
				if err := s.fulfillPurchase(ctx, order.OrderId); err != nil {
					t.Fatal(err)
				}
				got, err := s.purchaseOrder(ctx, account, order.OrderId)
				if err != nil || got.AccessOperationId == nil {
					t.Fatal("missing real prepared target", err)
				}
				operation = got.AccessOperationId
			}
			if state == "applied" {
				if err := s.applyAccess(ctx, *operation); err != nil {
					t.Fatal(err)
				}
			}
			if state == "needs_review" {
				if _, err := e.Pool.Exec(ctx, `UPDATE access_operations SET status='needs_review',write_started=true,reset_started=true,lease_hash=$2,lease_expires_at=clock_timestamp()+interval '1 minute' WHERE id=$1`, *operation, []byte{1}); err != nil {
					t.Fatal(err)
				}
			}
			if state == "extra" {
				receipt = "refund-extra"
				if err := s.receiveYooMoney(ctx, purchaseNotice(s, order.OrderId, receipt, "90071992547409.00", "90071992547409.93")); err != nil {
					t.Fatal(err)
				}
			}
			var target, steps string
			var write, reset bool
			if operation != nil {
				if err := e.Pool.QueryRow(ctx, `SELECT target::text,completed_steps::text,write_started,reset_started FROM access_operations WHERE id=$1`, *operation).Scan(&target, &steps, &write, &reset); err != nil {
					t.Fatal(err)
				}
			}
			p.mu.Lock()
			beforePanel, _ := json.Marshal(p.client)
			adds, resets := p.adds, p.resets
			p.mu.Unlock()
			input := payments.PurchaseRefundInput{ReceiptOperationId: receipt, Reference: "worker-return", Reason: "Full return; keep actual access", ConfirmFull: true, KeepAccess: true}
			owner, err := s.vpn.OpenAccessOwner(ctx, account)
			if err != nil {
				t.Fatal(err)
			}
			if err = owner.TryLock(ctx); err != nil {
				t.Fatal(err)
			}
			_, err = s.payments.ConfirmPurchaseRefund(ctx, actor, account, order.OrderId, uuid.New(), input)
			owner.Release()
			if !manualCode(err, "ACCOUNT_ACCESS_BUSY") {
				t.Fatal("active owner did not block refund", err)
			}
			if count(t, e, "purchase_refunds") != 0 {
				t.Fatal("busy owner partially committed refund")
			}
			refund, err := s.payments.ConfirmPurchaseRefund(ctx, actor, account, order.OrderId, uuid.New(), input)
			if err != nil {
				t.Fatal(err)
			}
			var eventID uuid.UUID
			var trial *uuid.UUID
			if err = e.Pool.QueryRow(ctx, `SELECT id,operation_id FROM audit_events WHERE id=$1 AND action='purchase_refund_confirmed'`, refund.RefundId).Scan(&eventID, &trial); err != nil || eventID != refund.RefundId || trial != nil {
				t.Fatal("refund lost its audit provenance or reused trial namespace", err)
			}
			for range 2 {
				restarted := newRegressionFixture(e.Pool, e.Redis, s.queue, *s.cfg)
				restarted.now = e.Clock
				if err = restarted.fulfillPurchase(ctx, order.OrderId); err != nil {
					t.Fatal(err)
				}
				if operation != nil {
					if err = restarted.applyAccess(ctx, *operation); err != nil {
						t.Fatal(err)
					}
				}
			}
			got, err := s.purchaseOrder(ctx, account, order.OrderId)
			if err != nil || got.PaymentStatus != "paid" || got.Quote.AmountMinor != "9007199254740993" {
				t.Fatal("refund rewrote paid quote", err)
			}
			if operation != nil {
				var afterTarget, afterSteps, status string
				var afterWrite, afterReset bool
				var lease []byte
				if err = e.Pool.QueryRow(ctx, `SELECT target::text,completed_steps::text,write_started,reset_started,status,lease_hash FROM access_operations WHERE id=$1`, *operation).Scan(&afterTarget, &afterSteps, &afterWrite, &afterReset, &status, &lease); err != nil {
					t.Fatal(err)
				}
				if target != afterTarget || state != "extra" && (steps != afterSteps || write != afterWrite || reset != afterReset) || lease != nil {
					t.Fatal("retirement rewrote target/write evidence or retained a lease")
				}
				want := "skipped"
				if state == "applied" || state == "extra" {
					want = "applied"
				}
				if status != want {
					t.Fatalf("access state %s, want %s", status, want)
				}
			}
			p.mu.Lock()
			afterPanel, _ := json.Marshal(p.client)
			afterAdds, afterResets := p.adds, p.resets
			p.mu.Unlock()
			if state != "extra" && (string(beforePanel) != string(afterPanel) || adds != afterAdds || resets != afterResets) {
				t.Fatal("refund or restarted worker changed native access/traffic")
			}
			if state == "extra" {
				if got.FullyRefunded != nil && *got.FullyRefunded || got.FulfillmentStatus != "applied" {
					t.Fatal("extra refund canceled the original funding")
				}
				return
			}
			if _, err = s.reconcilePurchaseOrder(ctx, actor, account, order.OrderId, uuid.New(), wire.PurchaseReconcileInput{Reason: "must not resurrect returned funds"}); !manualCode(err, "PURCHASE_ORDER_CONFLICT") {
				t.Fatal("refunded funding was reconciled", err)
			}
			if state == "unprepared" {
				current, err := s.currentPurchaseOrder(ctx, account)
				if err != nil || current.CanPurchase == nil || !*current.CanPurchase {
					t.Fatal("closed unissued payment blocks a new purchase", err)
				}
				if err = s.receiveYooMoney(ctx, purchaseNotice(s, order.OrderId, receipt, "90071992547409.00", "90071992547409.93")); err != nil {
					t.Fatal(err)
				}
				if count(t, e, "purchase_refunds") != 1 || count(t, e, "purchase_receipts") != 1 {
					t.Fatal("duplicate callback recreated money/refund")
				}
				if err = s.receiveYooMoney(ctx, purchaseNotice(s, order.OrderId, "refund-late-extra", "90071992547409.00", "90071992547409.93")); err != nil {
					t.Fatal(err)
				}
				c, err := s.payments.OperatorPaymentCase(ctx, actor, account, order.OrderId, nil)
				if err != nil || !c.FinancialReviewOpen || c.Order.FullyRefunded {
					t.Fatal("late separate receipt inherited another refund", err)
				}
				if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(order.Quote.PlanId)); !manualCode(err, "PURCHASE_NOT_ELIGIBLE") {
					t.Fatal("new receipt bypassed financial review", err)
				}
				input.ReceiptOperationId = "refund-late-extra"
				if _, err = s.payments.ConfirmPurchaseRefund(ctx, actor, account, order.OrderId, uuid.New(), input); !manualCode(err, "PAYMENT_REFUND_CONFLICT") {
					t.Fatal("one external operation refunded two receipts", err)
				}
				input.Reference = "second-full-return"
				if _, err = s.payments.ConfirmPurchaseRefund(ctx, actor, account, order.OrderId, uuid.New(), input); err != nil {
					t.Fatal(err)
				}
				if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(order.Quote.PlanId)); err != nil {
					t.Fatal("fully returned money still blocks a fresh server-priced order", err)
				}
				data, err := os.ReadFile("../../db/migrations/00023_purchase_refunds.sql")
				if err != nil {
					t.Fatal(err)
				}
				down := strings.Split(string(data), "-- +goose Down\n")
				if len(down) != 2 {
					t.Fatal("missing owning downgrade")
				}
				tx, err := e.Pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				_, err = tx.Exec(ctx, down[1])
				tx.Rollback(ctx)
				if err == nil || !strings.Contains(err.Error(), "refund downgrade blocked") || count(t, e, "purchase_refunds") != 2 {
					t.Fatal("downgrade destroyed retained return history", err)
				}
			}
		})
	}
}

// Fees and unknown provider net must not become USD net or a crypto conversion;
// full-return confirmation is an operator fact after the original real flow.
func TestPaymentResolutionMethods(t *testing.T) {
	for _, method := range []string{"manual", "yookassa", "cryptomus", "heleket"} {
		t.Run(method, func(t *testing.T) {
			ctx := context.Background()
			var s *regressionFixture
			var e *testkit.Env
			var account, order, actor uuid.UUID
			switch method {
			case "manual":
				var plan uuid.UUID
				s, e, account, plan, actor = manualFixture(t)
				o := manualOrder(t, s, account, plan)
				order = o.OrderId
				if _, err := s.payments.ReportManualPayment(ctx, account, order, uuid.New()); err != nil {
					t.Fatal(err)
				}
				amount := "9007199254740993"
				if _, err := s.payments.DecideManualPayment(ctx, actor, account, order, uuid.New(), payments.ManualPaymentDecisionInput{Decision: "approve", ConfirmedAmountMinor: &amount, Reason: "confirmed incoming"}); err != nil {
					t.Fatal(err)
				}
			case "yookassa":
				var o wire.PurchaseOrder
				var f *kassaStub
				s, e, account, o, f = kassaFixture(t)
				order = o.OrderId
				syncKassa(t, s, order)
				settleKassa(e, f)
				syncKassa(t, s, order)
			default:
				var o wire.PurchaseOrder
				var f *cryptoStub
				s, e, account, o, f = cryptoFixture(t, method)
				order = o.OrderId
				syncCrypto(t, s, order, method)
				settleCrypto(e, f)
				syncCrypto(t, s, order, method)
			}
			if actor == uuid.Nil {
				actor = verified(t, s, e, "refund-method-operator@example.test")
				if err := s.changeOperatorRole(ctx, actor, true); err != nil {
					t.Fatal(err)
				}
			}
			var receipt, proof string
			var beforeNet *int64
			if err := e.Pool.QueryRow(ctx, `SELECT r.operation_id,COALESCE(r.provider_data::text,''),r.net_minor FROM purchase_receipts r WHERE r.order_id=$1`, order).Scan(&receipt, &proof, &beforeNet); err != nil {
				t.Fatal(err)
			}
			input := payments.PurchaseRefundInput{ReceiptOperationId: receipt, Reference: "external-full-return", Reason: "Verified full external return", ConfirmFull: true, KeepAccess: true}
			if method == "cryptomus" || method == "heleket" {
				for _, bad := range []string{"", "0", "1e-3", "0.00200000", "NaN"} {
					input.ReturnedAmount = &bad
					if _, err := s.payments.ConfirmPurchaseRefund(ctx, actor, account, order, uuid.New(), input); !manualCode(err, "INVALID_INPUT") {
						t.Fatal("invalid/over-return crypto accepted", err)
					}
				}
				amount := "0.00120000"
				input.ReturnedAmount = &amount
			}
			// New sales can be disabled without losing confirmation of old facts.
			s.cfg.Payments.ManualEnabled = false
			s.cfg.Payments.YooKassaEnabled = false
			s.cfg.Payments.CryptomusEnabled = false
			s.cfg.Payments.HeleketEnabled = false
			out, err := s.payments.ConfirmPurchaseRefund(ctx, actor, account, order, uuid.New(), input)
			if err != nil {
				t.Fatal(err)
			}
			wantCurrency, wantAmount, wantGross, wantQuote := "RUB", "90071992547409.93", "9007199254740993", "RUB"
			if method == "cryptomus" || method == "heleket" {
				wantCurrency, wantAmount, wantGross, wantQuote = "BTC", "0.00120000", "12345", "USD"
			}
			if out.PaymentMethod != method || out.ReturnedCurrency != wantCurrency || out.ReturnedAmount != wantAmount || out.ReceiptGrossMinor != wantGross || out.ReceiptCurrency != wantQuote || out.Source != "operator" {
				t.Fatal("method/currency/amount/source conflated")
			}
			var afterProof string
			var afterNet *int64
			if err = e.Pool.QueryRow(ctx, `SELECT COALESCE(provider_data::text,''),net_minor FROM purchase_receipts WHERE operation_id=$1`, receipt).Scan(&afterProof, &afterNet); err != nil || proof != afterProof || !reflect.DeepEqual(beforeNet, afterNet) {
				t.Fatal("refund rewrote original provider evidence or invented net", err)
			}
			if err = s.fulfillPurchase(ctx, order); err != nil || count(t, e, "access_operations") != 0 {
				t.Fatal("method issued returned money", err)
			}
		})
	}
}
