package platform

import (
	"context"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

func (s *Service) accountByID(ctx context.Context, id uuid.UUID) (store.Account, error) {
	out, err := s.accounts.Lookup(ctx, id)
	return legacyAccount(out), accountError(err)
}
func (s *Service) accountByIDTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (store.Account, error) {
	out, err := s.accounts.LookupTx(ctx, tx, id)
	return legacyAccount(out), accountError(err)
}
func (s *Service) lockAccount(ctx context.Context, tx pgx.Tx, id uuid.UUID) (store.Account, error) {
	out, err := s.accounts.Lock(ctx, tx, id)
	return legacyAccount(out), accountError(err)
}
func (s *Service) accountVPNBan(ctx context.Context, id uuid.UUID) (bool, error) {
	out, err := s.accounts.VPNBanned(ctx, id)
	return out, accountError(err)
}
func legacyAccount(a accounts.Snapshot) store.Account {
	return store.Account{ID: a.ID, VpnID: a.VpnID, EmailKey: legacyText(a.EmailKey), Locale: a.Locale, PasswordHash: pgtype.Text{Valid: a.PasswordSet},
		VerifiedAt: legacyTime(a.VerifiedAt), Restricted: a.Restricted, SubID: a.SubID, PanelKey: a.PanelKey,
		TermsVersion: legacyText(a.TermsVersion), PrivacyVersion: legacyText(a.PrivacyVersion), TelegramID: legacyInt(a.TelegramID), LegacyUserID: legacyInt(a.LegacyUserID),
		AssignedPanelID: legacyText(a.AssignedPanelID), HadSubscription: a.HadSubscription, CredentialVersion: a.CredentialVersion, VpnBanned: a.VpnBanned,
		Kind: a.Kind, DisplayName: legacyText(a.DisplayName), CreatedAt: legacyTime(a.CreatedAt), RestrictionChangedAt: legacyTime(a.RestrictionChangedAt),
		RestrictionOperatorAccountID: a.RestrictionOperatorAccountID, AccessProfile: legacyText(a.AccessProfile)}
}
func legacyTime(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	return stamp(*v)
}

func legacyInt(id *int64) pgtype.Int8 {
	if id == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *id, Valid: true}
}

func legacyText(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}
