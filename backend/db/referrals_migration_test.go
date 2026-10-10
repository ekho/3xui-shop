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

func referralAccount(t *testing.T, e *testkit.Env) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := e.Pool.Exec(context.Background(), `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
	 VALUES($1,$2,'en','fixture',now(),$3,$4,$5,'1','1')`, id, id.String()+"@example.test", uuid.New(), strings.ReplaceAll(uuid.NewString(), "-", "")[:16], "acct_"+id.String())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestReferralMigrationPreservesGraphAndHistory(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	a, b, c := referralAccount(t, e), referralAccount(t, e), referralAccount(t, e)
	insert := `INSERT INTO referrals(id,referrer_account_id,referred_account_id,created_at,legacy_referral_id) VALUES($1,$2,$3,now(),$4)`
	if _, err := e.Pool.Exec(ctx, insert, uuid.New(), a, b, 73); err != nil {
		t.Fatal("first legacy relation", err)
	}
	if _, err := e.Pool.Exec(ctx, insert, uuid.New(), b, c, 74); err != nil {
		t.Fatal("second relation", err)
	}
	for _, pair := range [][2]uuid.UUID{{a, a}, {c, a}, {a, b}} {
		if _, err := e.Pool.Exec(ctx, insert, uuid.New(), pair[0], pair[1], nil); err == nil {
			t.Fatal("self, cycle or duplicate inviter accepted")
		}
	}
	for _, query := range []string{
		`UPDATE referrals SET referrer_account_id=$2 WHERE referred_account_id=$1`,
		`DELETE FROM referrals WHERE referred_account_id=$1 AND referrer_account_id<>$2`,
	} {
		if _, err := e.Pool.Exec(ctx, query, b, c); err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatal("persisted relationship changed")
		}
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO referrer_rewards(id,account_id,reward_type,reward_level,amount,payment_id,created_at,legacy_reward_id)
	 VALUES($1,$2,'MONEY',NULL,12345678901234567890.123456789012345678,'legacy-payment',now(),81)`, uuid.New(), a); err != nil {
		t.Fatal("exact nullable legacy reward", err)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO referrer_rewards(id,account_id,reward_type,reward_level,amount,payment_id,created_at)
	 VALUES($1,$2,'DAYS',1,0.5,'fractional',now())`, uuid.New(), a); err == nil {
		t.Fatal("fractional days accepted")
	}
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 40); err == nil || !strings.Contains(err.Error(), "referral downgrade blocked") {
		t.Fatal("retained referral history downgraded", err)
	}
	var amount string
	var source int64
	if err = e.Pool.QueryRow(ctx, `SELECT amount::text,legacy_reward_id FROM referrer_rewards WHERE account_id=$1`, a).Scan(&amount, &source); err != nil || amount != "12345678901234567890.123456789012345678" || source != 81 {
		t.Fatal("legacy numeric/ID lost", err)
	}
}

func TestReferralMigrationEmptyRollbackAndLinkGuard(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "link"}[retained], func(t *testing.T) {
			e := testkit.Open(t)
			ctx := context.Background()
			if retained {
				if _, err := e.Pool.Exec(ctx, `INSERT INTO referral_links(account_id,code,created_at) VALUES($1,$2,now())`, referralAccount(t, e), "r_"+strings.ReplaceAll(uuid.NewString(), "-", "")); err != nil {
					t.Fatal(err)
				}
			}
			database := stdlib.OpenDBFromPool(e.Pool)
			defer database.Close()
			provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.DownTo(ctx, 40)
			if retained {
				if err == nil || !strings.Contains(err.Error(), "referral downgrade blocked") {
					t.Fatal("personal link silently removed", err)
				}
			} else if err != nil {
				t.Fatal("empty schema cannot roll back", err)
			} else if _, err = provider.Up(ctx); err != nil {
				t.Fatal("schema cannot reapply", err)
			}
		})
	}
}
