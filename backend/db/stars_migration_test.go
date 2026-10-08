package db_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Catches erased older quotes, mutable Stars payer and a downgrade discarding invoices.
func TestStarsMigration(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.DownTo(ctx, 27); err != nil {
		t.Fatal(err)
	}
	account, old, order := uuid.New(), uuid.New(), uuid.New()
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,telegram_id,display_name,locale,vpn_id,sub_id,panel_key,terms_version,privacy_version,policy_accepted_at) VALUES($1,'telegram',701,'Owned Stars fixture','en',$2,'starsmigration','acct_stars_migration','1','1',now())`, account, uuid.New()); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,payment_method,created_at,expires_at,active) VALUES($1,$2,$3,$4,$5,100,$6,$7,now(),now()+interval '30 minutes',false)`
	if _, err = e.Pool.Exec(ctx, insert, old, account, uuid.New(), []byte{1}, json.RawMessage(`{"currency":"RUB"}`), "AC", "yoomoney"); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err = e.Pool.QueryRow(ctx, `SELECT to_jsonb(p)::text FROM purchase_orders p WHERE id=$1`, old).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = p.UpTo(ctx, 28); err != nil {
		t.Fatal(err)
	}
	if err = e.Pool.QueryRow(ctx, `SELECT to_jsonb(p)::text FROM purchase_orders p WHERE id=$1`, old).Scan(&after); err != nil || before != after {
		t.Fatal("old order changed", err)
	}
	if _, err = e.Pool.Exec(ctx, insert, order, account, uuid.New(), []byte{2}, json.RawMessage(`{"currency":"XTR"}`), "STARS", "telegram_stars"); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, `INSERT INTO stars_checkouts(order_id,bot_id,payer_id,payload) VALUES($1,123,701,'stars:v1:'||$1::text)`, order); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`UPDATE stars_checkouts SET payer_id=702 WHERE order_id=$1`, `UPDATE stars_checkouts SET bot_id=124 WHERE order_id=$1`, `DELETE FROM stars_checkouts WHERE order_id=$1`} {
		if _, err = e.Pool.Exec(ctx, sql, order); err == nil {
			t.Fatal("Stars provenance mutation allowed")
		}
	}
	if _, err = p.Down(ctx); err == nil || !strings.Contains(err.Error(), "Stars downgrade blocked") {
		t.Fatal("invoice history downgrade", err)
	}
	if v, err := p.GetDBVersion(ctx); err != nil || v != 28 {
		t.Fatal("downgrade not atomic", err)
	}
	var payer int64
	if err = e.Pool.QueryRow(ctx, `SELECT payer_id FROM stars_checkouts WHERE order_id=$1`, order).Scan(&payer); err != nil || payer != 701 {
		t.Fatal("immutable payer lost", err)
	}
}
