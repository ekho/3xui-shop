package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"net/url"
	"strings"
	"testing"
	"time"
)

func paymentAccount(t *testing.T, e *testkit.Env) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := e.Pool.Exec(context.Background(), `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
		VALUES($1,$2,'en','test-only-hash',$3,$4,$5,$6,'1','1')`, id, id.String()+"@example.test", e.Clock(), uuid.New(), strings.ReplaceAll(id.String(), "-", "")[:16], "acct_"+id.String())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A DTO field/tag/order change must not invalidate hashes or old replay results.
func TestPaymentsPersistedCompatibility(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	s := composeForTest(e.Pool, e.Redis, nil, app.Config{})
	account, actor, plan, order, operation, key := paymentAccount(t, e), paymentAccount(t, e), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	in := wire.PurchaseOrderInput{Action: "purchase", PaymentMethod: "yoomoney", PaymentType: "AC", PeriodDays: 30, PlanId: plan, Revision: 1}
	neutral := payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "yoomoney", PaymentType: "AC", PeriodDays: 30, PlanId: plan, Revision: 1}
	quote := wire.PurchaseQuote{AmountMinor: "9007199254740993", Currency: "RUB", Devices: 2, PeriodDays: 30, PlanId: plan, Profile: "regular", Revision: 1, TrafficGb: 10}
	neutralQuote := payments.PurchaseQuote{AmountMinor: "9007199254740993", Currency: "RUB", Devices: 2, PeriodDays: 30, PlanId: plan, Profile: "regular", Revision: 1, TrafficGb: 10}
	created := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	expires := created.Add(30 * time.Minute)
	oldResult := wire.PurchaseOrder{AccessOperationId: &operation, Action: "purchase", CreatedAt: created, Expired: true, ExpiresAt: expires, FulfillmentStatus: "applied", OrderId: order, PaymentMethod: "yoomoney", PaymentStatus: "paid", PaymentType: "AC", Quote: quote, ReviewRequired: true,
		Checkout: &wire.YooMoneyCheckout{Action: "https://yoomoney.ru/quickpay/confirm", Method: "POST", Fields: wire.YooMoneyCheckoutFields{Label: order, PaymentType: "AC", QuickpayForm: "button", Receiver: "test-wallet", SuccessURL: "https://cabinet.example.test/orders/" + order.String(), Sum: "90071992547409.93"}}}
	neutralResult := payments.PurchaseOrder{AccessOperationId: &operation, Action: "purchase", CreatedAt: created, Expired: true, ExpiresAt: expires, FulfillmentStatus: "applied", OrderId: order, PaymentMethod: "yoomoney", PaymentStatus: "paid", PaymentType: "AC", Quote: neutralQuote, ReviewRequired: true,
		Checkout: &payments.YooMoneyCheckout{Action: "https://yoomoney.ru/quickpay/confirm", Method: "POST", Fields: payments.YooMoneyCheckoutFields{Label: order, PaymentType: "AC", QuickpayForm: "button", Receiver: "test-wallet", SuccessURL: "https://cabinet.example.test/orders/" + order.String(), Sum: "90071992547409.93"}}}
	assertJSON := func(old, current any) {
		t.Helper()
		before, err := json.Marshal(old)
		if err != nil {
			t.Fatal(err)
		}
		after, err := json.Marshal(current)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("persisted JSON changed: %s -> %s (%v)", before, after, err)
		}
	}
	assertJSON(in, neutral)
	assertJSON(quote, neutralQuote)
	assertJSON(oldResult, neutralResult)
	assertJSON(wire.CurrentPurchaseOrder{Order: &oldResult}, payments.CurrentPurchaseOrder{Order: &neutralResult})
	assertJSON(wire.CurrentPurchaseOrder{}, payments.CurrentPurchaseOrder{})
	assertJSON(wire.PaymentMethods{Methods: []wire.PaymentMethod{}}, payments.PaymentMethods{Methods: []payments.PaymentMethod{}})
	assertJSON(wire.PaymentMethods{Methods: []wire.PaymentMethod{{Id: "yoomoney", Currency: "RUB"}}}, payments.PaymentMethods{Methods: []payments.PaymentMethod{{Id: "yoomoney", Currency: "RUB"}}})
	assertJSON(wire.PurchaseReconcileInput{Reason: " revisit "}, payments.PurchaseReconcileInput{Reason: " revisit "})
	raw, _ := json.Marshal(in)
	hash := sha256.Sum256(raw)
	quoteRaw, _ := json.Marshal(quote)
	if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,payment_status,active,created_at,expires_at)
		VALUES($1,$2,$3,$4,$5,9007199254740993,'AC','canceled',false,$6,$7)`, order, account, key, hash[:], quoteRaw, created, expires); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.createPurchaseOrder(ctx, account, key, in)
	if err != nil || replayed.OrderId != order || replayed.Quote != quote || replayed.PaymentStatus != "canceled" || replayed.Checkout != nil {
		t.Fatal("old order hash/quote did not replay with payment disabled", err)
	}
	if err = s.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	reconcileKey := uuid.New()
	reason := wire.PurchaseReconcileInput{Reason: " revisit "}
	oldBody, _ := json.Marshal(struct {
		Target, ID uuid.UUID
		Input      wire.PurchaseReconcileInput
	}{account, order, reason})
	oldHash := sha256.Sum256(oldBody)
	result, _ := json.Marshal(oldResult)
	if _, err = e.Pool.Exec(ctx, `INSERT INTO idempotency_records(principal,operation,key,body_hash,result,created_at)
		VALUES($1,'reconcilePurchaseOrder',$2,$3,$4,$5)`, "operator-account:"+actor.String(), reconcileKey, oldHash[:], result, created); err != nil {
		t.Fatal(err)
	}
	got, err := s.reconcilePurchaseOrder(ctx, actor, account, order, reconcileKey, reason)
	if err != nil {
		t.Fatal("old reconcile result did not replay", err)
	}
	assertJSON(oldResult, got)
}

// Miswiring the production owner or losing caller Tx breaks money/job atomicity.
func TestPaymentsComposition(t *testing.T) {
	for _, queued := range []bool{true, false} {
		t.Run(map[bool]string{true: "receipt-and-job", false: "nil-queue-rollback"}[queued], func(t *testing.T) {
			e := testkit.Open(t)
			ctx := context.Background()
			var queue *river.Client[pgx.Tx]
			if queued {
				var err error
				queue, err = river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
				if err != nil {
					t.Fatal(err)
				}
			}
			secret := []byte("test-only-notification-secret")
			s := composeForTest(e.Pool, e.Redis, queue, app.Config{Payments: payments.Config{YooMoneyEnabled: true, YooMoneyNotificationSecret: secret}})
			account, order, plan := paymentAccount(t, e), uuid.New(), uuid.New()
			now := time.Now().UTC().Truncate(time.Microsecond)
			quote := wire.PurchaseQuote{AmountMinor: "10000", Currency: "RUB", Devices: 2, PeriodDays: 30, PlanId: plan, Profile: "regular", Revision: 1}
			quoteRaw, _ := json.Marshal(quote)
			if _, err := e.Pool.Exec(ctx, "INSERT INTO catalogue_plans(id,current_revision,current_devices,current_profile,current_hidden) VALUES($1,1,2,'regular',false)", plan); err != nil {
				t.Fatal(err)
			}
			terms := []byte(`{"devices":2,"traffic_gb":0,"profile":"regular","hidden":false,"periods":[30],"prices":[{"amount_minor":"10000","currency":"RUB","period_days":30},{"amount_minor":"0","currency":"USD","period_days":30},{"amount_minor":"0","currency":"XTR","period_days":30}]}`)
			if _, err := e.Pool.Exec(ctx, "INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,1,$2,false,'legacy_import',$3)", plan, terms, now); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,created_at,expires_at)
				VALUES($1,$2,$3,$4,$5,10000,'AC',$6,$7)`, order, account, uuid.New(), []byte{1}, quoteRaw, now, now.Add(30*time.Minute)); err != nil {
				t.Fatal(err)
			}
			fields := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {"composed-payment"}, "amount": {"98.00"}, "withdraw_amount": {"100.00"}, "currency": {"643"}, "datetime": {now.Format(time.RFC3339Nano)}, "label": {order.String()}, "codepro": {"false"}, "unaccepted": {"false"}}
			fields.Set("sign", testkit.YooMoneySignature(fields, secret))
			err := s.receiveYooMoney(ctx, fields)
			if !queued {
				if err == nil || err.Error() != "SERVICE_UNAVAILABLE" {
					t.Fatal("nil queue accepted a paid receipt", err)
				}
			} else {
				if err != nil || s.receiveYooMoney(ctx, fields) != nil {
					t.Fatal("signed receipt/repeat failed", err)
				}
			}
			var receipts, jobs int
			var status string
			var funding *string
			if err = e.Pool.QueryRow(ctx, `SELECT payment_status,funding_operation_id,(SELECT count(*) FROM purchase_receipts),(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment') FROM purchase_orders WHERE id=$1`, order).Scan(&status, &funding, &receipts, &jobs); err != nil {
				t.Fatal(err)
			}
			if !queued {
				if status != "pending" || funding != nil || receipts != 0 || jobs != 0 {
					t.Fatal("nil queue partially committed money", status, receipts, jobs)
				}
				return
			}
			if status != "paid" || funding == nil || *funding != "composed-payment" || receipts != 1 || jobs != 1 {
				t.Fatal("receipt/funding/job lost or duplicated", status, funding, receipts, jobs)
			}
			var args []byte
			var jobQueue string
			var attempts int
			if err = e.Pool.QueryRow(ctx, "SELECT args,queue,max_attempts FROM river_job WHERE kind='purchase_fulfillment'").Scan(&args, &jobQueue, &attempts); err != nil {
				t.Fatal(err)
			}
			var persisted map[string]uuid.UUID
			// The existing River driver clamps requested 1,000,000 attempts to int16.
			if json.Unmarshal(args, &persisted) != nil || len(persisted) != 1 || persisted["order_id"] != order || jobQueue != "provision" || attempts != 32767 {
				t.Fatalf("pending worker payload changed: args=%s queue=%s attempts=%d", args, jobQueue, attempts)
			}
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			operation := uuid.New()
			desired, _ := json.Marshal(vpn.AccessDesired{Devices: 2, PeriodDays: &quote.PeriodDays, PlanId: &plan, Profile: "regular", Revision: &quote.Revision})
			if _, err = s.VPN.QueueAccessTx(ctx, tx, vpn.AccessWrite{ID: operation, AccountID: account, Kind: "purchase", Reason: "paid_order", PlanID: &plan, Revision: &quote.Revision, PeriodDays: &quote.PeriodDays, Desired: desired, Target: []byte(`{}`), PurchaseOrderID: &order, CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET access_operation_id=$2,fulfillment_status='running' WHERE id=$1", order, operation); err != nil {
				t.Fatal(err)
			}
			if reason, err := s.Payments.CheckPurchaseAccess(ctx, tx, order, account, operation); err != nil || reason != "" {
				t.Fatal("funding cannot see caller transaction", reason, err)
			}
			if err = s.Payments.RecordPurchaseAccessTx(ctx, tx, operation, "applied", ""); err != nil {
				t.Fatal("outcome cannot see caller transaction", err)
			}
			if err = tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			var linked bool
			if err = e.Pool.QueryRow(ctx, "SELECT fulfillment_status,access_operation_id IS NOT NULL FROM purchase_orders WHERE id=$1", order).Scan(&status, &linked); err != nil || status != "queued" || linked {
				t.Fatal("outcome escaped rollback", status, linked, err)
			}
		})
	}
}
