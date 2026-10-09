package httpapi

import (
	"context"
	"testing"

	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func TestMaintenancePurchaseDirectReplayAndNewAdmission(t *testing.T) {
	s, env, account, plan := purchaseFixture(t)
	ctx := context.Background()
	in := wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"}
	key := uuid.New()
	accepted, err := s.createPurchaseOrder(ctx, account, key, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = env.Pool.Exec(ctx, `UPDATE maintenance_state SET enabled=true,revision=1,changed_at=now() WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	replay, err := s.createPurchaseOrder(ctx, account, key, in)
	if err != nil || replay.OrderId != accepted.OrderId {
		t.Fatalf("accepted purchase replay: %+v %v", replay, err)
	}
	if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), in); err == nil || err.Error() != "MAINTENANCE" {
		t.Fatalf("new direct purchase admitted: %v", err)
	}
}

func TestMaintenanceStarsInvoiceExistingReplay(t *testing.T) {
	_, s, env, auth, plan := starsHTTPFixture(t)
	ctx := context.Background()
	order, err := s.payments.CreatePurchaseOrder(ctx, auth.Account.AccountId, uuid.New(), payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "telegram_stars", PaymentType: "STARS", PlanId: plan, Revision: 1, PeriodDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) {
		return "https://t.me/$maintenance_invoice", nil
	}, Refund: func(context.Context, int64, string) error { return nil }})
	if _, err = env.Pool.Exec(ctx, `UPDATE maintenance_state SET enabled=true,revision=1,changed_at=now() WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.payments.CreateStarsInvoice(ctx, auth.Account.AccountId, order.OrderId); err == nil || err.Error() != "MAINTENANCE" {
		t.Fatalf("new invoice admitted: %v", err)
	}
	if _, err = env.Pool.Exec(ctx, `UPDATE maintenance_state SET enabled=false,revision=2,changed_at=now() WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.payments.CreateStarsInvoice(ctx, auth.Account.AccountId, order.OrderId)
	if err != nil || accepted.StarsCheckout.URL == nil {
		t.Fatalf("accepted invoice: %+v %v", accepted.StarsCheckout, err)
	}
	if _, err = env.Pool.Exec(ctx, `UPDATE maintenance_state SET enabled=true,revision=3,changed_at=now() WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	replay, err := s.payments.CreateStarsInvoice(ctx, auth.Account.AccountId, order.OrderId)
	if err != nil || replay.StarsCheckout.URL == nil || *replay.StarsCheckout.URL != *accepted.StarsCheckout.URL {
		t.Fatalf("frozen invoice replay: %+v %v", replay.StarsCheckout, err)
	}
}

func TestMaintenanceStarsResumeOnly(t *testing.T) {
	_, s, env, auth, _, _ := starsSubscriptionFixture(t)
	ctx := context.Background()
	s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) {
		return "https://t.me/$maintenance_invoice", nil
	}, Refund: func(context.Context, int64, string) error { return nil }, EditSubscription: func(context.Context, int64, string, bool) error { return nil }})
	if _, err := s.payments.ControlStarsSubscription(ctx, auth.Account.AccountId, uuid.New(), payments.StarsSubscriptionControlInput{Action: "cancel", Confirmed: true}); err != nil {
		t.Fatalf("cancel before mode: %v", err)
	}
	if _, err := env.Pool.Exec(ctx, `UPDATE maintenance_state SET enabled=true,revision=1,changed_at=now() WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.payments.ControlStarsSubscription(ctx, auth.Account.AccountId, uuid.New(), payments.StarsSubscriptionControlInput{Action: "cancel", Confirmed: true}); err != nil {
		t.Fatalf("cancel during mode: %v", err)
	}
	if _, err := s.payments.ControlStarsSubscription(ctx, auth.Account.AccountId, uuid.New(), payments.StarsSubscriptionControlInput{Action: "resume", Confirmed: true}); err == nil || err.Error() != "MAINTENANCE" {
		t.Fatalf("resume admitted: %v", err)
	}
}
