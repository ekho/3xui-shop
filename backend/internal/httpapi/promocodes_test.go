package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

type promoHTTPRow struct {
	ID       uuid.UUID `json:"promocode_id"`
	Code     string    `json:"code"`
	Duration int       `json:"duration_days"`
	Revision int64     `json:"revision"`
	State    string    `json:"state"`
}

func TestPromocodesHTTPInputRightsAndAudit(t *testing.T) {
	h, e, cfg := httpFixture(t)
	actor := campaignOperator(t, h, e, cfg)
	client := supportLogin(t, h, e, cfg, "promo-client@example.test")
	noCSRF := actor
	noCSRF.csrf = ""
	valid := []byte(`{"duration_days":30,"reason":"owned promo boundary"}`)
	for _, tc := range []struct {
		session *supportSession
		origin  string
		body    []byte
		status  int
	}{
		{nil, cfg.HTTP.CabinetOrigin, valid, 401}, {&client, cfg.HTTP.CabinetOrigin, valid, 403}, {&noCSRF, cfg.HTTP.CabinetOrigin, valid, 403}, {&actor, "https://foreign.example.test", valid, 403},
		{&actor, cfg.HTTP.CabinetOrigin, []byte(`{"duration_days":366,"reason":"invalid days"}`), 400},
		{&actor, cfg.HTTP.CabinetOrigin, []byte(`{"duration_days":0,"reason":"invalid days"}`), 400},
		{&actor, cfg.HTTP.CabinetOrigin, []byte(`{"duration_days":30,"reason":"","is_activated":false}`), 400},
		{&actor, cfg.HTTP.CabinetOrigin, []byte(`{"duration_days":30,"reason":"   "}`), 400},
		{&actor, cfg.HTTP.CabinetOrigin, []byte(`{"duration_days":30,"reason":"owned","activated_account_id":"` + client.id.String() + `"}`), 400},
	} {
		r := supportRequest(h, tc.session, "POST", "/api/v1/operator/promocodes", "application/json", tc.body, tc.origin, uuid.New())
		if r.Code != tc.status {
			t.Fatal("input/right boundary", r.Code, tc.status)
		}
	}
	key := uuid.New()
	var first promoHTTPRow
	for i := 0; i < 2; i++ {
		r := supportRequest(h, &actor, "POST", "/api/v1/operator/promocodes", "application/json", valid, cfg.HTTP.CabinetOrigin, key)
		var row promoHTTPRow
		if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &row) != nil {
			t.Fatal("create retry", r.Code)
		}
		if i == 0 {
			first = row
		} else if row != first {
			t.Fatal("create replay changed identity")
		}
	}
	if r := supportRequest(h, &actor, "POST", "/api/v1/operator/promocodes", "application/json", []byte(`{"duration_days":7,"reason":"owned promo boundary"}`), cfg.HTTP.CabinetOrigin, key); r.Code != 409 {
		t.Fatal("key accepted another body", r.Code)
	}
	r := supportRequest(h, &actor, "POST", "/api/v1/operator/promocodes/search", "application/json", []byte(`{"page":1,"per_page":50}`), cfg.HTTP.CabinetOrigin, uuid.Nil)
	var list struct {
		Rows  []promoHTTPRow `json:"promocodes"`
		Total int            `json:"total"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &list) != nil || list.Total != 1 || len(list.Rows) != 1 {
		t.Fatal("list", r.Code)
	}
	r = supportRequest(h, &actor, "GET", "/api/v1/operator/promocodes/"+first.ID.String(), "application/json", nil, "", uuid.Nil)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"before":null`) {
		t.Fatal("detail/history", r.Code)
	}
	ctx := context.Background()
	var count int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events a JOIN promocode_events p ON p.id=a.id WHERE p.promocode_id=$1 AND a.account_id=$2 AND a.operator_account_id=$2 AND a.reason NOT LIKE '%'||$3||'%' AND p.after_snapshot::text NOT LIKE '%'||$3||'%'`, first.ID, actor.id, first.Code).Scan(&count); err != nil || count != 1 {
		t.Fatal("atomic metadata audit leaked code or lost actor", count, err)
	}
	if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", actor.id); err != nil {
		t.Fatal(err)
	}
	if r := supportRequest(h, &actor, "GET", "/api/v1/operator/promocodes/"+first.ID.String(), "application/json", nil, "", uuid.Nil); r.Code != 403 {
		t.Fatal("restricted actor read", r.Code)
	}
}

// The future activation path uses this same row lock; the grant itself belongs to #49.
func TestPromocodesConcurrentActivationWinsManagement(t *testing.T) {
	for _, tc := range []struct{ action, activator string }{{"edit", "client"}, {"delete", "client"}, {"edit", "operator"}, {"delete", "operator"}} {
		t.Run(tc.action+"/"+tc.activator, func(t *testing.T) {
			h, e, cfg := httpFixture(t)
			actor := campaignOperator(t, h, e, cfg)
			row := promoCreate(t, h, e, cfg, actor)
			activator := actor
			if tc.activator == "client" {
				activator = supportLogin(t, h, e, cfg, "promo-activator@example.test")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			// Activation locks the account before the code, including self-activation.
			if _, err = tx.Exec(ctx, "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", activator.id); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, "SELECT id FROM promocodes WHERE id=$1 FOR UPDATE", row.ID); err != nil {
				t.Fatal(err)
			}
			body := `{"expected_revision":1,"reason":"owned concurrent action"}`
			if tc.action == "edit" {
				body = `{"duration_days":7,"expected_revision":1,"reason":"owned concurrent action"}`
			}
			done := make(chan int, 1)
			go func() {
				done <- supportRequest(h, &actor, "POST", "/api/v1/operator/promocodes/"+row.ID.String()+"/"+tc.action, "application/json", []byte(body), cfg.HTTP.CabinetOrigin, uuid.New()).Code
			}()
			campaignWaitBlocked(t, e, ctx)
			if _, err = tx.Exec(ctx, "UPDATE promocodes SET is_activated=true,activated_account_id=$2,activated_at=now(),revision=revision+1 WHERE id=$1", row.ID, activator.id); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if status := <-done; status != 409 {
				t.Fatal("activation lost protected boundary", status)
			}
			var duration, events int
			var linked uuid.UUID
			var deleted *time.Time
			if err = e.Pool.QueryRow(ctx, "SELECT duration_days,activated_account_id,deleted_at FROM promocodes WHERE id=$1", row.ID).Scan(&duration, &linked, &deleted); err != nil || duration != 30 || linked != activator.id || deleted != nil {
				t.Fatal("used record/link changed", err)
			}
			if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM promocode_events WHERE promocode_id=$1", row.ID).Scan(&events); err != nil || events != 1 {
				t.Fatal("rejected action wrote history", events, err)
			}
		})
	}
}

func TestPromocodesEditReplayCannotUndoActivation(t *testing.T) {
	h, e, cfg := httpFixture(t)
	actor := campaignOperator(t, h, e, cfg)
	row := promoCreate(t, h, e, cfg, actor)
	ctx := context.Background()
	key := uuid.New()
	path := "/api/v1/operator/promocodes/" + row.ID.String() + "/edit"
	body := []byte(`{"duration_days":7,"expected_revision":1,"reason":"owned preactivation edit"}`)
	if r := supportRequest(h, &actor, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, key); r.Code != 200 {
		t.Fatal("edit", r.Code)
	}
	if _, err := e.Pool.Exec(ctx, "UPDATE promocodes SET is_activated=true,activated_account_id=$2,revision=revision+1 WHERE id=$1", row.ID, actor.id); err != nil {
		t.Fatal(err)
	}
	if r := supportRequest(h, &actor, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, key); r.Code != 200 {
		t.Fatal("stored replay", r.Code)
	}
	if r := supportRequest(h, &actor, "POST", path, "application/json", []byte(`{"duration_days":30,"expected_revision":3,"reason":"try used edit"}`), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 409 {
		t.Fatal("used edit accepted", r.Code)
	}
	var duration, events int
	var activated bool
	var revision int64
	if err := e.Pool.QueryRow(ctx, "SELECT duration_days,is_activated,revision FROM promocodes WHERE id=$1", row.ID).Scan(&duration, &activated, &revision); err != nil || duration != 7 || !activated || revision != 3 {
		t.Fatal("replay mutated used data", err)
	}
	if err := e.Pool.QueryRow(ctx, "SELECT count(*) FROM promocode_events WHERE promocode_id=$1", row.ID).Scan(&events); err != nil || events != 2 {
		t.Fatal("replay duplicated history", events, err)
	}
}

func TestPromocodesConcurrentRoleRevocation(t *testing.T) {
	h, e, cfg := httpFixture(t)
	actor := campaignOperator(t, h, e, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT account_id FROM operator_accounts WHERE account_id=$1 FOR UPDATE", actor.id); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		done <- supportRequest(h, &actor, "POST", "/api/v1/operator/promocodes", "application/json", []byte(`{"duration_days":7,"reason":"owned role race"}`), cfg.HTTP.CabinetOrigin, uuid.New()).Code
	}()
	campaignWaitBlocked(t, e, ctx)
	if _, err = tx.Exec(ctx, "DELETE FROM operator_accounts WHERE account_id=$1", actor.id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if status := <-done; status != 403 {
		t.Fatal("revocation lost race", status)
	}
	var count int
	if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM promocodes").Scan(&count); err != nil || count != 0 {
		t.Fatal("unauthorized effect", count, err)
	}
}

func promoCreate(t *testing.T, h http.Handler, e *testkit.Env, cfg app.Config, actor supportSession) promoHTTPRow {
	t.Helper()
	r := supportRequest(h, &actor, "POST", "/api/v1/operator/promocodes", "application/json", []byte(`{"duration_days":30,"reason":"owned synthetic promo"}`), cfg.HTTP.CabinetOrigin, uuid.New())
	var row promoHTTPRow
	if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &row) != nil || row.ID == uuid.Nil || row.Code == "" || row.State != "available" {
		t.Fatalf("create expected201 available code, got%d", r.Code)
	}
	return row
}

// Losing a role guard, revision check or replay persistence breaks real HTTP/DB behavior.
func TestPromocodesHTTPManagement(t *testing.T) {
	h, e, cfg := httpFixture(t)
	ctx := context.Background()
	actor := campaignOperator(t, h, e, cfg)
	row := promoCreate(t, h, e, cfg, actor)
	path := "/api/v1/operator/promocodes/" + row.ID.String()
	key := uuid.New()
	body := []byte(`{"duration_days":7,"expected_revision":1,"reason":"owned edit"}`)
	for i := 0; i < 2; i++ {
		r := supportRequest(h, &actor, "POST", path+"/edit", "application/json", body, cfg.HTTP.CabinetOrigin, key)
		var edited promoHTTPRow
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &edited) != nil || edited.Duration != 7 || edited.Revision != 2 || edited.Code != row.Code {
			t.Fatal("edit/replay", r.Code)
		}
	}
	var n int
	if err := e.Pool.QueryRow(ctx, "SELECT count(*) FROM promocode_events WHERE promocode_id=$1", row.ID).Scan(&n); err != nil || n != 2 {
		t.Fatal("duplicated/lost history", n, err)
	}
	// A new process uses the same persistent replay record.
	h = New(app.NewModules(e.Pool, e.Redis, nil, &cfg), e.Pool, cfg.HTTP)
	if r := supportRequest(h, &actor, "POST", path+"/edit", "application/json", body, cfg.HTTP.CabinetOrigin, key); r.Code != 200 {
		t.Fatal("restart replay", r.Code)
	}
	if r := supportRequest(h, &actor, "POST", path+"/edit", "application/json", body, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 409 {
		t.Fatal("stale revision", r.Code)
	}
	body = []byte(`{"expected_revision":2,"reason":"owned deletion"}`)
	if r := supportRequest(h, &actor, "POST", path+"/delete", "application/json", body, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 200 {
		t.Fatal("delete", r.Code)
	}
	if err := e.Pool.QueryRow(ctx, "SELECT count(*) FROM promocodes WHERE id=$1 AND deleted_at IS NOT NULL AND code=$2", row.ID, row.Code).Scan(&n); err != nil || n != 1 {
		t.Fatal("tombstone/identity lost", n, err)
	}
	if r := supportRequest(h, &actor, "POST", path+"/edit", "application/json", []byte(`{"duration_days":3,"expected_revision":3,"reason":"try revive"}`), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 409 {
		t.Fatal("deleted code revived", r.Code)
	}
	if _, err := e.Pool.Exec(ctx, "DELETE FROM operator_accounts WHERE account_id=$1", actor.id); err != nil {
		t.Fatal(err)
	}
	if r := supportRequest(h, &actor, "POST", path+"/edit", "application/json", []byte(`{"duration_days":7,"expected_revision":1,"reason":"owned edit"}`), cfg.HTTP.CabinetOrigin, key); r.Code != 403 {
		t.Fatal("revoked replay accepted", r.Code)
	}
}
