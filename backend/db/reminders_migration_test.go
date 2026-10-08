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

func TestReminderDowngradePreservesFacts(t *testing.T) {
	for _, mode := range []string{"empty", "preference", "reminder", "renew-route"} {
		t.Run(mode, func(t *testing.T) {
			e := testkit.Open(t)
			ctx := context.Background()
			database := stdlib.OpenDBFromPool(e.Pool)
			defer database.Close()
			provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
			if err != nil {
				t.Fatal(err)
			}
			if mode != "empty" {
				account := uuid.New()
				if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) VALUES($1,'reminder-db@example.test','ru','fixture',now(),$2,'aaaaaaaaaaaaaaaa',$3,'1','1')`, account, uuid.New(), "acct_"+account.String()); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "preference":
					_, err = e.Pool.Exec(ctx, `INSERT INTO reminder_preferences(account_id,email_enabled,updated_at) VALUES($1,false,now())`, account)
				case "reminder":
					_, err = e.Pool.Exec(ctx, `INSERT INTO reminders(id,account_id,kind,period,threshold,observed_at,expiry_ms) VALUES($1,$2,'expiry','fixture',1,now(),1)`, uuid.New(), account)
				case "renew-route":
					_, err = e.Pool.Exec(ctx, `INSERT INTO client_telegram_deliveries(id,account_id,telegram_id,credential_version,locale,event_key,route,created_at) VALUES($1,$2,744,0,'ru','fixture','renew',now())`, uuid.New(), account)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = provider.DownTo(ctx, 30)
			if mode == "empty" {
				if err != nil {
					t.Fatal("empty reminder layer downgrade", err)
				}
				if _, err = provider.Up(ctx); err != nil {
					t.Fatal("empty reminder layer restore", err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "Reminder downgrade blocked") {
					t.Fatal("retained fact downgrade", err)
				}
				var exists bool
				if e.Pool.QueryRow(ctx, `SELECT to_regclass('reminders') IS NOT NULL AND to_regclass('reminder_preferences') IS NOT NULL`).Scan(&exists) != nil || !exists {
					t.Fatal("failed downgrade erased schema")
				}
			}
		})
	}
}
