package auditreports

import (
	"context"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/testkit"
)

func TestLegacyAuditTxRollbackAndStandaloneDryRun(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	s := New(e.Pool, StatisticsPorts{}, HistoryPorts{}, Config{})
	p := LegacyAuditPackage{Version: 1, Events: []LegacyAuditInput{{SourceID: 42, CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Action: "fixture.source"}}}
	if out, err := s.ImportLegacy(ctx, p, true); err != nil || out.Inserted != 1 {
		t.Fatal("standalone dry run", out, err)
	}
	var count int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM legacy_audit_imports`).Scan(&count); err != nil || count != 0 {
		t.Fatal("standalone dry run wrote digest", err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if out, err := s.ImportLegacyTx(ctx, tx, p, false); err != nil || out.Inserted != 1 {
		t.Fatal("shared transaction import", out, err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM legacy_audit_imports WHERE source_id=42`).Scan(&count); err != nil || count != 1 {
		t.Fatal("digest missing inside transaction", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM legacy_audit_imports WHERE source_id=42`).Scan(&count); err != nil || count != 0 {
		t.Fatal("digest escaped rollback", err)
	}
}
