package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func starsAssignedSecondary(t *testing.T, mode string) (http.Handler, *regressionFixture, *testkit.Env, wire.MiniAppSessionResult, *app.Modules, payments.PurchaseOrder, *fakePanel, *fakePanel) {
	t.Helper()
	h, s, e, auth, m, order, assigned := starsCycleFixture(t)
	primary := &fakePanel{subscriptionBase: "https://new-primary.example.test/sub/"}
	primary.server = httptest.NewTLSServer(http.HandlerFunc(primary.serve))
	t.Cleanup(primary.server.Close)
	s.cfg.VPN.Panel.PanelRootCAs.AddCert(primary.server.Certificate())
	s.cfg.Subscriptions.PanelID = "new-primary"
	s.cfg.VPN.Panel.PanelURL = primary.server.URL
	ctx := context.Background()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = s.vpn.RegisterServerTx(ctx, tx, vpn.ServerInput{ID: "new-primary", Name: "New primary", Host: primary.server.URL, MaxClients: 10}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if mode != "valid" {
		id := "unknown"
		if mode == "other" {
			id = "new-primary"
		}
		if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET assigned_panel_id=$2 WHERE id=$1", auth.Account.AccountId, id); err != nil {
			t.Fatal(err)
		}
	}
	return h, s, e, auth, m, order, assigned, primary
}

// Subsequent real receipt/fulfillment must preserve the assigned target, not primary config.
func TestServerPoolStarsRecurringCycle(t *testing.T) {
	for _, mode := range []string{"valid", "unknown", "other"} {
		t.Run(mode, func(t *testing.T) {
			_, s, e, auth, _, root, assigned, primary := starsAssignedSecondary(t, mode)
			ctx := context.Background()
			expiry := integer(t, assigned.client["expiryTime"])
			oldID, oldSub := assigned.client["id"], assigned.client["subId"]
			e.Advance(30 * 24 * time.Hour)
			charge := starsCyclePayment(root.OrderId, e.Clock())
			if err := s.payments.RecordStarsPayment(ctx, charge); err != nil {
				t.Fatal(err)
			}
			if mode != "valid" {
				var review bool
				if err := e.Pool.QueryRow(ctx, "SELECT review_reason IS NOT NULL FROM purchase_receipts WHERE provider_data->>'charge_id'=$1", charge.ChargeID).Scan(&review); err != nil || !review || count(t, e, "purchase_orders") != 1 {
					t.Fatal("unproved assignment accepted a funded cycle", err)
				}
				return
			}
			child := starsCycleOrder(t, s, auth.Account.AccountId, root.OrderId, charge.ChargeID)
			if err := s.payments.FulfillPurchase(ctx, child.OrderId); err != nil {
				t.Fatal(err)
			}
			child, err := s.payments.PurchaseOrder(ctx, auth.Account.AccountId, child.OrderId)
			if err != nil || child.AccessOperationId == nil {
				t.Fatal("paid secondary cycle has no access", err)
			}
			if err = s.vpn.ApplyAccess(ctx, *child.AccessOperationId); err != nil {
				t.Fatal(err)
			}
			if assigned.client["id"] != oldID || assigned.client["subId"] != oldSub || integer(t, assigned.client["expiryTime"]) != expiry+30*24*60*60*1000 || primary.adds+primary.updates+primary.resets != 0 || count(t, e, "purchase_receipts") != 2 {
				t.Fatal("cycle changed identity, panel, paid period or receipts")
			}
		})
	}
}

// Cancel remains available, but only the actual assigned paid source can resume billing.
func TestServerPoolStarsResume(t *testing.T) {
	for _, mode := range []string{"valid", "unknown", "other"} {
		t.Run(mode, func(t *testing.T) {
			h, s, _, auth, _, _, _, _ := starsAssignedSecondary(t, mode)
			calls := 0
			starsSetter(s, func(context.Context, int64, string, bool) error { calls++; return nil })
			if r := starsRequest(h, s, auth, "POST", "/api/v1/stars-subscription/control", `{"action":"cancel","confirmed":true}`, uuid.New()); r.Code != 200 {
				t.Fatal("cancel refused", r.Code)
			}
			state := starsSubscriptionState(t, h, s, auth)
			if state.CanResume != (mode == "valid") {
				t.Fatal("resume eligibility used primary config or a foreign source")
			}
			r := starsRequest(h, s, auth, "POST", "/api/v1/stars-subscription/control", `{"action":"resume","confirmed":true}`, uuid.New())
			if mode == "valid" {
				if r.Code != 200 || calls != 2 {
					t.Fatal("secondary billing cannot resume", r.Code, calls)
				}
			} else if r.Code == 200 || calls != 1 {
				t.Fatal("unproved assignment resumed billing", r.Code, calls)
			}
		})
	}
}

// Confirmed secondary billing must keep expiry suppression and dated lapse facts.
func TestServerPoolStarsReminder(t *testing.T) {
	for _, mode := range []string{"valid", "unknown", "other"} {
		t.Run(mode, func(t *testing.T) {
			_, s, e, auth, m, _, assigned, primary := starsAssignedSecondary(t, mode)
			ctx := context.Background()
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			out, err := m.Payments.ReminderPolicyTx(ctx, tx, auth.Account.AccountId)
			if err != nil || out.Known != (mode == "valid") {
				t.Fatal("secondary billing became unknown or foreign became confirmed", err)
			}
			if mode != "valid" {
				return
			}
			if !out.SuppressExpiry || out.Lapsed || out.PaidUntil == nil {
				t.Fatal("confirmed active billing lost its period")
			}
			s.now = func() time.Time { return out.PaidUntil.Add(24*time.Hour + time.Nanosecond) }
			lapsed, err := m.Payments.ReminderPolicyTx(ctx, tx, auth.Account.AccountId)
			if err != nil || !lapsed.Known || !lapsed.Lapsed || lapsed.SuppressExpiry || primary.adds+primary.updates+primary.resets != 0 || assigned.adds != 1 || count(t, e, "purchase_receipts") != 1 {
				t.Fatal("dated secondary lapse lost facts or wrote business state", err)
			}
		})
	}
}

func TestServerPoolLegacyTrialIdentity(t *testing.T) {
	s, e := fixture(t)
	s.cfg.Subscriptions.PanelID = "eu:1"
	panel := panelFixture(t, s)
	account, op := approved(t, s, e, "legacy-pool-trial@example.test")
	if err := s.provision(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	assertPoolTrial(t, s, e, account, op, "eu:1")
	var raw []byte
	var target vpn.ProvisionTarget
	if err := e.Pool.QueryRow(context.Background(), "SELECT target FROM trial_operations WHERE id=$1", op).Scan(&raw); err != nil || json.Unmarshal(raw, &target) != nil || target.PanelID != "eu:1" || panel.adds != 1 {
		t.Fatal("legacy target ID was replaced or never issued", err)
	}
	if _, err := s.subscriptionKey(context.Background(), account); err != nil {
		t.Fatal("legacy assigned key unavailable", err)
	}
}
