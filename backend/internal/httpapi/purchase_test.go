package httpapi

import (
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
