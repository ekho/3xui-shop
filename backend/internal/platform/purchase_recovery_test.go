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

func purchaseInput(plan uuid.UUID) wire.PurchaseOrderInput {
	return wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "yoomoney", PaymentType: "AC"}
}

func purchaseNotice(s *Service, id uuid.UUID, operation, amount, gross string) url.Values {
	v := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {operation}, "amount": {amount}, "withdraw_amount": {gross}, "currency": {"643"}, "datetime": {"2026-10-01T00:01:00Z"}, "label": {id.String()}, "codepro": {"false"}, "unaccepted": {"false"}}
	v.Set("sign", yooMoneySignature(v, s.cfg.YooMoneyNotificationSecret))
	return v
}

func paidPurchase(t *testing.T) (*Service, *testkit.Env, uuid.UUID, wire.PurchaseOrder) {
	t.Helper()
	s, e, account, plan := purchaseFixture(t)
	order, err := s.CreatePurchaseOrder(context.Background(), account, uuid.New(), purchaseInput(plan))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReceiveYooMoney(context.Background(), purchaseNotice(s, order.OrderId, uuid.NewString(), "90071992547409.00", "90071992547409.93")); err != nil {
		t.Fatal(err)
	}
	return s, e, account, order
}

func TestPurchasePretargetRecoveryClearsFulfillmentReview(t *testing.T) {
	s, e, account, order := paidPurchase(t)
	ctx := context.Background()
	if err := s.purchaseReview(ctx, order.OrderId, "access_conflict"); err != nil {
		t.Fatal(err)
	}
	actor := verified(t, s, e, "purchase-pretarget-operator@example.test")
	if err := s.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	queued, err := s.ReconcilePurchaseOrder(ctx, actor, account, order.OrderId, uuid.New(), wire.PurchaseReconcileInput{Reason: "resolved access conflict"})
	if err != nil || queued.FulfillmentStatus != "queued" || queued.ReviewRequired {
		t.Fatalf("ordinary review survived successful preparation retry: %+v %v", queued, err)
	}
	if err = s.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || prepared.AccessOperationId == nil {
		t.Fatalf("same paid order not prepared: %+v %v", prepared, err)
	}
	if err = s.ApplyAccess(ctx, *prepared.AccessOperationId); err != nil {
		t.Fatal(err)
	}
	final, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || final.FulfillmentStatus != "applied" || final.ReviewRequired {
		t.Fatalf("ordinary review survived applied recovery: %+v %v", final, err)
	}
}

func TestPurchasePosttargetRecoveryClearsFulfillmentReview(t *testing.T) {
	s, e, account, order := paidPurchase(t)
	ctx := context.Background()
	p := panelFixture(t, s)
	if err := s.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || prepared.AccessOperationId == nil {
		t.Fatalf("target absent: %+v %v", prepared, err)
	}
	p.loseReset = true
	if err = s.ApplyAccess(ctx, *prepared.AccessOperationId); err != nil {
		t.Fatal(err)
	}
	review, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || review.FulfillmentStatus != "needs_review" || !review.ReviewRequired || p.resets != 1 {
		t.Fatalf("ambiguous reset not held: %+v %v resets=%d", review, err, p.resets)
	}
	actor := verified(t, s, e, "purchase-posttarget-operator@example.test")
	if err = s.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReconcileAccessOperation(ctx, actor, account, *prepared.AccessOperationId, uuid.New(), wire.AccessReconcileInput{Reason: "zero traffic confirmed"}); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyAccess(ctx, *prepared.AccessOperationId); err != nil {
		t.Fatal(err)
	}
	final, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || final.FulfillmentStatus != "applied" || final.ReviewRequired || p.resets != 1 {
		t.Fatalf("recovered purchase still requires support or repeated reset: %+v %v resets=%d", final, err, p.resets)
	}
}

func TestPurchaseReceiptDisputeSurvivesFulfillmentRecovery(t *testing.T) {
	s, e, account, order := paidPurchase(t)
	ctx := context.Background()
	if err := s.ReceiveYooMoney(ctx, purchaseNotice(s, order.OrderId, "second-real-transfer", "90071992547409.00", "90071992547409.93")); err != nil {
		t.Fatal(err)
	}
	if err := s.purchaseReview(ctx, order.OrderId, "access_conflict"); err != nil {
		t.Fatal(err)
	}
	actor := verified(t, s, e, "purchase-dispute-operator@example.test")
	if err := s.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	queued, err := s.ReconcilePurchaseOrder(ctx, actor, account, order.OrderId, uuid.New(), wire.PurchaseReconcileInput{Reason: "resolved access conflict"})
	if err != nil || !queued.ReviewRequired {
		t.Fatalf("second real transfer review cleared by preparation retry: %+v %v", queued, err)
	}
	if err = s.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || prepared.AccessOperationId == nil {
		t.Fatalf("funded target absent: %+v %v", prepared, err)
	}
	if err = s.ApplyAccess(ctx, *prepared.AccessOperationId); err != nil {
		t.Fatal(err)
	}
	final, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || final.FulfillmentStatus != "applied" || !final.ReviewRequired {
		t.Fatalf("second transfer review lost after access applied: %+v %v", final, err)
	}
	var reason string
	if err = s.pool.QueryRow(ctx, "SELECT review_reason FROM purchase_orders WHERE id=$1", order.OrderId).Scan(&reason); err != nil || reason != "additional_payment" {
		t.Fatalf("money review reason lost: %q %v", reason, err)
	}
}

func TestPurchaseSecondReceiptAfterAppliedKeepsOneAccessTarget(t *testing.T) {
	s, _, account, order := paidPurchase(t)
	ctx := context.Background()
	p := panelFixture(t, s)
	if err := s.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || prepared.AccessOperationId == nil {
		t.Fatalf("target absent: %+v %v", prepared, err)
	}
	if err = s.ApplyAccess(ctx, *prepared.AccessOperationId); err != nil {
		t.Fatal(err)
	}
	expiry, resets := integer(t, p.client["expiryTime"]), p.resets
	if err = s.ReceiveYooMoney(ctx, purchaseNotice(s, order.OrderId, "second-after-applied", "90071992547409.00", "90071992547409.93")); err != nil {
		t.Fatal(err)
	}
	if err = s.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	final, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || final.FulfillmentStatus != "applied" || !final.ReviewRequired || final.AccessOperationId == nil || *final.AccessOperationId != *prepared.AccessOperationId {
		t.Fatalf("second receipt changed applied access state: %+v %v", final, err)
	}
	var receipts, targets int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM purchase_receipts WHERE order_id=$1", order.OrderId).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatalf("second payment fact lost: %d %v", receipts, err)
	}
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM access_operations WHERE purchase_order_id=$1", order.OrderId).Scan(&targets); err != nil || targets != 1 {
		t.Fatalf("second target created: %d %v", targets, err)
	}
	if p.resets != resets || integer(t, p.client["expiryTime"]) != expiry {
		t.Fatalf("second receipt repeated native change: resets=%d expiry=%v", p.resets, p.client["expiryTime"])
	}
}

func TestPurchaseCancelAndNotificationOrder(t *testing.T) {
	for _, sequence := range []string{"cancel-first", "callback-first", "concurrent"} {
		t.Run(sequence, func(t *testing.T) {
			s, _, account, plan := purchaseFixture(t)
			ctx := context.Background()
			order, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
			if err != nil {
				t.Fatal(err)
			}
			notice := purchaseNotice(s, order.OrderId, "cancel-race-transfer", "90071992547409.00", "90071992547409.93")
			var cancelErr, callbackErr error
			switch sequence {
			case "cancel-first":
				_, cancelErr = s.CancelPurchaseOrder(ctx, account, order.OrderId, uuid.New())
				callbackErr = s.ReceiveYooMoney(ctx, notice)
			case "callback-first":
				callbackErr = s.ReceiveYooMoney(ctx, notice)
				_, cancelErr = s.CancelPurchaseOrder(ctx, account, order.OrderId, uuid.New())
			case "concurrent":
				start := make(chan struct{})
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-start
					_, cancelErr = s.CancelPurchaseOrder(ctx, account, order.OrderId, uuid.New())
				}()
				go func() { defer wg.Done(); <-start; callbackErr = s.ReceiveYooMoney(ctx, notice) }()
				close(start)
				wg.Wait()
			}
			if callbackErr != nil || (cancelErr != nil && !catalogueCode(cancelErr, "PURCHASE_ORDER_CONFLICT")) {
				t.Fatalf("cancel/callback failed: cancel=%v callback=%v", cancelErr, callbackErr)
			}
			paid, err := s.PurchaseOrder(ctx, account, order.OrderId)
			if err != nil || paid.PaymentStatus != "paid" || paid.CanPay || paid.CanCancel {
				t.Fatalf("real transfer lost or checkout active: %+v %v", paid, err)
			}
			var receipts, targets int
			if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM purchase_receipts WHERE order_id=$1", order.OrderId).Scan(&receipts); err != nil || receipts != 1 {
				t.Fatalf("real receipt lost or doubled: %d %v", receipts, err)
			}
			if err = s.FulfillPurchase(ctx, order.OrderId); err != nil {
				t.Fatal(err)
			}
			if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM access_operations WHERE purchase_order_id=$1", order.OrderId).Scan(&targets); err != nil || targets > 1 {
				t.Fatalf("duplicate access target: %d %v", targets, err)
			}
			if cancelErr == nil && (paid.FulfillmentStatus != "needs_review" || targets != 0) {
				t.Fatalf("canceled order auto-issued paid access: %+v targets=%d", paid, targets)
			}
			if cancelErr != nil && targets != 1 {
				t.Fatalf("in-window paid order lacked one target: %d %v", targets, cancelErr)
			}
		})
	}
}

func TestPurchaseExpiredFiniteTrialStartsPeriodNow(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	s.cfg.YooMoneyEnabled = true
	s.cfg.YooMoneyWalletID = "410000000000000"
	s.cfg.YooMoneyNotificationSecret = []byte("test-only-notification-secret")
	ctx := context.Background()
	account, trial := approved(t, s, e, "expired-trial-purchase@example.test")
	if err := s.Provision(ctx, trial); err != nil {
		t.Fatal(err)
	}
	beforeID, beforeSub, beforeKey := p.client["id"], p.client["subId"], p.client["email"]
	p.client["expiryTime"] = json.Number("1")
	p.client["enable"] = false
	var grantID uuid.UUID
	if err := s.pool.QueryRow(ctx, "SELECT operation_id FROM trial_grants WHERE account_id=$1", account).Scan(&grantID); err != nil {
		t.Fatal(err)
	}
	plan := uuid.New()
	terms := catalogueTerms(2)
	terms.Prices[0].AmountMinor = "10000"
	raw, err := json.Marshal(terms)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, "INSERT INTO catalogue_plans(id,current_revision,current_devices,current_profile,current_hidden) VALUES($1,1,2,'regular',false)", plan); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, "INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,1,$2,false,'legacy_import',$3)", plan, raw, e.Clock()); err != nil {
		t.Fatal(err)
	}
	order, err := s.CreatePurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReceiveYooMoney(ctx, purchaseNotice(s, order.OrderId, "expired-trial-transfer", "98.00", "100.00")); err != nil {
		t.Fatal(err)
	}
	if err = s.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || prepared.AccessOperationId == nil {
		t.Fatalf("target absent: %+v %v", prepared, err)
	}
	if err = s.ApplyAccess(ctx, *prepared.AccessOperationId); err != nil {
		t.Fatal(err)
	}
	wantExpiry := e.Clock().UnixMilli() + int64(30*24*time.Hour/time.Millisecond)
	if got := integer(t, p.client["expiryTime"]); got != wantExpiry {
		t.Fatalf("expired finite trial did not start paid period now: got=%d want=%d", got, wantExpiry)
	}
	if p.client["id"] != beforeID || p.client["subId"] != beforeSub || p.client["email"] != beforeKey || p.client["enable"] != true || p.resets != 1 {
		t.Fatalf("expired trial identity or paid activation changed: client=%v resets=%d", p.client, p.resets)
	}
	var afterGrant uuid.UUID
	if err = s.pool.QueryRow(ctx, "SELECT operation_id FROM trial_grants WHERE account_id=$1", account).Scan(&afterGrant); err != nil || afterGrant != grantID {
		t.Fatalf("trial grant lost: %s %v", afterGrant, err)
	}
}

func TestPurchaseAmbiguousResetRequiresExplicitCostAcknowledgement(t *testing.T) {
	s, e, account, order := paidPurchase(t)
	ctx := context.Background()
	p := panelFixture(t, s)
	if err := s.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || prepared.AccessOperationId == nil {
		t.Fatalf("target absent: %+v %v", prepared, err)
	}
	p.loseReset = true
	if err = s.ApplyAccess(ctx, *prepared.AccessOperationId); err != nil || p.resets != 1 {
		t.Fatalf("first ambiguous reset: %v resets=%d", err, p.resets)
	}
	if err = s.ApplyAccess(ctx, *prepared.AccessOperationId); err != nil || p.resets != 1 {
		t.Fatalf("unreconciled purchase reset repeated: %v resets=%d", err, p.resets)
	}
	actor := verified(t, s, e, "purchase-reset-operator@example.test")
	if err = s.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	p.up = 77 // Traffic after the ambiguous reset makes a read-only retry unsafe.
	if _, err = s.ReconcileAccessOperation(ctx, actor, account, *prepared.AccessOperationId, uuid.New(), wire.AccessReconcileInput{Reason: "inspect traffic"}); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyAccess(ctx, *prepared.AccessOperationId); err != nil || p.resets != 1 {
		t.Fatalf("read-only retry repeated destructive reset: %v resets=%d", err, p.resets)
	}
	stillReview, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || stillReview.FulfillmentStatus != "needs_review" || !stillReview.ReviewRequired {
		t.Fatalf("positive traffic escaped review: %+v %v", stillReview, err)
	}
	if _, err = s.ReconcileAccessOperation(ctx, actor, account, *prepared.AccessOperationId, uuid.New(), wire.AccessReconcileInput{Reason: "accept one more reset", AcknowledgeResetCost: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyAccess(ctx, *prepared.AccessOperationId); err != nil || p.resets != 2 || p.up != 0 {
		t.Fatalf("acknowledged reset not applied exactly once: %v resets=%d traffic=%d", err, p.resets, p.up)
	}
	final, err := s.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || final.FulfillmentStatus != "applied" || final.ReviewRequired {
		t.Fatalf("acknowledged purchase recovery not completed: %+v %v", final, err)
	}
}
