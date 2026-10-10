package bonuses

import (
	"context"
	"errors"
	"math"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/bonuses/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ReferredTrialConfig struct {
	Enabled    bool
	PeriodDays int64
}

// ReserveTelegramTrialTx runs under the subscriptions account lock; its period
// is the full trial, with the existing grant protecting retries and aliases.
func (s *Service) ReserveTelegramTrialTx(ctx context.Context, tx pgx.Tx, a accounts.Snapshot, request uuid.UUID, c ReferredTrialConfig) (int64, error) {
	if tx == nil || a.ID == uuid.Nil || request == uuid.Nil {
		return 0, unavailable()
	}
	if a.SourceKind != "telegram" || a.LegacyUserID != nil {
		return 0, nil
	}
	q := store.New(tx)
	r, err := q.TrialReferral(ctx, a.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, unavailable()
	}
	if r.ReferredBonusDays.Valid || r.ReferredRewardedAt.Valid {
		return 0, failure(409, "TRIAL_ALREADY_USED")
	}
	if !c.Enabled || r.LegacyReferralID.Valid {
		return 0, nil
	}
	if c.PeriodDays <= 0 || c.PeriodDays > math.MaxInt64/int64(24*time.Hour) {
		return 0, unavailable()
	}
	n, err := q.ReserveTrialBenefit(ctx, store.ReserveTrialBenefitParams{ID: r.ID, ReferredBonusDays: int32(c.PeriodDays)})
	if err != nil || n != 1 {
		return 0, unavailable()
	}
	reason := "referral=" + r.ID.String()
	// The operation is inserted next; the request links this atomic reservation.
	if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: "referral_trial_reserved", AccountID: a.ID, RequestID: &request, Reason: &reason}) != nil {
		return 0, unavailable()
	}
	return c.PeriodDays, nil
}

// RecordTelegramTrialAppliedTx is called only for the matching approved
// automatic trial after panel readback, in the grant's final transaction.
func (s *Service) RecordTelegramTrialAppliedTx(ctx context.Context, tx pgx.Tx, account, request, operation uuid.UUID) error {
	if tx == nil || account == uuid.Nil || request == uuid.Nil || operation == uuid.Nil {
		return unavailable()
	}
	id, err := store.New(tx).ApplyTrialBenefit(ctx, store.ApplyTrialBenefitParams{ReferredAccountID: account, ReferredRewardedAt: pgtype.Timestamptz{Time: s.now(), Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	reason := "referral=" + id.String()
	return auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: "referral_trial_granted", AccountID: account, RequestID: &request, OperationID: &operation, Reason: &reason})
}
