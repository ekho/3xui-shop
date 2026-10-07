package accounts

import (
	"context"
	"encoding/base64"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"strings"
	"time"
)

func (s *Service) LimitMiniAppLogin(ctx context.Context, ip string) error {
	return s.limitCredentialIP(ctx, ip)
}

func (s *Service) StartTelegramSession(ctx context.Context, in TelegramSessionInput) (Authentication, string, error) {
	empty := Authentication{}
	if in.TelegramID <= 0 || in.TelegramID > 1<<52-1 || !validOperatorName(in.DisplayName) || (in.Locale != "ru" && in.Locale != "en") || !validStartParam(in.StartParam) {
		return empty, "", failure(400, "INVALID_INPUT")
	}
	if s.cfg.TermsVersion == "" || s.cfg.PrivacyVersion == "" {
		return empty, "", unavailable()
	}
	consent := in.AcceptedTermsVersion != "" || in.AcceptedPrivacyVersion != ""
	if consent && (in.AcceptedTermsVersion != s.cfg.TermsVersion || in.AcceptedPrivacyVersion != s.cfg.PrivacyVersion) {
		return empty, "", failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, "", unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	err = s.CheckTelegramAvailable(ctx, tx, in.TelegramID)
	if err != nil && !errors.Is(err, ErrTelegramExists) {
		return empty, "", err
	}
	var account store.Account
	if errors.Is(err, ErrTelegramExists) {
		found, e := q.AccountByTelegramID(ctx, pgtype.Int8{Int64: in.TelegramID, Valid: true})
		if e != nil {
			return empty, "", unavailable()
		}
		account, err = q.LockAccount(ctx, found.ID)
		if err != nil {
			return empty, "", unavailable()
		}
		if account.Restricted {
			return empty, "", failure(403, "ACCOUNT_RESTRICTED")
		}
	} else {
		if !consent {
			return empty, "", failure(409, "CONSENT_REQUIRED")
		}
		created, e := s.CreateTelegram(ctx, tx, in.TelegramInput)
		if e != nil {
			return empty, "", e
		}
		account, err = q.AccountByID(ctx, created.ID)
		if err != nil {
			return empty, "", unavailable()
		}
		if s.credentialAudit(ctx, tx, account.ID, "mini_app_account_created") != nil {
			return empty, "", unavailable()
		}
	}
	if account.TelegramLoginDisabled || !SourceEligible(snapshot(account)) {
		return empty, "", failure(401, "INVALID_CREDENTIALS")
	}
	if !account.TermsVersion.Valid || !account.PrivacyVersion.Valid {
		if !consent {
			return empty, "", failure(409, "CONSENT_REQUIRED")
		}
		if q.AcceptTelegramPolicies(ctx, store.AcceptTelegramPoliciesParams{ID: account.ID, TermsVersion: pgtype.Text{String: in.AcceptedTermsVersion, Valid: true}, PrivacyVersion: pgtype.Text{String: in.AcceptedPrivacyVersion, Valid: true}, PolicyAcceptedAt: stamp(s.now())}) != nil {
			return empty, "", unavailable()
		}
		reason := "terms=" + in.AcceptedTermsVersion + " privacy=" + in.AcceptedPrivacyVersion
		if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: "mini_app_consent", AccountID: account.ID, Reason: &reason}) != nil {
			return empty, "", unavailable()
		}
	}
	if in.StartParam != "" && q.PreserveTelegramStartParam(ctx, store.PreserveTelegramStartParamParams{ID: account.ID, TelegramStartParam: pgtype.Text{String: in.StartParam, Valid: true}}) != nil {
		return empty, "", unavailable()
	}
	raw, csrf := "mini_"+opaque(), opaque()
	now := s.now()
	if q.AddTelegramSession(ctx, store.AddTelegramSessionParams{IDHash: digest(raw), AccountID: account.ID, CsrfToken: csrf, CreatedAt: stamp(now), LastSeen: stamp(now), AbsoluteExpiresAt: stamp(now.Add(30 * 24 * time.Hour)), TelegramID: pgtype.Int8{Int64: in.TelegramID, Valid: true}}) != nil || s.credentialAudit(ctx, tx, account.ID, "mini_app_login") != nil {
		return empty, "", unavailable()
	}
	account, err = q.AccountByID(ctx, account.ID)
	if err != nil || tx.Commit(ctx) != nil {
		return empty, "", unavailable()
	}
	return Authentication{Account: snapshot(account), CsrfToken: csrf}, raw, nil
}

func validStartParam(value string) bool {
	if len(value) > 512 {
		return false
	}
	for _, c := range value {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func (s *Service) AuthenticateTelegram(ctx context.Context, raw string, allowRestricted bool) (Authentication, error) {
	empty := Authentication{}
	if !strings.HasPrefix(raw, "mini_") {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, "mini_"))
	if err != nil || len(decoded) != 32 {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	q := store.New(s.pool)
	session, err := q.AuthenticateSession(ctx, store.AuthenticateSessionParams{IDHash: digest(raw), Now: stamp(s.now())})
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return empty, unavailable()
	}
	account, err := q.AccountByID(ctx, session.AccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return empty, unavailable()
	}
	if session.AuthSource != "telegram" || !session.TelegramID.Valid || !account.TelegramID.Valid || session.TelegramID.Int64 != account.TelegramID.Int64 || account.TelegramLoginDisabled || !SourceEligible(snapshot(account)) || !account.TermsVersion.Valid || !account.PrivacyVersion.Valid {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	if account.Restricted && !allowRestricted {
		return empty, failure(403, "ACCOUNT_RESTRICTED")
	}
	return Authentication{Account: snapshot(account), CsrfToken: session.CsrfToken}, nil
}

func (s *Service) LogoutTelegram(ctx context.Context, raw string) error {
	auth, err := s.AuthenticateTelegram(ctx, raw, true)
	var domain *Error
	if errors.As(err, &domain) && domain.Status == 401 {
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if _, err = q.LockAccount(ctx, auth.Account.ID); err != nil {
		return unavailable()
	}
	changed, err := q.DeleteTelegramSession(ctx, digest(raw))
	if err != nil {
		return unavailable()
	}
	if changed > 0 && s.credentialAudit(ctx, tx, auth.Account.ID, "mini_app_logout") != nil {
		return unavailable()
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}
