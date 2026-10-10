package accounts

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type switchAfterScanTx struct {
	pgx.Tx
	after func()
}

func (tx *switchAfterScanTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	row := tx.Tx.QueryRow(ctx, sql, args...)
	if tx.after == nil {
		return row
	}
	after := tx.after
	tx.after = nil
	return &switchAfterScanRow{Row: row, after: after}
}

type switchAfterScanRow struct {
	pgx.Row
	after func()
}

func (row *switchAfterScanRow) Scan(dest ...any) error {
	err := row.Row.Scan(dest...)
	row.after()
	return err
}

// A retired identity can become active during lookup. Its owner must remain
// discoverable throughout that handoff.
func TestReferralInviterAcrossRetiredToActiveHandoff(t *testing.T) {
	s, e, _ := fixture(t)
	ctx := context.Background()
	owner := uuid.New()
	_, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
	 VALUES($1,$2,'en','fixture',now(),$3,$4,$5,'1','1')`, owner, owner.String()+"@example.test", uuid.New(), strings.ReplaceAll(uuid.NewString(), "-", "")[:16], "acct_"+owner.String())
	if err != nil {
		t.Fatal(err)
	}
	const telegramID int64 = 983401
	if _, err := e.Pool.Exec(ctx, `INSERT INTO telegram_identity_reservations(telegram_id,account_id,retired_at) VALUES($1,$2,now())`, telegramID, owner); err != nil {
		t.Fatal(err)
	}
	read, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback(ctx)
	switched := false
	interleaved := &switchAfterScanTx{Tx: read, after: func() {
		write, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Rollback(ctx)
		if _, err := write.Exec(ctx, `UPDATE accounts SET telegram_id=$2 WHERE id=$1`, owner, telegramID); err != nil {
			t.Fatal(err)
		}
		if _, err := write.Exec(ctx, `DELETE FROM telegram_identity_reservations WHERE telegram_id=$1 AND account_id=$2`, telegramID, owner); err != nil {
			t.Fatal(err)
		}
		if err := write.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		switched = true
	}}
	got, err := s.ReferralInviterTx(ctx, interleaved, telegramID)
	if !switched {
		t.Fatal("identity did not switch between the read statements")
	}
	if err != nil || got.ID != owner {
		t.Fatalf("retired-to-active handoff lost the owner: got %s, err %v", got.ID, err)
	}
}
