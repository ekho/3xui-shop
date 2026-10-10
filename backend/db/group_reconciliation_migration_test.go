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

func TestGroupReconciliationMigrationHistoryAndPayloadGuard(t *testing.T) {
	for _, kind := range []string{"ordinary", "operation", "alert"} {
		t.Run(kind, func(t *testing.T) {
			e := testkit.Open(t)
			ctx := context.Background()
			database := stdlib.OpenDBFromPool(e.Pool)
			defer database.Close()
			provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
			if err != nil {
				t.Fatal(err)
			}
			account, operation := uuid.New(), uuid.New()
			if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
			 VALUES($1,'group-migration@example.test','ru','fixture',now(),$2,'dddddddddddddddd',$3,'1','1')`, account, uuid.New(), "acct_"+account.String()); err != nil {
				t.Fatal(err)
			}
			const insert = `INSERT INTO client_telegram_deliveries(id,account_id,telegram_id,credential_version,locale,event_key,route,created_at,vpn_alert_code,vpn_alert_account_id)
			 VALUES($1,$2,701,0,'ru','fixture','cabinet',now(),$3,$4)`
			for _, bad := range []struct {
				code   any
				target any
			}{{nil, account}, {"provider body", account}, {"panel_unavailable", nil}} {
				if _, err = e.Pool.Exec(ctx, insert, uuid.New(), account, bad.code, bad.target); err == nil {
					t.Fatal("incomplete/arbitrary VPN alert payload entered durable storage")
				}
			}
			switch kind {
			case "operation":
				_, err = e.Pool.Exec(ctx, `INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at)
				 VALUES($1,$2,'group_reconcile','needs_review','fixture','{}','{}',now(),now())`, operation, account)
			case "alert":
				_, err = e.Pool.Exec(ctx, insert, operation, account, "panel_unavailable", account)
			case "ordinary":
				_, err = e.Pool.Exec(ctx, insert, operation, account, nil, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.DownTo(ctx, 38)
			if kind == "ordinary" {
				if err != nil {
					t.Fatal("ordinary delivery blocked unrelated rollback", err)
				}
				if _, err = provider.Up(ctx); err != nil {
					t.Fatal("reapply", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "group reconciliation downgrade blocked") {
				t.Fatal("retained reconciliation history downgraded", err)
			}
			var count int
			query := `SELECT count(*) FROM client_telegram_deliveries WHERE id=$1`
			if kind == "operation" {
				query = `SELECT count(*) FROM access_operations WHERE id=$1`
			}
			if e.Pool.QueryRow(ctx, query, operation).Scan(&count) != nil || count != 1 {
				t.Fatal("reconciliation or unrelated delivery history lost")
			}
		})
	}
}
