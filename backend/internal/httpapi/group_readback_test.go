package httpapi

import (
	"context"
	"testing"

	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func TestGroupAlignedPanelConfirmsFreshBaseline(t *testing.T) {
	for _, mode := range []string{"deleted_inbound", "manually_recovered"} {
		t.Run(mode, func(t *testing.T) {
			s, e, p, account, _ := renewalFixture(t)
			ctx := context.Background()
			key, err := s.subscriptions.SubscriptionKey(ctx, account)
			if err != nil {
				t.Fatal(err)
			}
			var retired uuid.UUID
			if mode == "manually_recovered" {
				p.inboundRows = []map[string]any{{"id": 1, "enable": true, "tag": "foreign"}, {"id": 2, "enable": true, "tag": "foreign"}, {"id": 4, "enable": true, "tag": "regular"}}
				retired, err = s.vpn.PrepareGroupReconciliation(ctx, account)
				if err != nil || retired == uuid.Nil {
					t.Fatal("prepare failed group work", err)
				}
				p.failRead = true
				for i := 0; i < 5; i++ {
					_ = s.applyAccess(ctx, retired)
				}
				p.failRead = false
				p.ids = []int64{4}
			} else {
				p.inboundRows = []map[string]any{{"id": 2, "enable": true, "tag": "regular"}}
				p.ids = []int64{2}
			}
			targets := func(exclude uuid.UUID) string {
				t.Helper()
				var raw string
				if err := e.Pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id,'target',target) ORDER BY id),'[]'::jsonb)::text FROM access_operations WHERE account_id=$1 AND id<>$2`, account, exclude).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				return raw
			}
			before := targets(uuid.Nil)
			writes := p.adds + p.attaches + p.updates + p.resets + p.disables + p.detaches + p.otherWrites
			op, err := s.vpn.PrepareGroupReconciliation(ctx, account)
			if err != nil || op == uuid.Nil {
				t.Fatal("aligned memberships did not get a fresh confirmation", err)
			}
			if err = s.applyAccess(ctx, op); err != nil {
				t.Fatal(err)
			}
			if targets(op) != before || writes != p.adds+p.attaches+p.updates+p.resets+p.disables+p.detaches+p.otherWrites {
				t.Fatal("read-only confirmation changed a frozen target or wrote the panel")
			}
			if retired != uuid.Nil {
				var status string
				if err = e.Pool.QueryRow(ctx, `SELECT status FROM access_operations WHERE id=$1`, retired).Scan(&status); err != nil || status != "skipped" {
					t.Fatal("old group work was not retired", status, err)
				}
			}
			fresh, err := s.subscriptions.SubscriptionKey(ctx, account)
			if err != nil || fresh != key {
				t.Fatal("confirmed memberships lost the existing subscription URL", err)
			}
			sub, err := s.subscriptions.Subscription(ctx, account)
			if err != nil || sub.Status != "active" || sub.DataStale {
				t.Fatal("fresh confirmation was not readable", sub.Status, err)
			}
			if next, err := s.vpn.PrepareGroupReconciliation(ctx, account); err != nil || next != uuid.Nil {
				t.Fatal("unchanged confirmed baseline queued again", next, err)
			}
		})
	}
}

func TestGroupBanOnlyReadbackAndUnban(t *testing.T) {
	for _, mode := range []string{"direct_unban", "scan_then_unban"} {
		t.Run(mode, func(t *testing.T) {
			s, e, p, account, _ := renewalFixture(t)
			ctx := context.Background()
			key, err := s.subscriptions.SubscriptionKey(ctx, account)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = e.Pool.Exec(ctx, `UPDATE accounts SET vpn_banned=true WHERE id=$1`, account); err != nil {
				t.Fatal(err)
			}
			p.inboundRows = []map[string]any{{"id": 1, "enable": false, "tag": "regular"}, {"id": 2, "enable": false, "tag": "regular"}}
			op, err := s.vpn.PrepareGroupReconciliation(ctx, account)
			if err != nil || op == uuid.Nil {
				t.Fatal("prepare ban-only operation", err)
			}
			if err = s.applyAccess(ctx, op); err != nil {
				t.Fatal(err)
			}
			sub, err := s.subscriptions.Subscription(ctx, account)
			if err != nil || sub.Status != "banned" || sub.DataStale {
				t.Fatal("confirmed ban-only operation was not readable", sub.Status, err)
			}
			_, err = s.subscriptions.SubscriptionKey(ctx, account)
			if denial, ok := subscriptionError(err).(*apiError); !ok || denial.Status != 403 {
				t.Fatal("VPN ban exposed a subscription URL", err)
			}
			actor := verified(t, s, e, "group-unban@example.test")
			if err = s.changeOperatorRole(ctx, actor, true); err != nil {
				t.Fatal(err)
			}
			ban := false
			input := wire.AccessOperationInput{Kind: "set_vpn_ban", VpnBanned: &ban, Reason: "restore after provider recovery"}
			if _, err = s.createAccessOperation(ctx, actor, account, uuid.New(), input); err == nil {
				t.Fatal("unban accepted an empty enabled profile")
			}
			p.inboundRows = []map[string]any{{"id": 1, "enable": true, "tag": "regular"}, {"id": 2, "enable": true, "tag": "regular"}}
			if mode == "scan_then_unban" {
				writes := p.adds + p.attaches + p.updates + p.resets + p.disables + p.detaches + p.otherWrites
				next, err := s.vpn.PrepareGroupReconciliation(ctx, account)
				if err != nil || next == uuid.Nil || s.applyAccess(ctx, next) != nil {
					t.Fatal("restored enabled profile did not get confirmed", err)
				}
				if writes != p.adds+p.attaches+p.updates+p.resets+p.disables+p.detaches+p.otherWrites || p.client["enable"] != false {
					t.Fatal("profile confirmation wrote the panel or enabled a banned client")
				}
			}
			// Unban must read the ban-only baseline even before the next scheduled scan.
			request, err := s.createAccessOperation(ctx, actor, account, uuid.New(), input)
			if err != nil {
				t.Fatal("provider recovery left unban blocked", err)
			}
			if err = s.applyAccess(ctx, request.OperationId); err != nil {
				t.Fatal(err)
			}
			fresh, err := s.subscriptions.SubscriptionKey(ctx, account)
			if err != nil || fresh != key {
				t.Fatal("unban did not restore the same subscription URL", err)
			}
		})
	}
}
