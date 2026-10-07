package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Absence of migrated recurrence data must not turn a Telegram/legacy account
// into an independent external payer, including the old purchase action.
func TestRenewalUnknownBillingBlocksExistingPurchase(t *testing.T) {
	for _, column := range []string{"telegram_id", "legacy_user_id"} {
		t.Run(column, func(t *testing.T) {
			s, e, account, plan := purchaseFixture(t)
			ctx := context.Background()
			if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET "+column+"=1234567 WHERE id=$1", account); err != nil {
				t.Fatal(err)
			}
			if _, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan)); !catalogueCode(err, "EXTERNAL_BILLING_UNVERIFIED") {
				t.Fatal("unknown Stars billing accepted an external order", err)
			}
			if count(t, e, "purchase_orders") != 0 || count(t, e, "purchase_receipts") != 0 {
				t.Fatal("billing refusal wrote money")
			}
		})
	}
}

func TestRenewalLateBillingGuardProtectsCheckoutAndPhysicalWrite(t *testing.T) {
	for _, phase := range []string{"checkout", "paid", "target", "reconcile"} {
		t.Run(phase, func(t *testing.T) {
			s, e, account, plan := purchaseFixture(t)
			p := panelFixture(t, s)
			ctx := context.Background()
			order, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
			if err != nil {
				t.Fatal(err)
			}
			if phase != "checkout" {
				if err = s.receiveYooMoney(ctx, purchaseNotice(s, order.OrderId, uuid.NewString(), "90071992547409.00", "90071992547409.93")); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "target" {
				if err = s.fulfillPurchase(ctx, order.OrderId); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET telegram_id=1234567 WHERE id=$1", account); err != nil {
				t.Fatal(err)
			}
			current, err := s.purchaseOrder(ctx, account, order.OrderId)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "checkout" {
				if current.CanPay || current.Checkout != nil {
					t.Fatal("fresh checkout ignored unknown linked billing")
				}
				return
			}
			if phase == "paid" || phase == "reconcile" {
				err = s.fulfillPurchase(ctx, order.OrderId)
			} else {
				if current.AccessOperationId == nil {
					t.Fatal("prepared target missing")
				}
				err = s.applyAccess(ctx, *current.AccessOperationId)
			}
			if err != nil {
				t.Fatal(err)
			}
			current, err = s.purchaseOrder(ctx, account, order.OrderId)
			if err != nil || current.PaymentStatus != "paid" || current.FulfillmentStatus != "needs_review" || !current.ReviewRequired || p.adds != 0 || p.updates != 0 || p.resets != 0 || count(t, e, "purchase_receipts") != 1 {
				t.Fatal("late billing guard lost funds or wrote VPN", err)
			}
			if phase == "reconcile" {
				actor := verified(t, s, e, "renewal-reconcile-operator@example.test")
				if err = s.changeOperatorRole(ctx, actor, true); err != nil {
					t.Fatal(err)
				}
				jobs := count(t, e, "river_job")
				if _, err = s.reconcilePurchaseOrder(ctx, actor, account, order.OrderId, uuid.New(), wire.PurchaseReconcileInput{Reason: "billing is still unknown"}); !catalogueCode(err, "PURCHASE_ORDER_CONFLICT") {
					t.Fatal("reconcile requeued a funded order with unknown billing", err)
				}
				current, err = s.purchaseOrder(ctx, account, order.OrderId)
				if err != nil || current.FulfillmentStatus != "needs_review" || count(t, e, "river_job") != jobs {
					t.Fatal("refused reconcile changed paid state or queued work", err)
				}
			}
		})
	}
}

func renewalFixture(t *testing.T) (*regressionFixture, *testkit.Env, *fakePanel, uuid.UUID, uuid.UUID) {
	t.Helper()
	s, e, account, plan := purchaseFixture(t)
	p := panelFixture(t, s)
	order, err := s.createPurchaseOrder(context.Background(), account, uuid.New(), purchaseInput(plan))
	if err != nil {
		t.Fatal(err)
	}
	completeRenewalPayment(t, s, account, order)
	return s, e, p, account, plan
}

func completeRenewalPayment(t *testing.T, s *regressionFixture, account uuid.UUID, order wire.PurchaseOrder) wire.PurchaseOrder {
	t.Helper()
	ctx := context.Background()
	fields := purchaseNotice(s, order.OrderId, uuid.NewString(), "90071992547409.00", order.Quote.AmountMinor[:len(order.Quote.AmountMinor)-2]+"."+order.Quote.AmountMinor[len(order.Quote.AmountMinor)-2:])
	fields.Set("datetime", s.now().Add(time.Minute).UTC().Format(time.RFC3339))
	fields.Set("sign", yooMoneySignature(fields, s.cfg.Payments.YooMoneyNotificationSecret))
	for range 2 {
		if err := s.receiveYooMoney(ctx, fields); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := s.fulfillPurchase(ctx, order.OrderId); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := s.purchaseOrder(ctx, account, order.OrderId)
	if err != nil || prepared.AccessOperationId == nil {
		t.Fatal("funded target not retained", err)
	}
	for range 2 {
		if err = s.applyAccess(ctx, *prepared.AccessOperationId); err != nil {
			t.Fatal(err)
		}
	}
	applied, err := s.purchaseOrder(ctx, account, order.OrderId)
	if err != nil || applied.FulfillmentStatus != "applied" {
		t.Fatal("access did not publish applied outcome", err)
	}
	return applied
}

// Losing action=renew, adding from now for active access, or repeating a funded
// native write breaks a customer-visible result on real module/DB boundaries.
func TestRenewalActiveExpiredExhaustedAndRepeat(t *testing.T) {
	for _, mode := range []string{"active", "expired", "exhausted"} {
		t.Run(mode, func(t *testing.T) {
			s, e, p, account, plan := renewalFixture(t)
			ctx := context.Background()
			identity, err := s.accountByID(ctx, account)
			if err != nil {
				t.Fatal(err)
			}
			wantExpiry := e.Clock().Add(60 * 24 * time.Hour).UnixMilli()
			p.up = 9876
			if mode == "expired" {
				s.now = func() time.Time { return e.Clock().Add(31 * 24 * time.Hour) }
				p.client["enable"] = false
				wantExpiry = e.Clock().Add(61 * 24 * time.Hour).UnixMilli()
			}
			if mode == "exhausted" {
				p.up = 100 * 1024 * 1024 * 1024
				p.client["enable"] = false
			}
			in := purchaseInput(plan)
			in.Action = "renew"
			in.PaymentType = "PC"
			key := uuid.New()
			order, err := s.createPurchaseOrder(ctx, account, key, in)
			if err != nil {
				t.Fatal("renewal of applied finite access refused", err)
			}
			if order.Action != "renew" || order.Checkout == nil || !order.CanPay || order.Checkout.Fields.PaymentType != "PC" {
				t.Fatal("renewal checkout lost its purpose or method")
			}
			replay, err := s.createPurchaseOrder(ctx, account, key, in)
			if err != nil || replay.OrderId != order.OrderId {
				t.Fatal("lost response created a new renewal", err)
			}
			mismatch := in
			mismatch.PeriodDays = 31
			if _, err = s.createPurchaseOrder(ctx, account, key, mismatch); !catalogueCode(err, "IDEMPOTENCY_CONFLICT") {
				t.Fatal("one key accepted another body", err)
			}
			current, err := s.currentPurchaseOrder(ctx, account)
			if err != nil || current.Order == nil || current.Order.OrderId != order.OrderId {
				t.Fatal("old applied order hid current renewal", err)
			}
			if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), in); !catalogueCode(err, "PURCHASE_ORDER_CONFLICT") {
				t.Fatal("two pending renewals allowed", err)
			}
			applied := completeRenewalPayment(t, s, account, order)
			if applied.Action != "renew" || p.resets != 2 || p.up != 0 || integer(t, p.client["expiryTime"]) != wantExpiry || p.client["enable"] != true || integer(t, p.client["limitIp"]) != 3 || integer(t, p.client["totalGB"]) != 100*1024*1024*1024 {
				t.Fatal("renewal duplicated reset, lost duration or failed activation")
			}
			after, err := s.accountByID(ctx, account)
			if err != nil || after.VpnID != identity.VpnID || after.SubID != identity.SubID || after.PanelKey != identity.PanelKey || after.AssignedPanelID == nil || *after.AssignedPanelID != *identity.AssignedPanelID {
				t.Fatal("renewal moved existing VPN identity/server", err)
			}
			var orders, receipts, operations int
			if err = e.Pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM purchase_orders WHERE account_id=$1),(SELECT count(*) FROM purchase_receipts r JOIN purchase_orders o ON o.id=r.order_id WHERE o.account_id=$1),(SELECT count(*) FROM access_operations WHERE account_id=$1)", account).Scan(&orders, &receipts, &operations); err != nil || orders != 2 || receipts != 2 || operations != 2 {
				t.Fatal("repeats manufactured money or access", err)
			}
			if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), in); err != nil {
				t.Fatal("completed renewal blocks another period", err)
			}
		})
	}
}

func renewalOperator(t *testing.T, s *regressionFixture, e *testkit.Env) uuid.UUID {
	t.Helper()
	id := verified(t, s, e, "renewal-operator@example.test")
	if err := s.changeOperatorRole(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
	return id
}

// A hidden own plan must remain payable; catalogue changes after quoting must
// not rewrite paid terms. A foreign plan must never become a renewal offer.
func TestRenewalEligibilityAndSnapshot(t *testing.T) {
	t.Run("hidden own and frozen archive", func(t *testing.T) {
		s, e, p, account, plan := renewalFixture(t)
		ctx := context.Background()
		actor := renewalOperator(t, s, e)
		other, err := s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: catalogueTerms(3), Reason: "second visible offer"})
		if err != nil {
			t.Fatal(err)
		}
		terms := catalogueTerms(2)
		terms.Hidden = true
		terms.Prices[0].AmountMinor = "9007199254740993"
		if _, err = s.reviseCataloguePlan(ctx, actor, plan, uuid.New(), wire.CataloguePlanRevisionInput{ExpectedRevision: 1, Terms: terms, Reason: "hide own plan"}); err != nil {
			t.Fatal(err)
		}
		offer, err := s.payments.RenewalOffer(ctx, account)
		if err != nil || offer.PlanId != plan || !offer.Hidden || offer.Revision != 2 {
			t.Fatal("own hidden plan lost", err)
		}
		visible, err := s.catalogue(ctx, account)
		if err != nil || len(visible.Plans) != 1 || visible.Plans[0].PlanId != other.PlanId {
			t.Fatal("renewal disclosed hidden catalogue entries", err)
		}
		in := purchaseInput(plan)
		in.Action = "renew"
		if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), in); !catalogueCode(err, "PURCHASE_PLAN_CONFLICT") {
			t.Fatal("stale renewal revision accepted", err)
		}
		in.PlanId = other.PlanId
		if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), in); !catalogueCode(err, "RENEWAL_NOT_ELIGIBLE") {
			t.Fatal("another plan became a renewal", err)
		}
		in.PlanId, in.Revision = plan, 2
		in.PeriodDays = 31
		if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), in); !catalogueCode(err, "PURCHASE_PLAN_CONFLICT") {
			t.Fatal("unsupported renewal period accepted", err)
		}
		in.PeriodDays = 30
		order, err := s.createPurchaseOrder(ctx, account, uuid.New(), in)
		if err != nil {
			t.Fatal(err)
		}
		terms.TrafficGb = 20
		terms.Prices[0].AmountMinor = "10000"
		if _, err = s.reviseCataloguePlan(ctx, actor, plan, uuid.New(), wire.CataloguePlanRevisionInput{ExpectedRevision: 2, Terms: terms, Reason: "later offer"}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.archiveCataloguePlan(ctx, actor, plan, uuid.New(), wire.CataloguePlanArchiveInput{ExpectedRevision: 3, Reason: "archive later"}); err != nil {
			t.Fatal(err)
		}
		before := integer(t, p.client["expiryTime"])
		applied := completeRenewalPayment(t, s, account, order)
		if applied.Quote.Revision != 2 || applied.Quote.AmountMinor != "9007199254740993" || applied.Quote.TrafficGb != 100 || integer(t, p.client["totalGB"]) != 100*1024*1024*1024 || integer(t, p.client["expiryTime"]) != before+30*24*60*60*1000 {
			t.Fatal("catalogue edit changed paid renewal")
		}
		if _, err = s.payments.RenewalOffer(ctx, account); status(paymentError(err)) != 409 {
			t.Fatal("archived plan offered new renewal", err)
		}
	})
	for _, mode := range []string{"ban", "restricted", "unlimited", "telegram", "legacy", "other server", "native identity", "native missing", "native groups", "native perpetual", "unknown disabled", "zero RUB", "archive", "disabled methods"} {
		t.Run(mode, func(t *testing.T) {
			s, e, p, account, plan := renewalFixture(t)
			ctx := context.Background()
			in := purchaseInput(plan)
			in.Action = "renew"
			var err error
			switch mode {
			case "ban":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET vpn_banned=true WHERE id=$1", account)
			case "restricted":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", account)
			case "unlimited":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET access_profile='unlimited' WHERE id=$1", account)
			case "telegram":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET telegram_id=1234567 WHERE id=$1", account)
			case "legacy":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET legacy_user_id=1234567 WHERE id=$1", account)
			case "other server":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET assigned_panel_id='other' WHERE id=$1", account)
			case "native identity":
				p.client["id"] = uuid.NewString()
			case "native missing":
				p.client = nil
			case "native groups":
				p.ids = []int64{99}
			case "native perpetual":
				p.client["expiryTime"] = json.Number("0")
			case "unknown disabled":
				p.client["enable"] = false
			case "zero RUB", "archive":
				actor := renewalOperator(t, s, e)
				if _, err = s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: catalogueTerms(3), Reason: "second visible offer"}); err != nil {
					t.Fatal(err)
				}
				if mode == "archive" {
					_, err = s.archiveCataloguePlan(ctx, actor, plan, uuid.New(), wire.CataloguePlanArchiveInput{ExpectedRevision: 1, Reason: "retire"})
				} else {
					_, err = s.reviseCataloguePlan(ctx, actor, plan, uuid.New(), wire.CataloguePlanRevisionInput{ExpectedRevision: 1, Terms: catalogueTerms(2), Reason: "remove RUB offer"})
				}
				in.Revision = 2
			}
			if err != nil {
				t.Fatal(err)
			}
			updates, resets, jobs := p.updates, p.resets, count(t, e, "river_job")
			if mode == "disabled methods" {
				for _, method := range []string{"manual", "yookassa", "cryptomus", "heleket"} {
					in.PaymentMethod = wire.PurchaseOrderInputPaymentMethod(method)
					in.PaymentType = wire.PurchaseOrderInputPaymentType(map[string]string{"manual": "MANUAL", "yookassa": "YOOKASSA", "cryptomus": "CRYPTOMUS", "heleket": "HELEKET"}[method])
					if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), in); !catalogueCode(err, "PAYMENT_METHOD_UNAVAILABLE") {
						t.Fatal("disabled provider accepted renewal", err)
					}
				}
			} else if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), in); err == nil {
				t.Fatal("ineligible renewal created a checkout")
			}
			if count(t, e, "purchase_orders") != 1 || count(t, e, "purchase_receipts") != 1 || count(t, e, "river_job") != jobs || p.updates != updates || p.resets != resets {
				t.Fatal("refused renewal changed money or access")
			}
		})
	}
}

func clearPurchasePlan(t *testing.T, s *regressionFixture, actor, account uuid.UUID, transition string) {
	t.Helper()
	ctx := context.Background()
	if transition == "starter" {
		op, err := s.createAccessOperation(ctx, actor, account, uuid.New(), wire.AccessOperationInput{Kind: "starter_trial", Reason: "support starter"})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.applyAccess(ctx, op.OperationId); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, _, err := s.seedUnlimitedCatalogue(ctx); err != nil {
			t.Fatal(err)
		}
		for _, profile := range []string{"unlimited", "regular"} {
			op, err := s.createAccessOperation(ctx, actor, account, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput(profile), Reason: "support transition"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.applyAccess(ctx, op.OperationId); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Compensation/reset/ban preserve the selected plan; explicit starter issuance
// and unlimited revocation must not resurrect the older paid plan.
func TestRenewalPlanProvenance(t *testing.T) {
	for _, transition := range []string{"starter", "unlimited revoke"} {
		t.Run(transition, func(t *testing.T) {
			s, e, p, account, plan := renewalFixture(t)
			ctx := context.Background()
			actor := renewalOperator(t, s, e)
			banned, unbanned := true, false
			for _, input := range []wire.AccessOperationInput{{Kind: "compensate", Days: ptrInt(2), Reason: "support correction"}, {Kind: "reset_traffic", Reason: "support reset"}, {Kind: "set_vpn_ban", VpnBanned: &banned, Reason: "hold"}, {Kind: "set_vpn_ban", VpnBanned: &unbanned, Reason: "release"}} {
				op, err := s.createAccessOperation(ctx, actor, account, uuid.New(), input)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.applyAccess(ctx, op.OperationId); err != nil {
					t.Fatal(err)
				}
				selected, err := s.vpn.CurrentPlanIDTx(ctx, nil, account)
				if err != nil || selected == nil || *selected != plan {
					t.Fatal("non-plan operation lost current plan", err)
				}
			}
			if offer, err := s.payments.RenewalOffer(ctx, account); err != nil || offer.PlanId != plan {
				t.Fatal("preserved plan no longer offered", err)
			}
			if _, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan)); !catalogueCode(err, "PURCHASE_NOT_ELIGIBLE") {
				t.Fatal("current paid plan bypassed C17 through first purchase", err)
			}
			clearPurchasePlan(t, s, actor, account, transition)
			selected, err := s.vpn.CurrentPlanIDTx(ctx, nil, account)
			if err != nil || selected != nil {
				t.Fatal("starter transition resurrected paid plan", err)
			}
			if _, err = s.payments.RenewalOffer(ctx, account); status(paymentError(err)) != 409 {
				t.Fatal("starter-only access offered renewal", err)
			}
			current, err := s.currentPurchaseOrder(ctx, account)
			if err != nil || current.CanPurchase == nil || !*current.CanPurchase || current.Order == nil || current.Order.FulfillmentStatus != "applied" {
				t.Fatal("client API kept the historical paid-order block after clearing", err)
			}
			identity, err := s.accountByID(ctx, account)
			if err != nil {
				t.Fatal(err)
			}
			updates, resets := p.updates, p.resets
			order, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
			if err != nil {
				t.Fatal("proved trial clearing blocked C10 purchase", err)
			}
			applied := completeRenewalPayment(t, s, account, order)
			after, err := s.accountByID(ctx, account)
			if err != nil || applied.Action != "purchase" || after.PanelKey != identity.PanelKey || after.VpnID != identity.VpnID || after.SubID != identity.SubID || after.AssignedPanelID == nil || identity.AssignedPanelID == nil || *after.AssignedPanelID != *identity.AssignedPanelID || p.updates != updates+1 || p.resets != resets+1 {
				t.Fatal("C10 after clearing lost purpose/identity or repeated native writes", err)
			}
			if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan)); !catalogueCode(err, "PURCHASE_NOT_ELIGIBLE") {
				t.Fatal("new paid plan allowed another first purchase", err)
			}
			current, err = s.currentPurchaseOrder(ctx, account)
			if err != nil || current.CanPurchase == nil || *current.CanPurchase {
				t.Fatal("client API released a current paid-plan block", err)
			}
			if offer, err := s.payments.RenewalOffer(ctx, account); err != nil || offer.PlanId != plan {
				t.Fatal("new purchase did not restore normal renewal", err)
			}
		})
	}
}

// A late account/native change must retain the receipt and stop both target
// preparation and the physical write, rather than undo a ban during reset.
func TestRenewalLateGuards(t *testing.T) {
	for _, phase := range []string{"paid", "target"} {
		for _, mode := range []string{"ban", "restriction", "identity", "unknown disabled"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				s, e, p, account, plan := renewalFixture(t)
				ctx := context.Background()
				in := purchaseInput(plan)
				in.Action = "renew"
				order, err := s.createPurchaseOrder(ctx, account, uuid.New(), in)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.receiveYooMoney(ctx, purchaseNotice(s, order.OrderId, uuid.NewString(), "90071992547409.00", "90071992547409.93")); err != nil {
					t.Fatal(err)
				}
				if phase == "target" {
					if err = s.fulfillPurchase(ctx, order.OrderId); err != nil {
						t.Fatal(err)
					}
				}
				switch mode {
				case "ban":
					_, err = e.Pool.Exec(ctx, "UPDATE accounts SET vpn_banned=true WHERE id=$1", account)
				case "restriction":
					_, err = e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", account)
				case "identity":
					p.client["id"] = uuid.NewString()
				case "unknown disabled":
					p.client["enable"] = false
				}
				if err != nil {
					t.Fatal(err)
				}
				updates, resets := p.updates, p.resets
				if phase == "paid" {
					err = s.fulfillPurchase(ctx, order.OrderId)
				} else {
					current, e := s.purchaseOrder(ctx, account, order.OrderId)
					if e != nil || current.AccessOperationId == nil {
						t.Fatal("target missing", e)
					}
					err = s.applyAccess(ctx, *current.AccessOperationId)
				}
				if err != nil {
					t.Fatal(err)
				}
				current, err := s.purchaseOrder(ctx, account, order.OrderId)
				if err != nil || current.PaymentStatus != "paid" || current.FulfillmentStatus != "needs_review" || !current.ReviewRequired || p.updates != updates || p.resets != resets || count(t, e, "purchase_receipts") != 2 {
					t.Fatal("late change lost funds or wrote access", err)
				}
			})
		}
	}
	t.Run("different assigned plan", func(t *testing.T) {
		s, e, p, account, plan := renewalFixture(t)
		ctx := context.Background()
		actor := renewalOperator(t, s, e)
		other, err := s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: catalogueTerms(3), Reason: "different offer"})
		if err != nil {
			t.Fatal(err)
		}
		in := purchaseInput(plan)
		in.Action = "renew"
		order, err := s.createPurchaseOrder(ctx, account, uuid.New(), in)
		if err != nil {
			t.Fatal(err)
		}
		period := int64(30)
		op, err := s.createAccessOperation(ctx, actor, account, uuid.New(), wire.AccessOperationInput{Kind: "assign_plan", PlanId: &other.PlanId, Revision: &other.Revision, PeriodDays: &period, Reason: "support assignment"})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.applyAccess(ctx, op.OperationId); err != nil {
			t.Fatal(err)
		}
		fresh, err := s.purchaseOrder(ctx, account, order.OrderId)
		if err != nil || fresh.CanPay || fresh.Checkout != nil {
			t.Fatal("fresh checkout ignored new assigned plan", err)
		}
		updates, resets := p.updates, p.resets
		if err = s.receiveYooMoney(ctx, purchaseNotice(s, order.OrderId, uuid.NewString(), "90071992547409.00", "90071992547409.93")); err != nil {
			t.Fatal(err)
		}
		if err = s.fulfillPurchase(ctx, order.OrderId); err != nil {
			t.Fatal(err)
		}
		current, err := s.purchaseOrder(ctx, account, order.OrderId)
		if err != nil || current.PaymentStatus != "paid" || current.FulfillmentStatus != "needs_review" || current.AccessOperationId != nil || p.updates != updates || p.resets != resets || count(t, e, "purchase_receipts") != 2 {
			t.Fatal("late assignment overwrote plan or lost funds", err)
		}
	})
}

func TestRenewalHTTPBoundary(t *testing.T) {
	s, e, _, account, plan := renewalFixture(t)
	ctx := context.Background()
	h := New(app.NewModules(s.pool, s.limiter, s.queue, s.cfg), s.pool, s.cfg.HTTP)
	customer := manualLogin(t, h, *s.cfg, "purchase@example.test")
	if customer.id != account {
		t.Fatal("wrong fixture account")
	}
	if r := supportRequest(h, nil, "GET", "/api/v1/subscription/renewal", "", nil, "", uuid.Nil); r.Code != 401 {
		t.Fatal("anonymous renewal offer", r.Code)
	}
	r := supportRequest(h, &customer, "GET", "/api/v1/subscription/renewal", "", nil, "", uuid.Nil)
	var offer wire.CataloguePlanSnapshot
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &offer) != nil || offer.PlanId != plan || strings.Contains(r.Body.String(), "panel_key") || strings.Contains(r.Body.String(), "sub_id") {
		t.Fatal("renewal offer failed ownership/minimal DTO", r.Code)
	}
	in := purchaseInput(plan)
	in.Action = "renew"
	good, _ := json.Marshal(in)
	noCSRF := customer
	noCSRF.csrf = ""
	if r = supportRequest(h, &noCSRF, "POST", "/api/v1/orders", "application/json", good, s.cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 403 {
		t.Fatal("missing renewal CSRF", r.Code)
	}
	for _, tc := range []struct {
		body, origin string
		key          uuid.UUID
		want         int
	}{
		{string(good), "https://attacker.example.test", uuid.New(), 403},
		{string(good), s.cfg.HTTP.CabinetOrigin, uuid.Nil, 400},
		{strings.Replace(string(good), "\"renew\"", "\"forged\"", 1), s.cfg.HTTP.CabinetOrigin, uuid.New(), 400},
		{string(good[:len(good)-1]) + ",\"account_id\":\"" + uuid.NewString() + "\"}", s.cfg.HTTP.CabinetOrigin, uuid.New(), 400},
		{strings.Repeat("a", 16385), s.cfg.HTTP.CabinetOrigin, uuid.New(), 400},
	} {
		if r = supportRequest(h, &customer, "POST", "/api/v1/orders", "application/json", []byte(tc.body), tc.origin, tc.key); r.Code != tc.want {
			t.Fatal("renewal boundary", r.Code, tc.want)
		}
	}
	r = supportRequest(h, &customer, "POST", "/api/v1/orders", "application/json", good, s.cfg.HTTP.CabinetOrigin, uuid.New())
	var order wire.PurchaseOrder
	if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &order) != nil || order.Action != "renew" {
		t.Fatal("real HTTP renewal not created", r.Code)
	}
	foreign := supportLogin(t, h, e, *s.cfg, "renewal-foreign@example.test")
	if r = supportRequest(h, &foreign, "GET", "/api/v1/orders/"+order.OrderId.String(), "", nil, "", uuid.Nil); r.Code != 404 {
		t.Fatal("foreign renewal order disclosed", r.Code)
	}
	if r = supportRequest(h, &foreign, "GET", "/api/v1/subscription/renewal", "", nil, "", uuid.Nil); r.Code != 409 {
		t.Fatal("foreign account received own paid plan", r.Code)
	}
	current, err := s.currentPurchaseOrder(ctx, account)
	if err != nil || current.Order == nil || current.Order.OrderId != order.OrderId {
		t.Fatal("HTTP renewal lost behind paid history", err)
	}
}

func TestRenewalActionMigration(t *testing.T) {
	for _, mode := range []string{"purchase default", "renewal retained"} {
		t.Run(mode, func(t *testing.T) {
			s, e, _, account, plan := renewalFixture(t)
			ctx := context.Background()
			database := stdlib.OpenDBFromPool(e.Pool)
			t.Cleanup(func() { database.Close() })
			provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("../../db/migrations"))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "purchase default" {
				var before, after string
				if err = e.Pool.QueryRow(ctx, "SELECT (to_jsonb(p)-'action')::text FROM purchase_orders p WHERE account_id=$1", account).Scan(&before); err != nil {
					t.Fatal(err)
				}
				if _, err = provider.DownTo(ctx, 19); err != nil {
					t.Fatal(err)
				}
				if err = e.Pool.QueryRow(ctx, "SELECT to_jsonb(p)::text FROM purchase_orders p WHERE account_id=$1", account).Scan(&after); err != nil || after != before {
					t.Fatal("downgrade changed original money/proof", err)
				}
				if _, err = provider.Up(ctx); err != nil {
					t.Fatal(err)
				}
				var action string
				if err = e.Pool.QueryRow(ctx, "SELECT action FROM purchase_orders WHERE account_id=$1", account).Scan(&action); err != nil || action != "purchase" {
					t.Fatal("old purchase action did not default", err)
				}
				return
			}
			in := purchaseInput(plan)
			in.Action = "renew"
			order, err := s.createPurchaseOrder(ctx, account, uuid.New(), in)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = e.Pool.Exec(ctx, "UPDATE purchase_orders SET action='purchase' WHERE id=$1", order.OrderId); err == nil {
				t.Fatal("paid purpose can be rewritten")
			}
			var before, after string
			if err = e.Pool.QueryRow(ctx, "SELECT to_jsonb(p)::text FROM purchase_orders p WHERE id=$1", order.OrderId).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err = provider.DownTo(ctx, 19); err == nil || !strings.Contains(err.Error(), "renewal history requires compatible application") {
				t.Fatal("down erased retained renewal", err)
			}
			if err = e.Pool.QueryRow(ctx, "SELECT to_jsonb(p)::text FROM purchase_orders p WHERE id=$1", order.OrderId).Scan(&after); err != nil || after != before {
				t.Fatal("failed down changed stored order", err)
			}
			// Earlier Down steps commit separately; restore the current schema before current application reads.
			if _, err = provider.Up(ctx); err != nil {
				t.Fatal(err)
			}
			current, err := s.purchaseOrder(ctx, account, order.OrderId)
			if err != nil || current.Action != "renew" || current.Quote.AmountMinor != "9007199254740993" || count(t, e, "purchase_receipts") != 1 {
				t.Fatal("failed downgrade changed quote/action/money", err)
			}
		})
	}
}

// Concurrent requests must select one durable order even when both have read
// the old applied purchase. Callback/job replay is covered by the same target.
func TestRenewalConcurrentCreate(t *testing.T) {
	s, e, _, account, plan := renewalFixture(t)
	in := purchaseInput(plan)
	in.Action = "renew"
	start := make(chan struct{})
	type result struct {
		order wire.PurchaseOrder
		err   error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			o, err := s.createPurchaseOrder(context.Background(), account, uuid.New(), in)
			results <- result{o, err}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for r := range results {
		if r.err == nil {
			success++
		} else if catalogueCode(r.err, "PURCHASE_ORDER_CONFLICT") {
			conflicts++
		} else {
			t.Fatal("concurrent renewal failed", r.err)
		}
	}
	if success != 1 || conflicts != 1 || count(t, e, "purchase_orders") != 2 || count(t, e, "purchase_receipts") != 1 {
		t.Fatal("concurrent renewal created multiple durable orders")
	}
}

// Every existing funding path must accept a real second purchase after either
// applied clearing transition, keeping its receipt and native identity once.
func TestRenewalTrialClearingAllowsAllFirstPurchaseMethods(t *testing.T) {
	for _, transition := range []string{"starter", "unlimited revoke"} {
		for _, method := range []string{"yoomoney", "manual", "yookassa", "cryptomus", "heleket"} {
			t.Run(transition+"/"+method, func(t *testing.T) {
				ctx := context.Background()
				var p *fakePanel
				var actor uuid.UUID
				prepare := func(s *regressionFixture, e *testkit.Env, account, plan uuid.UUID) {
					p = panelFixture(t, s)
					first, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
					if err != nil {
						t.Fatal(err)
					}
					completeRenewalPayment(t, s, account, first)
					actor = renewalOperator(t, s, e)
					clearPurchasePlan(t, s, actor, account, transition)
				}
				var s *regressionFixture
				var e *testkit.Env
				var account uuid.UUID
				var order wire.PurchaseOrder
				switch method {
				case "yookassa":
					var f *kassaStub
					s, e, account, order, f = kassaFixture(t, prepare)
					syncKassa(t, s, order.OrderId)
					settleKassa(e, f)
					syncKassa(t, s, order.OrderId)
					syncKassa(t, s, order.OrderId)
				case "cryptomus", "heleket":
					var f *cryptoStub
					s, e, account, order, f = cryptoFixture(t, method, prepare)
					syncCrypto(t, s, order.OrderId, method)
					settleCrypto(e, f)
					syncCrypto(t, s, order.OrderId, method)
					syncCrypto(t, s, order.OrderId, method)
				default:
					var plan uuid.UUID
					s, e, account, plan = purchaseFixture(t)
					prepare(s, e, account, plan)
					in := purchaseInput(plan)
					if method == "manual" {
						s.cfg.Payments.ManualEnabled = true
						s.cfg.Payments.ManualCardDetails = "Local synthetic recipient"
						in.PaymentMethod = "manual"
						in.PaymentType = "MANUAL"
					}
					var err error
					order, err = s.createPurchaseOrder(ctx, account, uuid.New(), in)
					if err != nil {
						t.Fatal(err)
					}
					if method == "manual" {
						if _, err = s.payments.ReportManualPayment(ctx, account, order.OrderId, uuid.New()); err != nil {
							t.Fatal(err)
						}
						if _, err = s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, uuid.New(), payments.ManualPaymentDecisionInput{Decision: "approve", Reason: "Synthetic receipt checked", ConfirmedAmountMinor: &order.Quote.AmountMinor}); err != nil {
							t.Fatal(err)
						}
					} else {
						fields := purchaseNotice(s, order.OrderId, uuid.NewString(), "90071992547409.00", "90071992547409.93")
						for range 2 {
							if err = s.receiveYooMoney(ctx, fields); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				before, err := s.accountByID(ctx, account)
				if err != nil {
					t.Fatal(err)
				}
				updates, resets := p.updates, p.resets
				for range 2 {
					if err = s.fulfillPurchase(ctx, order.OrderId); err != nil {
						t.Fatal(err)
					}
				}
				funded, err := s.purchaseOrder(ctx, account, order.OrderId)
				if err != nil || funded.PaymentStatus != "paid" || funded.ReviewRequired || funded.AccessOperationId == nil {
					t.Fatal("cleared trial did not retain provider funding", err)
				}
				in := purchaseInput(order.Quote.PlanId)
				if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), in); !catalogueCode(err, "PURCHASE_NOT_ELIGIBLE") {
					t.Fatal("trial clearing bypassed unresolved paid access", err)
				}
				current, err := s.currentPurchaseOrder(ctx, account)
				if err != nil || current.Order == nil || current.Order.OrderId != order.OrderId || current.CanPurchase != nil && *current.CanPurchase {
					t.Fatal("client API bypassed unresolved paid access", err)
				}
				for range 2 {
					if err = s.applyAccess(ctx, *funded.AccessOperationId); err != nil {
						t.Fatal(err)
					}
				}
				applied, err := s.purchaseOrder(ctx, account, order.OrderId)
				if err != nil || applied.Action != "purchase" || applied.FulfillmentStatus != "applied" {
					t.Fatal("cleared trial purchase not applied", err)
				}
				after, err := s.accountByID(ctx, account)
				if err != nil || after.PanelKey != before.PanelKey || after.VpnID != before.VpnID || after.SubID != before.SubID || after.AssignedPanelID == nil || before.AssignedPanelID == nil || *after.AssignedPanelID != *before.AssignedPanelID || p.updates != updates+1 || p.resets != resets+1 {
					t.Fatal("cleared purchase changed identity or repeated native writes", err)
				}
				manualCounts(t, s, order.OrderId, 1, 1)
			})
		}
	}
}

func TestRenewalTrialClearingRetainsDisputedHistoryBlock(t *testing.T) {
	s, e, p, account, plan := renewalFixture(t)
	ctx := context.Background()
	actor := renewalOperator(t, s, e)
	clearPurchasePlan(t, s, actor, account, "starter")
	current, err := s.currentPurchaseOrder(ctx, account)
	if err != nil || current.Order == nil || current.CanPurchase == nil || !*current.CanPurchase {
		t.Fatal("cleared purchase unavailable before dispute", err)
	}
	updates, resets := p.updates, p.resets
	if err = s.receiveYooMoney(ctx, purchaseNotice(s, current.Order.OrderId, uuid.NewString(), "90071992547409.00", "90071992547409.93")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan)); !catalogueCode(err, "PURCHASE_NOT_ELIGIBLE") {
		t.Fatal("clearing discarded disputed payment history", err)
	}
	current, err = s.currentPurchaseOrder(ctx, account)
	if err != nil || current.CanPurchase == nil || *current.CanPurchase || current.Order == nil || !current.Order.ReviewRequired || count(t, e, "purchase_orders") != 1 || count(t, e, "purchase_receipts") != 2 || p.updates != updates || p.resets != resets {
		t.Fatal("dispute block lost history or wrote native access", err)
	}
}

func TestRenewalTrialClearingLatePlanBlocksPurchase(t *testing.T) {
	for _, phase := range []string{"checkout", "paid"} {
		t.Run(phase, func(t *testing.T) {
			s, e, p, account, plan := renewalFixture(t)
			ctx := context.Background()
			actor := renewalOperator(t, s, e)
			clearPurchasePlan(t, s, actor, account, "starter")
			order, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
			if err != nil {
				t.Fatal(err)
			}
			notice := purchaseNotice(s, order.OrderId, uuid.NewString(), "90071992547409.00", "90071992547409.93")
			if phase == "paid" {
				if err = s.receiveYooMoney(ctx, notice); err != nil {
					t.Fatal(err)
				}
			}
			revision, period := int64(1), int64(30)
			op, err := s.createAccessOperation(ctx, actor, account, uuid.New(), wire.AccessOperationInput{Kind: "assign_plan", PlanId: &plan, Revision: &revision, PeriodDays: &period, Reason: "Support assigned a paid plan"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.applyAccess(ctx, op.OperationId); err != nil {
				t.Fatal(err)
			}
			updates, resets := p.updates, p.resets
			current, err := s.purchaseOrder(ctx, account, order.OrderId)
			if err != nil || current.CanPay || current.Checkout != nil {
				t.Fatal("new assignment left old cleared-trial checkout open", err)
			}
			if phase == "checkout" {
				if err = s.receiveYooMoney(ctx, notice); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.fulfillPurchase(ctx, order.OrderId); err != nil {
				t.Fatal(err)
			}
			current, err = s.purchaseOrder(ctx, account, order.OrderId)
			if err != nil || current.PaymentStatus != "paid" || current.FulfillmentStatus != "needs_review" || !current.ReviewRequired || current.AccessOperationId != nil || count(t, e, "purchase_receipts") != 2 || p.updates != updates || p.resets != resets {
				t.Fatal("late plan assignment lost money or wrote native access", err)
			}
		})
	}
}
