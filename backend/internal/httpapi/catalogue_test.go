package httpapi

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"testing"
)

func TestCatalogueHTTPStrictQueriesAndWriteAuthority(t *testing.T) {
	h, e, cfg := httpFixture(t)
	customer := supportLogin(t, h, e, cfg, "catalogue-customer@example.test")
	operator := supportLogin(t, h, e, cfg, "catalogue-operator@example.test")
	if _, err := e.Pool.Exec(context.Background(), `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, operator.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/operator/catalogue", "/api/v1/operator/catalogue?page=1", "/api/v1/operator/catalogue?page=1&per_page=50&", "/api/v1/operator/catalogue?page=1&page=2", "/api/v1/operator/catalogue?per_page=51", "/api/v1/operator/catalogue?unknown=1", "/api/v1/operator/catalogue?page=999999999999999999999", "/api/v1/operator/catalogue?page=%GG", "/api/v1/operator/catalogue?page=-1"} {
		if r := supportRequest(h, &operator, "GET", path, "", nil, "", uuid.Nil); r.Code != 400 {
			t.Fatalf("query %s: %d", path, r.Code)
		}
	}
	if r := supportRequest(h, &operator, "GET", "/api/v1/operator/catalogue?page=1&per_page=50", "", nil, "", uuid.Nil); r.Code != 200 {
		t.Fatalf("valid page %d", r.Code)
	}
	if r := supportRequest(h, &customer, "GET", "/api/v1/operator/catalogue?page=1&per_page=50", "", nil, "", uuid.Nil); r.Code != 403 {
		t.Fatalf("nonoperator %d", r.Code)
	}
	if r := supportRequest(h, &customer, "GET", "/api/v1/catalogue?x=1", "", nil, "", uuid.Nil); r.Code != 400 {
		t.Fatalf("unexpected query %d", r.Code)
	}
	path := "/api/v1/operator/catalogue/plans"
	good := []byte(`{"reason":"initial","terms":{"devices":1,"traffic_gb":0,"profile":"regular","hidden":false,"periods":[30],"prices":[{"period_days":30,"currency":"RUB","amount_minor":"9007199254740993"},{"period_days":30,"currency":"USD","amount_minor":"0"},{"period_days":30,"currency":"XTR","amount_minor":"0"}]}}`)
	if r := supportRequest(h, &operator, "POST", path, "application/json", []byte(`{"reason":"x","terms":{},"actor_account_id":"`+customer.id.String()+`"}`), cfg.CabinetOrigin, uuid.New()); r.Code != 400 {
		t.Fatalf("forged actor %d", r.Code)
	}
	if r := supportRequest(h, &operator, "POST", path, "application/json", good, "https://attacker.example", uuid.New()); r.Code != 403 {
		t.Fatalf("origin %d", r.Code)
	}
	noCSRF := operator
	noCSRF.csrf = ""
	if r := supportRequest(h, &noCSRF, "POST", path, "application/json", good, cfg.CabinetOrigin, uuid.New()); r.Code != 403 {
		t.Fatalf("csrf %d", r.Code)
	}
	key := uuid.New()
	r := supportRequest(h, &operator, "POST", path, "application/json", good, cfg.CabinetOrigin, key)
	if r.Code != 201 {
		t.Fatalf("create %d: %s", r.Code, r.Body.String())
	}
	var plan wire.OperatorCataloguePlan
	if err := json.Unmarshal(r.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.ActorAccountId == nil || *plan.ActorAccountId != operator.id || plan.Prices[0].AmountMinor != "9007199254740993" {
		t.Fatal("response lost actor/price")
	}
	if r := supportRequest(h, &operator, "POST", path, "application/json", good, cfg.CabinetOrigin, key); r.Code != 201 {
		t.Fatalf("replay %d", r.Code)
	}
	if r := supportRequest(h, &customer, "GET", "/api/v1/catalogue", "", nil, "", uuid.Nil); r.Code != 200 || !json.Valid(r.Body.Bytes()) {
		t.Fatalf("public list %d", r.Code)
	}
	if _, err := e.Pool.Exec(context.Background(), `DELETE FROM operator_accounts WHERE account_id=$1`, operator.id); err != nil {
		t.Fatal(err)
	}
	if r := supportRequest(h, &operator, "POST", path, "application/json", good, cfg.CabinetOrigin, key); r.Code != 403 {
		t.Fatalf("revoked role replay %d", r.Code)
	}
}
