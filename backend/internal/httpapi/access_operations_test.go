package httpapi

import (
	"context"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestAccessOperationHTTPAuthorityAndStrictShape(t *testing.T) {
	h, e, cfg := httpFixture(t)
	customer := supportLogin(t, h, e, cfg, "access-http-customer@example.test")
	operator := supportLogin(t, h, e, cfg, "access-http-operator@example.test")
	path := "/api/v1/operator/clients/" + customer.id.String() + "/access-operations"
	good := []byte(`{"kind":"compensate","reason":"support","days":7}`)
	if r := supportRequest(h, &customer, "POST", path, "application/json", good, cfg.CabinetOrigin, uuid.New()); r.Code != 403 {
		t.Fatalf("customer wrote: %d", r.Code)
	}
	if _, err := e.Pool.Exec(context.Background(), "INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)", operator.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{[]byte(`{"kind":"compensate","reason":"x","days":7,"operator_account_id":"` + operator.id.String() + `"}`), []byte(`{"kind":"assign_plan","reason":"x","days":2}`), []byte(`{"kind":"reset_traffic","reason":"x","days":2}`), []byte(`{"kind":"compensate","reason":"x","days":7,"unknown":true}`)} {
		if r := supportRequest(h, &operator, "POST", path, "application/json", body, cfg.CabinetOrigin, uuid.New()); r.Code != 400 {
			t.Fatalf("invalid body accepted: %d", r.Code)
		}
	}
	if r := supportRequest(h, &operator, "POST", path, "application/json", []byte(strings.Repeat("x", 16385)), cfg.CabinetOrigin, uuid.New()); r.Code != 400 {
		t.Fatalf("oversize: %d", r.Code)
	}
	if r := supportRequest(h, &operator, "POST", path, "application/json", good, "https://attacker.example", uuid.New()); r.Code != 403 {
		t.Fatalf("origin: %d", r.Code)
	}
	noCSRF := operator
	noCSRF.csrf = ""
	if r := supportRequest(h, &noCSRF, "POST", path, "application/json", good, cfg.CabinetOrigin, uuid.New()); r.Code != 403 {
		t.Fatalf("csrf: %d", r.Code)
	}
	if _, err := e.Pool.Exec(context.Background(), "UPDATE accounts SET restricted=true WHERE id=$1", operator.id); err != nil {
		t.Fatal(err)
	}
	if r := supportRequest(h, &operator, "POST", path, "application/json", good, cfg.CabinetOrigin, uuid.New()); r.Code != 403 {
		t.Fatalf("restricted operator: %d", r.Code)
	}
}
