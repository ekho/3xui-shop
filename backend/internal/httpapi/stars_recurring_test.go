package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/wire"
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

func starsCyclePayment(root uuid.UUID, at time.Time) payments.StarsPaymentInput {
	in := starsPayment(root, at)
	in.ChargeID = "owned-next-cycle"
	in.Recurring = true
	in.SubscriptionExpiresAt = in.At.Add(30 * 24 * time.Hour).Unix()
	return in
}
func starsCycleOrder(t *testing.T, s *regressionFixture, account, root uuid.UUID, charge string) payments.PurchaseOrder {
	t.Helper()
	var id uuid.UUID
	if err := s.pool.QueryRow(context.Background(), `SELECT order_id FROM purchase_receipts WHERE provider_data->>'charge_id'=$1`, charge).Scan(&id); err != nil || id == root {
		t.Fatal("subsequent charge has no separate cycle", err)
	}
	order, err := s.payments.PurchaseOrder(context.Background(), account, id)
	if err != nil {
		t.Fatal("cycle state", err)
	}
	return order
}

// Catches repeated grants, changed catalogue terms and provider time being used as VPN expiry.
func TestStarsRecurringCycle(t *testing.T) {
	for _, mode := range []string{"normal", "gap", "duplicate-period", "early", "old-expiry", "wrong-first", "disabled", "ban", "operator-source", "pending-partial", "paid-client-cancel", "late-operator-source", "late-profile-after-cancel"} {
		t.Run(mode, func(t *testing.T) {
			_, s, e, auth, m, root, p := starsCycleFixture(t)
			ctx := context.Background()
			oldExpiry := integer(t, p.client["expiryTime"])
			var vpnID uuid.UUID
			var subID string
			if err := e.Pool.QueryRow(ctx, `SELECT vpn_id,sub_id FROM accounts WHERE id=$1`, auth.Account.AccountId).Scan(&vpnID, &subID); err != nil {
				t.Fatal(err)
			}
			terms := catalogueTerms(8)
			terms.TrafficGb = 500
			terms.Prices[2].AmountMinor = "900"
			raw, _ := json.Marshal(terms)
			if _, err := e.Pool.Exec(ctx, `INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,2,$2,false,'legacy_import',$3)`, root.Quote.PlanId, raw, e.Clock()); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(ctx, `UPDATE catalogue_plans SET current_revision=2,current_devices=8 WHERE id=$1`, root.Quote.PlanId); err != nil {
				t.Fatal(err)
			}
			e.Advance(30 * 24 * time.Hour)
			if mode == "gap" {
				e.Advance(4 * 24 * time.Hour)
			}
			in := starsCyclePayment(root.OrderId, e.Clock())
			switch mode {
			case "early":
				in.At = in.At.Add(-10 * time.Minute)
				in.SubscriptionExpiresAt = in.At.Add(30 * 24 * time.Hour).Unix()
			case "old-expiry":
				in.SubscriptionExpiresAt = in.At.Unix()
			case "wrong-first":
				in.FirstRecurring = true
			case "disabled":
				s.cfg.Payments.StarsEnabled = false
			case "ban":
				if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET vpn_banned=true WHERE id=$1`, auth.Account.AccountId); err != nil {
					t.Fatal(err)
				}
			case "operator-source":
				actor := verified(t, s, e, "cycle-source-operator@example.test")
				if err := m.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
					t.Fatal(err)
				}
				days := int64(30)
				rev := int64(2)
				op, err := m.Subscriptions.CreateAccessOperation(ctx, actor, auth.Account.AccountId, uuid.New(), subscriptions.AccessOperationInput{Kind: "assign_plan", Reason: "Owned same-plan source", PlanId: &root.Quote.PlanId, Revision: &rev, PeriodDays: &days})
				if err != nil {
					t.Fatal(err)
				}
				if err = m.VPN.ApplyAccess(ctx, op.OperationId); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.payments.RecordStarsPayment(ctx, in); err != nil {
				t.Fatal("owned recurring observation", err)
			}
			invalid := mode == "early" || mode == "old-expiry" || mode == "wrong-first" || mode == "disabled" || mode == "ban" || mode == "operator-source"
			if invalid {
				var review bool
				var orders int
				if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM purchase_orders),EXISTS(SELECT 1 FROM purchase_receipts WHERE provider_data->>'charge_id'=$1 AND review_reason IS NOT NULL)`, in.ChargeID).Scan(&orders, &review); err != nil || orders != 1 || !review {
					t.Fatal("invalid cycle lost money or granted access", err)
				}
				return
			}
			child := starsCycleOrder(t, s, auth.Account.AccountId, root.OrderId, in.ChargeID)
			if child.Action != "renew" || child.Quote.PeriodDays != 30 || child.Quote.Revision != 1 || child.Quote.Devices != 2 || child.Quote.TrafficGb != 100 || child.Quote.AmountMinor != "100" || child.StarsCheckout != nil {
				t.Fatal("cycle changed frozen terms or made a child invoice")
			}
			if mode == "late-operator-source" || mode == "late-profile-after-cancel" {
				starsSetter(s, func(context.Context, int64, string, bool) error { return nil })
				if mode == "late-profile-after-cancel" {
					if _, err := s.payments.ControlStarsSubscription(ctx, auth.Account.AccountId, uuid.New(), payments.StarsSubscriptionControlInput{Action: "cancel", Confirmed: true}); err != nil {
						t.Fatal(err)
					}
				}
				actor := renewalOperator(t, s, e)
				days, rev := int64(30), int64(2)
				input := subscriptions.AccessOperationInput{Kind: "assign_plan", Reason: "Owned source after cycle funding", PlanId: &root.Quote.PlanId, Revision: &rev, PeriodDays: &days}
				if mode == "late-profile-after-cancel" {
					profile := "euru"
					input = subscriptions.AccessOperationInput{Kind: "set_profile", Reason: "Owned profile after cancel", Profile: &profile}
				}
				op, err := m.Subscriptions.CreateAccessOperation(ctx, actor, auth.Account.AccountId, uuid.New(), input)
				if err != nil {
					t.Fatal(err)
				}
				if err = m.VPN.ApplyAccess(ctx, op.OperationId); err != nil {
					t.Fatal(err)
				}
				writes := p.updates + p.resets + p.attaches + p.detaches
				if err = s.payments.FulfillPurchase(ctx, child.OrderId); err != nil {
					t.Fatal(err)
				}
				child, err = s.payments.PurchaseOrder(ctx, auth.Account.AccountId, child.OrderId)
				if err != nil || child.FulfillmentStatus != "needs_review" || !child.ReviewRequired || child.AccessOperationId != nil || p.updates+p.resets+p.attaches+p.detaches != writes {
					t.Fatal("late operator change was overwritten by old paid cycle", err)
				}
				return
			}
			if err := s.payments.FulfillPurchase(ctx, child.OrderId); err != nil {
				t.Fatal(err)
			}
			child, err := s.payments.PurchaseOrder(ctx, auth.Account.AccountId, child.OrderId)
			if err != nil || child.AccessOperationId == nil {
				t.Fatal("cycle grant not prepared", err)
			}
			if mode == "pending-partial" {
				if _, err = e.Pool.Exec(ctx, `CREATE FUNCTION fail_cycle_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='purchase' AND NEW.status='applied' THEN RAISE EXCEPTION 'owned cycle partial'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_cycle_finish BEFORE UPDATE ON access_operations FOR EACH ROW EXECUTE FUNCTION fail_cycle_finish()`); err != nil {
					t.Fatal(err)
				}
			}
			starsSetter(s, func(context.Context, int64, string, bool) error { return nil })
			if mode == "paid-client-cancel" {
				if _, err = s.payments.ControlStarsSubscription(ctx, auth.Account.AccountId, uuid.New(), payments.StarsSubscriptionControlInput{Action: "cancel", Confirmed: true}); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.vpn.ApplyAccess(ctx, *child.AccessOperationId); err != nil {
				t.Fatal("cycle native write", err)
			}
			if mode == "pending-partial" {
				e.Advance(30 * 24 * time.Hour)
				third := starsCyclePayment(root.OrderId, e.Clock())
				third.ChargeID = "owned-third-while-partial"
				if err = s.payments.RecordStarsPayment(ctx, third); err != nil {
					t.Fatal(err)
				}
				if count(t, e, "purchase_orders") != 2 || count(t, e, "purchase_receipts") != 3 {
					t.Fatal("partial cycle allowed another grant")
				}
				return
			}
			want := oldExpiry + int64(30*24*time.Hour/time.Millisecond)
			if mode == "gap" {
				want = e.Clock().Add(30 * 24 * time.Hour).UnixMilli()
			}
			if integer(t, p.client["expiryTime"]) != want || p.adds != 1 {
				t.Fatal("cycle doubled/lost access or changed VPN identity")
			}
			if mode == "paid-client-cancel" {
				child, err = s.payments.PurchaseOrder(ctx, auth.Account.AccountId, child.OrderId)
				if err != nil || child.FulfillmentStatus != "applied" || child.ReviewRequired {
					t.Fatal("client cancellation erased already paid cycle", err)
				}
			}
			for range 2 {
				if err = s.payments.RecordStarsPayment(ctx, in); err != nil {
					t.Fatal("cycle replay", err)
				}
			}
			if count(t, e, "purchase_orders") != 2 || count(t, e, "stars_subscription_cycles") != 2 || count(t, e, "purchase_receipts") != 2 {
				t.Fatal("cycle replay duplicated funds/orders")
			}
			var same bool
			if err = e.Pool.QueryRow(ctx, `SELECT vpn_id=$2 AND sub_id=$3 FROM accounts WHERE id=$1`, auth.Account.AccountId, vpnID, subID).Scan(&same); err != nil || !same {
				t.Fatal("cycle changed identifiers", err)
			}
			if mode == "duplicate-period" {
				in.ChargeID = "owned-duplicate-period"
				if err = s.payments.RecordStarsPayment(ctx, in); err != nil {
					t.Fatal(err)
				}
				if count(t, e, "purchase_orders") != 2 || count(t, e, "purchase_receipts") != 3 {
					t.Fatal("duplicate paid period granted twice or lost money")
				}
			}
		})
	}
	t.Run("missing-canonical", func(t *testing.T) {
		_, s, e, auth, plan := starsHTTPFixture(t)
		ctx := context.Background()
		starsSetter(s, func(context.Context, int64, string, bool) error { return nil })
		root, err := s.payments.CreatePurchaseOrder(ctx, auth.Account.AccountId, uuid.New(), payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "telegram_stars", PaymentType: "STARS", PeriodDays: 30, PlanId: plan, Revision: 1, StarsRecurring: true})
		if err != nil {
			t.Fatal(err)
		}
		in := starsCyclePayment(root.OrderId, e.Clock())
		if err = s.payments.RecordStarsPayment(ctx, in); err != nil {
			t.Fatal(err)
		}
		if count(t, e, "purchase_receipts") != 1 || count(t, e, "stars_subscription_cycles") != 0 || count(t, e, "purchase_orders") != 1 {
			t.Fatal("missing first proof granted a cycle")
		}
	})
}

// Catches losing the immutable invoice root or retiring the wrong cycle after a negative event.
func TestStarsRecurringRefund(t *testing.T) {
	for _, mode := range []string{"early", "pending", "partial", "applied", "native-uncertain"} {
		t.Run(mode, func(t *testing.T) {
			_, s, e, auth, m, root, p := starsCycleFixture(t)
			ctx := context.Background()
			e.Advance(30 * 24 * time.Hour)
			in := starsCyclePayment(root.OrderId, e.Clock())
			negative := in
			negative.Recurring = false
			negative.FirstRecurring = false
			negative.SubscriptionExpiresAt = 0
			if mode == "early" {
				if err := s.payments.RecordStarsRefund(ctx, negative); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.payments.RecordStarsPayment(ctx, in); err != nil {
				t.Fatal(err)
			}
			child := starsCycleOrder(t, s, auth.Account.AccountId, root.OrderId, in.ChargeID)
			var before string
			if mode != "early" {
				if err := s.payments.FulfillPurchase(ctx, child.OrderId); err != nil {
					t.Fatal(err)
				}
				var err error
				child, err = s.payments.PurchaseOrder(ctx, auth.Account.AccountId, child.OrderId)
				if err != nil || child.AccessOperationId == nil {
					t.Fatal("refund cycle has no access", err)
				}
				if mode == "partial" {
					if _, err = e.Pool.Exec(ctx, `CREATE FUNCTION fail_cycle_refund_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='purchase' AND NEW.status='applied' THEN RAISE EXCEPTION 'owned cycle refund partial'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_cycle_refund_finish BEFORE UPDATE ON access_operations FOR EACH ROW EXECUTE FUNCTION fail_cycle_refund_finish()`); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "partial" || mode == "applied" {
					if err = s.vpn.ApplyAccess(ctx, *child.AccessOperationId); err != nil {
						t.Fatal(err)
					}
				}
				if err = e.Pool.QueryRow(ctx, `SELECT target::text||':'||completed_steps::text FROM access_operations WHERE id=$1`, *child.AccessOperationId).Scan(&before); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "native-uncertain" {
				actor := verified(t, s, e, "cycle-refund-operator@example.test")
				if err := m.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
					t.Fatal(err)
				}
				calls := 0
				s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) { return "", nil }, Refund: func(_ context.Context, payer int64, charge string) error {
					calls++
					if payer != 701 || charge != in.ChargeID {
						t.Error("cycle refund redirected")
					}
					return errors.New("owned lost payout reply")
				}})
				input := payments.StarsRefundInput{ReceiptOperationId: starsReceipt(t, s, child.OrderId), Reason: "Owned cycle refund", ConfirmFull: true, KeepAccess: true}
				for range 2 {
					out, err := s.payments.RefundStarsPurchase(ctx, actor, auth.Account.AccountId, child.OrderId, uuid.New(), input)
					if err != nil || out.State != "uncertain" {
						t.Fatal("cycle payout uncertainty lost", err)
					}
				}
				if calls != 1 {
					t.Fatal("cycle payout retried blindly")
				}
			}
			if mode != "early" {
				for range 2 {
					if err := s.payments.RecordStarsRefund(ctx, negative); err != nil {
						t.Fatal("cycle negative proof", err)
					}
				}
			}
			var mapped bool
			if err := e.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stars_refunds f JOIN purchase_refunds r ON r.receipt_operation_id=f.receipt_operation_id WHERE f.order_id=$1 AND r.order_id=$2 AND r.source='telegram')`, root.OrderId, child.OrderId).Scan(&mapped); err != nil || !mapped {
				t.Fatal("root/cycle refund provenance lost", err)
			}
			if mode != "early" {
				var status, after string
				if err := e.Pool.QueryRow(ctx, `SELECT status,target::text||':'||completed_steps::text FROM access_operations WHERE id=$1`, *child.AccessOperationId).Scan(&status, &after); err != nil || after != before || mode == "applied" && status != "applied" || mode != "applied" && status != "skipped" {
					t.Fatal("refund erased applied/partial evidence", err)
				}
				writes := p.updates + p.resets
				if err := s.vpn.ApplyAccess(ctx, *child.AccessOperationId); err != nil || p.updates+p.resets != writes || p.disables != 0 {
					t.Fatal("retired cycle wrote panel", err)
				}
			} else if child.AccessOperationId != nil || !child.FullyRefunded {
				t.Fatal("early negative funded a grant")
			}
			var intent bool
			if err := e.Pool.QueryRow(ctx, `SELECT desired_action='cancel' AND NOT bot_canceled FROM stars_subscriptions WHERE root_order_id=$1 AND canonical`, root.OrderId).Scan(&intent); err != nil || !intent {
				t.Fatal("refund inferred native cancellation or lost cancel intent", err)
			}
		})
	}
}

// Catches silently granting Mini renew/change without cancellation, or keeping
// Stars-only accounts stuck at the old first-purchase guard after actual cancel.
func TestStarsRecurringSourceAndHandoff(t *testing.T) {
	for _, action := range []string{"renew", "change_plan"} {
		t.Run(action, func(t *testing.T) {
			h, s, e, auth, m, root, _ := starsCycleFixture(t)
			ctx := context.Background()
			starsSetter(s, func(context.Context, int64, string, bool) error { return nil })
			target := root.Quote.PlanId
			rev := int64(1)
			if action == "change_plan" {
				actor := verified(t, s, e, "cycle-change-operator@example.test")
				if err := m.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
					t.Fatal(err)
				}
				terms := catalogueTerms(3)
				terms.Prices[2].AmountMinor = "150"
				plan, err := s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: terms, Reason: "Owned target"})
				if err != nil {
					t.Fatal(err)
				}
				target = plan.PlanId
			}
			input := map[string]any{"action": action, "payment_method": "telegram_stars", "payment_type": "STARS", "period_days": 30, "plan_id": target, "revision": rev}
			if action == "change_plan" {
				input["source_access_operation_id"] = *root.AccessOperationId
			}
			body, _ := json.Marshal(input)
			if r := starsRequest(h, s, auth, "POST", "/api/v1/orders", string(body), uuid.New()); r.Code < 400 {
				t.Fatal("active recurrence allowed a second checkout")
			}
			if _, err := s.payments.ControlStarsSubscription(ctx, auth.Account.AccountId, uuid.New(), payments.StarsSubscriptionControlInput{Action: "cancel", Confirmed: true}); err != nil {
				t.Fatal(err)
			}
			if action == "renew" {
				if _, err := s.payments.RenewalOffer(ctx, auth.Account.AccountId); err != nil {
					t.Fatal("Mini renewal offer refused", err)
				}
			}
			if action == "change_plan" {
				if _, err := s.payments.PlanChangeContext(ctx, auth.Account.AccountId); err != nil {
					t.Fatal("Mini change context refused", err)
				}
			}
			r := starsRequest(h, s, auth, "POST", "/api/v1/orders", string(body), uuid.New())
			if r.Code != 201 {
				t.Fatal("Mini one-shot managing checkout refused", r.Code)
			}
			var order payments.PurchaseOrder
			if json.Unmarshal(r.Body.Bytes(), &order) != nil {
				t.Fatal("managing order body")
			}
			if order.Quote.StarsRecurring {
				t.Fatal("managing checkout silently recurring")
			}
			state, err := s.payments.StarsSubscription(ctx, auth.Account.AccountId)
			if err != nil || state.CanResume {
				t.Fatal("pending one-shot checkout allowed double billing", err)
			}
			if _, err = s.payments.ControlStarsSubscription(ctx, auth.Account.AccountId, uuid.New(), payments.StarsSubscriptionControlInput{Action: "resume", Confirmed: true}); err == nil {
				t.Fatal("pending one-shot checkout resumed recurrence")
			}
			allowed, err := s.payments.CheckStarsPreCheckout(ctx, payments.StarsPreCheckoutInput{BotID: 123, PayerID: 701, Payload: "stars:v1:" + order.OrderId.String(), Currency: "XTR", Amount: 100})
			if action == "change_plan" {
				allowed, err = s.payments.CheckStarsPreCheckout(ctx, payments.StarsPreCheckoutInput{BotID: 123, PayerID: 701, Payload: "stars:v1:" + order.OrderId.String(), Currency: "XTR", Amount: 150})
			}
			if err != nil || !allowed {
				t.Fatal("native managing pre-checkout refused", err)
			}
			in := starsPayment(order.OrderId, e.Clock())
			in.ChargeID = "owned-one-shot-" + action
			in.Amount = 100
			if action == "change_plan" {
				in.Amount = 150
			}
			if err := s.payments.RecordStarsPayment(ctx, in); err != nil {
				t.Fatal(err)
			}
			if err := s.payments.FulfillPurchase(ctx, order.OrderId); err != nil {
				t.Fatal(err)
			}
			order, err = s.payments.PurchaseOrder(ctx, auth.Account.AccountId, order.OrderId)
			if err != nil || order.AccessOperationId == nil {
				t.Fatal("one-shot managing grant missing", err)
			}
			if err = s.vpn.ApplyAccess(ctx, *order.AccessOperationId); err != nil {
				t.Fatal(err)
			}
			state, err = s.payments.StarsSubscription(ctx, auth.Account.AccountId)
			if err != nil || state.CanResume {
				t.Fatal("old recurrence resumed after one-shot source changed", err)
			}
		})
	}
	// The shared billing proof must open every configured external checkout,
	// keep the same account, and close resume while any such order is unresolved.
	for _, method := range []string{"yoomoney", "manual", "yookassa", "cryptomus", "heleket"} {
		t.Run(method, func(t *testing.T) {
			h, s, e, auth, m, root, p := starsCycleFixture(t)
			ctx := context.Background()
			session := starsWebLogin(t, h, s, e, auth, m)
			starsSetter(s, func(context.Context, int64, string, bool) error { return nil })
			s.cfg.Payments.ManualEnabled, s.cfg.Payments.ManualCardDetails = true, "Owned transfer instructions"
			s.cfg.Payments.YooMoneyEnabled, s.cfg.Payments.YooKassaEnabled = true, true
			s.cfg.Payments.YooMoneyNotificationSecret = []byte("owned-test-notification-secret")
			s.cfg.Payments.YooKassaShopID, s.cfg.Payments.ShopEmail = "100001", "receipts@example.test"
			s.cfg.Payments.CryptomusEnabled, s.cfg.Payments.HeleketEnabled = true, true
			s.cfg.Payments.CryptomusMerchantID, s.cfg.Payments.CryptomusAPIKey = "00000000-0000-4000-8000-000000000001", "owned-test-key"
			s.cfg.Payments.HeleketMerchantID, s.cfg.Payments.HeleketAPIKey = "00000000-0000-4000-8000-000000000002", "owned-test-key"
			terms := catalogueTerms(2)
			terms.Prices[0].AmountMinor, terms.Prices[2].AmountMinor = "10000", "100"
			raw, _ := json.Marshal(terms)
			if _, err := e.Pool.Exec(ctx, `INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,2,$2,false,'legacy_import',$3)`, root.Quote.PlanId, raw, e.Clock()); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(ctx, `UPDATE catalogue_plans SET current_revision=2 WHERE id=$1`, root.Quote.PlanId); err != nil {
				t.Fatal(err)
			}
			input := payments.PurchaseOrderInput{Action: "renew", PaymentMethod: method, PaymentType: map[string]string{"yoomoney": "AC", "manual": "MANUAL", "yookassa": "YOOKASSA", "cryptomus": "CRYPTOMUS", "heleket": "HELEKET"}[method], PeriodDays: 30, PlanId: root.Quote.PlanId, Revision: 2}
			if _, err := s.payments.CreatePurchaseOrder(ctx, session.id, uuid.New(), input); err == nil {
				t.Fatal("active recurrence opened external billing")
			}
			if _, err := s.payments.ControlStarsSubscription(ctx, session.id, uuid.New(), payments.StarsSubscriptionControlInput{Action: "cancel", Confirmed: true}); err != nil {
				t.Fatal(err)
			}
			order, err := s.payments.CreatePurchaseOrder(ctx, session.id, uuid.New(), input)
			if err != nil || order.PaymentMethod != method || order.Action != "renew" || session.id != auth.Account.AccountId {
				t.Fatal("proved cancellation failed external handoff", err)
			}
			if _, err = s.payments.ControlStarsSubscription(ctx, session.id, uuid.New(), payments.StarsSubscriptionControlInput{Action: "resume", Confirmed: true}); err == nil {
				t.Fatal("pending external order resumed recurrence")
			}
			if method == "yoomoney" {
				if _, err = s.payments.CancelPurchaseOrder(ctx, session.id, order.OrderId, uuid.New()); err != nil {
					t.Fatal(err)
				}
				if _, err = s.payments.ControlStarsSubscription(ctx, session.id, uuid.New(), payments.StarsSubscriptionControlInput{Action: "resume", Confirmed: true}); err != nil {
					t.Fatal(err)
				}
				writes := p.updates + p.resets
				fields := purchaseNotice(s, order.OrderId, uuid.NewString(), "99.00", "100.00")
				if err = s.receiveYooMoney(ctx, fields); err != nil {
					t.Fatal(err)
				}
				if err = s.payments.FulfillPurchase(ctx, order.OrderId); err != nil {
					t.Fatal(err)
				}
				order, err = s.payments.PurchaseOrder(ctx, session.id, order.OrderId)
				if err != nil || !order.ReviewRequired || order.PaymentStatus != "paid" || order.AccessOperationId != nil || p.updates+p.resets != writes || count(t, e, "purchase_receipts") != 2 {
					t.Fatal("late external money bypassed latest resume or disappeared", err)
				}
			}
		})
	}
	t.Run("starter-does-not-reopen-first-Stars-purchase", func(t *testing.T) {
		_, s, e, auth, m, root, _ := starsCycleFixture(t)
		ctx := context.Background()
		starsSetter(s, func(context.Context, int64, string, bool) error { return nil })
		actor := renewalOperator(t, s, e)
		op, err := m.Subscriptions.CreateAccessOperation(ctx, actor, auth.Account.AccountId, uuid.New(), subscriptions.AccessOperationInput{Kind: "starter_trial", Reason: "Owned starter clearing"})
		if err != nil {
			t.Fatal(err)
		}
		if err = m.VPN.ApplyAccess(ctx, op.OperationId); err != nil {
			t.Fatal(err)
		}
		if err = s.payments.ReconcileStarsSubscriptions(ctx); err != nil {
			t.Fatal(err)
		}
		state, err := s.payments.StarsSubscription(ctx, auth.Account.AccountId)
		if err != nil || state.CanResume {
			t.Fatal("starter resumed old recurrence", err)
		}
		if _, err = s.payments.CreatePurchaseOrder(ctx, auth.Account.AccountId, uuid.New(), payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "telegram_stars", PaymentType: "STARS", PeriodDays: 30, PlanId: root.Quote.PlanId, Revision: 1}); err == nil {
			t.Fatal("starter clearing bypassed first-purchase Stars guard")
		}
	})
}
