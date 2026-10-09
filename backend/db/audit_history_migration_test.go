package db_test

import (
	"context"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"os"
	"strings"
	"testing"
)

func TestAuditHistorySchema(t *testing.T) {
	e := testkit.Open(t)
	var tables int
	if e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('legacy_audit_events','legacy_audit_imports','audit_system_events')`).Scan(&tables) != nil || tables != 3 {
		t.Fatal("audit history/import/system schema missing", tables)
	}
}

func TestAuditHistoryDowngradePreservesFacts(t *testing.T) {
	for _, mode := range []string{"empty", "ledger", "legacy-private", "system-receipt"} {
		t.Run(mode, func(t *testing.T) {
			e := testkit.Open(t)
			ctx := context.Background()
			database := stdlib.OpenDBFromPool(e.Pool)
			defer database.Close()
			provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "ledger" || mode == "legacy-private" {
				if _, err = e.Pool.Exec(ctx, `INSERT INTO legacy_audit_imports(source_id,source_hash) VALUES(7,$1)`, make([]byte, 32)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "legacy-private" {
				if _, err = e.Pool.Exec(ctx, `INSERT INTO legacy_audit_events(source_id,created_at,action,payload_json) VALUES(7,now(),'support.message',$1)`, []byte(`{"body":"retained-private"}`)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "system-receipt" {
				if _, err = e.Pool.Exec(ctx, `INSERT INTO audit_system_events(id,created_at,action,period_day,cutoff,retention_days,native_count,legacy_count,system_count) VALUES($1,now(),'audit.pruned',current_date,now()-interval '1 day',1,3,4,5)`, uuid.New()); err != nil {
					t.Fatal(err)
				}
			}
			var before string
			// Migration 34 may remove its empty additive column before migration 33
			// blocks. Compare all original audit facts across that schema boundary.
			snapshot := `SELECT jsonb_build_array((SELECT jsonb_agg(x ORDER BY source_id) FROM legacy_audit_imports x),(SELECT jsonb_agg(x ORDER BY source_id) FROM legacy_audit_events x),(SELECT jsonb_agg(to_jsonb(x)-'support_telegram' ORDER BY id) FROM audit_system_events x))::text`
			if err = e.Pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
				t.Fatal(err)
			}
			_, err = provider.DownTo(ctx, 32)
			if mode == "empty" {
				if err != nil {
					t.Fatal("empty audit downgrade", err)
				}
				if _, err = provider.Up(ctx); err != nil {
					t.Fatal("empty audit remigrate", err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "Audit history downgrade blocked") {
					t.Fatal("audit facts erased by downgrade", err)
				}
				var after string
				if err = e.Pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || after != before {
					t.Fatal("blocked downgrade changed private source/ledger/receipt", err)
				}
			}
		})
	}
}
