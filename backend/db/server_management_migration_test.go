package db_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestServerManagementMigrationEmptyRollbackAndHistoryGuard(t *testing.T) {
	for _, kind := range []string{"role", "action"} {
		t.Run(kind, func(t *testing.T) {
			e := testkit.Open(t)
			ctx := context.Background()
			database := stdlib.OpenDBFromPool(e.Pool)
			defer database.Close()
			provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = provider.DownTo(ctx, 36); err != nil {
				t.Fatalf("empty rollback: %v", err)
			}
			if _, err = provider.Up(ctx); err != nil {
				t.Fatalf("reapply: %v", err)
			}
			actor := uuid.New()
			_, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
			 VALUES($1,'management-migration@example.test','ru','fixture',now(),$2,'cccccccccccccccc',$3,'1','1')`, actor, uuid.New(), "acct_"+actor.String())
			if err != nil {
				t.Fatal(err)
			}
			if kind == "role" {
				_, err = e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,now())`, actor)
				if err != nil {
					t.Fatal(err)
				}
				_, err = e.Pool.Exec(ctx, `INSERT INTO infrastructure_operators(account_id,granted_at) VALUES($1,now())`, actor)
			} else {
				_, err = e.Pool.Exec(ctx, `INSERT INTO vpn_server_actions(actor_id,idempotency_key,action,input_hash,result,created_at) VALUES($1,$2,'sync',$3,'{}',now())`, actor, uuid.New(), bytes.Repeat([]byte{1}, 32))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = provider.DownTo(ctx, 36); err == nil || !strings.Contains(err.Error(), "server management downgrade blocked") {
				t.Fatalf("retained %s downgraded: %v", kind, err)
			}
			var count int
			if kind == "action" {
				err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM vpn_server_actions WHERE actor_id=$1`, actor).Scan(&count)
			}
			if kind == "role" {
				err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM infrastructure_operators WHERE account_id=$1`, actor).Scan(&count)
			}
			if err != nil || count != 1 {
				t.Fatalf("%s lost: %d %v", kind, count, err)
			}
		})
	}
}
