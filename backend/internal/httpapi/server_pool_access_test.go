package httpapi

import (
	"context"
	"sync"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestServerPoolOperatorInitialSingleConnection(t *testing.T) {
	s, e, a, b := serverPoolFixture(t, 0)
	actor := renewalOperator(t, s, e)
	account := verified(t, s, e, "pool-single-connection@example.test")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cfg := e.Pool.Config().Copy()
	cfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	owner := app.NewModules(one, e.Redis, s.queue, s.cfg).Subscriptions
	days := 1
	op, err := owner.CreateAccessOperation(ctx, actor, account, uuid.New(), subscriptions.AccessOperationInput{Kind: "compensate", Days: &days, Reason: "owned single connection"})
	if err != nil || op.Status != "pending" || count(t, e, "vpn_server_reservations") != 1 || a.adds+b.adds != 0 {
		t.Fatal("first assignment exhausted owned connection", err)
	}
}

func TestServerPoolAssignedChangeBeforeWrite(t *testing.T) {
	s, e, a, b, actor, account := poolAccessActors(t)
	op, err := s.createAccessOperation(context.Background(), actor, account, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Days: ptrInt(1), Reason: "assignment race"})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	b.beforeRead = func() {
		once.Do(func() {
			if _, err := e.Pool.Exec(context.Background(), "UPDATE accounts SET assigned_panel_id=$2 WHERE id=$1", account, s.cfg.Subscriptions.PanelID); err != nil {
				t.Error(err)
			}
		})
	}
	before := b.updates
	if err = s.applyAccess(context.Background(), op.OperationId); err != nil {
		t.Fatal(err)
	}
	out, err := s.getAccessOperation(context.Background(), actor, account, op.OperationId)
	if err != nil || out.Status != "needs_review" || b.updates != before || a.adds+a.updates != 0 {
		t.Fatalf("assignment changed after preparation: status=%s updates=%d err=%v", out.Status, b.updates-before, err)
	}
}

func poolAccessActors(t *testing.T) (*regressionFixture, *testkit.Env, *fakePanel, *fakePanel, uuid.UUID, uuid.UUID) {
	t.Helper()
	s, e, a, b := serverPoolFixture(t, 0)
	actor := renewalOperator(t, s, e)
	account, trial := approved(t, s, e, "pool-access@example.test")
	if err := s.provision(context.Background(), trial); err != nil {
		t.Fatal(err)
	}
	return s, e, a, b, actor, account
}

func TestServerPoolOperatorAccess(t *testing.T) {
	s, e, a, b, actor, account := poolAccessActors(t)
	id, sub := b.client["id"], b.client["subId"]
	for _, in := range []wire.AccessOperationInput{
		{Kind: "compensate", Days: ptrInt(2), Reason: "pool compensation"},
		{Kind: "assign_plan", PlanId: ptr(poolPlan(t, e, 2)), Revision: ptr(int64(1)), PeriodDays: ptr(int64(30)), Reason: "pool plan"},
		{Kind: "set_profile", Profile: profileInput("euru"), Reason: "pool profile"},
		{Kind: "reset_traffic", Reason: "pool reset"},
		{Kind: "set_vpn_ban", VpnBanned: ptr(true), Reason: "pool ban"},
		{Kind: "set_vpn_ban", VpnBanned: ptr(false), Reason: "pool unban"},
		{Kind: "starter_trial", Reason: "pool starter"},
	} {
		op, err := s.createAccessOperation(context.Background(), actor, account, uuid.New(), in)
		if err != nil {
			t.Fatalf("%s: %v", in.Kind, err)
		}
		if err = s.applyAccess(context.Background(), op.OperationId); err != nil {
			t.Fatal(err)
		}
		out, err := s.getAccessOperation(context.Background(), actor, account, op.OperationId)
		if err != nil || out.Status != "applied" {
			t.Fatalf("%s outcome=%s err=%v", in.Kind, out.Status, err)
		}
	}
	card, err := s.operatorClient(context.Background(), actor, account)
	if err != nil || card.Server == nil || card.Server.PanelId != "second" {
		t.Fatal("card has wrong assigned panel", err)
	}
	if b.client["id"] != id || b.client["subId"] != sub || a.adds+a.updates+a.resets+a.disables+a.attaches+a.detaches != 0 || count(t, e, "vpn_server_reservations") != 0 {
		t.Fatal("operator access moved identity/panel or leaked reserve")
	}
	op, err := s.createAccessOperation(context.Background(), actor, account, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Days: ptrInt(1), Reason: "revoked actor"})
	if err != nil {
		t.Fatal(err)
	}
	before := b.updates
	if err = s.changeOperatorRole(context.Background(), actor, false); err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(context.Background(), op.OperationId); err != nil {
		t.Fatal(err)
	}
	if b.updates != before {
		t.Fatal("revoked operator wrote assigned panel")
	}
}

func TestServerPoolOperatorInitialAndMetadata(t *testing.T) {
	s, e, a, b := serverPoolFixture(t, 0)
	actor := renewalOperator(t, s, e)
	account := verified(t, s, e, "pool-initial@example.test")
	if _, err := s.createAccessOperation(context.Background(), actor, account, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("euru"), Reason: "metadata"}); err != nil {
		t.Fatal(err)
	}
	if count(t, e, "vpn_server_reservations") != 0 || a.adds+b.adds != 0 {
		t.Fatal("metadata held capacity")
	}
	if _, err := s.createAccessOperation(context.Background(), actor, account, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Days: ptrInt(-1), Reason: "invalid"}); status(err) != 400 || count(t, e, "vpn_server_reservations") != 0 {
		t.Fatal("invalid input reserved capacity")
	}
	op, err := s.createAccessOperation(context.Background(), actor, account, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Days: ptrInt(2), Reason: "first access"})
	if err != nil {
		t.Fatal(err)
	}
	if count(t, e, "vpn_server_reservations") != 1 {
		t.Fatal("first operator target not reserved")
	}
	if err = s.applyAccess(context.Background(), op.OperationId); err != nil {
		t.Fatal(err)
	}
	if a.adds != 0 || b.adds != 1 || count(t, e, "vpn_server_reservations") != 0 {
		t.Fatal("initial operator issuance used wrong panel")
	}
}

func TestServerPoolMonthlyReset(t *testing.T) {
	s, e, a, b, actor, account := poolAccessActors(t)
	s.cfg.VPN.AccessResetTimezone = "UTC"
	if _, _, err := s.seedUnlimitedCatalogue(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := s.createAccessOperation(context.Background(), actor, account, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("unlimited"), Reason: "pool unlimited"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(context.Background(), op.OperationId); err != nil {
		t.Fatal(err)
	}
	before := b.resets
	if n, err := s.enqueueMonthlyResets(context.Background(), e.Clock()); err != nil || n != 1 {
		t.Fatalf("monthly claims=%d err=%v", n, err)
	}
	if err = s.applyMonthlyReset(context.Background(), account, "2026-10"); err != nil {
		t.Fatal(err)
	}
	var reset uuid.UUID
	if err = e.Pool.QueryRow(context.Background(), "SELECT operation_id FROM monthly_reset_periods WHERE account_id=$1", account).Scan(&reset); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = s.applyAccess(context.Background(), reset); err != nil {
			t.Fatal(err)
		}
	}
	if b.resets != before+1 || a.resets != 0 {
		t.Fatal("monthly reset repeated or used primary")
	}
}

func TestServerPoolAssignedOutage(t *testing.T) {
	s, e, a, b, actor, account := poolAccessActors(t)
	b.offline = true
	view, err := s.subscription(context.Background(), account)
	if err != nil || view.PanelError == nil || !view.DataStale {
		t.Fatal("assigned outage not reported", err)
	}
	if _, err = s.subscriptionKey(context.Background(), account); status(err) != 409 {
		t.Fatal("outage returned another panel's key")
	}
	if _, err = s.createAccessOperation(context.Background(), actor, account, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Days: ptrInt(1), Reason: "outage"}); status(err) != 409 {
		t.Fatal("assigned outage queued elsewhere")
	}
	if _, err = e.Pool.Exec(context.Background(), "UPDATE accounts SET assigned_panel_id='unknown' WHERE id=$1", account); err != nil {
		t.Fatal(err)
	}
	if _, err = s.subscriptionKey(context.Background(), account); status(err) != 409 {
		t.Fatal("unknown assignment remapped")
	}
	if a.adds+a.updates+a.resets != 0 || b.adds != 1 {
		t.Fatal("outage moved assigned client")
	}
}
