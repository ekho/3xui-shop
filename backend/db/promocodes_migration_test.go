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

func TestPromocodesMigrationLegacyFactsAndGuards(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	id := uuid.New()
	// Import must preserve source IDs beyond JS precision and unknown activation owner/time.
	if _, err := e.Pool.Exec(ctx, `INSERT INTO promocodes(id,code,duration_days,is_activated,activated_by_tg_id,legacy_source,legacy_promocode_id)
 VALUES($1,'legacy-Mixed_code48',730,true,9007199254740993,'owned-sqlite',9223372036854775807)`, id); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"DELETE FROM promocodes WHERE id=$1",
		"UPDATE promocodes SET duration_days=30,revision=revision+1 WHERE id=$1",
		"UPDATE promocodes SET is_activated=false,activated_by_tg_id=NULL,revision=revision+1 WHERE id=$1",
		"UPDATE promocodes SET deleted_at=now(),revision=revision+1 WHERE id=$1",
		"UPDATE promocodes SET legacy_promocode_id=1,revision=revision+1 WHERE id=$1",
		"UPDATE promocodes SET code='replacement',revision=revision+1 WHERE id=$1",
	} {
		if _, err := e.Pool.Exec(ctx, query, id); err == nil {
			t.Fatal("used/legacy metadata guard bypassed")
		}
	}
	var legacy, tg, code string
	var days int
	var used bool
	var created, activated any
	if err := e.Pool.QueryRow(ctx, "SELECT legacy_promocode_id::text,activated_by_tg_id::text,code,duration_days,is_activated,created_at,activated_at FROM promocodes WHERE id=$1", id).Scan(&legacy, &tg, &code, &days, &used, &created, &activated); err != nil || legacy != "9223372036854775807" || tg != "9007199254740993" || code != "legacy-Mixed_code48" || days != 730 || !used || created != nil || activated != nil {
		t.Fatal("legacy facts lost", err)
	}
	event := uuid.New()
	if _, err := e.Pool.Exec(ctx, "INSERT INTO promocode_events(id,promocode_id,action,created_at,after_snapshot) VALUES($1,$2,'legacy_import',now(),'{}')", event, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, "DELETE FROM promocode_events WHERE id=$1", event); err == nil {
		t.Fatal("history erased")
	}
	if _, err := e.Pool.Exec(ctx, "UPDATE promocode_events SET after_snapshot='{}' WHERE id=$1", event); err == nil {
		t.Fatal("history rewritten")
	}
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.DownTo(ctx, 39); err == nil || !strings.Contains(err.Error(), "promocode downgrade blocked") {
		t.Fatal("retained promo data downgraded", err)
	}
}

func TestPromocodesMigrationDeletedActivationBoundary(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	id := uuid.New()
	if _, err := e.Pool.Exec(ctx, "INSERT INTO promocodes(id,code,duration_days,created_at) VALUES($1,'deleted-boundary',30,now())", id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, "UPDATE promocodes SET deleted_at=now(),revision=revision+1 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE promocodes SET is_activated=true,revision=revision+1 WHERE id=$1",
		"UPDATE promocodes SET deleted_at=NULL,revision=revision+1 WHERE id=$1",
		"UPDATE promocodes SET duration_days=7,revision=revision+1 WHERE id=$1",
	} {
		if _, err := e.Pool.Exec(ctx, query, id); err == nil {
			t.Fatal("deleted code activated/revived")
		}
	}
	var revision int64
	if err := e.Pool.QueryRow(ctx, "SELECT revision FROM promocodes WHERE id=$1 AND NOT is_activated AND deleted_at IS NOT NULL", id).Scan(&revision); err != nil || revision != 2 {
		t.Fatal("terminal tombstone changed", err)
	}
}

func TestPromocodesMigrationEmptyRollback(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.DownTo(ctx, 39); err != nil {
		t.Fatal("empty rollback", err)
	}
	if _, err = p.Up(ctx); err != nil {
		t.Fatal("empty reapply", err)
	}
}
