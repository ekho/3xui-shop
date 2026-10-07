package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Catches silently routing disabled Telegram login to the web/unknown-route flow.
func TestMiniAppDisabledHTTP(t *testing.T) {
	h, _, cfg := httpFixture(t)
	rr := request(h, http.MethodPost, "/api/v1/telegram/mini-app/session", `{"init_data":"auth_date=1"}`, cfg.HTTP.CabinetOrigin)
	if rr.Code != 503 {
		t.Fatalf("disabled Mini App login: want 503 got %d", rr.Code)
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Fatal("Telegram login changed browser cookie")
	}
	if rr := request(h, http.MethodGet, "/healthz", "", ""); rr.Code != 200 {
		t.Fatal("disabled Telegram broke HTTP")
	}
}

func miniAppHTTPFixture(t *testing.T) (http.Handler, *testkit.Env, app.Config, ed25519.PrivateKey) {
	_, e, cfg := httpFixture(t)
	cfg.Accounts.Now = e.Clock
	queue, err := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	modules := app.NewModules(e.Pool, e.Redis, queue, &cfg)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	modules.MiniApp = telegram.NewMiniApp(123, pub, modules.Accounts, e.Clock)
	return New(modules, e.Pool, cfg.HTTP), e, cfg, priv
}
func miniAppRequest(h http.Handler, method, path, body, origin, token, csrf, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "__Host-session", Value: cookie})
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// Catches minting a browser/operator credential from signed Telegram-only input.
func TestMiniAppSignedHTTPPermissions(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	raw := testkit.SignedMiniAppData(key, 123, e.Clock(), `{"id":444,"first_name":"Mini client","language_code":"en"}`, "legacy_payload")
	data := map[string]any{"init_data": raw}
	body, _ := json.Marshal(data)
	rr := request(h, "POST", "/api/v1/telegram/mini-app/session", string(body), cfg.HTTP.CabinetOrigin)
	if rr.Code != 409 || !strings.Contains(rr.Body.String(), "CONSENT_REQUIRED") {
		t.Fatal("missing consent response", rr.Code)
	}
	data["accepted_terms_version"] = "1"
	data["accepted_privacy_version"] = "1"
	body, _ = json.Marshal(data)
	rr = request(h, "POST", "/api/v1/telegram/mini-app/session", string(body), cfg.HTTP.CabinetOrigin)
	var auth wire.MiniAppSessionResult
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &auth) != nil || auth.Account.Email != nil || auth.Account.TelegramId != 444 {
		t.Fatal("signed login", rr.Code)
	}
	if len(rr.Result().Cookies()) != 0 || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("login cookie/cache boundary")
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/api/v1/telegram/mini-app/account", "", 200}, {"GET", "/api/v1/subscription", "", 200}, {"GET", "/api/v1/catalogue", "", 200},
		{"GET", "/api/v1/orders/current", "", 200}, {"GET", "/api/v1/support", "", 200},
		{"GET", "/api/v1/me", "", 403}, {"GET", "/api/v1/me/security", "", 403}, {"GET", "/api/v1/operator/session", "", 403},
		{"POST", "/api/v1/orders", `{}`, 403}, {"POST", "/api/v1/auth/login", `{}`, 403},
		{"POST", "/api/v1/me/password-change", `{}`, 403}, {"POST", "/api/v1/auth/password-reset", `{}`, 403},
		{"POST", "/api/v1/payment-history", `{"kind":"orders"}`, 200},
	} {
		got := miniAppRequest(h, tc.method, tc.path, tc.body, cfg.HTTP.CabinetOrigin, auth.SessionToken, auth.CsrfToken, "")
		if got.Code != tc.status {
			t.Errorf("%s %s want %d got %d", tc.method, tc.path, tc.status, got.Code)
		}
		if strings.Contains(got.Body.String(), raw) || strings.Contains(got.Body.String(), auth.SessionToken) {
			t.Error("private auth input leaked")
		}
	}
	if got := miniAppRequest(h, "POST", "/api/v1/payment-history", `{"kind":"orders"}`, cfg.HTTP.CabinetOrigin, auth.SessionToken, "", ""); got.Code != 403 {
		t.Fatal("CSRF bypass")
	}
	if got := miniAppRequest(h, "POST", "/api/v1/payment-history", `{"kind":"orders"}`, "https://attacker.example.test", auth.SessionToken, auth.CsrfToken, ""); got.Code != 403 {
		t.Fatal("Origin bypass")
	}
	if got := miniAppRequest(h, "GET", "/api/v1/me", "", "", "", "", auth.SessionToken); got.Code != 401 {
		t.Fatal("mini token as cookie")
	}
	if got := miniAppRequest(h, "GET", "/api/v1/telegram/mini-app/account", "", "", "", "", auth.SessionToken); got.Code != 401 {
		t.Fatal("Mini route accepted cookie")
	}
	rr = request(h, "POST", "/api/v1/telegram/mini-app/session", string(body), cfg.HTTP.CabinetOrigin)
	var repeat wire.MiniAppSessionResult
	json.Unmarshal(rr.Body.Bytes(), &repeat)
	if rr.Code != 200 || repeat.Account.AccountId != auth.Account.AccountId {
		t.Fatal("repeat replaced account")
	}
	var n int
	e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM accounts WHERE telegram_id=444`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate identity")
	}
	e.Pool.Exec(context.Background(), `UPDATE accounts SET restricted=true WHERE id=$1`, auth.Account.AccountId)
	if got := miniAppRequest(h, "GET", "/api/v1/subscription", "", "", auth.SessionToken, "", ""); got.Code != 403 {
		t.Fatal("restricted access")
	}
	if got := miniAppRequest(h, "GET", "/api/v1/auth/session", "", "", auth.SessionToken, "", ""); got.Code != 200 {
		t.Fatal("restricted exit context")
	}
	if got := miniAppRequest(h, "POST", "/api/v1/telegram/mini-app/logout", "", cfg.HTTP.CabinetOrigin, auth.SessionToken, auth.CsrfToken, ""); got.Code != 204 || len(got.Result().Cookies()) != 0 {
		t.Fatal("restricted logout changed web cookie")
	}
	if got := miniAppRequest(h, "GET", "/api/v1/telegram/mini-app/account", "", "", auth.SessionToken, "", ""); got.Code != 401 {
		t.Fatal("logout revoked nothing")
	}
	for i := 0; i < 31; i++ {
		got := miniAppRequest(h, "POST", "/api/v1/telegram/mini-app/session", `{"init_data":"forged"}`, cfg.HTTP.CabinetOrigin, "", "", "")
		if i == 30 && (got.Code != 429 || got.Header().Get("Retry-After") == "") {
			t.Fatal("unsigned flood not limited")
		}
	}
	if got := request(h, "POST", "/api/v1/telegram/mini-app/session", `{"init_data":"private-forged"}`, cfg.HTTP.CabinetOrigin); bytes.Contains(got.Body.Bytes(), []byte("private-forged")) {
		t.Fatal("error echoed initData")
	}
}

// Catches exposing a payable external checkout to Mini App despite denied order creation.
func TestMiniAppExistingOrderIsReadOnly(t *testing.T) {
	s, e, client, plan := purchaseFixture(t)
	s.cfg.Payments.ManualEnabled = true
	s.cfg.Payments.ManualCardDetails = "Owned local fixture"
	order, err := s.createPurchaseOrder(context.Background(), client, uuid.New(), wire.PurchaseOrderInput{Action: "purchase", PlanId: plan, Revision: 1, PeriodDays: 30, PaymentMethod: "manual", PaymentType: "MANUAL"})
	if err != nil {
		t.Fatal(err)
	}
	if order.ManualPayment == nil || !order.ManualPayment.CanReport {
		t.Fatal("fixture needs payable browser order")
	}
	e.Pool.Exec(context.Background(), `UPDATE accounts SET telegram_id=555 WHERE id=$1`, client)
	auth, raw, err := s.accounts.StartTelegramSession(context.Background(), accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 555, DisplayName: "Linked", Locale: "en"}})
	if err != nil {
		t.Fatal(err)
	}
	webRaw := opaque()
	if _, err = e.Pool.Exec(context.Background(), `INSERT INTO sessions(id_hash,account_id,csrf_token,created_at,last_seen,absolute_expires_at) VALUES($1,$2,$3,$4,$4,$5)`, digest(webRaw), client, "cookie-csrf", e.Clock(), e.Clock().Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = s.accounts.ChangeOperatorRole(context.Background(), client, true); err != nil {
		t.Fatal(err)
	}
	cfg := *s.cfg
	cfg.Accounts.Now = e.Clock
	modules := app.NewModules(e.Pool, e.Redis, s.queue, &cfg)
	h := New(modules, e.Pool, s.cfg.HTTP)
	if rr := miniAppRequest(h, "GET", "/api/v1/me", "", "", "", "", webRaw); rr.Code != 200 {
		t.Fatal("browser cookie lost")
	}
	if rr := miniAppRequest(h, "GET", "/api/v1/subscription", "", "", "mini_"+strings.Repeat("x", 43), "", webRaw); rr.Code != 401 {
		t.Fatal("invalid Mini bearer fell back to cookie")
	}
	if rr := miniAppRequest(h, "GET", "/api/v1/operator/session", "", "", raw, auth.CsrfToken, webRaw); rr.Code != 403 {
		t.Fatal("linked operator gained Mini admin")
	}
	for _, path := range []string{"/api/v1/orders/" + order.OrderId.String(), "/api/v1/orders/current"} {
		rr := miniAppRequest(h, "GET", path, "", "", raw, auth.CsrfToken, "")
		if rr.Code != 200 {
			t.Fatalf("order route %s: %d", path, rr.Code)
		}
		var got wire.PurchaseOrder
		if strings.HasSuffix(path, "current") {
			var current wire.CurrentPurchaseOrder
			json.Unmarshal(rr.Body.Bytes(), &current)
			if current.Order == nil {
				t.Fatal("existing order lost")
			}
			got = *current.Order
		} else {
			json.Unmarshal(rr.Body.Bytes(), &got)
		}
		if got.CanPay || got.Checkout != nil || got.YookassaCheckout != nil || got.CryptomusCheckout != nil || got.HeleketCheckout != nil || got.ManualPayment != nil && got.ManualPayment.CanReport {
			t.Fatal("Mini App exposed external checkout")
		}
		if got.OrderId != order.OrderId || got.Quote.AmountMinor != order.Quote.AmountMinor {
			t.Fatal("read-only projection changed order")
		}
	}
}
