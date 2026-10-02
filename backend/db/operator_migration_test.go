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

func TestOperatorDowngradePreservesSourceAndActors(t *testing.T) {
	for _, kind := range []string{"telegram", "web-decision"} {
		t.Run(kind, func(t *testing.T) {
			e := testkit.Open(t)
			ctx := context.Background()
			target := uuid.New()
			if kind == "telegram" {
				_, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,display_name,telegram_id,locale,vpn_id,sub_id,panel_key)
					VALUES($1,'telegram','Fixture',101,'ru',$2,'aaaaaaaaaaaaaaaa',$3)`, target, uuid.New(), "acct_"+target.String())
				if err != nil {
					t.Fatal("fixture", err)
				}
			} else {
				actor := uuid.New()
				for i, id := range []uuid.UUID{target, actor} {
					sub := "aaaaaaaaaaaaaaaa"
					if i == 1 {
						sub = "bbbbbbbbbbbbbbbb"
					}
					_, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
						VALUES($1,$2,'ru','fixture',now(),$3,$4,$5,'1','1')`, id, "fixture"+sub+"@example.test", uuid.New(), sub, "acct_"+id.String())
					if err != nil {
						t.Fatal("fixture", err)
					}
				}
				_, err := e.Pool.Exec(ctx, `INSERT INTO trial_requests(id,account_id,status,comment,created_at,decided_at,operator_account_id)
					VALUES($1,$2,'rejected','',now(),now(),$3)`, uuid.New(), target, actor)
				if err != nil {
					t.Fatal("fixture", err)
				}
			}
			database := stdlib.OpenDBFromPool(e.Pool)
			defer database.Close()
			provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
			if err != nil {
				t.Fatal(err)
			}
			// Remove empty S09 and S48 before testing the S06 fail-closed guard.
			if _, err = provider.Down(ctx); err != nil {
				t.Fatal("down from empty catalogue", err)
			}
			if _, err = provider.Down(ctx); err != nil {
				t.Fatal("down to operator migration", err)
			}
			if _, err = provider.Down(ctx); err == nil || !strings.Contains(err.Error(), "operator client downgrade blocked") {
				t.Fatal("downgrade must fail closed")
			}
			var preserved string
			if err = e.Pool.QueryRow(ctx, `SELECT kind FROM accounts WHERE id=$1`, target).Scan(&preserved); err != nil {
				t.Fatal("source data lost", err)
			}
			if kind == "telegram" && preserved != "telegram" {
				t.Fatal("source changed")
			}
			if kind == "web-decision" {
				var n int
				if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM trial_requests WHERE operator_account_id IS NOT NULL`).Scan(&n); err != nil || n != 1 {
					t.Fatal("decision actor lost", err)
				}
			}
		})
	}
}

func TestOperatorMigrationKeepsOldCreationDateUnknown(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal("down from empty catalogue", err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal("down to operator migration", err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal("down to original accounts schema", err)
	}
	id := uuid.New()
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
		VALUES($1,'old@example.test','ru','fixture',now(),$2,'aaaaaaaaaaaaaaaa',$3,'1','1')`, id, uuid.New(), "acct_"+id.String()); err != nil {
		t.Fatal("old account fixture", err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal("operator migration", err)
	}
	var oldKind string
	var oldCreated *string
	if err = e.Pool.QueryRow(ctx, `SELECT kind,created_at::text FROM accounts WHERE id=$1`, id).Scan(&oldKind, &oldCreated); err != nil || oldKind != "web" || oldCreated != nil {
		t.Fatal("old account must retain unknown creation date", err)
	}
	var newCreated *string
	if err = e.Pool.QueryRow(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
		VALUES($1,'new@example.test','ru','fixture',now(),$2,'bbbbbbbbbbbbbbbb',$3,'1','1') RETURNING created_at::text`, uuid.New(), uuid.New(), "acct_new").Scan(&newCreated); err != nil || newCreated == nil {
		t.Fatal("new account must receive database creation date", err)
	}
}
