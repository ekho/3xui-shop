package accounts

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"strconv"
	"time"
)

var ErrTelegramRetired = failure(409, "TELEGRAM_UNLINKED")

func lockTelegramIdentity(ctx context.Context, tx pgx.Tx, id int64) error {
	if id <= 0 {
		return failure(400, "INVALID_INPUT")
	}
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('telegram-account:'||$1::text,0))`, strconv.FormatInt(id, 10)).Scan(&locked); err != nil {
		return unavailable()
	}
	if !locked {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	return nil
}

func (s *Service) StartTelegramLink(ctx context.Context, rawWeb string, in CurrentPasswordInput, ip string) (TelegramLinkChallenge, error) {
	empty := TelegramLinkChallenge{}
	a, _, err := s.authenticateCurrentPassword(ctx, rawWeb, in.CurrentPassword, ip)
	if err != nil {
		return empty, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	a, _, err = s.revalidateCredentialSession(ctx, q, rawWeb, a)
	if err != nil {
		return empty, err
	}
	if a.TelegramID.Valid {
		return empty, failure(409, "TELEGRAM_ALREADY_LINKED")
	}
	if err = s.lockCredentialEmails(ctx, tx, []string{a.EmailKey.String}); err != nil {
		return empty, err
	}
	if q.RevokeIdentityPurpose(ctx, store.RevokeIdentityPurposeParams{AccountID: &a.ID, Purpose: "telegram_link"}) != nil {
		return empty, unavailable()
	}
	id, token, now := uuid.New(), opaque(), s.now()
	out := TelegramLinkChallenge{LinkToken: token, ExpiresAt: now.Add(10 * time.Minute)}
	if q.AddTelegramLinkProof(ctx, store.AddTelegramLinkProofParams{ID: id, AccountID: a.ID, Email: a.EmailKey.String, CredentialVersion: a.CredentialVersion, TokenHash: digest(token), CodeHash: s.codeDigest(id, token), CreatedAt: stamp(now), ExpiresAt: stamp(out.ExpiresAt)}) != nil || s.credentialAudit(ctx, tx, a.ID, "telegram_link_requested") != nil || tx.Commit(ctx) != nil {
		return empty, unavailable()
	}
	return out, nil
}

func (s *Service) ConfirmTelegramLink(ctx context.Context, in ConfirmTelegramLinkInput) (TelegramLinkResult, error) {
	empty := TelegramLinkResult{}
	if in.TelegramID <= 0 || in.TelegramID > 1<<52-1 || !validOperatorName(in.DisplayName) || (in.Locale != "ru" && in.Locale != "en") || !validStartParam(in.StartParam) {
		return empty, failure(400, "INVALID_INPUT")
	}
	if err := s.identityConsent(in.AcceptedTermsVersion, in.AcceptedPrivacyVersion); err != nil {
		return empty, err
	}
	proof, _, err := s.lookupCredential(ctx, &in.LinkToken, nil, nil)
	if err != nil {
		return empty, err
	}
	if proof.Purpose != "telegram_link" || proof.AccountID == nil {
		return empty, failure(400, "INVALID_VERIFICATION")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	if err = lockTelegramIdentity(ctx, tx, in.TelegramID); err != nil {
		return empty, err
	}
	q := store.New(tx)
	a, err := q.LockAccount(ctx, *proof.AccountID)
	if err != nil {
		return empty, unavailable()
	}
	if a.Restricted {
		return empty, failure(403, "ACCOUNT_RESTRICTED")
	}
	if a.Kind != "web" || !a.EmailKey.Valid || !a.VerifiedAt.Valid || !a.PasswordHash.Valid || a.TelegramLoginDisabled {
		return empty, failure(400, "INVALID_VERIFICATION")
	}
	if a.TelegramID.Valid && a.TelegramID.Int64 != in.TelegramID {
		return empty, failure(409, "IDENTITY_CONFLICT")
	}
	owner, err := q.AccountByTelegramID(ctx, pgtype.Int8{Int64: in.TelegramID, Valid: true})
	if err == nil && owner.ID != a.ID {
		return empty, failure(409, "IDENTITY_CONFLICT")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return empty, unavailable()
	}
	retired, err := q.TelegramReservationOwner(ctx, in.TelegramID)
	if err == nil && retired != a.ID {
		return empty, failure(409, "IDENTITY_CONFLICT")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return empty, unavailable()
	}
	emails, err := s.credentialEmails(ctx, tx, a)
	if err != nil {
		return empty, err
	}
	if err = s.lockCredentialEmails(ctx, tx, emails); err != nil {
		return empty, err
	}
	proof, err = q.LockCredentialProof(ctx, proof.ID)
	if err != nil {
		return empty, unavailable()
	}
	if proof.Revoked || !s.now().Before(proof.TokenExpiresAt.Time) || proof.OriginalEmail != a.EmailKey.String || proof.TargetEmail != a.EmailKey.String {
		return empty, failure(400, "INVALID_VERIFICATION")
	}
	if proof.UsedAt.Valid {
		if a.TelegramID.Valid && a.TelegramID.Int64 == in.TelegramID && a.CredentialVersion == proof.CredentialVersion+1 {
			return TelegramLinkResult{Linked: true}, nil
		}
		return empty, failure(400, "INVALID_VERIFICATION")
	}
	if err = s.checkCredentialProof(ctx, tx, proof, a, true, nil); err != nil {
		return empty, err
	}
	if q.BindTelegramIdentity(ctx, store.BindTelegramIdentityParams{ID: a.ID, TelegramID: in.TelegramID, DisplayName: in.DisplayName, TermsVersion: in.AcceptedTermsVersion, PrivacyVersion: in.AcceptedPrivacyVersion, Now: stamp(s.now())}) != nil || q.ReactivateTelegramIdentity(ctx, store.ReactivateTelegramIdentityParams{TelegramID: in.TelegramID, AccountID: a.ID}) != nil {
		return empty, unavailable()
	}
	if in.StartParam != "" && q.PreserveTelegramStartParam(ctx, store.PreserveTelegramStartParamParams{ID: a.ID, TelegramStartParam: pgtype.Text{String: in.StartParam, Valid: true}}) != nil {
		return empty, unavailable()
	}
	if q.ConsumeCredentialProof(ctx, store.ConsumeCredentialProofParams{ID: proof.ID, UsedAt: stamp(s.now())}) != nil || q.DeleteAccountSessions(ctx, a.ID) != nil {
		return empty, unavailable()
	}
	if err = s.revokeCredentialProofs(ctx, tx, a.ID); err != nil {
		return empty, err
	}
	if s.identityGrantAudit(ctx, tx, a.ID, "telegram_linked", in.AcceptedTermsVersion, in.AcceptedPrivacyVersion) != nil || tx.Commit(ctx) != nil {
		return empty, unavailable()
	}
	return TelegramLinkResult{Linked: true}, nil
}

func (s *Service) UnlinkTelegram(ctx context.Context, rawWeb string, in CurrentPasswordInput, ip string) (TelegramUnlinkResult, error) {
	empty := TelegramUnlinkResult{}
	a, _, err := s.authenticateCurrentPassword(ctx, rawWeb, in.CurrentPassword, ip)
	if err != nil {
		return empty, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	if a.TelegramID.Valid {
		if err = lockTelegramIdentity(ctx, tx, a.TelegramID.Int64); err != nil {
			return empty, err
		}
	}
	q := store.New(tx)
	a, _, err = s.revalidateCredentialSession(ctx, q, rawWeb, a)
	if err != nil {
		return empty, err
	}
	if !a.TelegramID.Valid {
		return empty, nil
	}
	if a.LegacyUserID.Valid {
		return empty, failure(409, "UNLINK_UNAVAILABLE")
	}
	rows, err := q.RetireTelegramIdentity(ctx, store.RetireTelegramIdentityParams{TelegramID: a.TelegramID.Int64, AccountID: a.ID, RetiredAt: stamp(s.now())})
	if err != nil {
		return empty, unavailable()
	}
	if rows != 1 {
		return empty, failure(409, "IDENTITY_CONFLICT")
	}
	if q.ClearTelegramIdentity(ctx, a.ID) != nil || q.BumpCredentialVersion(ctx, a.ID) != nil || q.DeleteAccountSessions(ctx, a.ID) != nil {
		return empty, unavailable()
	}
	if err = s.revokeCredentialProofs(ctx, tx, a.ID); err != nil {
		return empty, err
	}
	if s.credentialAudit(ctx, tx, a.ID, "telegram_unlinked") != nil || tx.Commit(ctx) != nil {
		return empty, unavailable()
	}
	return TelegramUnlinkResult{Changed: true}, nil
}
