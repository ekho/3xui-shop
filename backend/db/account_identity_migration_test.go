package db_test

import (
	"bytes"
	"context"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"os"
	"strings"
	"testing"
)

func TestAccountIdentityMigrationPreservesAndBlocksLoss(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 24); err != nil {
		t.Fatal(err)
	}
	web, tg, vpn := uuid.New(), uuid.New(), uuid.New()
	hash := bytes.Repeat([]byte{11}, 32)
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,password_hash,verified_at,terms_version,privacy_version,locale,vpn_id,sub_id,panel_key) VALUES($1,'identity-old@example.test','owned-fixture',now(),'old-terms','old-privacy','en',$2,'identityweb00001','acct_identity_old_web')`, web, vpn); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,telegram_id,display_name,locale,vpn_id,sub_id,panel_key,legacy_user_id,terms_version,privacy_version,telegram_start_param,policy_accepted_at) VALUES($1,'telegram',444,'Old Telegram','ru',$2,'identitytg000001','acct_identity_old_tg',444,'tg-terms','tg-privacy','old_signed_payload',now())`, tg, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, `INSERT INTO sessions(id_hash,account_id,csrf_token,created_at,last_seen,absolute_expires_at,auth_source,telegram_id) VALUES($1,$2,'old-mini-csrf',$3,$3,$3::timestamptz+interval '30 days','telegram',444)`, hash, tg, e.Clock()); err != nil {
		t.Fatal(err)
	}
	const facts = `SELECT (to_jsonb(a)-ARRAY['original_kind','telegram_login_disabled'])::text FROM accounts a ORDER BY id`
	readFacts := func() []string {
		t.Helper()
		rows, err := e.Pool.Query(ctx, facts)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result []string
		for rows.Next() {
			var row string
			if err = rows.Scan(&row); err != nil {
				t.Fatal(err)
			}
			result = append(result, row)
		}
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		return result
	}
	before := strings.Join(readFacts(), "\n")
	check := func() {
		t.Helper()
		if strings.Join(readFacts(), "\n") != before {
			t.Fatal("migration changed source/credentials/VPN/legacy facts")
		}
		var stored []byte
		var csrf, source string
		var telegram int64
		if err = e.Pool.QueryRow(ctx, `SELECT id_hash,csrf_token,auth_source,telegram_id FROM sessions WHERE account_id=$1`, tg).Scan(&stored, &csrf, &source, &telegram); err != nil || !bytes.Equal(stored, hash) || csrf != "old-mini-csrf" || source != "telegram" || telegram != 444 {
			t.Fatal("old session source/hash changed", err)
		}
	}
	if _, err = provider.UpTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	check()
	var clean bool
	if err = e.Pool.QueryRow(ctx, `SELECT bool_and(original_kind IS NULL AND NOT telegram_login_disabled) FROM accounts`).Scan(&clean); err != nil || !clean {
		t.Fatal("migration invented identity facts")
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal("empty identity migration cannot downgrade", err)
	}
	check()
	if _, err = provider.UpTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{"source", "quarantine", "reservation", "initial_email", "telegram_link", "identity_recovery"} {
		t.Run(fact, func(t *testing.T) {
			switch fact {
			case "source":
				_, err = e.Pool.Exec(ctx, `UPDATE accounts SET original_kind='telegram' WHERE id=$1`, web)
			case "quarantine":
				_, err = e.Pool.Exec(ctx, `UPDATE accounts SET telegram_login_disabled=true WHERE id=$1`, tg)
			case "reservation":
				_, err = e.Pool.Exec(ctx, `INSERT INTO telegram_identity_reservations(telegram_id,account_id,retired_at) VALUES(555,$1,now())`, tg)
			default:
				original, target := "", "unowned@example.test"
				var actor *uuid.UUID
				if fact == "telegram_link" {
					original = target
				}
				if fact == "identity_recovery" {
					actor = &web
				}
				_, err = e.Pool.Exec(ctx, `INSERT INTO credential_challenges(id,purpose,account_id,original_email,target_email,credential_version,token_hash,code_hash,created_at,token_expires_at,code_expires_at,requested_by) VALUES($1,$2,$3,$4,$5,0,$6,$6,now(),now()+interval '30 minutes',now()+interval '10 minutes',$7)`, uuid.New(), fact, tg, original, target, bytes.Repeat([]byte{12}, 32), actor)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = provider.Down(ctx); err == nil || !strings.Contains(err.Error(), "account identity downgrade blocked") {
				t.Fatal("new identity history loss allowed", err)
			}
			var version int64
			if version, err = provider.GetDBVersion(ctx); err != nil || version != 25 {
				t.Fatal("rejected downgrade was not atomic", err)
			}
			if _, err = e.Pool.Exec(ctx, `DELETE FROM credential_challenges;DELETE FROM telegram_identity_reservations;UPDATE accounts SET original_kind=NULL,telegram_login_disabled=false`); err != nil {
				t.Fatal(err)
			}
			check()
		})
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	check()
	if _, err = provider.UpTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	check()
}
