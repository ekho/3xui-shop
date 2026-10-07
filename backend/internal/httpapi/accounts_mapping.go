package httpapi

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func accountError(err error) error {
	var domain *accounts.Error
	if errors.As(err, &domain) {
		return &apiError{Status: domain.Status, Code: domain.Code, Message: domain.Message, Details: domain.Details, RetryAfter: domain.RetryAfter}
	}
	if errors.Is(err, accounts.ErrTelegramExists) {
		return failure(409, "TRIAL_ALREADY_USED")
	}
	if errors.Is(err, accounts.ErrNotFound) {
		return pgx.ErrNoRows
	}
	return err
}

func (a *API) register(ctx context.Context, in wire.RegisterInput) (wire.RegistrationAccepted, error) {
	out, err := a.accounts.Register(ctx, accounts.RegisterInput{Email: string(in.Email), Locale: string(in.Locale), AcceptedTermsVersion: in.AcceptedTermsVersion, AcceptedPrivacyVersion: in.AcceptedPrivacyVersion})
	return wire.RegistrationAccepted(out), accountError(err)
}

func (a *API) resendVerification(ctx context.Context, in wire.ResendInput) (wire.ResendAccepted, error) {
	out, err := a.accounts.ResendVerification(ctx, accounts.ResendInput{Email: string(in.Email)})
	return wire.ResendAccepted(out), accountError(err)
}

func (a *API) verifyEmail(ctx context.Context, in wire.VerifyInput) (wire.VerifyResult, error) {
	out, err := a.accounts.VerifyEmail(ctx, accounts.VerifyInput{ChallengeId: in.ChallengeId, Code: in.Code, Token: in.Token, NewPassword: in.NewPassword})
	return wire.VerifyResult{Verified: wire.VerifyResultVerified(out.Verified)}, accountError(err)
}

func publicAccount(a accounts.Snapshot) wire.Account {
	email := ""
	if a.EmailKey != nil {
		email = *a.EmailKey
	}
	return wire.Account{AccountId: a.ID, Email: openapi_types.Email(email), EmailVerified: a.VerifiedAt != nil, Locale: wire.AccountLocale(a.Locale), TelegramLinked: false}
}

func (a *API) trialCapabilities(ctx context.Context, account accounts.Snapshot) (wire.Capabilities, error) {
	available, err := a.subscriptions.CanRequestTrial(ctx, account)
	if err != nil {
		return wire.Capabilities{}, subscriptionError(err)
	}
	out := wire.Capabilities{TrialAvailable: available}
	if a.subscriptions.TrialMode(account) == "activate" {
		mode := wire.CapabilitiesTrialMode("activate")
		out.TrialMode = &mode
	}
	return out, nil
}

func (a *API) login(ctx context.Context, in wire.LoginInput, ip string) (wire.LoginResult, string, error) {
	out, raw, err := a.accounts.Login(ctx, accounts.LoginInput{Email: string(in.Email), Password: in.Password}, ip)
	return wire.LoginResult{Account: publicAccount(out.Account), CsrfToken: out.CsrfToken}, raw, accountError(err)
}

func (a *API) authenticate(ctx context.Context, raw string) (wire.AccountResult, error) {
	out, err := a.accounts.Authenticate(ctx, raw)
	if err != nil {
		return wire.AccountResult{}, accountError(err)
	}
	capabilities, err := a.trialCapabilities(ctx, out.Account)
	if err != nil {
		return wire.AccountResult{}, err
	}
	return wire.AccountResult{Account: publicAccount(out.Account), CsrfToken: out.CsrfToken, Capabilities: capabilities}, nil
}

func (a *API) logout(ctx context.Context, raw string) error {
	return accountError(a.accounts.Logout(ctx, raw))
}

func (a *API) requestPasswordReset(ctx context.Context, in wire.PasswordResetInput, ip string) (wire.PasswordResetAccepted, error) {
	out, err := a.accounts.RequestPasswordReset(ctx, accounts.PasswordResetInput{Email: string(in.Email), Locale: string(in.Locale)}, ip)
	return wire.PasswordResetAccepted(out), accountError(err)
}

func (a *API) completePasswordReset(ctx context.Context, in wire.PasswordResetCompleteInput, ip string) error {
	return accountError(a.accounts.CompletePasswordReset(ctx, accounts.PasswordResetCompleteInput{ChallengeId: in.ChallengeId, Code: in.Code, Token: in.Token, NewPassword: in.NewPassword}, ip))
}

func (a *API) changePassword(ctx context.Context, raw string, in wire.PasswordChangeInput, ip string) (accounts.SessionRotation, error) {
	out, err := a.accounts.ChangePassword(ctx, raw, accounts.PasswordChangeInput(in), ip)
	return out, accountError(err)
}

func (a *API) revokeOtherSessions(ctx context.Context, raw string, in wire.CurrentPasswordInput, ip string) (accounts.SessionRotation, error) {
	out, err := a.accounts.RevokeOtherSessions(ctx, raw, accounts.CurrentPasswordInput(in), ip)
	return out, accountError(err)
}

func (a *API) getSessionContext(ctx context.Context, raw string) (wire.SessionContext, error) {
	out, err := a.accounts.GetSessionContext(ctx, raw)
	return wire.SessionContext(out), accountError(err)
}

func (a *API) getAccountSecurity(ctx context.Context, raw string) (wire.AccountSecurity, error) {
	out, err := a.accounts.GetAccountSecurity(ctx, raw)
	var pending *wire.PendingEmailChange
	if p := out.PendingEmailChange; p != nil {
		pending = &wire.PendingEmailChange{CurrentEmailConfirmed: p.CurrentEmailConfirmed, NewEmailConfirmed: p.NewEmailConfirmed, NewEmail: openapi_types.Email(p.NewEmail), ExpiresAt: p.ExpiresAt}
	}
	return wire.AccountSecurity{Email: openapi_types.Email(out.Email), HasOtherSessions: out.HasOtherSessions, PendingEmailChange: pending}, accountError(err)
}

func (a *API) requestEmailChange(ctx context.Context, raw string, in wire.EmailChangeInput, ip string) (wire.EmailChangeAccepted, error) {
	out, err := a.accounts.RequestEmailChange(ctx, raw, accounts.EmailChangeInput{CurrentPassword: in.CurrentPassword, NewEmail: string(in.NewEmail)}, ip)
	return wire.EmailChangeAccepted(out), accountError(err)
}

func (a *API) confirmEmailChange(ctx context.Context, in wire.EmailChangeConfirmInput, ip string) (wire.EmailChangeResult, error) {
	out, err := a.accounts.ConfirmEmailChange(ctx, accounts.EmailChangeConfirmInput{ChallengeId: in.ChallengeId, Code: in.Code, Token: in.Token}, ip)
	return wire.EmailChangeResult(out), accountError(err)
}

func (a *API) cancelEmailChange(ctx context.Context, raw string) error {
	return accountError(a.accounts.CancelEmailChange(ctx, raw))
}

func (a *API) setOperatorRestriction(ctx context.Context, actor, target, key uuid.UUID, in wire.OperatorRestrictionInput) (wire.OperatorRestrictionResult, error) {
	out, err := a.accounts.SetOperatorRestriction(ctx, actor, target, key, accounts.OperatorRestrictionInput(in))
	return wire.OperatorRestrictionResult(out), accountError(err)
}

func subscriptionError(err error) error {
	if err == nil {
		return nil
	}
	var a *subscriptions.Error
	if errors.As(err, &a) {
		return &apiError{Status: a.Status, Code: a.Code, Message: a.Message, Details: a.Details}
	}
	var v *vpn.Error
	if errors.As(err, &v) {
		return &apiError{Status: v.Status, Code: v.Code, Message: v.Message}
	}
	return accountError(err)
}
