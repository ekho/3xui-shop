package accounts

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) GetIdentity(ctx context.Context, id uuid.UUID) (IdentityContext, error) {
	q := store.New(s.pool)
	a, err := q.AccountByID(ctx, id)
	if err != nil {
		return IdentityContext{}, unavailable()
	}
	if a.Restricted {
		return IdentityContext{}, failure(403, "ACCOUNT_RESTRICTED")
	}
	account := snapshot(a)
	out := IdentityContext{Email: account.EmailKey, SourceKind: account.SourceKind,
		IndependentLogin: a.Kind == "web" && a.VerifiedAt.Valid && a.PasswordHash.Valid,
		TelegramLinked:   a.TelegramID.Valid}
	out.CanUnlink = out.IndependentLogin && out.TelegramLinked && !a.LegacyUserID.Valid
	if out.CanUnlink && s.cfg.CanUnlinkTelegram != nil {
		out.CanUnlink, err = s.cfg.CanUnlinkTelegram(ctx, nil, id)
		if err != nil {
			return IdentityContext{}, unavailable()
		}
	}
	if out.TelegramLinked && !out.CanUnlink {
		reason := "INDEPENDENT_LOGIN_REQUIRED"
		if a.LegacyUserID.Valid || out.IndependentLogin {
			reason = "UNLINK_UNAVAILABLE"
		}
		out.UnlinkBlockedReason = &reason
	}
	pending, err := q.PendingIdentityEmail(ctx, store.PendingIdentityEmailParams{AccountID: &id, Now: stamp(s.now())})
	if err == nil {
		out.PendingInitialEmail = &IdentityEmailPending{ChallengeId: pending.ID, Email: pending.TargetEmail, ExpiresAt: pending.CodeExpiresAt.Time}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return IdentityContext{}, unavailable()
	}
	return out, nil
}

// Keep Telegram/account/session/email lock order identical for identity mutations.
func (s *Service) lockIdentityMini(ctx context.Context, tx pgx.Tx, raw string, known Snapshot) (store.Account, error) {
	if known.TelegramID == nil {
		return store.Account{}, failure(401, "INVALID_CREDENTIALS")
	}
	if err := s.CheckTelegramAvailable(ctx, tx, *known.TelegramID); !errors.Is(err, ErrTelegramExists) {
		if err != nil {
			return store.Account{}, err
		}
		return store.Account{}, failure(401, "INVALID_CREDENTIALS")
	}
	q := store.New(tx)
	a, err := q.LockAccount(ctx, known.ID)
	if err != nil {
		return a, unavailable()
	}
	session, err := q.AuthenticateSession(ctx, store.AuthenticateSessionParams{IDHash: digest(raw), Now: stamp(s.now())})
	if errors.Is(err, pgx.ErrNoRows) {
		return a, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return a, unavailable()
	}
	if session.AuthSource != "telegram" || session.AccountID != a.ID || !session.TelegramID.Valid || !a.TelegramID.Valid || session.TelegramID.Int64 != a.TelegramID.Int64 || a.TelegramID.Int64 != *known.TelegramID || a.CredentialVersion != known.CredentialVersion || a.TelegramLoginDisabled {
		return a, failure(401, "INVALID_CREDENTIALS")
	}
	if a.Restricted {
		return a, failure(403, "ACCOUNT_RESTRICTED")
	}
	return a, nil
}

func identityEmailAvailable(ctx context.Context, q *store.Queries, email string) (bool, error) {
	_, err := q.AccountByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, unavailable()
	}
	return false, nil
}

func (s *Service) identityConsent(terms, privacy string) error {
	if s.cfg.TermsVersion == "" || s.cfg.PrivacyVersion == "" {
		return unavailable()
	}
	if terms != s.cfg.TermsVersion || privacy != s.cfg.PrivacyVersion {
		return failure(400, "INVALID_INPUT")
	}
	return nil
}

// Keep historical legal acceptance even after the account's current versions change.
func (s *Service) identityGrantAudit(ctx context.Context, tx pgx.Tx, id uuid.UUID, action, terms, privacy string) error {
	reason := "terms=" + terms + " privacy=" + privacy
	if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), AccountID: id, Action: action, Reason: &reason}) != nil {
		return unavailable()
	}
	return nil
}
