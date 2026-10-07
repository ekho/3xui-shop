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

// Change replaces the remaining period, while funding and native replay retain
// one immutable target and the customer's existing VPN identity.
func TestPlanChangeHTTPFlow(t *testing.T) {
	s, e, p, account, plan := renewalFixture(t)
	ctx := context.Background()
	before, err := s.accountByID(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	actor := renewalOperator(t, s, e)
	terms := catalogueTerms(5)
	terms.TrafficGb, terms.Profile = 120, "euru"
	terms.Prices[0].AmountMinor = "9007199254740993"
	target, err := s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: terms, Reason: "visible change target"})
	if err != nil {
		t.Fatal(err)
	}
	h := New(app.NewModules(s.pool, s.limiter, s.queue, s.cfg), s.pool, s.cfg.HTTP)
	customer := manualLogin(t, h, *s.cfg, "purchase@example.test")
	r := supportRequest(h, &customer, "GET", "/api/v1/subscription/plan-change", "", nil, "", uuid.Nil)
	var offer struct {
		CurrentPlanId uuid.UUID `json:"current_plan_id"`
		Source        uuid.UUID `json:"source_access_operation_id"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &offer) != nil || offer.CurrentPlanId != plan || offer.Source == uuid.Nil {
		t.Fatal("own applied plan-change context missing", r.Code)
	}
	body, _ := json.Marshal(map[string]any{"action": "change_plan", "plan_id": target.PlanId, "revision": 1, "period_days": 30, "payment_method": "yoomoney", "payment_type": "PC", "source_access_operation_id": offer.Source})
	key := uuid.New()
	var order wire.PurchaseOrder
	for range 2 {
		r = supportRequest(h, &customer, "POST", "/api/v1/orders", "application/json", body, s.cfg.HTTP.CabinetOrigin, key)
		var got wire.PurchaseOrder
		if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &got) != nil || got.Action != "change_plan" || (order.OrderId != uuid.Nil && got.OrderId != order.OrderId) {
			t.Fatal("HTTP change lost purpose or replay", r.Code)
		}
		order = got
	}
	p.up = 9876
	applied := completeRenewalPayment(t, s, account, order)
	after, err := s.accountByID(ctx, account)
	if err != nil || after.VpnID != before.VpnID || after.SubID != before.SubID || after.PanelKey != before.PanelKey || after.AssignedPanelID == nil || *after.AssignedPanelID != *before.AssignedPanelID {
		t.Fatal("change moved native identity/server", err)
	}
	if applied.Action != "change_plan" || applied.Quote.PlanId != target.PlanId || p.resets != 2 || p.up != 0 || integer(t, p.client["expiryTime"]) != e.Clock().Add(30*24*time.Hour).UnixMilli() || integer(t, p.client["limitIp"]) != 6 || integer(t, p.client["totalGB"]) != 120*1024*1024*1024 || after.AccessProfile == nil || *after.AccessProfile != "euru" {
		t.Fatal("change carried remaining days, lost limits/profile or repeated reset")
	}
	var orders, receipts, operations, jobs int
	if err = e.Pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM purchase_orders WHERE account_id=$1),(SELECT count(*) FROM purchase_receipts r JOIN purchase_orders o ON o.id=r.order_id WHERE o.account_id=$1),(SELECT count(*) FROM access_operations WHERE account_id=$1),(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=$2)", account, order.OrderId.String()).Scan(&orders, &receipts, &operations, &jobs); err != nil || orders != 2 || receipts != 2 || operations != 2 || jobs != 1 {
		t.Fatal("replay manufactured funding or access", err, orders, receipts, operations, jobs)
	}
}

func planChangeInput(t *testing.T, s *regressionFixture, account, plan uuid.UUID) wire.PurchaseOrderInput {
	t.Helper()
	source, err := s.payments.PlanChangeContext(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	in := purchaseInput(plan)
	in.Action, in.SourceAccessOperationId = "change_plan", &source.SourceAccessOperationId
	return in
}

func TestPlanChangeAllExternalMethods(t *testing.T) {
	for _, action := range []string{"change_plan", "renew"} {
		for _, method := range []string{"yoomoney", "manual", "yookassa", "cryptomus", "heleket"} {
			t.Run(action+"/"+method, func(t *testing.T) {
				ctx := context.Background()
				var p *fakePanel
				input := func(s *regressionFixture, e *testkit.Env, account, plan uuid.UUID) wire.PurchaseOrderInput {
					p = panelFixture(t, s)
					first, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
					if err != nil {
						t.Fatal(err)
					}
					completeRenewalPayment(t, s, account, first)
					if action == "change_plan" {
						return planChangeInput(t, s, account, plan)
					}
					in := purchaseInput(plan)
					in.Action = "renew"
					return in
				}
				var s *regressionFixture
				var e *testkit.Env
				var account uuid.UUID
				var order wire.PurchaseOrder
				switch method {
				case "yookassa":
					var f *kassaStub
					s, e, account, order, f = kassaOrderFixture(t, input)
					syncKassa(t, s, order.OrderId)
					settleKassa(e, f)
					syncKassa(t, s, order.OrderId)
					syncKassa(t, s, order.OrderId)
				case "cryptomus", "heleket":
					var f *cryptoStub
					s, e, account, order, f = cryptoOrderFixture(t, method, input)
					syncCrypto(t, s, order.OrderId, method)
					settleCrypto(e, f)
					syncCrypto(t, s, order.OrderId, method)
					syncCrypto(t, s, order.OrderId, method)
				default:
					var plan uuid.UUID
					s, e, account, plan = purchaseFixture(t)
					in := input(s, e, account, plan)
					if method == "manual" {
						s.cfg.Payments.ManualEnabled = true
						s.cfg.Payments.ManualCardDetails = "Local synthetic recipient"
						in.PaymentMethod, in.PaymentType = "manual", "MANUAL"
					}
					var err error
					order, err = s.createPurchaseOrder(ctx, account, uuid.New(), in)
					if err != nil {
						t.Fatal(err)
					}
					if method == "manual" {
						actor := renewalOperator(t, s, e)
						if _, err = s.payments.ReportManualPayment(ctx, account, order.OrderId, uuid.New()); err != nil {
							t.Fatal(err)
						}
						decisionKey := uuid.New()
						for range 2 {
							if _, err = s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, decisionKey, payments.ManualPaymentDecisionInput{Decision: "approve", Reason: "Synthetic receipt checked", ConfirmedAmountMinor: &order.Quote.AmountMinor}); err != nil {
								t.Fatal(err)
							}
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
				p.up = 9876
				for range 2 {
					if err = s.fulfillPurchase(ctx, order.OrderId); err != nil {
						t.Fatal(err)
					}
				}
				prepared, err := s.purchaseOrder(ctx, account, order.OrderId)
				if err != nil || prepared.AccessOperationId == nil || prepared.PaymentStatus != "paid" || prepared.ReviewRequired {
					t.Fatal("method did not retain valid funding", err)
				}
				for range 2 {
					if err = s.applyAccess(ctx, *prepared.AccessOperationId); err != nil {
						t.Fatal(err)
					}
				}
				after, err := s.accountByID(ctx, account)
				applied, readErr := s.purchaseOrder(ctx, account, order.OrderId)
				days := 30
				if action == "renew" {
					days = 60
				}
				if err != nil || readErr != nil || applied.Action != wire.PurchaseOrderAction(action) || applied.FulfillmentStatus != "applied" || p.resets != 2 || p.up != 0 || integer(t, p.client["expiryTime"]) != e.Clock().Add(time.Duration(days)*24*time.Hour).UnixMilli() || before.VpnID != after.VpnID || before.SubID != after.SubID || before.PanelKey != after.PanelKey || *before.AssignedPanelID != *after.AssignedPanelID {
					t.Fatal("provider/purpose lost target, identity or reset", err, readErr)
				}
				if (action == "change_plan") != (applied.Quote.SourceAccessOperationId != nil) {
					t.Fatal("quote lost frozen source")
				}
				var currency, amount string
				var net *int64
				if err = e.Pool.QueryRow(ctx, "SELECT currency,gross_minor::text,net_minor FROM purchase_receipts WHERE order_id=$1", order.OrderId).Scan(&currency, &amount, &net); err != nil || amount != applied.Quote.AmountMinor {
					t.Fatal("funding principal changed", err)
				}
				if method == "cryptomus" || method == "heleket" {
					if currency != "USD" || amount != "12345" || net != nil || applied.Quote.Currency != "USD" {
						t.Fatal("invented crypto USD net or repriced principal")
					}
				} else if applied.Quote.Currency != "RUB" || amount != "9007199254740993" {
					t.Fatal("RUB principal changed")
				}
				manualCounts(t, s, order.OrderId, 1, 1)
			})
		}
	}
}

func TestPlanChangeEligibility(t *testing.T) {
	for _, mode := range []string{"same target", "hidden source", "archived source", "hidden target", "archived target", "zero price", "stale revision", "unlimited target", "foreign source", "missing source", "nil source", "source on purchase", "source on renew", "cleared", "ban", "restriction", "telegram", "legacy", "unlimited", "other server", "native missing", "native identity", "native groups", "native perpetual", "unknown disabled", "expired", "exhausted"} {
		t.Run(mode, func(t *testing.T) {
			s, e, p, account, plan := renewalFixture(t)
			ctx := context.Background()
			in := planChangeInput(t, s, account, plan)
			var err error
			switch mode {
			case "hidden source", "archived source", "hidden target", "archived target", "zero price", "stale revision", "unlimited target":
				actor := renewalOperator(t, s, e)
				terms := catalogueTerms(4)
				terms.Prices[0].AmountMinor = "9007199254740993"
				if mode == "hidden target" {
					terms.Hidden = true
				}
				if mode == "zero price" {
					terms.Prices[0].AmountMinor = "0"
				}
				target, createErr := s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: terms, Reason: "test target"})
				if createErr != nil {
					t.Fatal(createErr)
				}
				in.PlanId = target.PlanId
				if mode == "unlimited target" {
					unlimited, _, seedErr := s.seedUnlimitedCatalogue(ctx)
					if seedErr != nil {
						t.Fatal(seedErr)
					}
					in.PlanId, in.Revision = unlimited.PlanId, unlimited.Revision
				}
				if mode == "archived target" {
					_, err = s.archiveCataloguePlan(ctx, actor, target.PlanId, uuid.New(), wire.CataloguePlanArchiveInput{ExpectedRevision: 1, Reason: "retire target"})
				}
				if mode == "stale revision" {
					in.Revision = 2
				}
				if mode == "hidden source" {
					terms = catalogueTerms(2)
					terms.Hidden = true
					_, err = s.reviseCataloguePlan(ctx, actor, plan, uuid.New(), wire.CataloguePlanRevisionInput{ExpectedRevision: 1, Terms: terms, Reason: "hide source"})
				}
				if mode == "archived source" {
					_, err = s.archiveCataloguePlan(ctx, actor, plan, uuid.New(), wire.CataloguePlanArchiveInput{ExpectedRevision: 1, Reason: "retire source"})
				}
			case "foreign source":
				id := uuid.New()
				in.SourceAccessOperationId = &id
			case "missing source":
				in.SourceAccessOperationId = nil
			case "nil source":
				id := uuid.Nil
				in.SourceAccessOperationId = &id
			case "source on purchase":
				in.Action = "purchase"
			case "source on renew":
				in.Action = "renew"
			case "cleared":
				clearPurchasePlan(t, s, renewalOperator(t, s, e), account, "starter")
			case "ban":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET vpn_banned=true WHERE id=$1", account)
			case "restriction":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", account)
			case "telegram":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET telegram_id=1234567 WHERE id=$1", account)
			case "legacy":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET legacy_user_id=1234567 WHERE id=$1", account)
			case "unlimited":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET access_profile='unlimited' WHERE id=$1", account)
			case "other server":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET assigned_panel_id='other' WHERE id=$1", account)
			case "native missing":
				p.client = nil
			case "native identity":
				p.client["id"] = uuid.NewString()
			case "native groups":
				p.ids = []int64{99}
			case "native perpetual":
				p.client["expiryTime"] = json.Number("0")
			case "unknown disabled":
				p.client["enable"] = false
			case "expired":
				s.now = func() time.Time { return e.Clock().Add(31 * 24 * time.Hour) }
				p.client["enable"] = false
			case "exhausted":
				p.up = 100 * 1024 * 1024 * 1024
				p.client["enable"] = false
			}
			if err != nil {
				t.Fatal(err)
			}
			orders, receipts, jobs, updates, resets := count(t, e, "purchase_orders"), count(t, e, "purchase_receipts"), count(t, e, "river_job"), p.updates, p.resets
			order, err := s.createPurchaseOrder(ctx, account, uuid.New(), in)
			allowed := mode == "same target" || mode == "hidden source" || mode == "archived source" || mode == "expired" || mode == "exhausted"
			if allowed {
				if err != nil || order.Action != "change_plan" {
					t.Fatal("eligible change refused", err)
				}
			} else if err == nil || count(t, e, "purchase_orders") != orders {
				t.Fatal("ineligible change created money", err)
			}
			if count(t, e, "purchase_receipts") != receipts || p.updates != updates || p.resets != resets || !allowed && count(t, e, "river_job") != jobs {
				t.Fatal("refusal changed money/access")
			}
		})
	}
}

func TestPlanChangeLateSource(t *testing.T) {
	for _, phase := range []string{"checkout", "paid", "target"} {
		t.Run(phase, func(t *testing.T) {
			s, e, p, account, plan := renewalFixture(t)
			ctx := context.Background()
			actor := renewalOperator(t, s, e)
			in := planChangeInput(t, s, account, plan)
			key := uuid.New()
			order, err := s.createPurchaseOrder(ctx, account, key, in)
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
				// A persisted external/imported assignment must still be caught by
				// the physical guard, even though the operator API serializes writes.
				_, err = e.Pool.Exec(ctx, "INSERT INTO access_operations(id,account_id,kind,status,reason,plan_id,plan_revision,period_days,desired,target,completed_steps,created_at,updated_at) SELECT $1,account_id,'assign_plan','applied','external assignment',plan_id,plan_revision,period_days,desired,target,completed_steps,created_at,updated_at FROM access_operations WHERE id=$2", uuid.New(), *in.SourceAccessOperationId)
			} else {
				rev, days := int64(1), int64(30)
				op, opErr := s.createAccessOperation(ctx, actor, account, uuid.New(), wire.AccessOperationInput{Kind: "assign_plan", PlanId: &plan, Revision: &rev, PeriodDays: &days, Reason: "same plan assigned again"})
				if opErr != nil {
					t.Fatal(opErr)
				}
				err = s.applyAccess(ctx, op.OperationId)
			}
			if err != nil {
				t.Fatal(err)
			}
			updates, resets := p.updates, p.resets
			replay, err := s.createPurchaseOrder(ctx, account, key, in)
			if err != nil || replay.OrderId != order.OrderId || replay.CanPay || replay.Checkout != nil {
				t.Fatal("stale source checkout/replay survived", err)
			}
			if phase == "checkout" {
				if err = s.receiveYooMoney(ctx, purchaseNotice(s, order.OrderId, uuid.NewString(), "90071992547409.00", "90071992547409.93")); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "target" {
				err = s.applyAccess(ctx, *replay.AccessOperationId)
			} else {
				err = s.fulfillPurchase(ctx, order.OrderId)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.purchaseOrder(ctx, account, order.OrderId)
			if err != nil || got.PaymentStatus != "paid" || got.FulfillmentStatus != "needs_review" || !got.ReviewRequired || p.updates != updates || p.resets != resets || count(t, e, "purchase_receipts") != 2 {
				t.Fatal("source change lost funds or wrote native access", err)
			}
			jobs := count(t, e, "river_job")
			if _, err = s.reconcilePurchaseOrder(ctx, actor, account, order.OrderId, uuid.New(), wire.PurchaseReconcileInput{Reason: "source still differs"}); !catalogueCode(err, "PURCHASE_ORDER_CONFLICT") || count(t, e, "river_job") != jobs {
				t.Fatal("reconcile accepted stale source", err)
			}
		})
	}
	t.Run("non-plan corrections preserve source", func(t *testing.T) {
		s, e, _, account, plan := renewalFixture(t)
		ctx := context.Background()
		in := planChangeInput(t, s, account, plan)
		actor := renewalOperator(t, s, e)
		banned, unbanned := true, false
		for _, opIn := range []wire.AccessOperationInput{{Kind: "compensate", Days: ptrInt(2), Reason: "support correction"}, {Kind: "reset_traffic", Reason: "reset"}, {Kind: "set_vpn_ban", VpnBanned: &banned, Reason: "hold"}, {Kind: "set_vpn_ban", VpnBanned: &unbanned, Reason: "release"}} {
			op, err := s.createAccessOperation(ctx, actor, account, uuid.New(), opIn)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.applyAccess(ctx, op.OperationId); err != nil {
				t.Fatal(err)
			}
		}
		current, err := s.payments.PlanChangeContext(ctx, account)
		if err != nil || current.SourceAccessOperationId != *in.SourceAccessOperationId {
			t.Fatal("non-plan corrections changed source", err)
		}
		if _, err = s.createPurchaseOrder(ctx, account, uuid.New(), in); err != nil {
			t.Fatal("preserved source refused", err)
		}
	})
}

func TestPlanChangeLateGuards(t *testing.T) {
	for _, phase := range []string{"paid", "target"} {
		for _, mode := range []string{"ban", "restriction", "telegram", "legacy", "identity", "groups", "unknown disabled", "unlimited"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				s, e, p, account, plan := renewalFixture(t)
				ctx := context.Background()
				order, err := s.createPurchaseOrder(ctx, account, uuid.New(), planChangeInput(t, s, account, plan))
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
				case "telegram":
					_, err = e.Pool.Exec(ctx, "UPDATE accounts SET telegram_id=1234567 WHERE id=$1", account)
				case "legacy":
					_, err = e.Pool.Exec(ctx, "UPDATE accounts SET legacy_user_id=1234567 WHERE id=$1", account)
				case "unlimited":
					_, err = e.Pool.Exec(ctx, "UPDATE accounts SET access_profile='unlimited' WHERE id=$1", account)
				case "identity":
					p.client["id"] = uuid.NewString()
				case "groups":
					p.ids = []int64{99}
				case "unknown disabled":
					p.client["enable"] = false
				}
				if err != nil {
					t.Fatal(err)
				}
				updates, resets := p.updates, p.resets
				if phase == "target" {
					prepared, readErr := s.purchaseOrder(ctx, account, order.OrderId)
					if readErr != nil || prepared.AccessOperationId == nil {
						t.Fatal(readErr)
					}
					err = s.applyAccess(ctx, *prepared.AccessOperationId)
				} else {
					err = s.fulfillPurchase(ctx, order.OrderId)
				}
				if err != nil {
					t.Fatal(err)
				}
				got, err := s.purchaseOrder(ctx, account, order.OrderId)
				if err != nil || got.PaymentStatus != "paid" || got.FulfillmentStatus != "needs_review" || !got.ReviewRequired || p.updates != updates || p.resets != resets || count(t, e, "purchase_receipts") != 2 {
					t.Fatal("late guard lost funds or changed VPN", err)
				}
			})
		}
	}
}

func TestPlanChangeFrozenQuote(t *testing.T) {
	s, e, p, account, sourcePlan := renewalFixture(t)
	ctx := context.Background()
	actor := renewalOperator(t, s, e)
	terms := catalogueTerms(4)
	terms.Prices[0].AmountMinor = "9007199254740993"
	target, err := s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: terms, Reason: "target"})
	if err != nil {
		t.Fatal(err)
	}
	order, err := s.createPurchaseOrder(ctx, account, uuid.New(), planChangeInput(t, s, account, target.PlanId))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(order.Quote)
	terms.Devices, terms.TrafficGb, terms.Prices[0].AmountMinor = 6, 20, "10000"
	if _, err = s.reviseCataloguePlan(ctx, actor, target.PlanId, uuid.New(), wire.CataloguePlanRevisionInput{ExpectedRevision: 1, Terms: terms, Reason: "later offer"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.archiveCataloguePlan(ctx, actor, target.PlanId, uuid.New(), wire.CataloguePlanArchiveInput{ExpectedRevision: 2, Reason: "retire later offer"}); err != nil {
		t.Fatal(err)
	}
	if err = s.receiveYooMoney(ctx, purchaseNotice(s, order.OrderId, uuid.NewString(), "90071992547409.00", "90071992547409.93")); err != nil {
		t.Fatal(err)
	}
	if err = s.fulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.purchaseOrder(ctx, account, order.OrderId)
	if err != nil || prepared.AccessOperationId == nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return e.Clock().Add(24 * time.Hour) }
	for range 2 {
		if err = s.fulfillPurchase(ctx, order.OrderId); err != nil {
			t.Fatal(err)
		}
		if err = s.applyAccess(ctx, *prepared.AccessOperationId); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.purchaseOrder(ctx, account, order.OrderId)
	after, _ := json.Marshal(got.Quote)
	source, sourceErr := s.vpn.CurrentPlanIDTx(ctx, nil, account)
	if err != nil || sourceErr != nil || string(before) != string(after) || got.Quote.PlanId == sourcePlan || source == nil || *source != target.PlanId || got.FulfillmentStatus != "applied" || integer(t, p.client["limitIp"]) != 5 || integer(t, p.client["totalGB"]) != 100*1024*1024*1024 || integer(t, p.client["expiryTime"]) != e.Clock().Add(30*24*time.Hour).UnixMilli() || p.resets != 2 {
		t.Fatal("recovery repriced/rebased frozen change", err, sourceErr)
	}
}

func TestPlanChangeHTTPBoundary(t *testing.T) {
	s, e, _, account, plan := renewalFixture(t)
	h := New(app.NewModules(s.pool, s.limiter, s.queue, s.cfg), s.pool, s.cfg.HTTP)
	customer := manualLogin(t, h, *s.cfg, "purchase@example.test")
	for _, path := range []string{"/api/v1/subscription/plan-change?account_id=" + account.String(), "/api/v1/subscription/plan-change?source=" + uuid.NewString()} {
		if r := supportRequest(h, &customer, "GET", path, "", nil, "", uuid.Nil); r.Code != 400 {
			t.Fatal("context accepted query", r.Code)
		}
	}
	if r := supportRequest(h, nil, "GET", "/api/v1/subscription/plan-change", "", nil, "", uuid.Nil); r.Code != 401 {
		t.Fatal("anonymous context", r.Code)
	}
	foreign := supportLogin(t, h, e, *s.cfg, "change-foreign@example.test")
	if r := supportRequest(h, &foreign, "GET", "/api/v1/subscription/plan-change", "", nil, "", uuid.Nil); r.Code != 409 {
		t.Fatal("trial-only context", r.Code)
	}
	in := planChangeInput(t, s, account, plan)
	good, _ := json.Marshal(in)
	noCSRF := customer
	noCSRF.csrf = ""
	if r := supportRequest(h, &noCSRF, "POST", "/api/v1/orders", "application/json", good, s.cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 403 {
		t.Fatal("missing CSRF", r.Code)
	}
	for _, tc := range []struct {
		body, origin string
		key          uuid.UUID
		want         int
	}{
		{string(good), "https://attacker.example.test", uuid.New(), 403},
		{string(good), s.cfg.HTTP.CabinetOrigin, uuid.Nil, 400},
		{string(good[:len(good)-1]) + "," + "\"account_id\":\"" + uuid.NewString() + "\"}", s.cfg.HTTP.CabinetOrigin, uuid.New(), 400},
		{strings.Replace(string(good), in.SourceAccessOperationId.String(), "not-a-uuid", 1), s.cfg.HTTP.CabinetOrigin, uuid.New(), 400},
		{strings.Repeat("a", 16385), s.cfg.HTTP.CabinetOrigin, uuid.New(), 400},
	} {
		if r := supportRequest(h, &customer, "POST", "/api/v1/orders", "application/json", []byte(tc.body), tc.origin, tc.key); r.Code != tc.want {
			t.Fatal("change boundary", r.Code, tc.want)
		}
	}
	r := supportRequest(h, &customer, "POST", "/api/v1/orders", "application/json", good, s.cfg.HTTP.CabinetOrigin, uuid.New())
	var order wire.PurchaseOrder
	if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &order) != nil {
		t.Fatal("HTTP change failed", r.Code)
	}
	if r = supportRequest(h, &foreign, "GET", "/api/v1/orders/"+order.OrderId.String(), "", nil, "", uuid.Nil); r.Code != 404 {
		t.Fatal("foreign order disclosed", r.Code)
	}
	if r = supportRequest(h, &customer, "GET", "/api/v1/subscription/plan-change", "", nil, "", uuid.Nil); r.Code != 200 || strings.Contains(r.Body.String(), "panel_key") || strings.Contains(r.Body.String(), "sub_id") || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("context disclosed native identity or cacheable", r.Code)
	}
	e.Pool.Close()
	if r = supportRequest(h, &customer, "GET", "/api/v1/subscription/plan-change", "", nil, "", uuid.Nil); r.Code != 503 {
		t.Fatal("dependency error not retryable", r.Code)
	}
}

func TestPlanChangeActionMigration(t *testing.T) {
	for _, mode := range []string{"unchanged purchase", "retained pending change"} {
		t.Run(mode, func(t *testing.T) {
			s, e, _, account, plan := renewalFixture(t)
			ctx := context.Background()
			db := stdlib.OpenDBFromPool(e.Pool)
			t.Cleanup(func() { db.Close() })
			provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../db/migrations"))
			if err != nil {
				t.Fatal(err)
			}
			var before, after string
			if mode == "unchanged purchase" {
				if err = e.Pool.QueryRow(ctx, "SELECT to_jsonb(p)::text FROM purchase_orders p WHERE account_id=$1", account).Scan(&before); err != nil {
					t.Fatal(err)
				}
				if _, err = provider.DownTo(ctx, 20); err != nil {
					t.Fatal(err)
				}
				if err = e.Pool.QueryRow(ctx, "SELECT to_jsonb(p)::text FROM purchase_orders p WHERE account_id=$1", account).Scan(&after); err != nil || after != before {
					t.Fatal("down21 changed old bytes", err)
				}
				if _, err = provider.Up(ctx); err != nil {
					t.Fatal(err)
				}
				return
			}
			order, err := s.createPurchaseOrder(ctx, account, uuid.New(), planChangeInput(t, s, account, plan))
			if err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{"UPDATE purchase_orders SET action='purchase' WHERE id=$1", "UPDATE purchase_orders SET quote=jsonb_set(quote,'{source_access_operation_id}',to_jsonb('00000000-0000-4000-8000-000000000001'::text)) WHERE id=$1"} {
				if _, err = e.Pool.Exec(ctx, query, order.OrderId); err == nil {
					t.Fatal("immutable purpose/source changed")
				}
			}
			if _, err = s.cancelPurchaseOrder(ctx, account, order.OrderId, uuid.New()); err != nil {
				t.Fatal(err)
			}
			if err = e.Pool.QueryRow(ctx, "SELECT to_jsonb(p)::text FROM purchase_orders p WHERE id=$1", order.OrderId).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err = provider.DownTo(ctx, 20); err == nil || !strings.Contains(err.Error(), "plan change history requires compatible application") {
				t.Fatal("down erased cancelled change history", err)
			}
			if err = e.Pool.QueryRow(ctx, "SELECT to_jsonb(p)::text FROM purchase_orders p WHERE id=$1", order.OrderId).Scan(&after); err != nil || after != before {
				t.Fatal("failed down changed stored order", err)
			}
			// Earlier Down steps commit separately; restore the current schema before current application reads.
			if _, err = provider.Up(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := s.purchaseOrder(ctx, account, order.OrderId)
			if err != nil || got.Action != "change_plan" || got.PaymentStatus != "canceled" || got.Quote.SourceAccessOperationId == nil || *got.Quote.SourceAccessOperationId != *order.Quote.SourceAccessOperationId || count(t, e, "purchase_receipts") != 1 {
				t.Fatal("failed down changed historical quote/money", err)
			}
		})
	}
}

func TestPlanChangeConcurrentCreate(t *testing.T) {
	s, e, _, account, plan := renewalFixture(t)
	in := planChangeInput(t, s, account, plan)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			_, err := s.createPurchaseOrder(context.Background(), account, uuid.New(), in)
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if catalogueCode(err, "PURCHASE_ORDER_CONFLICT") {
			conflicts++
		} else {
			t.Fatal("unexpected concurrent failure", err)
		}
	}
	if success != 1 || conflicts != 1 || count(t, e, "purchase_orders") != 2 || count(t, e, "purchase_receipts") != 1 {
		t.Fatal("two durable changes accepted")
	}
}

// Every valid funding proof must retain paid money but refuse automatic
// fulfillment immediately if the quoted source was replaced before funding.
func TestPlanChangeFundingGuards(t *testing.T) {
	for _, method := range []string{"yoomoney", "manual", "yookassa", "cryptomus", "heleket"} {
		t.Run(method, func(t *testing.T) {
			ctx := context.Background()
			var p *fakePanel
			input := func(s *regressionFixture, e *testkit.Env, account, plan uuid.UUID) wire.PurchaseOrderInput {
				p = panelFixture(t, s)
				first, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
				if err != nil {
					t.Fatal(err)
				}
				completeRenewalPayment(t, s, account, first)
				return planChangeInput(t, s, account, plan)
			}
			var s *regressionFixture
			var e *testkit.Env
			var account uuid.UUID
			var order wire.PurchaseOrder
			var fund func()
			switch method {
			case "yookassa":
				var f *kassaStub
				s, e, account, order, f = kassaOrderFixture(t, input)
				syncKassa(t, s, order.OrderId)
				fund = func() { settleKassa(e, f); syncKassa(t, s, order.OrderId); syncKassa(t, s, order.OrderId) }
			case "cryptomus", "heleket":
				var f *cryptoStub
				s, e, account, order, f = cryptoOrderFixture(t, method, input)
				syncCrypto(t, s, order.OrderId, method)
				fund = func() {
					settleCrypto(e, f)
					syncCrypto(t, s, order.OrderId, method)
					syncCrypto(t, s, order.OrderId, method)
				}
			default:
				var plan uuid.UUID
				s, e, account, plan = purchaseFixture(t)
				in := input(s, e, account, plan)
				if method == "manual" {
					s.cfg.Payments.ManualEnabled = true
					s.cfg.Payments.ManualCardDetails = "Local synthetic recipient"
					in.PaymentMethod, in.PaymentType = "manual", "MANUAL"
				}
				var err error
				order, err = s.createPurchaseOrder(ctx, account, uuid.New(), in)
				if err != nil {
					t.Fatal(err)
				}
			}
			actor := renewalOperator(t, s, e)
			rev, days := int64(1), int64(30)
			op, err := s.createAccessOperation(ctx, actor, account, uuid.New(), wire.AccessOperationInput{Kind: "assign_plan", PlanId: &order.Quote.PlanId, Revision: &rev, PeriodDays: &days, Reason: "same plan reassigned before payment"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.applyAccess(ctx, op.OperationId); err != nil {
				t.Fatal(err)
			}
			if method == "manual" {
				if _, err = s.payments.ReportManualPayment(ctx, account, order.OrderId, uuid.New()); err != nil {
					t.Fatal(err)
				}
				fund = func() {
					key := uuid.New()
					for range 2 {
						if _, err = s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, key, payments.ManualPaymentDecisionInput{Decision: "approve", Reason: "Synthetic money confirmed", ConfirmedAmountMinor: &order.Quote.AmountMinor}); err != nil {
							t.Fatal(err)
						}
					}
				}
			} else if method == "yoomoney" {
				fund = func() {
					fields := purchaseNotice(s, order.OrderId, uuid.NewString(), "90071992547409.00", "90071992547409.93")
					for range 2 {
						if err = s.receiveYooMoney(ctx, fields); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			updates, resets := p.updates, p.resets
			fund()
			got, err := s.purchaseOrder(ctx, account, order.OrderId)
			if err != nil || got.PaymentStatus != "paid" || got.FulfillmentStatus != "needs_review" || !got.ReviewRequired || got.AccessOperationId != nil || p.updates != updates || p.resets != resets {
				t.Fatal("funding failed to retain paid review without automatic work", err)
			}
			manualCounts(t, s, order.OrderId, 1, 0)
			var receiptReview *string
			var funding *string
			if err = e.Pool.QueryRow(ctx, "SELECT r.review_reason,o.funding_operation_id FROM purchase_orders o JOIN purchase_receipts r ON r.order_id=o.id WHERE o.id=$1", order.OrderId).Scan(&receiptReview, &funding); err != nil || receiptReview != nil || funding == nil {
				t.Fatal("eligibility change invalidated an authentic financial proof", err)
			}
		})
	}
}
