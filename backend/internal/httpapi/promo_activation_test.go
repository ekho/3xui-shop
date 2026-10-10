package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPromoActivationHTTPInputAndAuthority(t *testing.T) {
	h, e, cfg := httpFixture(t)
	client := supportLogin(t, h, e, cfg, "promo-activation-client@example.test")
	noCSRF := client
	noCSRF.csrf = ""
	for _, tc := range []struct {
		session *supportSession
		origin  string
		body    string
		key     uuid.UUID
		status  int
	}{
		{nil, cfg.HTTP.CabinetOrigin, `{"code":"fixture"}`, uuid.New(), 401},
		{&noCSRF, cfg.HTTP.CabinetOrigin, `{"code":"fixture"}`, uuid.New(), 403},
		{&client, "https://foreign.example.test", `{"code":"fixture"}`, uuid.New(), 403},
		{&client, cfg.HTTP.CabinetOrigin, `{"code":""}`, uuid.New(), 400},
		{&client, cfg.HTTP.CabinetOrigin, `{"code":"` + strings.Repeat("x", 33) + `"}`, uuid.New(), 400},
		{&client, cfg.HTTP.CabinetOrigin, `{"code":"fixture","account_id":"` + uuid.NewString() + `"}`, uuid.New(), 400},
		{&client, cfg.HTTP.CabinetOrigin, `{"code":"fixture"}`, uuid.Nil, 400},
		{&client, cfg.HTTP.CabinetOrigin, `{"code":"missing"}`, uuid.New(), 404},
	} {
		r := supportRequest(h, tc.session, "POST", "/api/v1/promocodes/activate", "application/json", []byte(tc.body), tc.origin, tc.key)
		if r.Code != tc.status {
			t.Fatalf("activation authority/input: got %d, want %d", r.Code, tc.status)
		}
	}
	if _, err := e.Pool.Exec(context.Background(), "UPDATE accounts SET restricted=true WHERE id=$1", client.id); err != nil {
		t.Fatal(err)
	}
	if r := supportRequest(h, &client, "POST", "/api/v1/promocodes/activate", "application/json", []byte(`{"code":"missing"}`), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 403 {
		t.Fatal("restricted replay authority", r.Code)
	}
}
