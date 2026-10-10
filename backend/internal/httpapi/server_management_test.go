package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func managedRequest(h http.Handler, cookie *http.Cookie, cfg app.Config, csrf, key, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/json")
	if method == "POST" {
		r.Header.Set("Origin", cfg.HTTP.CabinetOrigin)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	return out
}

func TestServerManagementHTTPAuthorityAndReplay(t *testing.T) {
	s, e := fixture(t)
	panelFixture(t, s)
	cfg := *s.cfg
	h := New(app.NewModules(e.Pool, e.Redis, s.queue, s.cfg), e.Pool, cfg.HTTP)
	verifiedHTTP(t, h, e, cfg)
	login := request(h, "POST", "/api/v1/auth/login", `{"email":"login@example.test","password":"my long safe password ✨"}`, cfg.HTTP.CabinetOrigin)
	if login.Code != 200 {
		t.Fatal("login", login.Code)
	}
	var session wire.LoginResult
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookie := login.Result().Cookies()[0]
	ctx := context.Background()
	actor := session.Account.AccountId
	if out := managedRequest(h, cookie, cfg, "", "", "GET", "/api/v1/operator/servers", ""); out.Code != 403 {
		t.Fatal("client read", out.Code)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,now())`, actor); err != nil {
		t.Fatal(err)
	}
	if out := managedRequest(h, cookie, cfg, "", "", "GET", "/api/v1/operator/servers", ""); out.Code != 403 {
		t.Fatal("ordinary operator read", out.Code)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO infrastructure_operators(account_id,granted_at) VALUES($1,now())`, actor); err != nil {
		t.Fatal(err)
	}
	if out := managedRequest(h, cookie, cfg, "", "", "GET", "/api/v1/operator/servers", ""); out.Code != 200 {
		t.Fatal("infrastructure list", out.Code, out.Body.String())
	}
	key := uuid.NewString()
	body := `{"name":"New Panel","host":"https://panel.example.test","max_clients":5}`
	if out := managedRequest(h, cookie, cfg, "", key, "POST", "/api/v1/operator/servers", body); out.Code != 403 {
		t.Fatal("missing csrf", out.Code)
	}
	first := managedRequest(h, cookie, cfg, session.CsrfToken, key, "POST", "/api/v1/operator/servers", body)
	if first.Code != 201 {
		t.Fatal("create", first.Code, first.Body.String())
	}
	var server wire.OperatorServerDetail
	if err := json.Unmarshal(first.Body.Bytes(), &server); err != nil || server.Id == "" {
		t.Fatal("response", err)
	}
	if out := managedRequest(h, cookie, cfg, session.CsrfToken, key, "POST", "/api/v1/operator/servers", body); out.Code != 200 {
		t.Fatal("replay", out.Code)
	}
	if out := managedRequest(h, cookie, cfg, session.CsrfToken, key, "POST", "/api/v1/operator/servers", strings.Replace(body, "New Panel", "Changed", 1)); out.Code != 409 {
		t.Fatal("changed input", out.Code)
	}
	if out := managedRequest(h, cookie, cfg, session.CsrfToken, uuid.NewString(), "POST", "/api/v1/operator/servers/"+server.Id+"/delete", `{"confirmation":false}`); out.Code != 400 {
		t.Fatal("confirmation", out.Code)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO vpn_servers(id,name,host,max_clients) VALUES('legacy/id','legacy','https://legacy.example.test',0)`); err != nil {
		t.Fatal(err)
	}
	if out := managedRequest(h, cookie, cfg, "", "", "GET", "/api/v1/operator/servers/legacy%2Fid", ""); out.Code != 200 {
		t.Fatalf("opaque legacy ID path: %d %s", out.Code, out.Body.String())
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO vpn_servers(id,name,host,max_clients) VALUES('legacy%2Fid','legacy-percent','https://legacy-percent.example.test',0)`); err != nil {
		t.Fatal(err)
	}
	if out := managedRequest(h, cookie, cfg, "", "", "GET", "/api/v1/operator/servers/legacy%252Fid", ""); out.Code != 200 {
		t.Fatalf("literal percent legacy ID path: %d %s", out.Code, out.Body.String())
	}
}
