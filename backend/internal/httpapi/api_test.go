package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/platform"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"fmt"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func httpFixture(t *testing.T) (http.Handler, *testkit.Env, platform.Config) {
	t.Helper()
	env := testkit.Open(t)
	queue, err := river.NewClient(riverpgxv5.New(env.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := platform.Config{CabinetOrigin: "https://cabinet.example.test", TermsVersion: "1", PrivacyVersion: "1", MailKey: bytes.Repeat([]byte{1}, 32), CodeKey: bytes.Repeat([]byte{2}, 32), RateNamespace: uuid.NewString()}
	cfg.Operators = []int64{101, 202}
	cfg.AdapterToken = strings.Repeat("x", 43)
	cfg.PanelID = "dedicated-test"
	cfg.TrialEnabled = true
	cfg.TrialPeriodDays = 3
	cfg.TrialTrafficGB = 15
	cfg.TrialDevices = 1
	return New(platform.NewService(env.Pool, env.Redis, queue, cfg), cfg), env, cfg
}
func request(h http.Handler, method, path, body, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
func TestRegistrationHTTP(t *testing.T) {
	h, env, cfg := httpFixture(t)
	good := `{"email":"web@example.test","locale":"ru","accepted_terms_version":"1","accepted_privacy_version":"1"}`
	for _, tc := range []struct {
		method, path, body, origin string
		status                     int
	}{{"POST", "/api/v1/auth/register", good, cfg.CabinetOrigin, 202}, {"POST", "/api/v1/auth/register", good, "https://attacker.example.test", 403}, {"POST", "/api/v1/auth/register", good, "", 403}, {"POST", "/api/v1/auth/register", strings.Replace(good, `"ru"`, `"xx"`, 1), cfg.CabinetOrigin, 400}, {"POST", "/api/v1/auth/register", `{"email":"x@example.test"}`, cfg.CabinetOrigin, 400}, {"POST", "/api/v1/auth/register", `{"password":"sensitive",` + good[1:], cfg.CabinetOrigin, 400}, {"POST", "/api/v1/auth/register?unknown=true", good, cfg.CabinetOrigin, 400}, {"POST", "/api/v1/auth/verify-email", `{"token":"` + strings.Repeat("x", 43) + `","code":"12345678","challenge_id":"` + uuid.NewString() + `","new_password":"long safe password"}`, cfg.CabinetOrigin, 400}, {"POST", "/api/v1/auth/register", strings.Repeat("a", 16385), cfg.CabinetOrigin, 400}, {"GET", "/api/v1/auth/verify-email?token=sensitive", "", "", 400}} {
		rr := request(h, tc.method, tc.path, tc.body, tc.origin)
		if rr.Code != tc.status {
			t.Errorf("%s %s want %d got %d", tc.method, tc.path, tc.status, rr.Code)
		}
		if rr.Code >= 400 {
			var body map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body["error"] == nil {
				t.Error("unsafe error envelope")
			}
			if strings.Contains(rr.Body.String(), "sensitive") {
				t.Error("input echoed")
			}
		}
	}
	var n int
	env.Pool.QueryRow(context.Background(), `SELECT count(*) FROM accounts`).Scan(&n)
	if n != 0 {
		t.Fatal("scanner/unverified requests created account")
	}
	if rr := request(h, "GET", "/healthz", "", ""); rr.Code != 200 {
		t.Fatal("health must depend on PG")
	}
	env.Redis.Close()
	if rr := request(h, "GET", "/healthz", "", ""); rr.Code != 200 {
		t.Fatal("redis should not disable health")
	}
	env.Pool.Close()
	if rr := request(h, "GET", "/healthz", "", ""); rr.Code != 503 {
		t.Fatal("PG outage must disable health")
	}
}

func verifiedHTTP(t *testing.T, h http.Handler, e *testkit.Env, cfg platform.Config) {
	t.Helper()
	rr := request(h, "POST", "/api/v1/auth/register", `{"email":"login@example.test","locale":"ru","accepted_terms_version":"1","accepted_privacy_version":"1"}`, cfg.CabinetOrigin)
	if rr.Code != 202 {
		t.Fatal("registration", rr.Code)
	}
	var registered wire.RegistrationAccepted
	json.Unmarshal(rr.Body.Bytes(), &registered)
	_, token, _ := testkit.MailSecrets(t, e.Pool, cfg.MailKey, registered.ChallengeId)
	body, _ := json.Marshal(map[string]string{"token": token, "new_password": "my long safe password ✨"})
	if rr = request(h, "POST", "/api/v1/auth/verify-email", string(body), cfg.CabinetOrigin); rr.Code != 200 {
		t.Fatal("verification", rr.Code)
	}
}
func TestSessionBoundary(t *testing.T) {
	h, e, cfg := httpFixture(t)
	verifiedHTTP(t, h, e, cfg)
	rr := request(h, "POST", "/api/v1/auth/login", `{"email":"login@example.test","password":"my long safe password ✨"}`, cfg.CabinetOrigin)
	if rr.Code != 200 {
		t.Fatalf("login want200 got %d", rr.Code)
	}
	var login wire.LoginResult
	json.Unmarshal(rr.Body.Bytes(), &login)
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("one cookie expected")
	}
	cookie := cookies[0]
	if cookie.Name != "__Host-session" || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("cookie boundary")
	}
	send := func(method, path, origin, csrf string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(cookie)
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrf)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out
	}
	if rr = send("GET", "/api/v1/me", "", ""); rr.Code != 200 {
		t.Fatal("session read", rr.Code)
	}
	if rr = send("POST", "/api/v1/auth/logout", "https://attacker.example.test", login.CsrfToken); rr.Code != 403 {
		t.Fatal("foreign Origin logout", rr.Code)
	}
	if rr = send("POST", "/api/v1/auth/logout", cfg.CabinetOrigin, ""); rr.Code != 403 {
		t.Fatal("missing CSRF logout", rr.Code)
	}
	if rr = send("POST", "/api/v1/auth/logout", cfg.CabinetOrigin, login.CsrfToken); rr.Code != 204 {
		t.Fatal("logout", rr.Code)
	}
	if rr = send("GET", "/api/v1/me", "", ""); rr.Code != 401 {
		t.Fatal("revoked cookie accepted", rr.Code)
	}
	if rr = send("POST", "/api/v1/auth/logout", cfg.CabinetOrigin, ""); rr.Code != 204 {
		t.Fatal("logout retry", rr.Code)
	}
}

func TestUnknownRouteError(t *testing.T) {
	h, _, _ := httpFixture(t)
	if rr := request(h, "GET", "/api/v1/not-a-route", "", ""); rr.Code != 404 {
		t.Fatalf("unknown route want404 got%d", rr.Code)
	}
}
func TestLoginRateLimit(t *testing.T) {
	h, _, cfg := httpFixture(t)
	for i := range 31 {
		body := fmt.Sprintf(`{"email":"unknown%d@example.test","password":"wrong long password"}`, i)
		req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body))
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", cfg.CabinetOrigin)
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		want := 401
		if i == 30 {
			want = 429
		}
		if rr.Code != want {
			t.Fatalf("forged XFF request%d want%d got%d", i+1, want, rr.Code)
		}
	}
}

func TestInternalOperatorBoundary(t *testing.T) {
	h, e, cfg := httpFixture(t)
	verifiedHTTP(t, h, e, cfg)
	rr := request(h, "POST", "/api/v1/auth/login", `{"email":"login@example.test","password":"my long safe password ✨"}`, cfg.CabinetOrigin)
	if rr.Code != 200 {
		t.Fatal("login", rr.Code)
	}
	var login wire.LoginResult
	json.Unmarshal(rr.Body.Bytes(), &login)
	cookie := rr.Result().Cookies()[0]
	create := func(csrf string, key uuid.UUID) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/trial-requests", strings.NewReader(`{"comment":"test request"}`))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", cfg.CabinetOrigin)
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Idempotency-Key", key.String())
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		return out
	}
	if rr = create("", uuid.New()); rr.Code != 403 {
		t.Fatal("trial missing CSRF", rr.Code)
	}
	key := uuid.New()
	if rr = create(login.CsrfToken, key); rr.Code != 201 {
		t.Fatalf("trial creation want201 got%d", rr.Code)
	}
	var trial wire.TrialRequest
	json.Unmarshal(rr.Body.Bytes(), &trial)
	if rr = create(login.CsrfToken, key); rr.Code != 200 {
		t.Fatal("trial replay", rr.Code)
	}
	internal := func(token string, actor int64, mode string, withCookie bool) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"operator_tg_id":%d,"decision":"%s","callback_query_id":"%s"}`, actor, mode, uuid.NewString())
		r := httptest.NewRequest("POST", "/internal/v1/trial-requests/"+trial.RequestId.String()+"/decision", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if withCookie {
			r.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		return out
	}
	for _, token := range []string{"", "wrong"} {
		if rr = internal(token, 101, "approve", true); rr.Code != 401 {
			t.Fatal("user cookie must not authorize internal", rr.Code)
		}
	}
	if rr = internal(cfg.AdapterToken, 999, "approve", false); rr.Code != 403 {
		t.Fatal("forged operator", rr.Code)
	}
	if rr = internal(cfg.AdapterToken, 101, "approve", false); rr.Code != 200 {
		t.Fatal("authorized decision", rr.Code)
	}
	if rr = internal(cfg.AdapterToken, 202, "reject", false); rr.Code != 409 || !strings.Contains(rr.Body.String(), `"current_request_status":"approved"`) {
		t.Fatal("winning state", rr.Code)
	}
	r := httptest.NewRequest("GET", "/api/v1/trial-requests/current", nil)
	r.AddCookie(cookie)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), "operator_tg_id") || strings.Contains(rr.Body.String(), "support_declined") {
		t.Fatal("internal state leaked")
	}
}

func TestSubscriptionPrivacy(t *testing.T) {
	h, e, cfg := httpFixture(t)
	verifiedHTTP(t, h, e, cfg)
	login := request(h, "POST", "/api/v1/auth/login", `{"email":"login@example.test","password":"my long safe password ✨"}`, cfg.CabinetOrigin)
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	for _, tc := range []struct {
		path          string
		authenticated bool
		status        int
	}{{"/api/v1/subscription/key", false, 401}, {"/api/v1/subscription/key", true, 409}, {"/api/v1/subscription/key?account_id=" + uuid.NewString(), true, 400}, {"/api/v1/subscription/" + uuid.NewString() + "/key", true, 404}, {"/api/v1/subscription", true, 200}} {
		r := httptest.NewRequest("GET", tc.path, nil)
		if tc.authenticated {
			r.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		if out.Code != tc.status || out.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("key owner/cache boundary", out.Code)
		}
		if tc.status >= 400 && strings.Contains(out.Body.String(), "subscription_url") {
			t.Fatal("key in failure")
		}
		if tc.status == 409 && !strings.Contains(out.Body.String(), "OPERATION_NOT_READY") {
			t.Fatal("key error contract")
		}
	}
}

func TestTelegramLease(t *testing.T) {
	h, _, cfg := httpFixture(t)
	if rr := request(h, "POST", "/internal/v1/telegram/jobs/claim", `{"limit":1}`, ""); rr.Code != 401 {
		t.Fatal("unauthorized claim", rr.Code)
	}
	r := httptest.NewRequest("POST", "/internal/v1/telegram/jobs/claim", strings.NewReader(`{"limit":1}`))
	r.Header.Set("Authorization", "Bearer "+cfg.AdapterToken)
	r.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != `{"jobs":[]}` {
		t.Fatal("empty claim shape", rr.Code)
	}
}
