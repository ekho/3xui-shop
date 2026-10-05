package accounts

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

func snapshot(a store.Account) Snapshot {
	return Snapshot{ID: a.ID, VpnID: a.VpnID, EmailKey: textPointer(a.EmailKey), Locale: a.Locale, PasswordSet: a.PasswordHash.Valid,
		VerifiedAt: timePointer(a.VerifiedAt), Restricted: a.Restricted, SubID: a.SubID, PanelKey: a.PanelKey,
		TermsVersion: textPointer(a.TermsVersion), PrivacyVersion: textPointer(a.PrivacyVersion), TelegramID: intPointer(a.TelegramID), LegacyUserID: intPointer(a.LegacyUserID),
		AssignedPanelID: textPointer(a.AssignedPanelID), HadSubscription: a.HadSubscription, CredentialVersion: a.CredentialVersion, VpnBanned: a.VpnBanned,
		Kind: a.Kind, DisplayName: textPointer(a.DisplayName), CreatedAt: timePointer(a.CreatedAt), RestrictionChangedAt: timePointer(a.RestrictionChangedAt),
		RestrictionOperatorAccountID: a.RestrictionOperatorAccountID, AccessProfile: textPointer(a.AccessProfile)}
}
func (s *Service) Lookup(ctx context.Context, id uuid.UUID) (Snapshot, error) {
	a, err := store.New(s.pool).AccountByID(ctx, id)
	return accountResult(a, err)
}

// Lock participates in the caller's transaction; the caller commits or rolls back.
func (s *Service) Lock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Snapshot, error) {
	a, err := store.New(tx).LockAccount(ctx, id)
	return accountResult(a, err)
}
func accountResult(a store.Account, err error) (Snapshot, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, unavailable()
	}
	return snapshot(a), nil
}

func textPointer(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
func intPointer(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
func timePointer(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}
