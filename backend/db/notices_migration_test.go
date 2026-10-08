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

func TestNoticeDowngradePreservesFacts(t *testing.T) {
	for _, mode := range []string{"empty", "preference", "confirmed-deliveries"} {
		t.Run(mode, func(t *testing.T) {
			e := testkit.Open(t)
			ctx := context.Background()
			database := stdlib.OpenDBFromPool(e.Pool)
			defer database.Close()
			provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
			if err != nil {
				t.Fatal(err)
			}
			account, preview, recipient, action := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			if mode != "empty" {
				if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) VALUES($1,'notice-db@example.test','ru','fixture',now(),$2,'aaaaaaaaaaaaaaaa',$3,'1','1')`, account, uuid.New(), "acct_"+account.String()); err != nil {
					t.Fatal(err)
				}
				if mode == "preference" {
					_, err = e.Pool.Exec(ctx, `INSERT INTO notice_preferences(account_id,email_enabled,updated_at) VALUES($1,false,now())`, account)
				} else {
					for _, q := range []struct {
						sql  string
						args []any
					}{
						{`INSERT INTO notice_previews(id,operator_account_id,notice_id,mode,audience,html,plain_text,reason,expected_revision,recipient_snapshot,created_at,expires_at,confirmed_at) VALUES($1,$2,$1,'send','personal','Owned notice','Owned notice','Fixture',0,'[]',now(),now()+interval '15 minutes',now())`, []any{preview, account}},
						{`INSERT INTO notices(id,operator_account_id,current_preview_id,revision,created_at) VALUES($1,$2,$1,1,now())`, []any{preview, account}},
						{`INSERT INTO notice_recipients(id,notice_id,account_id,credential_version,telegram_id,locale,email_hash,email_enabled,cabinet_visible) VALUES($1,$2,$3,0,733,'ru',$4,true,true)`, []any{recipient, preview, account, make([]byte, 32)}},
						{`INSERT INTO notice_actions(id,preview_id,recipient_id,cabinet_state,telegram_state,email_state) VALUES($1,$2,$3,'succeeded','pending','pending')`, []any{action, preview, recipient}},
						{`INSERT INTO mail_deliveries(id,email_key,ciphertext,created_at,kind,notice_action_id) VALUES($1,'notice-db@example.test',$2,now(),'operator_notice',$3)`, []any{uuid.New(), []byte{1, 2, 3}, action}},
						{`INSERT INTO client_telegram_deliveries(id,account_id,telegram_id,credential_version,locale,event_key,route,created_at,notice_action_id) VALUES($1,$2,733,0,'ru',$3,'cabinet',now(),$4)`, []any{uuid.New(), account, "notice:" + action.String(), action}},
					} {
						if _, err = e.Pool.Exec(ctx, q.sql, q.args...); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = provider.DownTo(ctx, 31)
			if mode == "empty" {
				if err != nil {
					t.Fatal("empty notice downgrade", err)
				}
				if _, err = provider.Up(ctx); err != nil {
					t.Fatal("notice restore", err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "Notice downgrade blocked") {
					t.Fatal("retained notice facts erased", err)
				}
				var retained bool
				query := `SELECT EXISTS(SELECT 1 FROM notice_preferences)`
				if mode == "confirmed-deliveries" {
					query = `SELECT EXISTS(SELECT 1 FROM notices) AND EXISTS(SELECT 1 FROM notice_actions) AND EXISTS(SELECT 1 FROM client_telegram_deliveries WHERE notice_action_id IS NOT NULL) AND EXISTS(SELECT 1 FROM mail_deliveries WHERE kind='operator_notice' AND ciphertext=$1)`
					err = e.Pool.QueryRow(ctx, query, []byte{1, 2, 3}).Scan(&retained)
				} else {
					err = e.Pool.QueryRow(ctx, query).Scan(&retained)
				}
				if err != nil || !retained {
					t.Fatal("failed downgrade lost facts", err)
				}
			}
		})
	}
}
