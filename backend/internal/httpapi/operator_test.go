package httpapi

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"testing"
)

func TestOperatorHTTPAuthorityAndActions(t *testing.T) {
	h, e, cfg := httpFixture(t)
	customer := supportLogin(t, h, e, cfg, "s06-http-customer@example.test")
	operator := supportLogin(t, h, e, cfg, "s06-http-operator@example.test")
	search := []byte(`{"q":"","page":1,"per_page":50}`)
	if r := supportRequest(h, &customer, "POST", "/api/v1/operator/clients/search", "application/json", search, cfg.CabinetOrigin, uuid.Nil); r.Code != 403 {
		t.Fatal("customer search", r.Code)
	}
	if r := supportRequest(h, &customer, "GET", "/api/v1/operator/clients/"+operator.id.String()+"/key", "", nil, "", uuid.Nil); r.Code != 403 {
		t.Fatal("customer key", r.Code)
	}
	if _, err := e.Pool.Exec(context.Background(), `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, operator.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	if r := supportRequest(h, &operator, "GET", "/api/v1/operator/session", "", nil, "", uuid.Nil); r.Code != 200 {
		t.Fatal("operator session", r.Code)
	}
	if r := supportRequest(h, &operator, "POST", "/api/v1/operator/clients/search", "application/json", search, "https://attacker.example.test", uuid.Nil); r.Code != 403 {
		t.Fatal("origin bypass", r.Code)
	}
	noCSRF := operator
	noCSRF.csrf = ""
	if r := supportRequest(h, &noCSRF, "POST", "/api/v1/operator/clients/search", "application/json", search, cfg.CabinetOrigin, uuid.Nil); r.Code != 403 {
		t.Fatal("csrf bypass", r.Code)
	}
	if r := supportRequest(h, &operator, "POST", "/api/v1/operator/clients/search", "application/json", search, cfg.CabinetOrigin, uuid.Nil); r.Code != 200 {
		t.Fatal("search", r.Code)
	}
	if r := supportRequest(h, &operator, "GET", "/api/v1/operator/clients/"+customer.id.String(), "", nil, "", uuid.Nil); r.Code != 200 {
		t.Fatal("card", r.Code)
	}
	trial := supportRequest(h, &customer, "POST", "/api/v1/trial-requests", "application/json", []byte(`{}`), cfg.CabinetOrigin, uuid.New())
	if trial.Code != 201 {
		t.Fatal("trial", trial.Code)
	}
	var request wire.TrialRequest
	if err := json.Unmarshal(trial.Body.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	decisionPath := "/api/v1/operator/trial-requests/" + request.RequestId.String() + "/decision"
	if r := supportRequest(h, &operator, "POST", decisionPath, "application/json", []byte(`{"decision":"reject","reason":"bad\u0000reason"}`), cfg.CabinetOrigin, uuid.New()); r.Code != 400 {
		t.Fatal("NUL reason accepted", r.Code)
	}
	forged := []byte(`{"decision":"approve","reason":"","operator_tg_id":101}`)
	if r := supportRequest(h, &operator, "POST", decisionPath, "application/json", forged, cfg.CabinetOrigin, uuid.New()); r.Code != 400 {
		t.Fatal("forged actor", r.Code)
	}
	key := uuid.New()
	body := []byte(`{"decision":"approve","reason":""}`)
	if r := supportRequest(h, &operator, "POST", decisionPath, "application/json", body, cfg.CabinetOrigin, key); r.Code != 200 {
		t.Fatal("decision", r.Code)
	}
	if r := supportRequest(h, &operator, "POST", decisionPath, "application/json", body, cfg.CabinetOrigin, key); r.Code != 200 {
		t.Fatal("decision replay", r.Code)
	}
	createPath := "/api/v1/operator/clients/trial"
	if r := supportRequest(h, &operator, "POST", createPath, "application/json", []byte(`{"telegram_id":"9223372036854775807","display_name":"Fixture","locale":"ru"}`), cfg.CabinetOrigin, uuid.New()); r.Code != 201 {
		t.Fatal("Telegram trial", r.Code)
	}
	if _, err := e.Pool.Exec(context.Background(), `DELETE FROM operator_accounts WHERE account_id=$1`, operator.id); err != nil {
		t.Fatal(err)
	}
	if r := supportRequest(h, &operator, "GET", "/api/v1/operator/clients/"+customer.id.String(), "", nil, "", uuid.Nil); r.Code != 403 {
		t.Fatal("role revoke", r.Code)
	}
	if r := supportRequest(h, &operator, "POST", createPath, "application/json", []byte(`{}`), cfg.CabinetOrigin, uuid.New()); r.Code != 403 {
		t.Fatal("revoked before parse", r.Code)
	}
}
