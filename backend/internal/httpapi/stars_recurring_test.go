package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/payments"
	"github.com/google/uuid"
)

// Catches changed persisted one-time input/quote bytes and ignored explicit recurrence.
func TestStarsRecurringInputCompatibility(t *testing.T) {
	const oldInput = `{"action":"purchase","payment_method":"telegram_stars","payment_type":"STARS","period_days":30,"plan_id":"00000000-0000-4000-8000-000000000001","revision":1}`
	const oldQuote = `{"amount_minor":"100","currency":"XTR","devices":2,"period_days":30,"plan_id":"00000000-0000-4000-8000-000000000001","profile":"regular","revision":1,"traffic_gb":10}`
	for _, raw := range []string{oldInput, oldQuote} {
		var value any
		if strings.Contains(raw, "action") {
			value = &payments.PurchaseOrderInput{}
		} else {
			value = &payments.PurchaseQuote{}
		}
		for _, suffix := range []string{"", `,"stars_recurring":false`} {
			source := strings.TrimSuffix(raw, "}") + suffix + "}"
			if err := json.Unmarshal([]byte(source), value); err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(value)
			if err != nil || string(got) != raw || sha256.Sum256(got) != sha256.Sum256([]byte(raw)) {
				t.Fatal("one-time persisted bytes changed", err)
			}
		}
		source := strings.TrimSuffix(raw, "}") + `,"stars_recurring":true}`
		if err := json.Unmarshal([]byte(source), value); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(value)
		if err != nil || string(got) != source {
			t.Fatal("explicit recurring field was dropped or reordered", err)
		}
	}
}

// Catches omitted native period, multiple starts and duplicate first-payment grants.
func TestStarsRecurringFirstPayment(t *testing.T) {
	h, s, e, auth, plan := starsHTTPFixture(t)
	panel := panelFixture(t, s)
	ctx := context.Background()
	body := fmt.Sprintf(`{"action":"purchase","payment_method":"telegram_stars","payment_type":"STARS","period_days":30,"plan_id":%q,"revision":1,"stars_recurring":true}`, plan.String())
	r := starsRequest(h, s, auth, "POST", "/api/v1/orders", body, uuid.New())
	if r.Code != 201 {
		t.Fatal("recurring signed checkout refused", r.Code)
	}
	var order payments.PurchaseOrder
	if json.Unmarshal(r.Body.Bytes(), &order) != nil {
		t.Fatal("invalid order response")
	}
	var sent map[string]any
	s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(_ context.Context, in payments.StarsInvoice) (string, error) {
		raw, _ := json.Marshal(in)
		if json.Unmarshal(raw, &sent) != nil {
			t.Fatal("invoice arguments")
		}
		return "https://t.me/$owned_recurring_invoice", nil
	}, Refund: func(context.Context, int64, string) error { return nil }})
	if _, err := s.payments.CreateStarsInvoice(ctx, auth.Account.AccountId, order.OrderId); err != nil {
		t.Fatal("native recurring invoice", err)
	}
	if sent["SubscriptionPeriod"] != float64(2592000) || sent["Amount"] != float64(100) {
		t.Fatal("missing native recurring period/amount")
	}
	input := payments.StarsPreCheckoutInput{BotID: 123, PayerID: 701, Amount: 100, Currency: "XTR", Payload: "stars:v1:" + order.OrderId.String()}
	if err := json.Unmarshal([]byte(`{"QueryID":"owned-recurring-first-query"}`), &input); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if ok, err := s.payments.CheckStarsPreCheckout(ctx, input); err != nil || !ok {
			t.Fatal("first query or replay refused", err)
		}
	}
	if err := json.Unmarshal([]byte(`{"QueryID":"owned-recurring-other-query"}`), &input); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.payments.CheckStarsPreCheckout(ctx, input); err != nil || ok {
		t.Fatal("duplicate subscription start allowed", err)
	}
	charge := starsPayment(order.OrderId, e.Clock())
	charge.Recurring = true
	charge.FirstRecurring = true
	charge.SubscriptionExpiresAt = charge.At.Add(30 * 24 * time.Hour).Unix()
	for range 2 {
		if err := s.payments.RecordStarsPayment(ctx, charge); err != nil {
			t.Fatal("first recurring charge", err)
		}
	}
	var receipts, subscriptions, cycles, jobs int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM purchase_receipts WHERE order_id=$1),(SELECT count(*) FROM stars_subscriptions WHERE root_order_id=$1),(SELECT count(*) FROM stars_subscription_cycles WHERE root_order_id=$1),(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=$1::uuid::text)`, order.OrderId).Scan(&receipts, &subscriptions, &cycles, &jobs); err != nil || receipts != 1 || subscriptions != 1 || cycles != 1 || jobs != 1 {
		t.Fatal("first charge proof/grant duplication", err)
	}
	if err := s.payments.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal("recurring funding refused", err)
	}
	got, err := s.payments.PurchaseOrder(ctx, auth.Account.AccountId, order.OrderId)
	if err != nil || got.AccessOperationId == nil {
		t.Fatal("first recurring access missing", err)
	}
	for range 2 {
		if err := s.vpn.ApplyAccess(ctx, *got.AccessOperationId); err != nil {
			t.Fatal(err)
		}
	}
	if panel.adds != 1 {
		t.Fatal("first recurring charge grants twice")
	}
	extra := charge
	extra.ChargeID = "owned-recurring-extra-first-charge"
	if err := s.payments.RecordStarsPayment(ctx, extra); err != nil {
		t.Fatal("extra first charge lost", err)
	}
	var canonical, cancelRequired, accesses int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM purchase_receipts WHERE order_id=$1),(SELECT count(*) FROM stars_subscriptions WHERE root_order_id=$1),(SELECT count(*) FROM stars_subscriptions WHERE root_order_id=$1 AND canonical),(SELECT count(*) FROM stars_subscriptions WHERE root_order_id=$1 AND desired_action='cancel'),(SELECT count(*) FROM access_operations WHERE purchase_order_id=$1),(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=$1::uuid::text)`, order.OrderId).Scan(&receipts, &subscriptions, &canonical, &cancelRequired, &accesses, &jobs); err != nil || receipts != 2 || subscriptions != 2 || canonical != 1 || cancelRequired != 2 || accesses != 1 || jobs != 1 {
		t.Fatal("extra billing identity discarded or granted", err)
	}
	var retained bool
	if err := e.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM access_operations WHERE id=$1 AND status='applied')`, *got.AccessOperationId).Scan(&retained); err != nil || !retained {
		t.Fatal("extra first charge erased already-paid access", err)
	}
	for _, sql := range []string{
		`UPDATE stars_subscriptions SET payer_id=702 WHERE root_order_id=$1`,
		`UPDATE stars_subscriptions SET first_charge_id='changed' WHERE root_order_id=$1`,
		`UPDATE stars_subscriptions SET canonical=false WHERE root_order_id=$1 AND canonical`,
		`UPDATE stars_subscription_cycles SET paid_until=paid_until+interval '1 second' WHERE root_order_id=$1`,
		`UPDATE stars_subscription_controls SET reason='changed' WHERE subscription_receipt_id IN (SELECT first_receipt_id FROM stars_subscriptions WHERE root_order_id=$1)`,
		`UPDATE stars_subscription_controls SET action='resume' WHERE subscription_receipt_id IN (SELECT first_receipt_id FROM stars_subscriptions WHERE root_order_id=$1)`,
		`DELETE FROM stars_subscription_controls WHERE subscription_receipt_id IN (SELECT first_receipt_id FROM stars_subscriptions WHERE root_order_id=$1)`,
	} {
		if _, err := e.Pool.Exec(ctx, sql, order.OrderId); err == nil {
			t.Fatal("native billing provenance mutation accepted")
		}
	}
}

// Catches recurring options bypassing their supported method, period and identity boundary.
func TestStarsRecurringFirstPaymentInvalid(t *testing.T) {
	for _, kind := range []string{"period", "external", "renew", "change", "amount", "payer", "flags", "expiry", "disabled", "late"} {
		t.Run(kind, func(t *testing.T) {
			h, s, e, auth, plan := starsHTTPFixture(t)
			ctx := context.Background()
			in := payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "telegram_stars", PaymentType: "STARS", PeriodDays: 30, PlanId: plan, Revision: 1}
			json.Unmarshal([]byte(`{"stars_recurring":true}`), &in)
			switch kind {
			case "period":
				in.PeriodDays = 60
			case "external":
				in.PaymentMethod = "yoomoney"
				in.PaymentType = "AC"
			case "renew":
				in.Action = "renew"
			case "change":
				in.Action = "change_plan"
				x := uuid.New()
				in.SourceAccessOperationId = &x
			case "amount":
				if _, err := e.Pool.Exec(ctx, `INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) SELECT plan_id,2,jsonb_set(terms,'{prices,2,amount_minor}','"10001"'),false,'legacy_import',$2 FROM catalogue_revisions WHERE plan_id=$1 AND revision=1`, plan, e.Clock()); err != nil {
					t.Fatal(err)
				}
				if _, err := e.Pool.Exec(ctx, `UPDATE catalogue_plans SET current_revision=2 WHERE id=$1`, plan); err != nil {
					t.Fatal(err)
				}
				in.Revision = 2
			}
			raw, _ := json.Marshal(in)
			var body map[string]any
			json.Unmarshal(raw, &body)
			body["stars_recurring"] = true
			raw, _ = json.Marshal(body)
			r := starsRequest(h, s, auth, "POST", "/api/v1/orders", string(raw), uuid.New())
			if kind == "period" || kind == "external" || kind == "renew" || kind == "change" || kind == "amount" {
				if r.Code != 400 && r.Code != 403 && r.Code != 409 {
					t.Fatal("unsupported recurring option accepted", r.Code)
				}
				return
			}
			if r.Code != 201 {
				t.Fatal("recurring fixture order", r.Code)
			}
			var order payments.PurchaseOrder
			json.Unmarshal(r.Body.Bytes(), &order)
			charge := starsPayment(order.OrderId, e.Clock())
			charge.Recurring = true
			charge.FirstRecurring = true
			charge.SubscriptionExpiresAt = charge.At.Add(30 * 24 * time.Hour).Unix()
			switch kind {
			case "payer":
				charge.PayerID = 702
			case "flags":
				charge.FirstRecurring = false
			case "expiry":
				charge.SubscriptionExpiresAt = charge.At.Add(31 * 24 * time.Hour).Unix()
			case "disabled":
				s.cfg.Payments.StarsEnabled = false
			case "late":
				charge.At = e.Clock().Add(31 * time.Minute)
				charge.SubscriptionExpiresAt = charge.At.Add(30 * 24 * time.Hour).Unix()
			}
			if err := s.payments.RecordStarsPayment(ctx, charge); err != nil {
				t.Fatal("owned malformed charge was lost", err)
			}
			var receipts, jobs int
			var safe bool
			if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM purchase_receipts WHERE order_id=$1),(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=$1::uuid::text),EXISTS(SELECT 1 FROM purchase_orders WHERE id=$1 AND review_required AND funding_operation_id IS NULL AND access_operation_id IS NULL)`, order.OrderId).Scan(&receipts, &jobs, &safe); err != nil || receipts != 1 || jobs != 0 || !safe {
				t.Fatal("unsafe recurring charge funded or lost", err)
			}
		})
	}
}
