package httpapi

import (
	"context"
	"testing"

	"example.com/cabinet/backend/internal/modules/audit_reports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestServerPoolStatisticsAndReminders(t *testing.T) {
	s, e, a, b := serverPoolFixture(t, 10)
	ctx := context.Background()
	actor := renewalOperator(t, s, e)
	first, firstOp := approved(t, s, e, "pool-reports-first@example.test")
	if err := s.provision(ctx, firstOp); err != nil {
		t.Fatal(err)
	}
	second, secondOp := approved(t, s, e, "pool-reports-second@example.test")
	if err := s.provision(ctx, secondOp); err != nil {
		t.Fatal(err)
	}
	if a.adds != 1 || b.adds != 1 {
		t.Fatal("report fixture must issue one client on each server")
	}
	snapshot := func() string {
		t.Helper()
		var digest string
		if err := e.Pool.QueryRow(ctx, `SELECT md5(jsonb_build_array(
		 (SELECT jsonb_agg(x ORDER BY id) FROM vpn_servers x),
		 (SELECT jsonb_agg(x ORDER BY account_id) FROM vpn_server_reservations x))::text)`).Scan(&digest); err != nil {
			t.Fatal(err)
		}
		return reminderBusinessSnapshot(t, e) + digest
	}
	before := snapshot()
	read := func() auditreports.StatisticsReport {
		t.Helper()
		out, err := s.auditReports.Statistics(ctx, actor, nil)
		if err != nil || len(out.Servers) != 2 {
			t.Fatalf("pool report must include both servers: servers=%d err=%v", len(out.Servers), err)
		}
		return out
	}
	out := read()
	if out.Activity.ActiveUsers == nil || *out.Activity.ActiveUsers != 2 || out.Activity.UnknownUsers != 0 || out.Activity.KnownInactiveUsers != 1 {
		t.Fatal("healthy snapshots crossed assignments or lost activity")
	}
	for _, server := range out.Servers {
		if server.Availability != "available" || server.Clients == nil || *server.Clients != 1 || server.Inbounds == nil || *server.Inbounds != 5 {
			t.Fatal("server counts not partitioned")
		}
	}
	a.mu.Lock()
	a.offline = true
	a.mu.Unlock()
	out = read()
	if out.Activity.ActiveUsers != nil || out.Activity.UnknownUsers != 1 || out.Activity.KnownActiveUsers != 1 {
		t.Fatal("one outage erased healthy facts or fabricated total")
	}
	for _, server := range out.Servers {
		if server.PanelID == s.cfg.Subscriptions.PanelID && (server.Availability != "unavailable" || server.Clients != nil) || server.PanelID == "second" && (server.Availability != "available" || server.Clients == nil || *server.Clients != 1) {
			t.Fatal("outage/count attributed to wrong panel")
		}
	}
	for _, group := range out.Groups {
		if group.InboundReferences != nil {
			t.Fatal("partial infrastructure became an exact global count")
		}
	}
	if err := s.reminders.Generate(ctx); err != nil {
		t.Fatal("one outage stopped healthy reminders", err)
	}
	var firstNotices, secondNotices int
	if err := e.Pool.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM reminders WHERE account_id=$1),
	 (SELECT count(*) FROM reminders WHERE account_id=$2)`, first, second).Scan(&firstNotices, &secondNotices); err != nil || firstNotices != 0 || secondNotices != 1 {
		t.Fatalf("reminder facts not partitioned: first=%d second=%d err=%v", firstNotices, secondNotices, err)
	}
	b.mu.Lock()
	b.client["id"] = uuid.NewString()
	b.mu.Unlock()
	out = read()
	if out.Activity.KnownActiveUsers != 0 || out.Activity.UnknownUsers != 2 {
		t.Fatal("foreign panel identity confirmed another account")
	}
	b.mu.Lock()
	b.bulkUnavailable = true
	b.mu.Unlock()
	if out = read(); out.Servers[1].Clients != nil || out.Activity.UnknownUsers != 2 {
		t.Fatal("unsupported bulk API fabricated a count")
	}
	if before != snapshot() || a.adds != 1 || b.adds != 1 || a.updates+a.resets+b.updates+b.resets != 0 {
		t.Fatal("readonly report/reminder changed registry, grants, keys, money or panel")
	}
	// A missing assigned identity cannot borrow even the healthy primary's facts.
	if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET assigned_panel_id='unknown' WHERE id=$1", second); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	period, err := s.vpn.ReminderPeriodTx(ctx, tx, second)
	if err != nil || period.Known {
		t.Fatal("unknown assignment has a reminder period", err)
	}
}
