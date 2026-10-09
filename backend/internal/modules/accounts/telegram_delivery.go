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
	first, second := tg, tg
	if source, ok := TelegramActor(ctx); ok && *source.TelegramID != tg {
		second = *source.TelegramID
		if first > second {
			first, second = second, first
		}
	}
	if err = lockTelegramIdentity(ctx, tx, first); err != nil {
		return false, err
	}
	if second != first {
		if err = lockTelegramIdentity(ctx, tx, second); err != nil {
			return false, err
		}
	}
	var a Snapshot
	if source, ok := TelegramActor(ctx); ok && source.ID != id {
		a, err = s.LockOperatorPair(ctx, tx, source.ID, id)
	} else {
		raw, e := store.New(tx).LockAccount(ctx, id)
		err = e
		a = snapshot(raw)
		if err == nil {
			err = telegramPrincipal(ctx, a)
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		var sourceError *Error
		if errors.As(err, &sourceError) {
			return false, err
		}
		return false, unavailable()
	}
	if a.TelegramID == nil || *a.TelegramID != tg || a.CredentialVersion != version || a.TelegramLoginDisabled || a.Restricted || !SourceEligible(a) || a.TermsVersion == nil || a.PrivacyVersion == nil {
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
