package httpapi

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func activationHTTPRequest(origin, token, csrf, key, body string) *http.Request {
	r := httptest.NewRequest("POST", "/api/v1/trials/activate", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Idempotency-Key", key)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

// Catches a missing signed-client route/capability, a lost-response second
// operation, and leaking credentials/connection details in activation replies.
func TestTelegramTrialHTTPActivation(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	raw := testkit.SignedMiniAppData(key, 123, e.Clock(), `{"id":701,"first_name":"Owned client","language_code":"ru"}`, "campaign")
	body, _ := json.Marshal(map[string]string{"init_data": raw, "accepted_terms_version": "1", "accepted_privacy_version": "1"})
	rr := request(h, "POST", "/api/v1/telegram/mini-app/session", string(body), cfg.HTTP.CabinetOrigin)
	var auth wire.MiniAppSessionResult
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &auth) != nil {
		t.Fatal("signed session failed", rr.Code)
	}
	var profile struct {
		Capabilities struct {
			Available bool   `json:"trial_available"`
			Mode      string `json:"trial_mode"`
		}
	}
	if json.Unmarshal(rr.Body.Bytes(), &profile) != nil || !profile.Capabilities.Available || profile.Capabilities.Mode != "activate" {
		t.Fatal("Telegram capability did not select activation")
	}
	id := uuid.NewString()
	var first wire.TrialRequest
	for i, want := range []int{201, 200} {
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, activationHTTPRequest(cfg.HTTP.CabinetOrigin, auth.SessionToken, auth.CsrfToken, id, `{}`))
		var out wire.TrialRequest
		if rr.Code != want || json.Unmarshal(rr.Body.Bytes(), &out) != nil || out.Status != "approved" || out.OperationId == nil {
			t.Fatal("activation response", rr.Code)
		}
		if i == 0 {
			first = out
		} else if out.RequestId != first.RequestId || *out.OperationId != *first.OperationId {
			t.Fatal("HTTP replay changed operation")
		}
		if rr.Header().Get("Cache-Control") != "no-store" || strings.Contains(rr.Body.String(), auth.SessionToken) || strings.Contains(rr.Body.String(), "subscription_url") {
			t.Fatal("activation cache/disclosure boundary")
		}
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, activationHTTPRequest(cfg.HTTP.CabinetOrigin, auth.SessionToken, auth.CsrfToken, uuid.NewString(), `{}`))
	if rr.Code != 409 {
		t.Fatal("new key gave another trial", rr.Code)
	}
	if count(t, e, "trial_grants") != 1 {
		t.Fatal("duplicate HTTP grant")
	}
}

// Catches exposing the new mutation without the existing session/Origin/CSRF,
// strict input and idempotency boundaries. Denied requests must not reserve.
func TestTelegramTrialHTTPBoundary(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	raw := testkit.SignedMiniAppData(key, 123, e.Clock(), `{"id":702,"first_name":"Owned client","language_code":"en"}`, "")
	body, _ := json.Marshal(map[string]string{"init_data": raw, "accepted_terms_version": "1", "accepted_privacy_version": "1"})
	rr := request(h, "POST", "/api/v1/telegram/mini-app/session", string(body), cfg.HTTP.CabinetOrigin)
	var auth wire.MiniAppSessionResult
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &auth) != nil {
		t.Fatal("signed fixture failed")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		body   string
		want   int
	}{
		{"no_auth", func(r *http.Request) { r.Header.Del("Authorization") }, `{}`, 401},
		{"forged_bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer mini_"+strings.Repeat("z", 43)) }, `{}`, 401},
		{"no_origin", func(r *http.Request) { r.Header.Del("Origin") }, `{}`, 403},
		{"no_csrf", func(r *http.Request) { r.Header.Del("X-CSRF-Token") }, `{}`, 403},
		{"missing_key", func(r *http.Request) { r.Header.Del("Idempotency-Key") }, `{}`, 400},
		{"zero_key", func(r *http.Request) { r.Header.Set("Idempotency-Key", uuid.Nil.String()) }, `{}`, 400},
		{"extra_query", func(r *http.Request) { r.URL.RawQuery = "account_id=" + uuid.NewString() }, `{}`, 400},
		{"duplicate_auth", func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+auth.SessionToken) }, `{}`, 401},
		{"unknown_json", func(*http.Request) {}, `{"account_id":"private-fixture"}`, 400},
		{"non_object", func(*http.Request) {}, `[]`, 400},
		{"null", func(*http.Request) {}, `null`, 400},
		{"oversize", func(*http.Request) {}, strings.Repeat(" ", 16385), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := activationHTTPRequest(cfg.HTTP.CabinetOrigin, auth.SessionToken, auth.CsrfToken, uuid.NewString(), tc.body)
			tc.mutate(r)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, r)
			if rr.Code != tc.want {
				t.Fatalf("want %d got %d", tc.want, rr.Code)
			}
			if strings.Contains(rr.Body.String(), "private-fixture") || strings.Contains(rr.Body.String(), auth.SessionToken) {
				t.Fatal("untrusted input echoed")
			}
		})
	}
	if count(t, e, "trial_requests") != 0 || count(t, e, "trial_grants") != 0 {
		t.Fatal("denied HTTP wrote state")
	}
}

// Catches silently changing old web JSON or treating a Telegram binding as
// original registration source. Independent TG-origin credentials can activate.
func TestTelegramTrialHTTPWebPolicy(t *testing.T) {
	for _, source := range []string{"web", "telegram"} {
		t.Run(source, func(t *testing.T) {
			h, e, cfg := httpFixture(t)
			verifiedHTTP(t, h, e, cfg)
			if _, err := e.Pool.Exec(context.Background(), `UPDATE accounts SET telegram_id=701,original_kind=CASE WHEN $1='telegram' THEN 'telegram' ELSE NULL END,policy_accepted_at=CASE WHEN $1='telegram' THEN now() ELSE policy_accepted_at END WHERE email_key='login@example.test'`, source); err != nil {
				t.Fatal(err)
			}
			rr := request(h, "POST", "/api/v1/auth/login", `{"email":"login@example.test","password":"my long safe password ✨"}`, cfg.HTTP.CabinetOrigin)
			var auth wire.LoginResult
			if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &auth) != nil || len(rr.Result().Cookies()) != 1 {
				t.Fatal("web session failed")
			}
			cookie := rr.Result().Cookies()[0]
			r := httptest.NewRequest("GET", "/api/v1/me", nil)
			r.AddCookie(cookie)
			rr = httptest.NewRecorder()
			h.ServeHTTP(rr, r)
			var profile map[string]any
			if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &profile) != nil {
				t.Fatal("web account failed")
			}
			caps := profile["capabilities"].(map[string]any)
			if source == "web" {
				if len(caps) != 1 || caps["trial_available"] != true {
					t.Fatal("old web JSON changed")
				}
			} else if caps["trial_mode"] != "activate" {
				t.Fatal("TG credentials lost original policy")
			}
			r = activationHTTPRequest(cfg.HTTP.CabinetOrigin, "", auth.CsrfToken, uuid.NewString(), `{}`)
			r.AddCookie(cookie)
			rr = httptest.NewRecorder()
			h.ServeHTTP(rr, r)
			want := 403
			if source == "telegram" {
				want = 201
			}
			if rr.Code != want {
				t.Fatal("cookie source policy", rr.Code)
			}
			if source == "web" {
				r = activationHTTPRequest(cfg.HTTP.CabinetOrigin, "", auth.CsrfToken, uuid.NewString(), `{}`)
				r.URL.Path = "/api/v1/trial-requests"
				r.AddCookie(cookie)
				rr = httptest.NewRecorder()
				h.ServeHTTP(rr, r)
				if rr.Code != 201 || !strings.Contains(rr.Body.String(), `"pending"`) {
					t.Fatal("manual web request changed", rr.Code)
				}
			}
		})
	}
}
