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

func TestReferralDeliveryMigrationRetentionAndLegacy(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "native"}[native], func(t *testing.T) {
			e := testkit.Open(t)
			ctx := context.Background()
			a := referralAccount(t, e)
			if _, err := e.Pool.Exec(ctx, `INSERT INTO referrer_rewards(id,account_id,reward_type,amount,payment_id,created_at,legacy_reward_id)
 VALUES($1,$2,'MONEY',12345678901234567890.123456789012345678,'legacy-payment',now(),91)`, uuid.New(), a); err != nil {
				t.Fatal(err)
			}
			if native {
				order, id, op := uuid.New(), uuid.New(), uuid.New()
				if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,created_at,expires_at)
 VALUES($1,$2,$3,'fixture','{}',100,'AC',now(),now()+interval '1 hour')`, order, a, uuid.New()); err != nil {
					t.Fatal(err)
				}
				if _, err := e.Pool.Exec(ctx, `INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at) VALUES($1,$2,'compensate','pending','referral_reward','{}','{}',now(),now())`, op, a); err != nil {
					t.Fatal(err)
				}
				if _, err := e.Pool.Exec(ctx, `INSERT INTO referrer_rewards(id,account_id,reward_type,reward_level,amount,payment_id,source_order_id,created_at)
 VALUES($1,$2,'DAYS',1,10,$3,$4,now())`, id, a, "order:"+order.String(), order); err != nil {
					t.Fatal(err)
				}
				if _, err := e.Pool.Exec(ctx, `UPDATE referrer_rewards SET access_operation_id=$2,rewarded_at=now() WHERE id=$1`, id, op); err != nil {
					t.Fatal(err)
				}
				for _, query := range []string{`DELETE FROM referrer_rewards WHERE id=$1`, `UPDATE referrer_rewards SET amount=11 WHERE id=$1`, `UPDATE referrer_rewards SET access_operation_id=NULL WHERE id=$1`, `UPDATE referrer_rewards SET rewarded_at=NULL WHERE id=$1`, `UPDATE referrer_rewards SET source_order_id=NULL WHERE id=$1`} {
					if _, err := e.Pool.Exec(ctx, query, id); err == nil {
						t.Fatal("retained native fact changed")
					}
				}
			}
			database := stdlib.OpenDBFromPool(e.Pool)
			defer database.Close()
			provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.DownTo(ctx, 41)
			if native {
				if err == nil || !strings.Contains(err.Error(), "referral delivery downgrade blocked") {
					t.Fatal("native delivery facts downgraded", err)
				}
			} else {
				if err != nil {
					t.Fatal("additive delivery schema cannot rollback with legacy history", err)
				}
				if _, err = provider.Up(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var amount string
			var legacyID int64
			if err = e.Pool.QueryRow(ctx, `SELECT amount::text,legacy_reward_id FROM referrer_rewards WHERE legacy_reward_id=91`).Scan(&amount, &legacyID); err != nil || amount != "12345678901234567890.123456789012345678" || legacyID != 91 {
				t.Fatal("legacy exact fact altered", err)
			}
		})
	}
}
