package platform

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"time"
)

func accountError(err error) error {
	var domain *accounts.Error
	if errors.As(err, &domain) {
		return &Error{Status: domain.Status, Code: domain.Code, Message: domain.Message, Details: domain.Details, RetryAfter: domain.RetryAfter}
	}
	if errors.Is(err, accounts.ErrNotFound) {
		return pgx.ErrNoRows
	}
	return err
}
func (s *Service) Register(ctx context.Context, in wire.RegisterInput) (wire.RegistrationAccepted, error) {
	out, err := s.accounts.Register(ctx, accounts.RegisterInput{Email: string(in.Email), Locale: string(in.Locale), AcceptedTermsVersion: in.AcceptedTermsVersion, AcceptedPrivacyVersion: in.AcceptedPrivacyVersion})
	return wire.RegistrationAccepted(out), accountError(err)
}
func (s *Service) ResendVerification(ctx context.Context, in wire.ResendInput) (wire.ResendAccepted, error) {
	out, err := s.accounts.ResendVerification(ctx, accounts.ResendInput{Email: string(in.Email)})
	return wire.ResendAccepted(out), accountError(err)
}
func (s *Service) VerifyEmail(ctx context.Context, in wire.VerifyInput) (wire.VerifyResult, error) {
	out, err := s.accounts.VerifyEmail(ctx, accounts.VerifyInput{ChallengeId: in.ChallengeId, Code: in.Code, Token: in.Token, NewPassword: in.NewPassword})
	return wire.VerifyResult{Verified: wire.VerifyResultVerified(out.Verified)}, accountError(err)
}
func publicAccount(a accounts.Snapshot) wire.Account {
	email := ""
	if a.EmailKey != nil {
		email = *a.EmailKey
	}
	return wire.Account{AccountId: a.ID, Email: openapi_types.Email(email), EmailVerified: a.VerifiedAt != nil, Locale: wire.AccountLocale(a.Locale), TelegramLinked: false}
}
func (s *Service) Login(ctx context.Context, in wire.LoginInput, ip string) (wire.LoginResult, string, error) {
	out, raw, err := s.accounts.Login(ctx, accounts.LoginInput{Email: string(in.Email), Password: in.Password}, ip)
	return wire.LoginResult{Account: publicAccount(out.Account), CsrfToken: out.CsrfToken}, raw, accountError(err)
}
func (s *Service) Authenticate(ctx context.Context, raw string) (wire.AccountResult, error) {
	out, err := s.accounts.Authenticate(ctx, raw)
	if err != nil {
		return wire.AccountResult{}, accountError(err)
	}
	available, err := s.canRequestTrial(ctx, store.New(s.pool), legacyAccount(out.Account))
	if err != nil {
		return wire.AccountResult{}, err
	}
	return wire.AccountResult{Account: publicAccount(out.Account), CsrfToken: out.CsrfToken, Capabilities: wire.Capabilities{TrialAvailable: available}}, nil
}
func (s *Service) Logout(ctx context.Context, raw string) error {
	return accountError(s.accounts.Logout(ctx, raw))
}
func (s *Service) RequestPasswordReset(ctx context.Context, in wire.PasswordResetInput, ip string) (wire.PasswordResetAccepted, error) {
	out, err := s.accounts.RequestPasswordReset(ctx, accounts.PasswordResetInput{Email: string(in.Email), Locale: string(in.Locale)}, ip)
	return wire.PasswordResetAccepted(out), accountError(err)
}
func (s *Service) CompletePasswordReset(ctx context.Context, in wire.PasswordResetCompleteInput, ip string) error {
	return accountError(s.accounts.CompletePasswordReset(ctx, accounts.PasswordResetCompleteInput{ChallengeId: in.ChallengeId, Code: in.Code, Token: in.Token, NewPassword: in.NewPassword}, ip))
}

type SessionRotation = accounts.SessionRotation

func (s *Service) ChangePassword(ctx context.Context, raw string, in wire.PasswordChangeInput, ip string) (SessionRotation, error) {
	out, err := s.accounts.ChangePassword(ctx, raw, accounts.PasswordChangeInput(in), ip)
	return out, accountError(err)
}
func (s *Service) RevokeOtherSessions(ctx context.Context, raw string, in wire.CurrentPasswordInput, ip string) (SessionRotation, error) {
	out, err := s.accounts.RevokeOtherSessions(ctx, raw, accounts.CurrentPasswordInput(in), ip)
	return out, accountError(err)
}
func (s *Service) GetSessionContext(ctx context.Context, raw string) (wire.SessionContext, error) {
	out, err := s.accounts.GetSessionContext(ctx, raw)
	return wire.SessionContext(out), accountError(err)
}
func (s *Service) GetAccountSecurity(ctx context.Context, raw string) (wire.AccountSecurity, error) {
	out, err := s.accounts.GetAccountSecurity(ctx, raw)
	var pending *wire.PendingEmailChange
	if p := out.PendingEmailChange; p != nil {
		pending = &wire.PendingEmailChange{CurrentEmailConfirmed: p.CurrentEmailConfirmed, NewEmailConfirmed: p.NewEmailConfirmed, NewEmail: openapi_types.Email(p.NewEmail), ExpiresAt: p.ExpiresAt}
	}
	return wire.AccountSecurity{Email: openapi_types.Email(out.Email), HasOtherSessions: out.HasOtherSessions, PendingEmailChange: pending}, accountError(err)
}
func (s *Service) RequestEmailChange(ctx context.Context, raw string, in wire.EmailChangeInput, ip string) (wire.EmailChangeAccepted, error) {
	out, err := s.accounts.RequestEmailChange(ctx, raw, accounts.EmailChangeInput{CurrentPassword: in.CurrentPassword, NewEmail: string(in.NewEmail)}, ip)
	return wire.EmailChangeAccepted(out), accountError(err)
}
func (s *Service) ConfirmEmailChange(ctx context.Context, in wire.EmailChangeConfirmInput, ip string) (wire.EmailChangeResult, error) {
	out, err := s.accounts.ConfirmEmailChange(ctx, accounts.EmailChangeConfirmInput{ChallengeId: in.ChallengeId, Code: in.Code, Token: in.Token}, ip)
	return wire.EmailChangeResult(out), accountError(err)
}
func (s *Service) CancelEmailChange(ctx context.Context, raw string) error {
	return accountError(s.accounts.CancelEmailChange(ctx, raw))
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
