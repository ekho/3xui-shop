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

func TestAccountRestrictionDowngradePreservesHistory(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	id := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,display_name,telegram_id,legacy_user_id,locale,vpn_id,sub_id,panel_key)
	 VALUES($1,'telegram','Fixture',701,51,'ru',$2,'aaaaaaaaaaaaaaaa',$3)`, id, uuid.New(), "acct_"+id.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO legacy_approval_snapshots(account_id,source_legacy_user_id,source_tg_id,status) VALUES($1,51,701,'rejected')`, id); err != nil {
		t.Fatal(err)
	}
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err == nil || !strings.Contains(err.Error(), "account restriction downgrade blocked") {
		t.Fatal("history downgrade permitted", err)
	}
	var count int
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM legacy_approval_snapshots WHERE account_id=$1`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("history lost", err)
	}
}
