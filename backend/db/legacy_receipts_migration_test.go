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

func TestLegacyReceiptMigrationRetainsFinancialProof(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	id := uuid.New()
	_, err := e.Pool.Exec(ctx, `INSERT INTO legacy_payment_receipts(id,provider,event_kind,source_id,source_reference,amount_minor,currency,occurred_at,proof,state) VALUES($1,'telegram_stars','recurring','charge','charge',4,'XTR',now(),'{}','review')`, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE legacy_payment_receipts SET amount_minor=5 WHERE id=$1`, id); err == nil {
		t.Fatal("financial proof changed")
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE legacy_payment_receipts SET state='conflict' WHERE id=$1`, id); err != nil {
		t.Fatal("conflict classification blocked", err)
	}
	if _, err = e.Pool.Exec(ctx, `DELETE FROM legacy_payment_receipts WHERE id=$1`, id); err == nil {
		t.Fatal("financial proof deleted")
	}
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 43); err == nil || !strings.Contains(err.Error(), "legacy payment receipt downgrade blocked") {
		t.Fatal("financial journal downgraded", err)
	}
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 44 {
		t.Fatal("failed downgrade changed version", err)
	}
}
