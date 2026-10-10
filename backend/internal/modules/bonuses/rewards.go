package bonuses

import (
	"context"
	"errors"
	"math/big"
	"time"

	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/bonuses/internal/store"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

type RewardConfig struct {
	Enabled                    bool
	LevelOneDays, LevelTwoDays int
}

// ConfigureRewards connects the existing money owner once, before requests/workers start.
func (s *Service) ConfigureRewards(owner *payments.Service, queue func() *river.Client[pgx.Tx], config func() RewardConfig) {
	s.payments, s.queue, s.rewardConfig = owner, queue, config
}

// RecordPaidPurchaseTx commits rewards/jobs with the existing funding transaction.
func (s *Service) RecordPaidPurchaseTx(ctx context.Context, tx pgx.Tx, order uuid.UUID) error {
	if tx == nil || s.payments == nil || s.rewardConfig == nil {
		return unavailable()
	}
	c := s.rewardConfig()
	if !c.Enabled {
		return nil
	}
	if c.LevelOneDays < 0 || c.LevelOneDays > 365 || c.LevelTwoDays < 0 || c.LevelTwoDays > 365 || s.queue == nil || s.queue() == nil {
		return unavailable()
	}
	p, funded, err := s.payments.ConfirmedPurchaseTx(ctx, tx, order)
	if err != nil {
		return unavailable()
	}
	if !funded {
		return nil
	}
	q := store.New(tx)
	recipients, err := q.RewardRecipients(ctx, p.AccountID)
	if err != nil {
		return unavailable()
	}
	for _, r := range recipients {
		days := c.LevelOneDays
		if r.Level == 2 {
			days = c.LevelTwoDays
		}
		if days == 0 || r.AccountID == p.AccountID {
			continue
		}
		id := uuid.NewSHA1(order, []byte(r.AccountID.String()))
		n, err := q.AddNativeReward(ctx, store.AddNativeRewardParams{ID: id, AccountID: r.AccountID, RewardLevel: pgtype.Int2{Int16: r.Level, Valid: true}, Amount: pgtype.Numeric{Int: big.NewInt(int64(days)), Valid: true}, PaymentID: "order:" + order.String(), SourceOrderID: &order, CreatedAt: pgtype.Timestamptz{Time: s.now(), Valid: true}})
		if err != nil {
			return unavailable()
		}
		if n == 0 {
			continue
		}
		if _, err = s.queue().InsertTx(ctx, tx, RewardArgs{RewardID: id}, &river.InsertOpts{Queue: "provision", MaxAttempts: 1000000}); err != nil {
			return unavailable()
		}
		reason := "reward=" + id.String()
		system := true
		if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: r.AccountID, CreatedAt: s.now(), Action: "referral_reward_created", Reason: &reason, SystemActor: &system}) != nil {
			return unavailable()
		}
	}
	return nil
}

func (s *Service) rewardFunded(ctx context.Context, tx pgx.Tx, order, account uuid.UUID, level int16) (bool, error) {
	if s.payments == nil {
		return false, unavailable()
	}
	p, valid, err := s.payments.ConfirmedPurchaseTx(ctx, tx, order)
	if err != nil || !valid {
		return false, err
	}
	var db store.DBTX = s.pool
	if tx != nil {
		db = tx
	}
	recipients, err := store.New(db).RewardRecipients(ctx, p.AccountID)
	if err != nil {
		return false, unavailable()
	}
	for _, r := range recipients {
		if r.AccountID == account && r.Level == level && r.AccountID != p.AccountID {
			return true, nil
		}
	}
	return false, nil
}

// CheckRewardAccess is also called by the sole access executor before writes/final commit.
// Unrelated bonus operations keep their existing source guard.
func (s *Service) CheckRewardAccess(ctx context.Context, tx pgx.Tx, account, operation uuid.UUID) (string, error) {
	var db store.DBTX = s.pool
	if tx != nil {
		db = tx
	}
	r, err := store.New(db).RewardForAccess(ctx, &operation)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", unavailable()
	}
	if r.AccountID != account || r.SourceOrderID == nil {
		return "referral_source_invalid", nil
	}
	valid, err := s.rewardFunded(ctx, tx, *r.SourceOrderID, account, r.RewardLevel.Int16)
	if err != nil {
		return "", unavailable()
	}
	if !valid {
		return "referral_funding_invalid", nil
	}
	return "", nil
}

// RecordRewardAccessTx runs after native readback in the access owner's final transaction.
func (s *Service) RecordRewardAccessTx(ctx context.Context, tx pgx.Tx, account, operation uuid.UUID) error {
	if tx == nil {
		return unavailable()
	}
	q := store.New(tx)
	r, err := q.RewardForAccess(ctx, &operation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	if reason, err := s.CheckRewardAccess(ctx, tx, account, operation); err != nil || reason != "" {
		return unavailable()
	}
	// Funding/order locks precede reward locks, matching receipt callbacks and grant persistence.
	if _, err = q.LockNativeReward(ctx, r.ID); err != nil {
		return unavailable()
	}
	n, err := q.CompleteReward(ctx, store.CompleteRewardParams{ID: r.ID, RewardedAt: pgtype.Timestamptz{Time: s.now(), Valid: true}})
	if err != nil {
		return unavailable()
	}
	if n == 0 {
		return nil
	}
	reason := "reward=" + r.ID.String()
	system := true
	return auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: account, CreatedAt: s.now(), Action: "referral_reward_granted", Reason: &reason, AccessOperationID: &operation, SystemActor: &system})
}

// ProcessReward prepares at most one compensation intent; AccessWorker owns panel writes.
func (s *Service) ProcessReward(ctx context.Context, id uuid.UUID) error {
	r, err := store.New(s.pool).NativeReward(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	if r.RewardedAt.Valid {
		return nil
	}
	if s.subscriptions == nil || r.SourceOrderID == nil {
		return unavailable()
	}
	valid, err := s.rewardFunded(ctx, nil, *r.SourceOrderID, r.AccountID, r.RewardLevel.Int16)
	if err != nil {
		return unavailable()
	}
	if !valid {
		return river.JobSnooze(time.Minute)
	}
	if r.AccessOperationID != nil {
		_, err := s.subscriptions.GetBonusOperation(ctx, r.AccountID, *r.AccessOperationID)
		if err != nil {
			return rewardRetry(err)
		}
		return river.JobSnooze(10 * time.Second)
	}
	_, err = s.subscriptions.GrantBonusDays(ctx, r.AccountID, r.ID, int(r.Days), "referral_reward", func(ctx context.Context, tx pgx.Tx, op subscriptions.AccessOperation) error {
		valid, err := s.rewardFunded(ctx, tx, *r.SourceOrderID, r.AccountID, r.RewardLevel.Int16)
		if err != nil {
			return unavailable()
		}
		if !valid {
			return failure(409, "REFERRAL_SOURCE_CHANGED")
		}
		q := store.New(tx)
		current, err := q.LockNativeReward(ctx, id)
		if err != nil || current.AccountID != r.AccountID || current.Days != r.Days || current.AccessOperationID != nil || current.RewardedAt.Valid {
			return unavailable()
		}
		n, err := q.AttachRewardAccess(ctx, store.AttachRewardAccessParams{ID: id, AccessOperationID: &op.OperationId})
		if err != nil || n != 1 {
			return unavailable()
		}
		return nil
	})
	if err != nil {
		return rewardRetry(err)
	}
	return river.JobSnooze(10 * time.Second)
}

func rewardRetry(err error) error {
	var subscription *subscriptions.Error
	var bonus *Error
	if errors.As(err, &subscription) && subscription.Status < 500 || errors.As(err, &bonus) && bonus.Status == 409 {
		return river.JobSnooze(30 * time.Second)
	}
	return unavailable()
}
