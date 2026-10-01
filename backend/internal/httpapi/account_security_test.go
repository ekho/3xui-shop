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
	"time"
)

// Missing reset routes, relaxed strict decode/origin, or falsely claiming delivery fails here.
func TestAccountSecurityHTTP(t *testing.T) {
	h, env, cfg := httpFixture(t)
	for _, tc := range []struct {
		path, body, origin string
		want               int
	}{
		{"/api/v1/auth/password-reset", `{"email":"unknown@example.test","locale":"en"}`, cfg.CabinetOrigin, 202},
		{"/api/v1/auth/password-reset", `{"email":"other@example.test","locale":"en","extra":true}`, cfg.CabinetOrigin, 400},
		{"/api/v1/auth/password-reset", `{"email":"other@example.test","locale":"en"}`, "https://foreign.example.test", 403},
		{"/api/v1/auth/password-reset?unknown=1", `{"email":"other@example.test","locale":"en"}`, cfg.CabinetOrigin, 400},
		{"/api/v1/auth/password-reset", strings.Repeat("x", 16385), cfg.CabinetOrigin, 400},
		{"/api/v1/auth/password-reset/complete", `{"token":"` + strings.Repeat("x", 43) + `","new_password":"my long safe password ✨"}`, cfg.CabinetOrigin, 400},
	} {
		rr := request(h, "POST", tc.path, tc.body, tc.origin)
		if rr.Code != tc.want {
			t.Errorf("reset boundary want%d got%d", tc.want, rr.Code)
		}
		if rr.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cache boundary")
		}
		if len(rr.Result().Cookies()) != 0 {
			t.Fatal("reset returned session cookie")
		}
		if tc.want == 202 {
			var out map[string]any
			json.Unmarshal(rr.Body.Bytes(), &out)
			if len(out) != 2 || out["challenge_id"] == nil || out["resend_after"] != float64(60) {
				t.Fatal("reset accepted shape")
			}
		}
	}
	if rr := request(h, "GET", "/api/v1/auth/password-reset/complete?token=secret", "", " "); rr.Code != 400 {
		t.Fatal("scanner query accepted")
	}
	// Cookie B must never be consumed, rotated or replaced by a reset proof belonging to A.
	verifiedHTTP(t, h, env, cfg)
	rr := request(h, "POST", "/api/v1/auth/login", `{"email":"login@example.test","password":"my long safe password ✨"}`, cfg.CabinetOrigin)
	if rr.Code != 200 {
		t.Fatal("owner B login")
	}
	cookie := rr.Result().Cookies()[0]
	var login wire.LoginResult
	json.Unmarshal(rr.Body.Bytes(), &login)
	aID := uuid.New()
	ctx := context.Background()
	if _, err := env.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) SELECT $1,'reset-a@example.test',locale,password_hash,verified_at,$2,'0123456789abcdef',$3,terms_version,privacy_version FROM accounts WHERE email_key='login@example.test'`, aID, uuid.New(), "acct_"+strings.ReplaceAll(aID.String(), "-", "")); err != nil {
		t.Fatal("owner A fixture")
	}
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		var encoded []byte
		if body != nil {
			encoded, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(string(encoded)))
		req.AddCookie(cookie)
		req.Header.Set("Origin", cfg.CabinetOrigin)
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out
	}
	rr = call(http.MethodPost, "/api/v1/auth/password-reset", map[string]string{"email": "reset-a@example.test", "locale": "en"})
	if rr.Code != 202 {
		t.Fatal("owner A reset request")
	}
	var accepted wire.PasswordResetAccepted
	json.Unmarshal(rr.Body.Bytes(), &accepted)
	_, token, _ := testkit.CredentialMailSecrets(t, env.Pool, cfg.MailKey, accepted.ChallengeId)
	rr = call(http.MethodPost, "/api/v1/auth/password-reset/complete", map[string]string{"token": token, "new_password": "a different safe password ✨"})
	if rr.Code != 204 || len(rr.Result().Cookies()) != 0 {
		t.Fatal("reset A changed browser cookie")
	}
	rr = call(http.MethodGet, "/api/v1/me", nil)
	var after wire.AccountResult
	json.Unmarshal(rr.Body.Bytes(), &after)
	if rr.Code != 200 || after.Account.AccountId != login.Account.AccountId {
		t.Fatal("reset A changed browser owner B")
	}
}

func TestRestrictedLogout(t *testing.T) {
	h, e, cfg := httpFixture(t)
	verifiedHTTP(t, h, e, cfg)
	rr := request(h, "POST", "/api/v1/auth/login", `{"email":"login@example.test","password":"my long safe password ✨"}`, cfg.CabinetOrigin)
	cookie := rr.Result().Cookies()[0]
	if _, err := e.Pool.Exec(context.Background(), `UPDATE accounts SET restricted=true`); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, origin, csrf string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(cookie)
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrf)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out
	}
	rr = call("GET", "/api/v1/auth/session", "", "")
	if rr.Code != 200 {
		t.Fatal("restricted context", rr.Code)
	}
	var out wire.SessionContext
	if json.Unmarshal(rr.Body.Bytes(), &out) != nil || out.CsrfToken == "" {
		t.Fatal("context shape")
	}
	var fields map[string]any
	json.Unmarshal(rr.Body.Bytes(), &fields)
	if len(fields) != 1 {
		t.Fatal("context exposed business data")
	}
	if rr = call("GET", "/api/v1/me/security", "", ""); rr.Code != 403 {
		t.Fatal("restricted business access")
	}
	for _, pair := range [][2]string{{cfg.CabinetOrigin, ""}, {"https://foreign.example.test", out.CsrfToken}} {
		if rr = call("POST", "/api/v1/auth/logout", pair[0], pair[1]); rr.Code != 403 {
			t.Fatal("logout CSRF/Origin bypass", rr.Code)
		}
	}
	e.Redis.Close()
	rr = call("POST", "/api/v1/auth/logout", cfg.CabinetOrigin, out.CsrfToken)
	if rr.Code != 204 || rr.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("restricted logout", rr.Code)
	}
	if rr = call("GET", "/api/v1/auth/session", "", ""); rr.Code != 401 {
		t.Fatal("session survived logout")
	}
}

func TestPasswordChangeHTTP(t *testing.T) {
	for _, path := range []string{"/api/v1/me/password-change", "/api/v1/me/sessions/revoke-others"} {
		t.Run(path, func(t *testing.T) {
			h, e, cfg := httpFixture(t)
			verifiedHTTP(t, h, e, cfg)
			rr := request(h, "POST", "/api/v1/auth/login", `{"email":"login@example.test","password":"my long safe password ✨"}`, cfg.CabinetOrigin)
			cookie := rr.Result().Cookies()[0]
			var login wire.LoginResult
			json.Unmarshal(rr.Body.Bytes(), &login)
			var expiry time.Time
			e.Pool.QueryRow(context.Background(), `SELECT absolute_expires_at FROM sessions`).Scan(&expiry)
			body := `{"current_password":"my long safe password ✨"}`
			if strings.HasSuffix(path, "password-change") {
				body = `{"current_password":"my long safe password ✨","new_password":"a different safe password ✨"}`
			}
			call := func(cookie *http.Cookie, method, path, body, csrf, origin string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				if cookie != nil {
					req.AddCookie(cookie)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-CSRF-Token", csrf)
				req.Header.Set("Origin", origin)
				out := httptest.NewRecorder()
				h.ServeHTTP(out, req)
				return out
			}
			for _, pair := range [][2]string{{"", cfg.CabinetOrigin}, {login.CsrfToken, "https://foreign.example.test"}} {
				if rr = call(cookie, "POST", path, body, pair[0], pair[1]); rr.Code != 403 {
					t.Fatal("CSRF/Origin", rr.Code)
				}
			}
			if rr = call(nil, "POST", path, body, login.CsrfToken, cfg.CabinetOrigin); rr.Code != 401 {
				t.Fatal("missing session")
			}
			if rr = call(cookie, "POST", path, body[:len(body)-1]+`,"account_id":"`+uuid.NewString()+`"}`, login.CsrfToken, cfg.CabinetOrigin); rr.Code != 400 {
				t.Fatal("foreign ID accepted")
			}
			rr = call(cookie, "POST", path, body, login.CsrfToken, cfg.CabinetOrigin)
			if rr.Code != 204 || len(rr.Result().Cookies()) != 1 {
				t.Fatal("rotation", rr.Code)
			}
			rotated := rr.Result().Cookies()[0]
			if rotated.Value == cookie.Value || !rotated.Secure || !rotated.HttpOnly || rotated.Domain != "" || rotated.Path != "/" || rotated.SameSite != http.SameSiteLaxMode || !rotated.Expires.Equal(expiry.Truncate(time.Second)) || rotated.MaxAge > cookie.MaxAge {
				t.Fatal("rotated cookie boundary")
			}
			if rr = call(cookie, "GET", "/api/v1/me", "", "", ""); rr.Code != 401 {
				t.Fatal("copied cookie survived")
			}
			rr = call(rotated, "GET", "/api/v1/me", "", "", "")
			var after wire.AccountResult
			json.Unmarshal(rr.Body.Bytes(), &after)
			if rr.Code != 200 || after.CsrfToken == login.CsrfToken || after.Account.AccountId != login.Account.AccountId {
				t.Fatal("new cookie or CSRF")
			}
		})
	}
}
