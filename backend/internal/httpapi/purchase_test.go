package httpapi

import (
	"context"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPurchaseHTTPStrictBoundary(t *testing.T) {
	h, e, cfg := httpFixture(t)
	customer := supportLogin(t, h, e, cfg, "purchase-http@example.test")
	if r := supportRequest(h, &customer, "GET", "/api/v1/payment-methods", "", nil, "", uuid.Nil); r.Code != 200 || !strings.Contains(r.Body.String(), `"methods":[]`) {
		t.Fatalf("disabled methods: %d %s", r.Code, r.Body.String())
	}
	good := []byte(`{"action":"purchase","plan_id":"` + uuid.NewString() + `","revision":1,"period_days":30,"payment_method":"yoomoney","payment_type":"AC"}`)
	for _, body := range [][]byte{[]byte(`{"action":"purchase","amount_minor":"1"}`), append(append([]byte{}, good[:len(good)-1]...), []byte(`,"account_id":"`+customer.id.String()+`"}`)...)} {
		if r := supportRequest(h, &customer, "POST", "/api/v1/orders", "application/json", body, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 400 {
			t.Fatalf("forged order: %d", r.Code)
		}
	}
	if r := supportRequest(h, &customer, "POST", "/api/v1/orders", "application/json", good, "https://attacker.example", uuid.New()); r.Code != 403 {
		t.Fatalf("origin: %d", r.Code)
	}
	if r := supportRequest(h, &customer, "GET", "/api/v1/orders/"+uuid.NewString(), "", nil, "", uuid.Nil); r.Code != 404 {
		t.Fatalf("foreign order: %d", r.Code)
	}
	if r := supportRequest(h, &customer, "GET", "/api/v1/orders/current", "", nil, "", uuid.Nil); r.Code != 200 || !strings.Contains(r.Body.String(), `"order":null`) {
		t.Fatalf("current empty: %d %s", r.Code, r.Body.String())
	}
	_ = wire.PurchaseOrder{}
}

func TestYooMoneyHTTPRejectsAmbiguousForm(t *testing.T) {
	h, _, _ := httpFixture(t)
	for _, body := range []string{"sign=x&sign=y", "sign=x&label=a&label=b", "sign=x&sender=%FF", "sign=x&label=a&=bad", strings.Repeat("a", 16385)} {
		req := httptest.NewRequest("POST", "/webhooks/yoomoney", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != 400 {
			t.Fatalf("ambiguous form %d", r.Code)
		}
	}
}

// Exercise the anonymous callback's money boundary through the actual handler.
func TestYooMoneyHTTPReceiptBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, field, value string
		unsigned           bool
		status, receipts   int
		payment            wire.PurchaseOrderPaymentStatus
	}{
		{"sha1-only", "sha1_hash", strings.Repeat("a", 40), true, 403, 0, "pending"},
		{"unsigned-test", "test_notification", "true", true, 403, 0, "pending"},
		{"signed-test", "test_notification", "true", false, 200, 0, "pending"},
		{"legacy-label", "label", "legacy-unimported", false, 200, 0, "pending"},
		{"unknown-order", "label", "00000000-0000-4000-8000-000000000099", false, 200, 0, "pending"},
		{"missing-gross", "withdraw_amount", "", false, 400, 0, "pending"},
		{"wrong-currency", "currency", "840", false, 200, 1, "paid"},
		{"wrong-type", "notification_type", "withdrawal", false, 200, 1, "paid"},
		{"zero-net", "amount", "0.00", false, 200, 1, "paid"},
		{"net-above-gross", "amount", "90071992547409.94", false, 200, 1, "paid"},
		{"unaccepted", "unaccepted", "true", false, 200, 1, "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, account, plan := purchaseFixture(t)
			ctx := context.Background()
			order, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
			if err != nil {
				t.Fatal(err)
			}
			fields := purchaseNotice(s, order.OrderId, "http-boundary", "90071992547409.00", "90071992547409.93")
			fields.Set(tc.field, tc.value)
			if tc.unsigned {
				fields.Del("sign")
			} else {
				fields.Set("sign", yooMoneySignature(fields, s.cfg.Payments.YooMoneyNotificationSecret))
			}
			s.cfg.Payments.YooMoneyEnabled = false // Existing notifications survive disabling new sales.
			h := New(app.NewModules(s.pool, s.limiter, s.queue, s.cfg), s.pool, s.cfg.HTTP)
			for range 2 {
				r := supportRequest(h, nil, "POST", "/webhooks/yoomoney", "application/x-www-form-urlencoded; charset=UTF-8", []byte(fields.Encode()), "", uuid.Nil)
				if r.Code != tc.status {
					t.Fatalf("callback status %d, want %d", r.Code, tc.status)
				}
			}
			manualCounts(t, s, order.OrderId, tc.receipts, 0)
			got, err := s.purchaseOrder(ctx, account, order.OrderId)
			if err != nil || got.PaymentStatus != tc.payment || got.ReviewRequired != (tc.receipts == 1) || got.AccessOperationId != nil {
				t.Fatal("callback changed money/access outside the accepted boundary", got, err)
			}
			var funding *string
			if err = s.pool.QueryRow(ctx, "SELECT funding_operation_id FROM purchase_orders WHERE id=$1", order.OrderId).Scan(&funding); err != nil || funding != nil {
				t.Fatal("invalid/test/unknown callback manufactured funding", err)
			}
			if tc.receipts == 1 && (got.FulfillmentStatus != "needs_review" || got.CanPay || got.CanCancel) {
				t.Fatal("disputed receipt still permits checkout or issuance", got)
			}
		})
	}
}

func TestYooMoneyHTTPReceiptConflict(t *testing.T) {
	s, _, account, plan := purchaseFixture(t)
	ctx := context.Background()
	order, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
	if err != nil {
		t.Fatal(err)
	}
	h := New(app.NewModules(s.pool, s.limiter, s.queue, s.cfg), s.pool, s.cfg.HTTP)
	fields := purchaseNotice(s, order.OrderId, "http-original-fact", "90071992547409.00", "90071992547409.93")
	for range 2 {
		if r := supportRequest(h, nil, "POST", "/webhooks/yoomoney", "application/x-www-form-urlencoded", []byte(fields.Encode()), "", uuid.Nil); r.Code != 200 {
			t.Fatal("valid callback/replay rejected", r.Code)
		}
	}
	manualCounts(t, s, order.OrderId, 1, 1)
	fields.Set("amount", "90071992547409.01")
	fields.Set("sign", yooMoneySignature(fields, s.cfg.Payments.YooMoneyNotificationSecret))
	for range 2 {
		if r := supportRequest(h, nil, "POST", "/webhooks/yoomoney", "application/x-www-form-urlencoded", []byte(fields.Encode()), "", uuid.Nil); r.Code != 200 {
			t.Fatal("signed conflict/replay rejected", r.Code)
		}
	}
	manualCounts(t, s, order.OrderId, 1, 1)
	var gross, net int64
	var reason, funding string
	if err = s.pool.QueryRow(ctx, "SELECT gross_minor,net_minor,review_reason FROM purchase_receipts WHERE operation_id='http-original-fact'").Scan(&gross, &net, &reason); err != nil || gross != 9007199254740993 || net != 9007199254740900 || reason != "conflicting_operation_id" {
		t.Fatal("conflict overwrote or concealed the first monetary fact", gross, net, reason, err)
	}
	if err = s.pool.QueryRow(ctx, "SELECT funding_operation_id FROM purchase_orders WHERE id=$1", order.OrderId).Scan(&funding); err != nil || funding != "http-original-fact" {
		t.Fatal("conflict replaced funding identity", err)
	}
	if err = s.fulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	got, err := s.purchaseOrder(ctx, account, order.OrderId)
	if err != nil || got.PaymentStatus != "paid" || !got.ReviewRequired || got.FulfillmentStatus != "needs_review" || got.AccessOperationId != nil {
		t.Fatal("conflicted funding created access", got, err)
	}
}
