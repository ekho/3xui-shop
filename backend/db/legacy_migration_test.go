package db_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestLegacyMigrationRetainedHistoryAndNativeKeyGuards(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	id := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,telegram_id,legacy_user_id,display_name,locale,vpn_id,sub_id,panel_key) VALUES($1,'telegram',101,1,'Synthetic','en',$2,$3,'101')`, id, uuid.New(), uuid.NewString()); err != nil {
		t.Fatal("legacy UUID subscription key denied")
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,telegram_id,display_name,locale,vpn_id,sub_id,panel_key) VALUES($1,'telegram',102,'Synthetic','en',$2,$3,'102')`, uuid.New(), uuid.New(), uuid.NewString()); err == nil {
		t.Fatal("native subscription key constraint weakened")
	}
	for _, statement := range []string{
		`INSERT INTO legacy_account_imports(source_id,account_id,source_tg_id,source_snapshot) VALUES(1,$1,101,'{}')`,
		`INSERT INTO legacy_stars_imports(source_legacy_user_id,account_id,source_tg_id,source_snapshot) VALUES(1,$1,101,'{}')`,
		`INSERT INTO legacy_bonus_imports(kind,source_id,entity_id,source_snapshot) VALUES('reward',1,$1,'{}')`,
		`INSERT INTO legacy_migration_runs(source,source_digest,catalogue_source,report,operator_account_id,created_at) VALUES('synthetic',repeat('0',64),'{}','{}',$1,now())`,
	} {
		if _, err := e.Pool.Exec(ctx, statement, id); err != nil {
			t.Fatal("retained history prerequisite unavailable")
		}
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO vpn_servers(id,name,host,max_clients,online) VALUES('1','Synthetic','https://example.invalid',1,false)`); err != nil {
		t.Fatal("server prerequisite unavailable")
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO legacy_server_imports(source_id,server_id,source_snapshot) VALUES(1,'1','{}')`); err != nil {
		t.Fatal("server provenance prerequisite unavailable")
	}
	for _, statement := range []string{
		`UPDATE legacy_account_imports SET source_snapshot='{}'`, `DELETE FROM legacy_account_imports`,
		`UPDATE legacy_server_imports SET source_snapshot='{}'`, `DELETE FROM legacy_server_imports`,
		`UPDATE legacy_bonus_imports SET source_snapshot='{}'`, `DELETE FROM legacy_bonus_imports`,
		`UPDATE legacy_stars_imports SET is_stars_auto_renew=false`, `DELETE FROM legacy_stars_imports`,
		`UPDATE legacy_migration_runs SET report='{}'`, `DELETE FROM legacy_migration_runs`,
	} {
		if _, err := e.Pool.Exec(ctx, statement); err == nil {
			t.Fatal("source history mutation accepted")
		}
	}
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal("migration provider unavailable")
	}
	if _, err = provider.DownTo(ctx, 42); err == nil || !strings.Contains(err.Error(), "legacy migration downgrade blocked") {
		t.Fatal("retained source history downgraded")
	}
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 43 {
		t.Fatal("failed downgrade changed schema")
	}
}

func TestLegacyMigrationEmptyRollback(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal("migration provider unavailable")
	}
	if _, err = provider.DownTo(ctx, 42); err != nil {
		t.Fatal("empty migration rollback failed")
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal("empty migration reapply failed")
	}
}
