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

func TestClientTelegramMigrationPreservesAndBlocksLoss(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,telegram_id,display_name,locale,vpn_id,sub_id,panel_key,terms_version,privacy_version,policy_accepted_at)
 VALUES($1,'telegram',701,'Owned fixture','ru',$2,'migrationclient1','acct_client_migration','1','1',now())`, id, uuid.New()); err != nil {
		t.Fatal(err)
	}
	var before string
	if err = e.Pool.QueryRow(ctx, `SELECT to_jsonb(a)::text FROM accounts a WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		var after string
		if err = e.Pool.QueryRow(ctx, `SELECT to_jsonb(a)::text FROM accounts a WHERE id=$1`, id).Scan(&after); err != nil || before != after {
			t.Fatal("client migration changed account identity/access facts", err)
		}
	}
	if _, err = provider.UpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	check()
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal("empty client outbox cannot downgrade", err)
	}
	check()
	if _, err = provider.UpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"pending", "sent", "failed", "skipped"} {
		t.Run(state, func(t *testing.T) {
			job := uuid.New()
			if _, err = e.Pool.Exec(ctx, `INSERT INTO client_telegram_deliveries(id,account_id,telegram_id,credential_version,locale,event_key,route,state,created_at)
 VALUES($1,$2,701,0,'ru',$3,'cabinet',$3,now())`, job, id, state); err != nil {
				t.Fatal(err)
			}
			if _, err = provider.Down(ctx); err == nil || !strings.Contains(err.Error(), "delivery facts exist") {
				t.Fatal("client delivery history loss allowed", err)
			}
			version, err := provider.GetDBVersion(ctx)
			if err != nil || version != 26 {
				t.Fatal("rejected downgrade not atomic", err)
			}
			var count int
			if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM client_telegram_deliveries WHERE id=$1 AND state=$2`, job, state).Scan(&count); err != nil || count != 1 {
				t.Fatal("rejected downgrade changed delivery fact", err)
			}
			check()
			if _, err = e.Pool.Exec(ctx, `DELETE FROM client_telegram_deliveries WHERE id=$1`, job); err != nil {
				t.Fatal(err)
			}
		})
	}
}
