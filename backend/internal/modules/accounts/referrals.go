package accounts

import (
	"context"

	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"github.com/jackc/pgx/v5"
)

// ReferralInviterTx preserves old links after the original identity is retired.
func (s *Service) ReferralInviterTx(ctx context.Context, tx pgx.Tx, telegramID int64) (Snapshot, error) {
	if telegramID <= 0 {
		return Snapshot{}, ErrNotFound
	}
	q := store.New(tx)
	a, err := q.ReferralInviter(ctx, telegramID)
	return accountResult(a, err)
}
