package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

func poolPlan(t *testing.T, e *testkit.Env, devices int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	terms := catalogueTerms(devices)
	terms.Prices[0].AmountMinor = "9007199254740993"
	raw, err := json.Marshal(terms)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = e.Pool.Exec(ctx, "INSERT INTO catalogue_plans(id,current_revision,current_devices,current_profile,current_hidden) VALUES($1,1,$2,'regular',false)", id, devices); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, "INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,1,$2,false,'legacy_import',$3)", id, raw, e.Clock()); err != nil {
		t.Fatal(err)
	}
	return id
}

func poolPaid(t *testing.T) (*regressionFixture, *testkit.Env, *fakePanel, *fakePanel, uuid.UUID, uuid.UUID, wire.PurchaseOrder) {
	t.Helper()
	s, e, a, b := serverPoolFixture(t, 0)
	s.cfg.Payments.YooMoneyEnabled = true
	s.cfg.Payments.YooMoneyWalletID = "410000000000000"
	s.cfg.Payments.YooMoneyNotificationSecret = []byte("test-only-notification-secret")
	account := verified(t, s, e, "pool-purchase@example.test")
	plan := poolPlan(t, e, 2)
	order, err := s.createPurchaseOrder(context.Background(), account, uuid.New(), purchaseInput(plan))
	if err != nil {
		t.Fatal(err)
	}
	if count(t, e, "vpn_server_reservations") != 0 {
		t.Fatal("unpaid checkout reserved capacity")
	}
	if err = s.receiveYooMoney(context.Background(), purchaseNotice(s, order.OrderId, "pool-funded", "90071992547409.00", "90071992547409.93")); err != nil {
		t.Fatal(err)
	}
	return s, e, a, b, account, plan, order
}

func poolPrepared(t *testing.T, s *regressionFixture, account uuid.UUID, order wire.PurchaseOrder) uuid.UUID {
	t.Helper()
	if err := s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
		t.Fatal(err)
	}
	out, err := s.purchaseOrder(context.Background(), account, order.OrderId)
	if err != nil || out.AccessOperationId == nil {
		t.Fatalf("target not prepared: %v", err)
	}
	return *out.AccessOperationId
}

func TestServerPoolPaidFulfillment(t *testing.T) {
	s, e, a, b, account, _, order := poolPaid(t)
	op := poolPrepared(t, s, account, order)
	var reserved string
	if err := e.Pool.QueryRow(context.Background(), "SELECT server_id FROM vpn_server_reservations WHERE account_id=$1", account).Scan(&reserved); err != nil || reserved != "second" {
		t.Fatalf("paid reservation=%s err=%v", reserved, err)
	}
	if err := s.applyAccess(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
			t.Fatal(err)
		}
		if err := s.applyAccess(context.Background(), op); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.purchaseOrder(context.Background(), account, order.OrderId)
	if err != nil || out.FulfillmentStatus != "applied" || a.adds != 0 || b.adds != 1 || count(t, e, "purchase_receipts") != 1 || count(t, e, "vpn_server_reservations") != 0 {
		t.Fatalf("paid outcome=%s primary=%d secondary=%d err=%v", out.FulfillmentStatus, a.adds, b.adds, err)
	}
	key, err := s.subscriptionKey(context.Background(), account)
	if err != nil || !strings.HasPrefix(key.SubscriptionUrl, "https://second.example.test/sub/") {
		t.Fatal("paid key uses wrong panel", err)
	}
}

func TestServerPoolPurchaseSelectionRace(t *testing.T) {
	s, e, a, b, account, _, order := poolPaid(t)
	b.afterRead = func() {
		if _, err := e.Pool.Exec(context.Background(), "UPDATE vpn_servers SET max_clients=1,revision=revision+1 WHERE id=$1", s.cfg.Subscriptions.PanelID); err != nil {
			t.Error(err)
		}
	}
	var snooze *river.JobSnoozeError
	if err := s.payments.FulfillPurchase(context.Background(), order.OrderId); !errors.As(err, &snooze) {
		t.Fatalf("changed candidate did not snooze: %v", err)
	}
	out, err := s.purchaseOrder(context.Background(), account, order.OrderId)
	if err != nil || out.PaymentStatus != "paid" || out.AccessOperationId != nil || count(t, e, "vpn_server_reservations") != 0 || a.adds+b.adds != 0 {
		t.Fatal("selection race lost money or queued stale target", err)
	}
	if _, err = e.Pool.Exec(context.Background(), "UPDATE vpn_servers SET max_clients=0,revision=revision+1 WHERE id=$1", s.cfg.Subscriptions.PanelID); err != nil {
		t.Fatal(err)
	}
	op := poolPrepared(t, s, account, order)
	if err = s.applyAccess(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if a.adds != 0 || b.adds != 1 {
		t.Fatal("retry wrote wrong panel")
	}
}

func TestServerPoolRenewalAndPlanChange(t *testing.T) {
	s, e, a, b, account, plan, order := poolPaid(t)
	op := poolPrepared(t, s, account, order)
	if err := s.applyAccess(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	id, sub := b.client["id"], b.client["subId"]
	expiry := integer(t, b.client["expiryTime"])
	in := purchaseInput(plan)
	in.Action = "renew"
	renew, err := s.createPurchaseOrder(context.Background(), account, uuid.New(), in)
	if err != nil {
		t.Fatal(err)
	}
	completeRenewalPayment(t, s, account, renew)
	if integer(t, b.client["expiryTime"]) != expiry+30*24*60*60*1000 {
		t.Fatal("renewal did not extend assigned subscription")
	}
	change, err := s.createPurchaseOrder(context.Background(), account, uuid.New(), planChangeInput(t, s, account, poolPlan(t, e, 3)))
	if err != nil {
		t.Fatal(err)
	}
	completeRenewalPayment(t, s, account, change)
	if b.client["id"] != id || b.client["subId"] != sub || integer(t, b.client["limitIp"]) != 4 || a.adds+a.updates+a.resets != 0 || b.adds != 1 || count(t, e, "purchase_receipts") != 3 {
		t.Fatal("renewal/change crossed identity, panel or money")
	}
}

func TestServerPoolRefundReservation(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "before write", true: "ambiguous write"}[partial], func(t *testing.T) {
			s, e, a, b, account, _, order := poolPaid(t)
			op := poolPrepared(t, s, account, order)
			if count(t, e, "vpn_server_reservations") != 1 {
				t.Fatal("prepared first access has no reserve")
			}
			if partial {
				b.failAfterAdd = true
				if err := s.applyAccess(context.Background(), op); err != nil {
					t.Fatal(err)
				}
			}
			actor := renewalOperator(t, s, e)
			var receipt string
			if err := e.Pool.QueryRow(context.Background(), "SELECT funding_operation_id FROM purchase_orders WHERE id=$1", order.OrderId).Scan(&receipt); err != nil {
				t.Fatal(err)
			}
			if _, err := s.payments.ConfirmPurchaseRefund(context.Background(), actor, account, order.OrderId, uuid.New(), payments.PurchaseRefundInput{ReceiptOperationId: receipt, Reference: "pool-refund", Reason: "Owned fixture full return", ConfirmFull: true, KeepAccess: true}); err != nil {
				t.Fatal(err)
			}
			want := 0
			if partial {
				want = 1
			}
			if count(t, e, "vpn_server_reservations") != want {
				t.Fatalf("refund retained wrong reserve: partial=%v", partial)
			}
			before := b.adds + b.updates + b.resets
			if err := s.applyAccess(context.Background(), op); err != nil {
				t.Fatal(err)
			}
			if a.adds != 0 || b.adds+b.updates+b.resets != before || count(t, e, "purchase_refunds") != 1 {
				t.Fatal("refunded intent wrote or duplicated return")
			}
		})
	}
}
