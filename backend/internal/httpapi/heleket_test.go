package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

// Missing method selection, wrong currency or duplicate queueing breaks this.
func TestHeleketOrderAtomic(t *testing.T) {
	s, _, account, plan := purchaseFixture(t)
	if err := json.Unmarshal([]byte(`{"HeleketEnabled":true,"HeleketMerchantID":"00000000-0000-4000-8000-000000000015","HeleketAPIKey":"test-only-heleket-key"}`), &s.cfg.Payments); err != nil {
		t.Fatal(err)
	}
	in := purchaseInput(plan)
	in.PaymentMethod, in.PaymentType = "heleket", "HELEKET"
	key := uuid.New()
	out, err := s.createPurchaseOrder(context.Background(), account, key, in)
	if err != nil {
		t.Fatal("enabled Heleket did not create a server-priced order", err)
	}
	if out.Quote.AmountMinor != "12345" || out.Quote.Currency != "USD" || out.CanPay || out.Checkout != nil || out.PaymentMethod != "heleket" {
		t.Fatal("Heleket selected RUB or became a YooMoney form")
	}
	again, err := s.createPurchaseOrder(context.Background(), account, key, in)
	if err != nil || again.OrderId != out.OrderId {
		t.Fatal("lost response created another order", err)
	}
	var checkouts, jobs int
	if err = s.pool.QueryRow(context.Background(), "SELECT count(*) FROM heleket_checkouts WHERE order_id=$1", out.OrderId).Scan(&checkouts); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(context.Background(), "SELECT count(*) FROM river_job WHERE kind='heleket_payment' AND args->>'order_id'=$1", out.OrderId.String()).Scan(&jobs); err != nil || checkouts != 1 || jobs != 1 {
		t.Fatal("replay did not retain one Heleket request/job", err)
	}
	methods, err := s.paymentMethods(context.Background(), account)
	if err != nil || len(methods.Methods) != 2 || methods.Methods[1].Id != "heleket" || methods.Methods[1].Currency != "USD" {
		t.Fatal("enabled-only methods lost Heleket/USD or existing YooMoney", err)
	}
}

// Foreign credentials/proof and a shared invoice UUID must not mix providers.
func TestHeleketProviderIsolation(t *testing.T) {
	t.Run("source-and-key", func(t *testing.T) {
		s, _, _, order, f := cryptoFixture(t, "heleket")
		syncCrypto(t, s, order.OrderId, "heleket")
		h := New(app.NewModules(s.pool, s.limiter, s.queue, s.cfg), s.pool, s.cfg.HTTP)
		unsigned := fmt.Sprintf(`{"type":"payment","uuid":"%s","order_id":"%s","status":"paid"}`, f.payment["uuid"], order.OrderId)
		for _, tc := range []struct {
			source, key string
			want        int
		}{
			{"91.227.144.54:12345", "heleket", 403},
			{"31.133.220.8:12345", "cryptomus", 403},
			{"31.133.220.8:12345", "heleket", 200},
		} {
			before := f.infos
			r := httptest.NewRequest("POST", "/webhooks/heleket", bytes.NewReader(signedCryptoBody(unsigned, tc.key)))
			r.Header.Set("Content-Type", "application/json")
			r.RemoteAddr = tc.source
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || tc.want == 403 && f.infos != before {
				t.Fatal("foreign source/key accepted or own hint rejected", w.Code)
			}
		}
		manualCounts(t, s, order.OrderId, 0, 0)
	})
	t.Run("same-uuid-separate-receipts", func(t *testing.T) {
		s, e, account, order, f := cryptoFixture(t, "heleket")
		syncCrypto(t, s, order.OrderId, "heleket")
		s.cfg.Payments.CryptomusEnabled = true
		s.cfg.Payments.CryptomusMerchantID = "00000000-0000-4000-8000-000000000014"
		s.cfg.Payments.CryptomusAPIKey = "test-only-crypto-key"
		other := verified(t, s, e, "other-crypto-provider@example.test")
		in := purchaseInput(order.Quote.PlanId)
		in.PaymentMethod, in.PaymentType = "cryptomus", "CRYPTOMUS"
		old, err := s.createPurchaseOrder(context.Background(), other, uuid.New(), in)
		if err != nil {
			t.Fatal(err)
		}
		invoice := f.payment["uuid"].(string)
		if _, err = s.pool.Exec(context.Background(), "UPDATE cryptomus_checkouts SET first_attempt_at=$2,invoice_id=$3 WHERE order_id=$1", old.OrderId, e.Clock(), invoice); err != nil {
			t.Fatal(err)
		}
		seedCryptoReceipt(t, s, e, old, invoice, "cryptomus")
		settleCrypto(e, f)
		syncCrypto(t, s, order.OrderId, "heleket")
		manualCounts(t, s, order.OrderId, 1, 1)
		for _, id := range []uuid.UUID{old.OrderId, order.OrderId} {
			if err = s.fulfillPurchase(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
		if err != nil || got.AccessOperationId == nil || got.ReviewRequired {
			t.Fatal("other provider UUID blocked own money", err)
		}
		var n int
		if err = s.pool.QueryRow(context.Background(), "SELECT count(*) FROM purchase_receipts WHERE operation_id IN ($1,$2)", "cryptomus:"+invoice, "heleket:"+invoice).Scan(&n); err != nil || n != 2 {
			t.Fatal("receipt namespaces collided", err)
		}
		s.cfg.Payments.ManualEnabled = true
		s.cfg.Payments.ManualCardDetails = "test-only instructions"
		s.cfg.Payments.YooKassaEnabled = true
		methods, err := s.paymentMethods(context.Background(), account)
		if err != nil || len(methods.Methods) != 5 {
			t.Fatal("five enabled methods not exposed", err)
		}
	})
	t.Run("foreign-proof-blocks-fulfillment", func(t *testing.T) {
		s, e, account, order, f := cryptoFixture(t, "heleket")
		syncCrypto(t, s, order.OrderId, "heleket")
		seedCryptoReceipt(t, s, e, order, f.payment["uuid"].(string), "cryptomus")
		if err := s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
			t.Fatal(err)
		}
		got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
		if err != nil || got.AccessOperationId != nil || !got.ReviewRequired {
			t.Fatal("Cryptomus facts authorized Heleket access", err)
		}
	})
}

// Controlled guard fixture: syntactically valid facts, provider chosen by the test.
func seedCryptoReceipt(t *testing.T, s *regressionFixture, e *testkit.Env, order wire.PurchaseOrder, invoice, provider string) {
	t.Helper()
	proof, err := json.Marshal(map[string]any{"provider": provider, "invoice_id": invoice, "merchant_id": cryptoCaseFor(provider).merchant, "order_id": order.OrderId.String(), "status": "paid", "payment_status": "paid", "is_final": true, "amount_minor": "12345", "currency": "USD", "payment_amount": "0.00123450", "payer_amount": "0.00123450", "merchant_amount": "0.00120981", "payer_currency": "BTC", "created_at": e.Clock().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	op := provider + ":" + invoice
	if _, err = s.pool.Exec(context.Background(), "INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,created_at,provider_data) VALUES($1,$2,$3,12345,NULL,'USD',$4,false,false,$3,$5)", op, order.OrderId, e.Clock(), provider+".paid", proof); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(context.Background(), "UPDATE purchase_orders SET payment_status='paid',paid_at=$2,active=false,fulfillment_status='queued',funding_operation_id=$3 WHERE id=$1", order.OrderId, e.Clock(), op); err != nil {
		t.Fatal(err)
	}
}

func TestHeleketCheckoutHosts(t *testing.T) {
	for _, tc := range []struct {
		host  string
		ready bool
	}{{"new-pay.heleket.com", true}, {"pay.heleket.com", true}, {"pay.cryptomus.com", false}, {"new-pay.heleket.com.attacker.example.test", false}} {
		t.Run(tc.host, func(t *testing.T) {
			s, _, account, order, f := cryptoFixture(t, "heleket")
			f.payment["url"] = "https://" + tc.host + "/pay/" + f.payment["uuid"].(string)
			syncCrypto(t, s, order.OrderId, "heleket")
			got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
			if err != nil || got.CanPay != tc.ready || got.HeleketCheckout == nil || (got.HeleketCheckout.Url != nil) != tc.ready {
				t.Fatal("wrong provider checkout host allowed", err)
			}
		})
	}
}

func TestHeleketPaymentOffset(t *testing.T) {
	s, e, _, order, f := cryptoFixture(t, "heleket")
	syncCrypto(t, s, order.OrderId, "heleket")
	settleCrypto(e, f)
	f.payment["created_at"], f.payment["updated_at"] = "2026-10-01T03:00:00+03:00", "2026-10-01T03:00:00+03:00"
	syncCrypto(t, s, order.OrderId, "heleket")
	manualCounts(t, s, order.OrderId, 1, 1)
	var occurred time.Time
	if err := s.pool.QueryRow(context.Background(), "SELECT occurred_at FROM purchase_receipts WHERE order_id=$1", order.OrderId).Scan(&occurred); err != nil || !occurred.Equal(e.Clock()) {
		t.Fatal("explicit timezone offset changed payment time", err)
	}
}

func TestHeleketHTTPAuthoritativeStatus(t *testing.T) {
	testCryptoHTTPAuthoritativeStatus(t, "heleket")
}
func TestHeleketExpiryAndDrift(t *testing.T)       { testCryptoExpiryAndDrift(t, "heleket") }
func TestHeleketProviderHTTPFailures(t *testing.T) { testCryptoProviderHTTPFailures(t, "heleket") }
func TestHeleketRequestAndRecovery(t *testing.T)   { testCryptoRequestAndRecovery(t, "heleket") }
func TestHeleketPendingNullableDates(t *testing.T) { testCryptoPendingNullableDates(t, "heleket") }
func TestHeleketFundingBoundary(t *testing.T)      { testCryptoFundingBoundary(t, "heleket") }
func TestHeleketReceiptConflictAndForeignCollision(t *testing.T) {
	testCryptoReceiptConflictAndForeignCollision(t, "heleket")
}
func TestHeleketChangedFactsBlockPreparedAccess(t *testing.T) {
	testCryptoChangedFactsBlockPreparedAccess(t, "heleket")
}
func TestHeleketObservationRace(t *testing.T)       { testCryptoObservationRace(t, "heleket") }
func TestHeleketOrderGuards(t *testing.T)           { testCryptoOrderGuards(t, "heleket") }
func TestHeleketReviewCannotReconcile(t *testing.T) { testCryptoReviewCannotReconcile(t, "heleket") }
func TestHeleketHTTPBoundary(t *testing.T)          { testCryptoHTTPBoundary(t, "heleket") }
