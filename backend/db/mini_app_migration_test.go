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

func TestMiniAppMigrationPreservesOldIdentityAndBlocksLoss(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 23); err != nil {
		t.Fatal(err)
	}
	web, telegram, vpn := uuid.New(), uuid.New(), uuid.New()
	_, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,password_hash,verified_at,terms_version,privacy_version,locale,vpn_id,sub_id,panel_key) VALUES($1,'old@example.test','owned-fixture',now(),'old-terms','old-privacy','en',$2,'oldweb0123456789','acct_old_web')`, web, vpn)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,telegram_id,display_name,locale,vpn_id,sub_id,panel_key) VALUES($1,'telegram',444,'Old Telegram','ru',$2,'oldtg01234567890','acct_old_tg')`, telegram, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	oldHash := bytes.Repeat([]byte{1}, 32)
	_, err = e.Pool.Exec(ctx, `INSERT INTO sessions(id_hash,account_id,csrf_token,created_at,last_seen,absolute_expires_at) VALUES($1,$2 ,'old-csrf',$3::timestamptz,$3::timestamptz,$3::timestamptz+interval '30 days')`, oldHash, web, e.Clock())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 24); err != nil {
		t.Fatal(err)
	}
	checkOld := func(withSource bool) {
		t.Helper()
		var gotVPN uuid.UUID
		var sub, panel, email string
		if err = e.Pool.QueryRow(ctx, `SELECT vpn_id,sub_id,panel_key,email_key FROM accounts WHERE id=$1`, web).Scan(&gotVPN, &sub, &panel, &email); err != nil || gotVPN != vpn || sub != "oldweb0123456789" || panel != "acct_old_web" || email != "old@example.test" {
			t.Fatal("old identity changed", err)
		}
		var stored []byte
		var csrf string
		if err = e.Pool.QueryRow(ctx, `SELECT id_hash,csrf_token FROM sessions WHERE account_id=$1`, web).Scan(&stored, &csrf); err != nil || !bytes.Equal(stored, oldHash) || csrf != "old-csrf" {
			t.Fatal("old session changed", err)
		}
		if withSource {
			var source string
			var bound *int64
			if err = e.Pool.QueryRow(ctx, `SELECT auth_source,telegram_id FROM sessions WHERE account_id=$1`, web).Scan(&source, &bound); err != nil || source != "web" || bound != nil {
				t.Fatal("old session source changed", err)
			}
		}
	}
	checkOld(true)
	newHash := bytes.Repeat([]byte{2}, 32)
	_, err = e.Pool.Exec(ctx, `INSERT INTO sessions(id_hash,account_id,csrf_token,created_at,last_seen,absolute_expires_at,auth_source,telegram_id) VALUES($1,$2,'mini-csrf',$3::timestamptz,$3::timestamptz,$3::timestamptz+interval '30 days','telegram',444)`, newHash, telegram, e.Clock())
	if err != nil {
		t.Fatal(err)
	}
	blocked := func() {
		t.Helper()
		if _, err = provider.Down(ctx); err == nil || !strings.Contains(err.Error(), "Mini App downgrade blocked") {
			t.Fatal("new history loss allowed", err)
		}
		checkOld(true)
	}
	blocked()
	if _, err = e.Pool.Exec(ctx, `DELETE FROM sessions WHERE id_hash=$1`, newHash); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE accounts SET terms_version='1',privacy_version='1',policy_accepted_at=$2 WHERE id=$1`, telegram, e.Clock()); err != nil {
		t.Fatal(err)
	}
	blocked()
	if _, err = e.Pool.Exec(ctx, `UPDATE accounts SET terms_version=NULL,privacy_version=NULL,policy_accepted_at=NULL,telegram_start_param='owned_payload' WHERE id=$1`, telegram); err != nil {
		t.Fatal(err)
	}
	blocked()
	if _, err = e.Pool.Exec(ctx, `UPDATE accounts SET telegram_start_param=NULL WHERE id=$1`, telegram); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal("empty Mini migration cannot downgrade", err)
	}
	checkOld(false)
	if _, err = provider.UpTo(ctx, 24); err != nil {
		t.Fatal(err)
	}
	checkOld(true)
}
