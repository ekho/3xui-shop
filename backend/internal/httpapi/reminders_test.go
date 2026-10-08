package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Removing own-session auth/strict input or default-false consent breaks this real HTTP contract.
func TestReminderHTTPAuthority(t *testing.T) {
	h, e, cfg := httpFixture(t)
	client := supportLogin(t, h, e, cfg, "reminder-client@example.test")
	call := func(s *supportSession, method, path, body, origin string) *httptest.ResponseRecorder {
		return supportRequest(h, s, method, path, "application/json", []byte(body), origin, uuid.Nil)
	}
	r := call(&client, "GET", "/api/v1/reminders", "", "")
	if r.Code != 200 {
		t.Fatal("own reminders", r.Code)
	}
	var out struct {
		Version        string            `json:"version"`
		EmailEnabled   bool              `json:"email_enabled"`
		EmailAvailable bool              `json:"email_available"`
		Reminders      []json.RawMessage `json:"reminders"`
	}
	if json.Unmarshal(r.Body.Bytes(), &out) != nil || out.Version != "reminders-v1" || out.EmailEnabled || !out.EmailAvailable || out.Reminders == nil || len(out.Reminders) != 0 {
		t.Fatal("empty own result/default-false independent email consent")
	}
	missing := "/api/v1/reminders/" + uuid.NewString() + "/dismiss"
	noCSRF := client
	noCSRF.csrf = ""
	for _, tc := range []struct {
		name                       string
		session                    *supportSession
		method, path, body, origin string
		want                       int
	}{
		{"absent session", nil, "GET", "/api/v1/reminders", "", "", 401},
		{"unauthenticated preference", nil, "POST", "/api/v1/reminders/preferences", `{"email_enabled":true}`, cfg.HTTP.CabinetOrigin, 401},
		{"csrf", &noCSRF, "POST", "/api/v1/reminders/preferences", `{"email_enabled":true}`, cfg.HTTP.CabinetOrigin, 403},
		{"origin", &client, "POST", "/api/v1/reminders/preferences", `{"email_enabled":true}`, "https://other.example.test", 403},
		{"unknown field", &client, "POST", "/api/v1/reminders/preferences", `{"email_enabled":true,"account_id":"sensitive"}`, cfg.HTTP.CabinetOrigin, 400},
		{"missing field", &client, "POST", "/api/v1/reminders/preferences", `{}`, cfg.HTTP.CabinetOrigin, 400},
		{"wrong type", &client, "POST", "/api/v1/reminders/preferences", `{"email_enabled":"true"}`, cfg.HTTP.CabinetOrigin, 400},
		{"unknown query", &client, "GET", "/api/v1/reminders?account_id=sensitive", "", "", 400},
		{"malformed id", &client, "POST", "/api/v1/reminders/invalid/dismiss", `{}`, cfg.HTTP.CabinetOrigin, 400},
		{"noncanonical id", &client, "POST", "/api/v1/reminders/AAAAAAAA-AAAA-4AAA-AAAA-AAAAAAAAAAAA/dismiss", "", cfg.HTTP.CabinetOrigin, 400},
		{"absent own id", &client, "POST", missing, "", cfg.HTTP.CabinetOrigin, 404},
		{"empty object is not empty body", &client, "POST", missing, `{}`, cfg.HTTP.CabinetOrigin, 400},
		{"dismiss input", &client, "POST", missing, `{"account_id":"sensitive"}`, cfg.HTTP.CabinetOrigin, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := call(tc.session, tc.method, tc.path, tc.body, tc.origin)
			if r.Code != tc.want || strings.Contains(r.Body.String(), "sensitive") {
				t.Fatal("boundary", r.Code, "want", tc.want)
			}
		})
	}
	for _, enabled := range []bool{true, true, false} {
		body, _ := json.Marshal(map[string]bool{"email_enabled": enabled})
		r = call(&client, "POST", "/api/v1/reminders/preferences", string(body), cfg.HTTP.CabinetOrigin)
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.EmailEnabled != enabled {
			t.Fatal("explicit idempotent preference", r.Code)
		}
	}
	var changes int
	if err := e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE account_id=$1 AND action='notification_preferences_updated'`, client.id).Scan(&changes); err != nil || changes != 2 {
		t.Fatal("audit real changes only", changes, err)
	}
	if _, err := e.Pool.Exec(context.Background(), `UPDATE accounts SET restricted=true WHERE id=$1`, client.id); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/reminders", "/api/v1/reminders/preferences", missing} {
		method, body := "POST", `{"email_enabled":true}`
		if path == "/api/v1/reminders" {
			method, body = "GET", ""
		}
		if path == missing {
			body = `{}`
		}
		if r = call(&client, method, path, body, cfg.HTTP.CabinetOrigin); r.Code != 403 {
			t.Fatal("restricted before data", r.Code)
		}
	}
}
