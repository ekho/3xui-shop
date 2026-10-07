package accounts

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

// WithTelegramDelivery linearizes a bounded send with binding/recovery/restriction changes.
// Work must use this Tx, including with a one-connection pool.
func (s *Service) WithTelegramDelivery(ctx context.Context, id uuid.UUID, tg, version int64, work func(pgx.Tx) error) (bool, error) {
	if id == uuid.Nil || tg <= 0 || tg > 1<<52-1 || version < 0 || work == nil {
		return false, failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, unavailable()
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		tx.Rollback(cleanup)
	}()
	if err = lockTelegramIdentity(ctx, tx, tg); err != nil {
		return false, err
	}
	a, err := store.New(tx).LockAccount(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, unavailable()
	}
	if !a.TelegramID.Valid || a.TelegramID.Int64 != tg || a.CredentialVersion != version || a.TelegramLoginDisabled || a.Restricted || !SourceEligible(snapshot(a)) || !a.TermsVersion.Valid || !a.PrivacyVersion.Valid {
		return false, nil
	}
	// ponytail: account/job locks cover the bounded 10-second send; split the delivery proof
	// only if notification throughput makes this short credential-change wait material.
	if err = work(tx); err != nil {
		return false, err
	}
	if tx.Commit(ctx) != nil {
		return false, unavailable()
	}
	return true, nil
}
