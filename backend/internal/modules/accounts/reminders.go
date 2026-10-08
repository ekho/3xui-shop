package accounts

import (
	"context"
	"example.com/cabinet/backend/internal/modules/notifications"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) ReminderAudienceTx(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	if tx == nil {
		return nil, failure(400, "INVALID_INPUT")
	}
	rows, err := tx.Query(ctx, `SELECT id FROM accounts ORDER BY id`)
	if err != nil {
		return nil, unavailable()
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) != nil {
			return nil, unavailable()
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		return nil, unavailable()
	}
	return ids, nil
}
func (s *Service) ReminderRecipientTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock bool) (notifications.ReminderRecipient, error) {
	if tx == nil || id == uuid.Nil {
		return notifications.ReminderRecipient{}, failure(400, "INVALID_INPUT")
	}
	var a Snapshot
	var err error
	if lock {
		a, err = s.Lock(ctx, tx, id)
	} else {
		a, err = s.LookupTx(ctx, tx, id)
	}
	if err != nil {
		return notifications.ReminderRecipient{}, err
	}
	r := notifications.ReminderRecipient{AccountID: a.ID, CredentialVersion: a.CredentialVersion, Locale: a.Locale}
	r.Eligible = SourceEligible(a) && !a.Restricted && a.TermsVersion != nil && a.PrivacyVersion != nil && (a.SourceKind != "telegram" || !a.TelegramLoginDisabled)
	if a.EmailKey != nil {
		r.Email = *a.EmailKey
	}
	r.EmailAvailable = r.Eligible && r.Email != "" && a.VerifiedAt != nil && a.PasswordSet
	if !a.TelegramLoginDisabled && a.TelegramID != nil {
		r.TelegramID = *a.TelegramID
	}
	return r, nil
}
