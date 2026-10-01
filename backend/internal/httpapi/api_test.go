package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/s01"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func httpFixture(t *testing.T) (http.Handler, *testkit.Env, s01.Config) {
	t.Helper()
	env := testkit.Open(t)
	queue, err := river.NewClient(riverpgxv5.New(env.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := s01.Config{CabinetOrigin: "https://cabinet.example.test", TermsVersion: "1", PrivacyVersion: "1", MailKey: bytes.Repeat([]byte{1}, 32), CodeKey: bytes.Repeat([]byte{2}, 32), RateNamespace: uuid.NewString()}
	return New(s01.NewService(env.Pool, env.Redis, queue, cfg), cfg), env, cfg
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
