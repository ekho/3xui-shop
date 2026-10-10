package subscriptions

import (
	"context"
	"errors"

	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) lockBonusAccount(ctx context.Context, tx pgx.Tx, id uuid.UUID) (accounts.Snapshot, error) {
	a, err := s.lockAccount(ctx, tx, id)
	if err != nil {
		return a, err
	}
	if a.Restricted {
		return a, failure(403, "ACCOUNT_RESTRICTED")
	}
	if !accounts.SourceEligible(a) || a.TermsVersion == nil || a.PrivacyVersion == nil || a.Kind == "telegram" && a.TelegramLoginDisabled {
		return a, failure(403, "INVALID_CREDENTIALS")
	}
	if a.VpnBanned || stringValue(a.AccessProfile) == "unlimited" {
		return a, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	return a, nil
}

// GetBonusOperation exposes only the current customer's compensation operation.
func (s *Service) GetBonusOperation(ctx context.Context, account, id uuid.UUID) (AccessOperation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AccessOperation{}, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.lockBonusAccount(ctx, tx, account); err != nil {
		return AccessOperation{}, err
	}
	r, err := s.vpn.AccessStateForAccountTx(ctx, tx, id, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccessOperation{}, failure(404, "PROMOCODE_ACTIVATION_NOT_FOUND")
	}
	if err != nil {
		return AccessOperation{}, unavailable()
	}
	if r.OperatorAccountID != nil || r.Kind != "compensate" {
		return AccessOperation{}, failure(404, "PROMOCODE_ACTIVATION_NOT_FOUND")
	}
	return accessPublic(r)
}
