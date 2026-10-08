package accounts

import (
	"context"
	"errors"
	"time"

	"example.com/cabinet/backend/internal/modules/notifications"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) NoticeRecipientTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock bool) (notifications.ReminderRecipient, bool, error) {
	r, err := s.ReminderRecipientTx(ctx, tx, id, lock)
	var fault *Error
	if errors.Is(err, ErrNotFound) || errors.As(err, &fault) && fault.Status == 404 {
		return r, false, nil
	}
	return r, err == nil, err
}
func (s *Service) LockNoticeOperatorTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (bool, error) {
	_, err := s.LockOperatorPair(ctx, tx, id, id)
	return noticeAllowed(err)
}
func noticeAllowed(err error) (bool, error) {
	var fault *Error
	if errors.Is(err, ErrNotFound) || errors.As(err, &fault) && (fault.Status == 401 || fault.Status == 403 || fault.Status == 404) {
		return false, nil
	}
	return err == nil, err
}
func (s *Service) LockNoticePairTx(ctx context.Context, tx pgx.Tx, actor, target uuid.UUID) (notifications.ReminderRecipient, bool, error) {
	_, err := s.LockOperatorPair(ctx, tx, actor, target)
	allowed, err := noticeAllowed(err)
	if err != nil || !allowed {
		return notifications.ReminderRecipient{}, false, err
	}
	r, exists, err := s.NoticeRecipientTx(ctx, tx, target, false)
	return r, exists && r.Eligible, err
}
func (s *Service) WithNoticeDelivery(ctx context.Context, actor, target uuid.UUID, tg, version int64, work func(pgx.Tx) error) (bool, error) {
	if actor == uuid.Nil || target == uuid.Nil || tg <= 0 || tg > 1<<52-1 || version < 0 || work == nil {
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
	r, allowed, err := s.LockNoticePairTx(ctx, tx, actor, target)
	if err != nil || !allowed || r.TelegramID != tg || r.CredentialVersion != version {
		return false, err
	}
	if err = work(tx); err != nil {
		return false, err
	}
	if tx.Commit(ctx) != nil {
		return false, unavailable()
	}
	return true, nil
}
