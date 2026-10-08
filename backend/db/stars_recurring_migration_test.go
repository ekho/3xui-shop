package db_test

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"os"
	"strings"
	"testing"
)

// Catches migration erasing one-shot proof or downgrade losing recurring invoice facts.
func TestStarsRecurringMigration(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.DownTo(ctx, 28); err != nil {
		t.Fatal(err)
	}
	account, order, rec := uuid.New(), uuid.New(), uuid.New()
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,telegram_id,display_name,locale,vpn_id,sub_id,panel_key,terms_version,privacy_version,policy_accepted_at) VALUES($1,'telegram',701,'Owned recurring fixture','en',$2,'recurringtest001','acct_recurring_mig','1','1',now())`, account, uuid.New()); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,payment_method,created_at,expires_at,active) VALUES($1,$2,$3,$4,$5,100,'STARS','telegram_stars',now(),now()+interval '30 minutes',false)`
	if _, err = e.Pool.Exec(ctx, insert, order, account, uuid.New(), []byte{1}, json.RawMessage(`{"currency":"XTR"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, `INSERT INTO stars_checkouts(order_id,bot_id,payer_id,payload) VALUES($1::uuid,123,701,'stars:v1:'||$1::uuid::text)`, order); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err = e.Pool.QueryRow(ctx, `SELECT to_jsonb(p)::text FROM purchase_orders p WHERE id=$1`, order).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = p.UpTo(ctx, 29); err != nil {
		t.Fatal("recurring migration missing", err)
	}
	if err = e.Pool.QueryRow(ctx, `SELECT to_jsonb(p)::text FROM purchase_orders p WHERE id=$1`, order).Scan(&after); err != nil || before != after {
		t.Fatal("old order changed", err)
	}
	var period int64
	var query *string
	if err = e.Pool.QueryRow(ctx, `SELECT subscription_period,pre_checkout_id FROM stars_checkouts WHERE order_id=$1`, order).Scan(&period, &query); err != nil || period != 0 || query != nil {
		t.Fatal("one-shot provenance changed", err)
	}
	if _, err = e.Pool.Exec(ctx, insert, rec, account, uuid.New(), []byte{2}, json.RawMessage(`{"currency":"XTR","stars_recurring":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, `INSERT INTO stars_checkouts(order_id,bot_id,payer_id,payload,subscription_period) VALUES($1::uuid,123,701,'stars:v1:'||$1::uuid::text,2592000)`, rec); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE stars_checkouts SET pre_checkout_id='owned-query' WHERE order_id=$1`, rec); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`UPDATE stars_checkouts SET subscription_period=0 WHERE order_id=$1`, `UPDATE stars_checkouts SET pre_checkout_id='other-query' WHERE order_id=$1`, `UPDATE stars_checkouts SET payer_id=702 WHERE order_id=$1`, `DELETE FROM stars_checkouts WHERE order_id=$1`} {
		if _, err = e.Pool.Exec(ctx, sql, rec); err == nil {
			t.Fatal("recurring checkout provenance mutation")
		}
	}
	if _, err = p.Down(ctx); err == nil || !strings.Contains(err.Error(), "Stars recurring downgrade blocked") {
		t.Fatal("recurring invoice downgrade", err)
	}
	if v, err := p.GetDBVersion(ctx); err != nil || v != 29 {
		t.Fatal("recurring downgrade not atomic", err)
	}
}
