package httpapi

import (
	"example.com/cabinet/backend/internal/platform"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDisabledLegacyTransportRejectsEmptyBearer(t *testing.T) {
	h := New(nil, platform.Config{})
	for _, auth := range []string{"", "Bearer ", "Bearer any-value"} {
		r := httptest.NewRequest("POST", "/internal/v1/telegram/jobs/claim", strings.NewReader(`{"limit":1}`))
		r.Header.Set("Authorization", auth)
		r.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		if out.Code != 401 {
			t.Fatal("disabled internal route authorized", out.Code)
		}
	}
}
