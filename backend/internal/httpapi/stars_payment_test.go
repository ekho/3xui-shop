package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func starsHTTPFixture(t *testing.T) (http.Handler, *regressionFixture, *testkit.Env, wire.MiniAppSessionResult, uuid.UUID) {
	t.Helper()
	s, e := fixture(t)
	s.now = e.Clock
	s.cfg.Accounts.Now = func() time.Time { return s.now() }
	panelFixture(t, s)
	plan := uuid.New()
	terms := catalogueTerms(2)
	terms.Prices[2].AmountMinor = "100"
	raw, _ := json.Marshal(terms)
	if _, err := e.Pool.Exec(context.Background(), `INSERT INTO catalogue_plans(id,current_revision,current_devices,current_profile,current_hidden) VALUES($1,1,2,'regular',false)`, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(context.Background(), `INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,1,$2,false,'legacy_import',$3)`, plan, raw, e.Clock()); err != nil {
		t.Fatal(err)
	}
	s.cfg.Payments.YooMoneyEnabled = true
	// JSON makes the missing feature produce a behavior failure, not a compile error.
	if err := json.Unmarshal([]byte(`{"StarsEnabled":true}`), &s.cfg.Payments); err != nil {
		t.Fatal(err)
	}
	modules := app.NewModules(e.Pool, e.Redis, s.queue, s.cfg)
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	modules.MiniApp = telegram.NewMiniApp(123, pub, modules.Accounts, e.Clock)
	if _, err = app.NewTelegram(telegram.Config{Enabled: true, Token: "123:owned-local-test-token-0000", Operators: []int64{901}}, modules, s.cfg.HTTP.CabinetOrigin, nil); err != nil {
		t.Fatal(err)
	}
	h := New(modules, e.Pool, s.cfg.HTTP)
	body, _ := json.Marshal(map[string]string{"init_data": testkit.SignedMiniAppData(key, 123, e.Clock(), `{"id":701,"first_name":"Stars client","language_code":"en"}`, ""), "accepted_terms_version": "1", "accepted_privacy_version": "1"})
	r := request(h, "POST", "/api/v1/telegram/mini-app/session", string(body), s.cfg.HTTP.CabinetOrigin)
	var auth wire.MiniAppSessionResult
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &auth) != nil {
		t.Fatal("signed fixture login", r.Code)
	}
	s.payments = modules.Payments
	return h, s, e, auth, plan
}

func starsTestOrder(t *testing.T) (*regressionFixture, *testkit.Env, wire.MiniAppSessionResult, payments.PurchaseOrder) {
	t.Helper()
	_, s, e, auth, plan := starsHTTPFixture(t)
	order, err := s.payments.CreatePurchaseOrder(context.Background(), auth.Account.AccountId, uuid.New(), payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "telegram_stars", PaymentType: "STARS", PlanId: plan, Revision: 1, PeriodDays: 30})
	if err != nil {
		t.Fatal("Stars fixture order", err)
	}
	return s, e, auth, order
}

// Catches an invoice paid by a different user, stale quote or unsafe current account.
func TestStarsPreCheckout(t *testing.T) {
	s, e, auth, order := starsTestOrder(t)
	ctx := context.Background()
	in := payments.StarsPreCheckoutInput{BotID: 123, PayerID: 701, Amount: 100, Currency: "XTR", Payload: "stars:v1:" + order.OrderId.String()}
	if ok, err := s.payments.CheckStarsPreCheckout(ctx, in); err != nil || !ok {
		t.Fatal("valid invoice refused", err)
	}
	for _, change := range []func(*payments.StarsPreCheckoutInput){func(p *payments.StarsPreCheckoutInput) { p.PayerID = 702 }, func(p *payments.StarsPreCheckoutInput) { p.BotID = 124 }, func(p *payments.StarsPreCheckoutInput) { p.Amount = 10000 }, func(p *payments.StarsPreCheckoutInput) { p.Amount = 0 }, func(p *payments.StarsPreCheckoutInput) { p.Currency = "RUB" }, func(p *payments.StarsPreCheckoutInput) { p.Payload = "legacy:701:100" }, func(p *payments.StarsPreCheckoutInput) { p.Payload = "stars:v1:" + uuid.NewString() }} {
		bad := in
		change(&bad)
		if ok, err := s.payments.CheckStarsPreCheckout(ctx, bad); err != nil || ok {
			t.Fatal("mismatched invoice accepted", err)
		}
	}
	for _, flag := range []string{"restricted", "vpn_banned", "telegram_login_disabled"} {
		if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET "+flag+"=true WHERE id=$1", auth.Account.AccountId); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.payments.CheckStarsPreCheckout(ctx, in); err != nil || ok {
			t.Fatal("restricted invoice accepted", flag, err)
		}
		if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET "+flag+"=false WHERE id=$1", auth.Account.AccountId); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=702 WHERE id=$1`, auth.Account.AccountId); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.payments.CheckStarsPreCheckout(ctx, in); err != nil || ok {
		t.Fatal("changed binding retained pay authority", err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=701 WHERE id=$1`, auth.Account.AccountId); err != nil {
		t.Fatal(err)
	}
	s.cfg.Payments.StarsEnabled = false
	if ok, err := s.payments.CheckStarsPreCheckout(ctx, in); err != nil || ok {
		t.Fatal("disabled Stars invoice accepted", err)
	}
	s.cfg.Payments.StarsEnabled = true
	s.now = func() time.Time { return e.Clock().Add(31 * time.Minute) }
	if ok, err := s.payments.CheckStarsPreCheckout(ctx, in); err != nil || ok {
		t.Fatal("expired Stars invoice accepted", err)
	}
}

// Catches invoice retry changing payload/quote and unsafe URLs becoming client links.
func TestStarsInvoice(t *testing.T) {
	for _, link := range []string{"https://t.me/$owned_invoice", "https://t.me/$" + strings.Repeat("a", 256), "https://t.me.evil.test/$owned_invoice", "https://t.me/$owned_invoice?token=unsafe", "http://t.me/$owned_invoice"} {
		t.Run(link, func(t *testing.T) {
			s, e, auth, order := starsTestOrder(t)
			calls := 0
			s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(_ context.Context, in payments.StarsInvoice) (string, error) {
				calls++
				if in.Amount != 100 || in.Payload != "stars:v1:"+order.OrderId.String() || in.Title == "" || in.Description == "" {
					t.Fatal("invoice argument lost frozen quote")
				}
				return link, nil
			}, Refund: func(context.Context, int64, string) error { return nil }})
			for range 2 {
				got, err := s.payments.CreateStarsInvoice(context.Background(), auth.Account.AccountId, order.OrderId)
				if strings.HasPrefix(link, "https://t.me/$") && !strings.Contains(link, "?") {
					if err != nil || !got.CanPay || got.StarsCheckout == nil || got.StarsCheckout.URL == nil || *got.StarsCheckout.URL != link {
						t.Fatal("invoice retry result", err)
					}
				} else if err == nil {
					t.Fatal("unsafe invoice accepted")
				}
			}
			if strings.HasPrefix(link, "https://t.me/$") && !strings.Contains(link, "?") && calls != 1 {
				t.Fatal("cached invoice was recreated")
			}
			var retained *string
			if err := e.Pool.QueryRow(context.Background(), `SELECT invoice_url FROM stars_checkouts WHERE order_id=$1`, order.OrderId).Scan(&retained); err != nil {
				t.Fatal(err)
			}
			if (retained != nil) != (strings.HasPrefix(link, "https://t.me/$") && !strings.Contains(link, "?")) {
				t.Fatal("unsafe URL retained")
			}
			if _, err := s.payments.CreateStarsInvoice(context.Background(), uuid.New(), order.OrderId); err == nil {
				t.Fatal("foreign order invoice returned")
			}
		})
	}
}

func starsRequest(h http.Handler, s *regressionFixture, auth wire.MiniAppSessionResult, method, path, body string, key uuid.UUID) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", s.cfg.HTTP.CabinetOrigin)
	r.Header.Set("Authorization", "Bearer "+auth.SessionToken)
	r.Header.Set("X-CSRF-Token", auth.CsrfToken)
	if key != uuid.Nil {
		r.Header.Set("Idempotency-Key", key.String())
	}
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	return out
}

// Catches rejecting signed Stars purchases, decimal scaling and duplicate orders.
func TestStarsSignedHTTPOrder(t *testing.T) {
	h, s, e, auth, plan := starsHTTPFixture(t)
	r := starsRequest(h, s, auth, "GET", "/api/v1/payment-methods", "", uuid.Nil)
	if r.Code != 200 || r.Body.String() != `{"methods":[{"currency":"XTR","id":"telegram_stars"}]}`+"\n" {
		t.Fatal("signed Mini does not expose its Stars-only method", r.Code, r.Body.String())
	}
	in := `{"action":"purchase","plan_id":"` + plan.String() + `","revision":1,"period_days":30,"payment_method":"telegram_stars","payment_type":"STARS"}`
	key := uuid.New()
	r = starsRequest(h, s, auth, "POST", "/api/v1/orders", in, key)
	var order wire.PurchaseOrder
	if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &order) != nil {
		t.Fatal("signed Mini cannot buy Stars", r.Code, r.Body.String())
	}
	if order.Quote.AmountMinor != "100" || order.Quote.Currency != "XTR" || order.Checkout != nil || order.PaymentMethod != "telegram_stars" {
		t.Fatal("Stars quote scaled or external checkout leaked")
	}
	r = starsRequest(h, s, auth, "POST", "/api/v1/orders", in, key)
	var replay wire.PurchaseOrder
	if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &replay) != nil || replay.OrderId != order.OrderId {
		t.Fatal("lost order response duplicated purchase", r.Code)
	}
	var payer, bot int64
	var payload string
	if err := e.Pool.QueryRow(context.Background(), `SELECT payer_id,bot_id,payload FROM stars_checkouts WHERE order_id=$1`, order.OrderId).Scan(&payer, &bot, &payload); err != nil || payer != 701 || bot != 123 || payload != "stars:v1:"+order.OrderId.String() {
		t.Fatal("invoice provenance not retained", err)
	}
	for _, body := range []string{strings.ReplaceAll(strings.ReplaceAll(in, "telegram_stars", "yoomoney"), "STARS", "AC"), strings.ReplaceAll(in, `"purchase"`, `"renew"`)} {
		r = starsRequest(h, s, auth, "POST", "/api/v1/orders", body, uuid.New())
		if r.Code != 403 {
			t.Fatal("Stars grant widened Mini external/renew authority", r.Code)
		}
	}
}

// Catches a browser cookie obtaining Telegram-only money authority.
func TestStarsCookieCannotCreate(t *testing.T) {
	h, e, cfg := httpFixture(t)
	client := supportLogin(t, h, e, cfg, "stars-cookie@example.test")
	body := []byte(`{"action":"purchase","plan_id":"` + uuid.NewString() + `","revision":1,"period_days":30,"payment_method":"telegram_stars","payment_type":"STARS"}`)
	r := supportRequest(h, &client, "POST", "/api/v1/orders", "application/json", body, cfg.HTTP.CabinetOrigin, uuid.New())
	if r.Code != 403 {
		t.Fatal("cookie Stars source boundary", r.Code)
	}
}
