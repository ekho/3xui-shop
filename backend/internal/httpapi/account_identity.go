package httpapi

import (
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (a *API) GetAccountIdentity(c *echo.Context) error {
	auth, err := a.auth(c, false)
	if err != nil {
		return err
	}
	out, err := a.accounts.GetIdentity(c.Request().Context(), auth.Account.ID)
	if err != nil {
		return accountError(err)
	}
	var email *openapi_types.Email
	if out.Email != nil {
		v := openapi_types.Email(*out.Email)
		email = &v
	}
	result := wire.IdentityContext{Email: email, SourceKind: wire.IdentityContextSourceKind(out.SourceKind), IndependentLogin: out.IndependentLogin, TelegramLinked: out.TelegramLinked, CanUnlink: out.CanUnlink, UnlinkBlockedReason: out.UnlinkBlockedReason}
	if out.PendingInitialEmail != nil {
		p := out.PendingInitialEmail
		result.PendingInitialEmail = &wire.IdentityEmailPending{ChallengeId: p.ChallengeId, Email: openapi_types.Email(p.Email), ExpiresAt: p.ExpiresAt}
	}
	return c.JSON(200, result)
}

func (a *API) RequestInitialEmail(c *echo.Context) error {
	raw, err := miniAppRaw(c)
	if err != nil {
		return err
	}
	if _, err = a.auth(c, true); err != nil {
		return err
	}
	in, err := decode[wire.InitialEmailInput](a, c, "InitialEmailInput")
	if err != nil {
		return err
	}
	out, err := a.accounts.RequestInitialEmail(c.Request().Context(), raw, accounts.InitialEmailInput{Email: string(in.Email)}, c.RealIP())
	if err != nil {
		return accountError(err)
	}
	return c.JSON(202, wire.RegistrationAccepted{ChallengeId: out.ChallengeId, ResendAfter: out.ResendAfter})
}

func (a *API) CompleteInitialEmail(c *echo.Context) error {
	raw, err := miniAppRaw(c)
	if err != nil {
		return err
	}
	if _, err = a.auth(c, true); err != nil {
		return err
	}
	in, err := decode[wire.InitialEmailCompleteInput](a, c, "InitialEmailCompleteInput")
	if err != nil {
		return err
	}
	out, err := a.accounts.CompleteInitialEmail(c.Request().Context(), raw, accounts.InitialEmailCompleteInput{ChallengeId: in.ChallengeId, Code: in.Code, NewPassword: in.NewPassword, AcceptedTermsVersion: in.AcceptedTermsVersion, AcceptedPrivacyVersion: in.AcceptedPrivacyVersion}, c.RealIP())
	if err != nil {
		return accountError(err)
	}
	return c.JSON(200, wire.VerifyResult{Verified: wire.VerifyResultVerified(out.Verified)})
}
