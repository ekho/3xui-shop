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

func TestAccessOperationHistoryBlocksDowngrade(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	id := uuid.New()
	account := uuid.New()
	if _, err := e.Pool.Exec(ctx, "INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) VALUES($1,'access-db@example.test','ru','fixture',now(),$2,'aaaaaaaaaaaaaaaa',$3,'1','1')", account, uuid.New(), "acct_"+account.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, "INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at) VALUES($1,$2,'reset_traffic','applied','fixture','{}','{}',now(),now())", id, account); err != nil {
		t.Fatal(err)
	}
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err == nil || !strings.Contains(err.Error(), "access operation downgrade blocked") {
		t.Fatalf("history downgrade: %v", err)
	}
	var retained int
	if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM access_operations WHERE id=$1", id).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("history lost: %v", err)
	}
}
