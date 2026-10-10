package httpapi

import (
	"example.com/cabinet/backend/internal/app"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRetiredTransitionRoutesRemainAbsent(t *testing.T) {
	h := New(&app.Modules{}, nil, app.HTTPConfig{})
	for _, path := range []string{
		"/internal/v1/telegram/jobs/claim",
		"/internal/v1/telegram/jobs/00000000-0000-0000-0000-000000000001/result",
		"/internal/v1/trial-requests/00000000-0000-0000-0000-000000000001/decision",
		"/internal/v1/trial-requests/00000000-0000-0000-0000-000000000001/reconsider",
		"/internal/v1/trial-operations/00000000-0000-0000-0000-000000000001/reconcile",
	} {
		for _, auth := range []string{"", "Bearer ", "Bearer arbitrary-token"} {
			r := httptest.NewRequest("POST", path, strings.NewReader(`{"limit":1}`))
			r.Header.Set("Authorization", auth)
			r.Header.Set("Origin", "https://arbitrary.example.test")
			r.Header.Set("X-CSRF-Token", "arbitrary")
			out := httptest.NewRecorder()
			h.ServeHTTP(out, r)
			if out.Code != 404 {
				t.Fatalf("retired route %s with %q returned %d", path, auth, out.Code)
			}
		}
	}
}

func TestLegacyPaymentWebhookAliasesUseCurrentBoundary(t *testing.T) {
	h, _, _ := httpFixture(t)
	for _, provider := range []string{"yoomoney", "yookassa", "cryptomus", "heleket"} {
		var previous int
		for _, path := range []string{"/webhooks/" + provider, "/" + provider} {
			r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
			r.Header.Set("Content-Type", "application/json")
			r.RemoteAddr = "192.0.2.10:12345"
			out := httptest.NewRecorder()
			h.ServeHTTP(out, r)
			if out.Code == 404 || out.Code < 400 || previous != 0 && out.Code != previous {
				t.Fatalf("%s callback boundary differs: %d versus %d", provider, out.Code, previous)
			}
			previous = out.Code
		}
	}
}
