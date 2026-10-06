package platform

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"net/url"
	"sync"
	"testing"
	"time"
)

var yooMoneySignature = testkit.YooMoneySignature

func purchaseFixture(t *testing.T) (*Service, *testkit.Env, uuid.UUID, uuid.UUID) {
	t.Helper()
	s, e := fixture(t)
	panelFixture(t, s)
	s.cfg.YooMoneyEnabled = true
	s.cfg.YooMoneyWalletID = "410000000000000"
	s.cfg.YooMoneyNotificationSecret = []byte("test-only-notification-secret")
	account := verified(t, s, e, "purchase@example.test")
	plan := uuid.New()
	terms := catalogueTerms(2)
	terms.Prices[0].AmountMinor = "9007199254740993"
	raw, err := json.Marshal(terms)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(context.Background(), "INSERT INTO catalogue_plans(id,current_revision,current_devices,current_profile,current_hidden) VALUES($1,1,2,'regular',false)", plan); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(context.Background(), "INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,1,$2,false,'legacy_import',$3)", plan, raw, e.Clock()); err != nil {
		t.Fatal(err)
	}
	return s, e, account, plan
}

func TestPurchaseOrderQuoteAndReplay(t *testing.T) {
	s, _, account, plan := purchaseFixture(t)
	in := wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"}
	key := uuid.New()
	out, err := s.CreatePurchaseOrder(context.Background(), account, key, in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Quote.AmountMinor != "9007199254740993" || out.Quote.Devices != 2 || out.Quote.PeriodDays != 30 || out.Checkout == nil || out.Checkout.Fields.Sum != "90071992547409.93" || out.Checkout.Fields.Label != out.OrderId {
		t.Fatalf("wrong server quote: %+v", out)
	}
	again, err := s.CreatePurchaseOrder(context.Background(), account, key, in)
	if err != nil || again.OrderId != out.OrderId {
		t.Fatalf("lost response did not replay order: %v", err)
	}
	in.PaymentType = "PC"
	if _, err = s.CreatePurchaseOrder(context.Background(), account, key, in); !catalogueCode(err, "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("same key changed body: %v", err)
	}
	if _, err = s.CreatePurchaseOrder(context.Background(), account, uuid.New(), in); !catalogueCode(err, "PURCHASE_ORDER_CONFLICT") {
		t.Fatalf("second open order: %v", err)
	}
}

func TestYooMoneyReceiptAndSecondPayment(t *testing.T) {
	s, _, account, plan := purchaseFixture(t)
	ctx := context.Background()
	order, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"})
	if err != nil {
		t.Fatal(err)
	}
	callback := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {"test-payment-1"}, "amount": {"90071992547409.00"}, "withdraw_amount": {"90071992547409.93"}, "currency": {"643"}, "datetime": {"2026-10-01T00:01:00Z"}, "label": {order.OrderId.String()}, "codepro": {"false"}, "unaccepted": {"false"}}
	callback.Set("sign", yooMoneySignature(callback, s.cfg.YooMoneyNotificationSecret))
	if err = s.ReceiveYooMoney(ctx, callback); err != nil {
		t.Fatal(err)
	}
	if err = s.ReceiveYooMoney(ctx, callback); err != nil {
		t.Fatal(err)
	}
	paid, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || paid.PaymentStatus != "paid" || paid.FulfillmentStatus != "queued" || paid.CanPay {
		t.Fatalf("paid state: %+v %v", paid, err)
	}
	var receipts int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM purchase_receipts WHERE order_id=$1", order.OrderId).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("duplicate receipt count %d: %v", receipts, err)
	}
	callback.Set("operation_id", "test-payment-2")
	callback.Set("sign", yooMoneySignature(callback, s.cfg.YooMoneyNotificationSecret))
	if err = s.ReceiveYooMoney(ctx, callback); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM purchase_receipts WHERE order_id=$1", order.OrderId).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatalf("second real payment lost: %d %v", receipts, err)
	}
	paid, err = s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || !paid.ReviewRequired {
		t.Fatalf("second payment not flagged: %+v %v", paid, err)
	}
}

func TestYooMoneyWrongThenCorrectStaysInReview(t *testing.T) {
	s, e, account, plan := purchaseFixture(t)
	ctx := context.Background()
	order, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"})
	if err != nil {
		t.Fatal(err)
	}
	callback := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {"wrong-amount"}, "amount": {"98.00"}, "withdraw_amount": {"100.00"}, "currency": {"643"}, "datetime": {"2026-10-01T00:01:00.123456789Z"}, "label": {order.OrderId.String()}, "codepro": {"false"}, "unaccepted": {"false"}}
	callback.Set("sign", yooMoneySignature(callback, s.cfg.YooMoneyNotificationSecret))
	if err = s.ReceiveYooMoney(ctx, callback); err != nil {
		t.Fatal(err)
	}
	if err = s.ReceiveYooMoney(ctx, callback); err != nil {
		t.Fatalf("nanosecond replay flagged: %v", err)
	}
	got, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || got.PaymentStatus != "paid" || got.FulfillmentStatus != "needs_review" || got.CanPay || got.CanCancel {
		t.Fatalf("wrong gross state: %+v %v", got, err)
	}
	actor := verified(t, s, e, "purchase-review-operator@example.test")
	if err = s.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReconcilePurchaseOrder(ctx, actor, account, order.OrderId, uuid.New(), wire.PurchaseReconcileInput{Reason: "investigate"}); !catalogueCode(err, "PURCHASE_ORDER_CONFLICT") {
		t.Fatalf("underpayment retried as funding: %v", err)
	}
	if _, err = s.pool.Exec(ctx, "UPDATE purchase_orders SET fulfillment_status='queued' WHERE id=$1", order.OrderId); err != nil {
		t.Fatal(err)
	}
	if err = s.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	var accessCreated bool
	if err = s.pool.QueryRow(ctx, "SELECT access_operation_id IS NOT NULL FROM purchase_orders WHERE id=$1", order.OrderId).Scan(&accessCreated); err != nil || accessCreated {
		t.Fatalf("unfunded paid order issued access: %v %v", accessCreated, err)
	}
	callback.Set("operation_id", "right-after-wrong")
	callback.Set("withdraw_amount", "90071992547409.93")
	callback.Set("amount", "90071992547409.00")
	callback.Set("sign", yooMoneySignature(callback, s.cfg.YooMoneyNotificationSecret))
	if err = s.ReceiveYooMoney(ctx, callback); err != nil {
		t.Fatal(err)
	}
	got, err = s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || got.FulfillmentStatus != "needs_review" || got.AccessOperationId != nil {
		t.Fatalf("second transfer auto-issued: %+v %v", got, err)
	}
}

func TestYooMoneyLateAndProtectedStayInReview(t *testing.T) {
	for _, kind := range []string{"late", "protected"} {
		t.Run(kind, func(t *testing.T) {
			s, _, account, plan := purchaseFixture(t)
			ctx := context.Background()
			order, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"})
			if err != nil {
				t.Fatal(err)
			}
			when := "2026-10-01T00:01:00Z"
			codepro := "false"
			if kind == "late" {
				when = "2026-10-01T00:31:00Z"
				s.now = func() time.Time { return time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC) }
			} else {
				codepro = "true"
			}
			callback := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {kind + "-payment"}, "amount": {"90071992547409.00"}, "withdraw_amount": {"90071992547409.93"}, "currency": {"643"}, "datetime": {when}, "label": {order.OrderId.String()}, "codepro": {codepro}, "unaccepted": {"false"}}
			callback.Set("sign", yooMoneySignature(callback, s.cfg.YooMoneyNotificationSecret))
			if err = s.ReceiveYooMoney(ctx, callback); err != nil {
				t.Fatal(err)
			}
			got, err := s.PurchaseOrder(ctx, account, order.OrderId)
			if err != nil || got.FulfillmentStatus != "needs_review" || got.CanPay || got.CanCancel || got.AccessOperationId != nil {
				t.Fatalf("review state: %+v %v", got, err)
			}
			if kind == "late" && got.PaymentStatus != "paid" {
				t.Fatal("late real transfer not marked paid")
			}
			if kind == "protected" && got.PaymentStatus != "pending" {
				t.Fatal("protected transfer marked available money")
			}
		})
	}
}

func TestYooMoneyExpiredOrderAndNewOrderCannotBothFulfill(t *testing.T) {
	s, _, account, plan := purchaseFixture(t)
	ctx := context.Background()
	in := wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"}
	first, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), in)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Date(2026, 10, 1, 0, 31, 0, 0, time.UTC) }
	second, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), in)
	if err != nil {
		t.Fatal(err)
	}
	makeCallback := func(id uuid.UUID, operation, when string) url.Values {
		v := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {operation}, "amount": {"90071992547409.00"}, "withdraw_amount": {"90071992547409.93"}, "currency": {"643"}, "datetime": {when}, "label": {id.String()}, "codepro": {"false"}, "unaccepted": {"false"}}
		v.Set("sign", yooMoneySignature(v, s.cfg.YooMoneyNotificationSecret))
		return v
	}
	callbacks := []url.Values{makeCallback(first.OrderId, "old-in-window", "2026-10-01T00:29:00Z"), makeCallback(second.OrderId, "new-in-window", "2026-10-01T00:31:00Z")}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	for _, v := range callbacks {
		wg.Add(1)
		go func(v url.Values) { defer wg.Done(); errorsCh <- s.ReceiveYooMoney(ctx, v) }(v)
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	var queued, review, receipts int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FILTER (WHERE fulfillment_status='queued'),count(*) FILTER (WHERE fulfillment_status='needs_review') FROM purchase_orders WHERE account_id=$1", account).Scan(&queued, &review); err != nil || queued != 1 || review != 1 {
		t.Fatalf("two first purchases: queued=%d review=%d err=%v", queued, review, err)
	}
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM purchase_receipts WHERE order_id=$1 OR order_id=$2", first.OrderId, second.OrderId).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatalf("receipt lost: %d %v", receipts, err)
	}
	for _, id := range []uuid.UUID{first.OrderId, second.OrderId} {
		order, err := s.PurchaseOrder(ctx, account, id)
		if err != nil || order.CanPay || order.CanCancel {
			t.Fatalf("stale checkout still open: %+v %v", order, err)
		}
	}
	current, err := s.CurrentPurchaseOrder(ctx, account)
	if err != nil || current.Order == nil || current.Order.FulfillmentStatus != "needs_review" {
		t.Fatalf("review payment hidden from current order: %+v %v", current, err)
	}
}

func TestPurchaseCurrentPrefersPaidOlderOrder(t *testing.T) {
	s, _, account, plan := purchaseFixture(t)
	ctx := context.Background()
	in := wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"}
	first, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), in)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Date(2026, 10, 1, 0, 31, 0, 0, time.UTC) }
	if _, err = s.CreatePurchaseOrder(ctx, account, uuid.New(), in); err != nil {
		t.Fatal(err)
	}
	callback := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {"older-window-payment"}, "amount": {"90071992547409.00"}, "withdraw_amount": {"90071992547409.93"}, "currency": {"643"}, "datetime": {"2026-10-01T00:29:00Z"}, "label": {first.OrderId.String()}, "codepro": {"false"}, "unaccepted": {"false"}}
	callback.Set("sign", yooMoneySignature(callback, s.cfg.YooMoneyNotificationSecret))
	if err = s.ReceiveYooMoney(ctx, callback); err != nil {
		t.Fatal(err)
	}
	current, err := s.CurrentPurchaseOrder(ctx, account)
	if err != nil || current.Order == nil || current.Order.OrderId != first.OrderId || current.Order.PaymentStatus != "paid" {
		t.Fatalf("paid older order hidden: %+v %v", current, err)
	}
}

func TestPurchaseFulfillmentPreservesTrial(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	s.cfg.YooMoneyEnabled = true
	s.cfg.YooMoneyWalletID = "410000000000000"
	s.cfg.YooMoneyNotificationSecret = []byte("test-only-notification-secret")
	ctx := context.Background()
	account, trial := approved(t, s, e, "trial-purchase@example.test")
	if err := s.Provision(ctx, trial); err != nil {
		t.Fatal(err)
	}
	beforeExpiry := integer(t, p.client["expiryTime"])
	beforeID, beforeSub, beforeKey := p.client["id"], p.client["subId"], p.client["email"]
	plan := uuid.New()
	terms := catalogueTerms(2)
	terms.Prices[0].AmountMinor = "10000"
	raw, _ := json.Marshal(terms)
	if _, err := e.Pool.Exec(ctx, "INSERT INTO catalogue_plans(id,current_revision,current_devices,current_profile,current_hidden) VALUES($1,1,2,'regular',false)", plan); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, "INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,1,$2,false,'legacy_import',$3)", plan, raw, e.Clock()); err != nil {
		t.Fatal(err)
	}
	order, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"})
	if err != nil {
		t.Fatal(err)
	}
	callback := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {"trial-payment"}, "amount": {"98.00"}, "withdraw_amount": {"100.00"}, "currency": {"643"}, "datetime": {"2026-10-01T00:01:00Z"}, "label": {order.OrderId.String()}, "codepro": {"false"}, "unaccepted": {"false"}}
	callback.Set("sign", yooMoneySignature(callback, s.cfg.YooMoneyNotificationSecret))
	if err = s.ReceiveYooMoney(ctx, callback); err != nil {
		t.Fatal(err)
	}
	if err = s.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	got, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || got.AccessOperationId == nil {
		t.Fatalf("operation missing: %+v %v", got, err)
	}
	if err = s.ApplyAccess(ctx, *got.AccessOperationId); err != nil {
		t.Fatal(err)
	}
	if p.client["id"] != beforeID || p.client["subId"] != beforeSub || p.client["email"] != beforeKey {
		t.Fatal("trial identity changed")
	}
	if integer(t, p.client["expiryTime"]) != beforeExpiry+int64(30*24*time.Hour/time.Millisecond) {
		t.Fatal("remaining trial time lost")
	}
	if integer(t, p.client["limitIp"]) != 3 || integer(t, p.client["totalGB"]) != 100*1024*1024*1024 || p.resets != 1 {
		t.Fatal("paid access terms not applied")
	}
	got, err = s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || got.FulfillmentStatus != "applied" {
		t.Fatalf("readback not published: %+v %v", got, err)
	}
}

func TestPurchaseRejectsPerpetualNativeAccessBeforeCheckout(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	s.cfg.YooMoneyEnabled = true
	s.cfg.YooMoneyWalletID = "410000000000000"
	s.cfg.YooMoneyNotificationSecret = []byte("test-only-notification-secret")
	ctx := context.Background()
	account, trial := approved(t, s, e, "perpetual-purchase@example.test")
	if err := s.Provision(ctx, trial); err != nil {
		t.Fatal(err)
	}
	p.client["expiryTime"] = json.Number("0")
	plan := uuid.New()
	terms := catalogueTerms(2)
	terms.Prices[0].AmountMinor = "10000"
	raw, _ := json.Marshal(terms)
	if _, err := e.Pool.Exec(ctx, "INSERT INTO catalogue_plans(id,current_revision,current_devices,current_profile,current_hidden) VALUES($1,1,2,'regular',false)", plan); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, "INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,1,$2,false,'legacy_import',$3)", plan, raw, e.Clock()); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"})
	if !catalogueCode(err, "PURCHASE_NOT_ELIGIBLE") {
		t.Fatalf("perpetual native client got checkout: %v", err)
	}
	if count(t, e, "purchase_orders") != 0 {
		t.Fatal("rejected checkout persisted")
	}
}

func TestPurchaseRejectsDisabledUnexpiredClientBeforeCheckout(t *testing.T) {
	s, e, _, plan := purchaseFixture(t)
	p := panelFixture(t, s)
	ctx := context.Background()
	account, trial := approved(t, s, e, "disabled-purchase@example.test")
	if err := s.Provision(ctx, trial); err != nil {
		t.Fatal(err)
	}
	if integer(t, p.client["expiryTime"]) <= e.Clock().UnixMilli() {
		t.Fatal("fixture must have an unexpired finite client")
	}
	p.client["enable"] = false
	jobs, access, adds := count(t, e, "river_job"), count(t, e, "access_operations"), p.adds
	_, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"})
	if !catalogueCode(err, "PURCHASE_NOT_ELIGIBLE") {
		t.Fatalf("disabled unexpired client got checkout: %v", err)
	}
	if count(t, e, "purchase_orders") != 0 || count(t, e, "river_job") != jobs || count(t, e, "access_operations") != access || p.adds != adds || p.updates != 0 || p.client["enable"] != false {
		t.Fatal("rejected checkout persisted work or changed native access")
	}
}

func TestPurchaseAccessReconcileDoesNotDependOnOperatorRole(t *testing.T) {
	s, e, account, plan := purchaseFixture(t)
	ctx := context.Background()
	order, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"})
	if err != nil {
		t.Fatal(err)
	}
	callback := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {"reconcile-payment"}, "amount": {"90071992547409.00"}, "withdraw_amount": {"90071992547409.93"}, "currency": {"643"}, "datetime": {"2026-10-01T00:01:00Z"}, "label": {order.OrderId.String()}, "codepro": {"false"}, "unaccepted": {"false"}}
	callback.Set("sign", yooMoneySignature(callback, s.cfg.YooMoneyNotificationSecret))
	if err = s.ReceiveYooMoney(ctx, callback); err != nil {
		t.Fatal(err)
	}
	if err = s.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || prepared.AccessOperationId == nil {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	if _, err = e.Pool.Exec(ctx, "UPDATE access_operations SET status='needs_review',review_reason='test_review' WHERE id=$1", *prepared.AccessOperationId); err != nil {
		t.Fatal(err)
	}
	actor := verified(t, s, e, "purchase-reconcile-actor@example.test")
	if err = s.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReconcileAccessOperation(ctx, actor, account, *prepared.AccessOperationId, uuid.New(), wire.AccessReconcileInput{Reason: "resume paid access"}); err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeOperatorRole(ctx, actor, false); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyAccess(ctx, *prepared.AccessOperationId); err != nil {
		t.Fatal(err)
	}
	final, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || final.FulfillmentStatus != "applied" {
		t.Fatalf("operator revocation blocked paid access: %+v %v", final, err)
	}
}

func TestPurchaseDisputedFundingBlocksNativeWrite(t *testing.T) {
	s, e, account, plan := purchaseFixture(t)
	ctx := context.Background()
	order, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"})
	if err != nil {
		t.Fatal(err)
	}
	callback := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {"disputed-payment"}, "amount": {"90071992547409.00"}, "withdraw_amount": {"90071992547409.93"}, "currency": {"643"}, "datetime": {"2026-10-01T00:01:00Z"}, "label": {order.OrderId.String()}, "codepro": {"false"}, "unaccepted": {"false"}}
	callback.Set("sign", yooMoneySignature(callback, s.cfg.YooMoneyNotificationSecret))
	if err = s.ReceiveYooMoney(ctx, callback); err != nil {
		t.Fatal(err)
	}
	if err = s.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || prepared.AccessOperationId == nil {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	p := panelFixture(t, s)
	p.afterRead = func() {
		_, err = e.Pool.Exec(ctx, "UPDATE purchase_receipts SET review_reason='conflicting_operation_id' WHERE operation_id='disputed-payment'")
		if err != nil {
			t.Error(err)
		}
	}
	if err = s.ApplyAccess(ctx, *prepared.AccessOperationId); err != nil {
		t.Fatal(err)
	}
	if p.adds != 0 {
		t.Fatal("native add after funding dispute")
	}
	var assigned bool
	if err = e.Pool.QueryRow(ctx, "SELECT assigned_panel_id IS NOT NULL FROM accounts WHERE id=$1", account).Scan(&assigned); err != nil || assigned {
		t.Fatalf("unfunded native write: assigned=%v %v", assigned, err)
	}
	got, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || got.PaymentStatus != "paid" || got.FulfillmentStatus != "needs_review" {
		t.Fatalf("paid review lost: %+v %v", got, err)
	}
}
