package payments

import (
	"context"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func TestLegacyPaymentsTxSeesUncommittedAccountAndRollsBack(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	id := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id,legacy_user_id) VALUES($1,$2,'ru','fixture',now(),$3,'aaaaaaaaaaaaaaaa',$4,'1','1',701,5)`, id, id.String()+"@example.test", uuid.New(), "acct_"+id.String()); err != nil {
		t.Fatal(err)
	}
	authority := accounts.New(e.Pool, e.Redis, nil, accounts.Config{Now: e.Clock})
	s := New(e.Pool, authority, nil, nil, nil, nil, nil, e.Clock, nil)
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	p := LegacyPaymentPackage{Version: 1, Users: []LegacyPaymentUser{{SourceLegacyUserID: 5, SourceTgID: 701}}, Transactions: []LegacyPaymentTransaction{{SourceID: 9, SourceTgID: 701, PaymentID: "fixture-charge", Subscription: "unknown:preserve", Status: "pending", CreatedAt: now, UpdatedAt: now}}}
	if out, err := s.ImportLegacyPaymentsTx(ctx, tx, p, false); err != nil || out.Inserted != 1 {
		t.Fatal("transaction cannot see its new account", out, err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM legacy_payment_transactions WHERE source_id=9`).Scan(&count); err != nil || count != 1 {
		t.Fatal("payment missing inside transaction", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM legacy_payment_transactions WHERE source_id=9`).Scan(&count); err != nil || count != 0 {
		t.Fatal("payment escaped rollback", err)
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatal("account escaped rollback", err)
	}
}
