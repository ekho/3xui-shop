package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func TestGroupRecoveryHTTPRequiresCurrentInfrastructureGrant(t *testing.T) {
	s, e, p, account, _ := renewalFixture(t)
	ctx := context.Background()
	p.inboundRows = []map[string]any{{"id": 1, "enable": true, "tag": "regular"}, {"id": 2, "enable": true, "tag": "regular"}, {"id": 4, "enable": true, "tag": "regular"}}
	op, err := s.vpn.PrepareGroupReconciliation(ctx, account)
	if err != nil || op == uuid.Nil {
		t.Fatal("owned group operation missing", err)
	}
	p.failRead = true
	for i := 0; i < 5; i++ {
		err = s.applyAccess(ctx, op)
		if i < 4 && status(err) != 503 || i == 4 && err != nil {
			t.Fatal("bounded read-only worker retry did not retain the same operation", err)
		}
	}
	p.failRead = false
	var target string
	if e.Pool.QueryRow(ctx, `SELECT target::text FROM access_operations WHERE id=$1 AND status='needs_review'`, op).Scan(&target) != nil {
		t.Fatal("owned group operation did not enter review")
	}
	cfg := *s.cfg
	h := New(app.NewModules(e.Pool, e.Redis, s.queue, s.cfg), e.Pool, cfg.HTTP)
	verifiedHTTP(t, h, e, cfg)
	login := request(h, "POST", "/api/v1/auth/login", `{"email":"login@example.test","password":"my long safe password ✨"}`, cfg.HTTP.CabinetOrigin)
	var session wire.LoginResult
	if login.Code != 200 || json.Unmarshal(login.Body.Bytes(), &session) != nil {
		t.Fatal("owned HTTP operator session missing")
	}
	cookie, actor := login.Result().Cookies()[0], session.Account.AccountId
	if s.accounts.ChangeOperatorRole(ctx, actor, true) != nil {
		t.Fatal("owned role unavailable")
	}
	path := "/api/v1/operator/clients/" + account.String() + "/access-operations/" + op.String() + "/reconcile"
	body := `{"reason":"confirmed provider recovery","acknowledge_reset_cost":false}`
	key := uuid.NewString()
	jobs := count(t, e, "river_job")
	if out := managedRequest(h, cookie, cfg, session.CsrfToken, key, "POST", path, body); out.Code != 403 || count(t, e, "river_job") != jobs {
		t.Fatal("ordinary operator requeued infrastructure work", out.Code)
	}
	if s.accounts.ChangeInfrastructureRole(ctx, actor, true) != nil {
		t.Fatal("owned infrastructure role unavailable")
	}
	if out := managedRequest(h, cookie, cfg, session.CsrfToken, key, "POST", path, body); out.Code != 202 {
		t.Fatal("infrastructure recovery rejected", out.Code, out.Body.String())
	}
	if s.accounts.ChangeInfrastructureRole(ctx, actor, false) != nil {
		t.Fatal("owned revoke unavailable")
	}
	if err = s.applyAccess(ctx, op); err != nil {
		t.Fatal(err)
	}
	if e.Pool.QueryRow(ctx, `SELECT target::text FROM access_operations WHERE id=$1 AND status='needs_review'`, op).Scan(&target) != nil || p.attaches != 0 || p.detaches != 0 {
		t.Fatal("revoked infrastructure actor reached panel write")
	}
	if s.accounts.ChangeInfrastructureRole(ctx, actor, true) != nil {
		t.Fatal("owned grant unavailable")
	}
	if out := managedRequest(h, cookie, cfg, session.CsrfToken, uuid.NewString(), "POST", path, body); out.Code != 202 {
		t.Fatal("fresh infrastructure recovery rejected", out.Code)
	}
	if err = s.applyAccess(ctx, op); err != nil {
		t.Fatal(err)
	}
	var after string
	if e.Pool.QueryRow(ctx, `SELECT target::text FROM access_operations WHERE id=$1 AND status='applied'`, op).Scan(&after) != nil || target != after || p.attaches != 1 || p.resets != 1 {
		t.Fatal("manual recovery rewrote target, duplicated reset or lost membership")
	}
	createPath := "/api/v1/operator/clients/" + account.String() + "/access-operations"
	if out := managedRequest(h, cookie, cfg, session.CsrfToken, uuid.NewString(), "POST", createPath, `{"kind":"group_reconcile","reason":"unsupported user action"}`); out.Code != 400 {
		t.Fatal("system-only operation became an API creation option", out.Code)
	}
}
