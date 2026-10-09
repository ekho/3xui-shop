package httpapi

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func TestMaintenanceHTTPAuthorityReplayAndAdmission(t *testing.T) {
	h, env, cfg := httpFixture(t)
	ctx := context.Background()
	actor := supportLogin(t, h, env, cfg, "maintenance-operator@example.test")
	client := supportLogin(t, h, env, cfg, "maintenance-client@example.test")
	if _, err := env.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, actor.id, env.Clock()); err != nil {
		t.Fatal(err)
	}
	read := func() wire.MaintenanceStatus {
		r := request(h, "GET", "/api/v1/maintenance", "", "")
		var out wire.MaintenanceStatus
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil {
			t.Fatalf("public status: %d %s", r.Code, r.Body.String())
		}
		return out
	}
	if state := read(); state.Enabled || state.Revision != 0 || state.ChangedAt != nil {
		t.Fatalf("initial status: %+v", state)
	}
	trialKey := uuid.New()
	if r := supportRequest(h, &client, "POST", "/api/v1/trial-requests", "application/json", []byte(`{}`), cfg.HTTP.CabinetOrigin, trialKey); r.Code != 201 {
		t.Fatalf("accepted trial: %d %s", r.Code, r.Body.String())
	}
	path := "/api/v1/operator/maintenance"
	body := []byte(`{"enabled":true,"expected_revision":0,"reason":"planned work","confirmed":true}`)
	key := uuid.New()
	if r := supportRequest(h, &client, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, key); r.Code != 403 {
		t.Fatalf("client role: %d", r.Code)
	}
	if r := supportRequest(h, &actor, "POST", path, "application/json", body, "https://wrong.example.test", key); r.Code != 403 {
		t.Fatalf("origin: %d", r.Code)
	}
	noCSRF := actor
	noCSRF.csrf = ""
	if r := supportRequest(h, &noCSRF, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, key); r.Code != 403 {
		t.Fatalf("csrf: %d", r.Code)
	}
	first := supportRequest(h, &actor, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, key)
	if first.Code != 200 {
		t.Fatalf("enable: %d %s", first.Code, first.Body.String())
	}
	var written wire.MaintenanceStatus
	if err := json.Unmarshal(first.Body.Bytes(), &written); err != nil {
		t.Fatal(err)
	}
	if state := read(); !state.Enabled || state.Revision != 1 || state.ChangedAt == nil || written.ChangedAt == nil || !state.ChangedAt.Equal(*written.ChangedAt) {
		t.Fatalf("enabled state: %+v", state)
	}
	if r := supportRequest(h, &client, "POST", "/api/v1/trial-requests", "application/json", []byte(`{}`), cfg.HTTP.CabinetOrigin, trialKey); r.Code != 200 {
		t.Fatalf("accepted trial replay: %d %s", r.Code, r.Body.String())
	}
	if r := supportRequest(h, &client, "POST", "/api/v1/trial-requests", "application/json", []byte(`{}`), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 503 {
		t.Fatalf("new trial admitted: %d %s", r.Code, r.Body.String())
	}
	if r := supportRequest(h, &actor, "POST", path, "application/json", []byte(`{"enabled":false,"expected_revision":1,"reason":"done","confirmed":true}`), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 200 {
		t.Fatalf("disable: %d %s", r.Code, r.Body.String())
	}
	if replay := supportRequest(h, &actor, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, key); replay.Code != 200 || replay.Body.String() != first.Body.String() {
		t.Fatalf("frozen replay: %d %s", replay.Code, replay.Body.String())
	}
	if state := read(); state.Enabled || state.Revision != 2 {
		t.Fatalf("stale replay changed live state: %+v", state)
	}
	if r := supportRequest(h, &actor, "POST", path, "application/json", []byte(`{"enabled":true,"expected_revision":0,"reason":"other","confirmed":true}`), cfg.HTTP.CabinetOrigin, key); r.Code != 409 {
		t.Fatalf("key reused for different body: %d", r.Code)
	}
	if r := supportRequest(h, &actor, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 409 {
		t.Fatalf("stale revision: %d", r.Code)
	}
	if r := supportRequest(h, &actor, "POST", path, "application/json", []byte(`{"enabled":false,"expected_revision":2,"reason":"checked","confirmed":true}`), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 200 {
		t.Fatalf("no-op: %d %s", r.Code, r.Body.String())
	}
	var audits int
	if err := env.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action IN ('maintenance.enabled','maintenance.disabled')`).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("transition audit count %d: %v", audits, err)
	}
	if _, err := env.Pool.Exec(ctx, `DELETE FROM operator_accounts WHERE account_id=$1`, actor.id); err != nil {
		t.Fatal(err)
	}
	if r := supportRequest(h, &actor, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, key); r.Code != 403 {
		t.Fatalf("revoked replay: %d", r.Code)
	}
}

func TestMaintenanceConcurrentRevisionAndSharedInstances(t *testing.T) {
	h, env, cfg := httpFixture(t)
	ctx := context.Background()
	first := supportLogin(t, h, env, cfg, "first-maintenance@example.test")
	second := supportLogin(t, h, env, cfg, "second-maintenance@example.test")
	for _, actor := range []supportSession{first, second} {
		if _, err := env.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, actor.id, env.Clock()); err != nil {
			t.Fatal(err)
		}
	}
	other := New(app.NewModules(env.Pool, env.Redis, nil, &cfg), env.Pool, cfg.HTTP)
	const body = `{"enabled":true,"expected_revision":0,"reason":"planned","confirmed":true}`
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, actor := range []supportSession{first, second} {
		wg.Add(1)
		go func(actor supportSession) {
			defer wg.Done()
			results <- supportRequest(h, &actor, "POST", "/api/v1/operator/maintenance", "application/json", []byte(body), cfg.HTTP.CabinetOrigin, uuid.New()).Code
		}(actor)
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for code := range results {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent revision outcomes: %+v", counts)
	}
	r := request(other, "GET", "/api/v1/maintenance", "", "")
	var state wire.MaintenanceStatus
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &state) != nil || !state.Enabled || state.Revision != 1 {
		t.Fatalf("second instance state: %d %s", r.Code, r.Body.String())
	}
}

func TestMaintenanceAdmissionFailsClosedOnStoreError(t *testing.T) {
	h, env, cfg := httpFixture(t)
	client := supportLogin(t, h, env, cfg, "store-failure@example.test")
	if _, err := env.Pool.Exec(context.Background(), `DROP TABLE maintenance_state`); err != nil {
		t.Fatal(err)
	}
	if r := request(h, "GET", "/api/v1/maintenance", "", ""); r.Code != 503 {
		t.Fatalf("status error: %d", r.Code)
	}
	if r := supportRequest(h, &client, "POST", "/api/v1/trial-requests", "application/json", []byte(`{}`), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 503 {
		t.Fatalf("new trial admitted on store error: %d", r.Code)
	}
}
