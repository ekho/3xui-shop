package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type supportSession struct {
	id     uuid.UUID
	cookie *http.Cookie
	csrf   string
}

func supportLogin(t *testing.T, h http.Handler, e *testkit.Env, cfg app.Config, email string) supportSession {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "locale": "ru", "accepted_terms_version": "1", "accepted_privacy_version": "1"})
	r := request(h, "POST", "/api/v1/auth/register", string(body), cfg.HTTP.CabinetOrigin)
	if r.Code != 202 {
		t.Fatal("register", r.Code)
	}
	var registration wire.RegistrationAccepted
	if json.Unmarshal(r.Body.Bytes(), &registration) != nil {
		t.Fatal("register response")
	}
	_, token, _ := testkit.MailSecrets(t, e.Pool, cfg.Mail.MailKey, registration.ChallengeId)
	body, _ = json.Marshal(map[string]string{"token": token, "new_password": "long safe password"})
	if r = request(h, "POST", "/api/v1/auth/verify-email", string(body), cfg.HTTP.CabinetOrigin); r.Code != 200 {
		t.Fatal("verify", r.Code)
	}
	body, _ = json.Marshal(map[string]string{"email": email, "password": "long safe password"})
	if r = request(h, "POST", "/api/v1/auth/login", string(body), cfg.HTTP.CabinetOrigin); r.Code != 200 {
		t.Fatal("login", r.Code)
	}
	var login wire.LoginResult
	if json.Unmarshal(r.Body.Bytes(), &login) != nil || len(r.Result().Cookies()) != 1 {
		t.Fatal("login response")
	}
	return supportSession{login.Account.AccountId, r.Result().Cookies()[0], login.CsrfToken}
}

func supportRequest(h http.Handler, session *supportSession, method, path, contentType string, body []byte, origin string, key uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if session != nil {
		req.AddCookie(session.cookie)
		req.Header.Set("X-CSRF-Token", session.csrf)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if key != uuid.Nil {
		req.Header.Set("Idempotency-Key", key.String())
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func supportMultipart(t *testing.T, text, name string, file []byte) (string, []byte) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("text", text); err != nil {
		t.Fatal(err)
	}
	if name != "" {
		part, err := w.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(file); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w.FormDataContentType(), body.Bytes()
}

func TestSupportHTTPBoundaries(t *testing.T) {
	h, e, cfg := httpFixture(t)
	customer := supportLogin(t, h, e, cfg, "support-http-customer@example.test")
	operator := supportLogin(t, h, e, cfg, "support-http-operator@example.test")
	foreign := supportLogin(t, h, e, cfg, "support-http-foreign@example.test")
	path := "/api/v1/operator/clients/" + customer.id.String() + "/support"
	if _, err := e.Pool.Exec(context.Background(), `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, operator.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	ct, body := supportMultipart(t, "private", "proof.txt", []byte("private bytes"))
	malformed := []byte("not a multipart body")
	if r := supportRequest(h, nil, "POST", "/api/v1/support/messages", ct, malformed, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 401 {
		t.Fatal("auth before multipart", r.Code)
	}
	if r := supportRequest(h, &foreign, "POST", path+"/messages", ct, malformed, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 403 {
		t.Fatal("role before multipart", r.Code)
	}
	noCSRF := customer
	noCSRF.csrf = ""
	if r := supportRequest(h, &noCSRF, "POST", "/api/v1/support/messages", ct, malformed, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 403 {
		t.Fatal("csrf before multipart", r.Code)
	}
	if r := supportRequest(h, &customer, "POST", "/api/v1/support/messages", ct, malformed, "https://attacker.example.test", uuid.New()); r.Code != 403 {
		t.Fatal("origin before multipart", r.Code)
	}
	if r := supportRequest(h, &customer, "POST", "/api/v1/support/messages", ct, bytes.Repeat([]byte("x"), 10*1024*1024+16*1024+1), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 413 {
		t.Fatal("multipart body cap", r.Code)
	}
	badCT, badBody := supportMultipart(t, "", "bad/name", []byte{1})
	if r := supportRequest(h, &customer, "POST", "/api/v1/support/messages", badCT, badBody, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 400 {
		t.Fatal("path filename", r.Code)
	}
	if r := supportRequest(h, &customer, "POST", "/api/v1/support/messages", "application/json", []byte(`{"text":"\u0000"}`), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 400 {
		t.Fatal("JSON NUL text", r.Code)
	}
	nulCT, nulBody := supportMultipart(t, "before\x00after", "", nil)
	if r := supportRequest(h, &customer, "POST", "/api/v1/support/messages", nulCT, nulBody, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 400 {
		t.Fatal("multipart NUL text", r.Code)
	}
	key := uuid.New()
	r := supportRequest(h, &customer, "POST", "/api/v1/support/messages", ct, body, cfg.HTTP.CabinetOrigin, key)
	if r.Code != 201 {
		t.Fatal("create", r.Code)
	}
	var message wire.SupportMessage
	if json.Unmarshal(r.Body.Bytes(), &message) != nil || message.Attachment == nil {
		t.Fatal("message response")
	}
	r = supportRequest(h, &customer, "POST", "/api/v1/support/messages", ct, body, cfg.HTTP.CabinetOrigin, key)
	if r.Code != 200 {
		t.Fatal("idempotent replay", r.Code)
	}
	r = supportRequest(h, &customer, "POST", "/api/v1/support/messages", "application/json", []byte(`{"text":"changed"}`), cfg.HTTP.CabinetOrigin, key)
	if r.Code != 409 {
		t.Fatal("payload mismatch", r.Code)
	}
	r = supportRequest(h, &customer, "POST", "/api/v1/support/messages", "application/json", []byte(`{"text":"x","account_id":"`+foreign.id.String()+`"}`), cfg.HTTP.CabinetOrigin, uuid.New())
	if r.Code != 400 {
		t.Fatal("body actor accepted", r.Code)
	}
	attachment := "/api/v1/support/messages/" + message.Id.String() + "/attachment"
	if r = supportRequest(h, &foreign, "GET", attachment, "", nil, "", uuid.Nil); r.Code != 404 {
		t.Fatal("foreign attachment", r.Code)
	}
	r = supportRequest(h, &operator, "GET", attachment, "", nil, "", uuid.Nil)
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), []byte("private bytes")) || r.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(r.Header().Get("Content-Disposition"), "attachment;") || r.Header().Get("Cache-Control") != "no-store" || r.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("private download headers", r.Code)
	}
	if r = supportRequest(h, &operator, "GET", path, "", nil, "", uuid.Nil); r.Code != 200 {
		t.Fatal("operator read", r.Code)
	}
	if r = supportRequest(h, &operator, "POST", path+"/messages", "application/json", []byte(`{"text":"operator reply"}`), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 201 {
		t.Fatal("operator reply", r.Code)
	}
	if r = supportRequest(h, &operator, "POST", path+"/read", "application/json", []byte(`{"sequence":`+string(mustJSON(t, message.Sequence))+`}`), cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 204 {
		t.Fatal("ack", r.Code)
	}
	if r = supportRequest(h, &operator, "POST", path+"/ban", "application/json", []byte(`{"banned":true,"reason":"abuse"}`), cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 204 {
		t.Fatal("ban", r.Code)
	}
	if r = supportRequest(h, &customer, "POST", "/api/v1/support/messages", "application/json", []byte(`{"text":"blocked"}`), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 403 {
		t.Fatal("ban write", r.Code)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
