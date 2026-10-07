package httpapi

import (
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"net/http"
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

func (a *API) StartTelegramLink(c *echo.Context) error {
	if _, err := a.auth(c, true); err != nil {
		return err
	}
	raw, err := sessionRaw(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.CurrentPasswordInput](a, c, "CurrentPasswordInput")
	if err != nil {
		return err
	}
	out, err := a.accounts.StartTelegramLink(c.Request().Context(), raw, accounts.CurrentPasswordInput{CurrentPassword: in.CurrentPassword}, c.RealIP())
	if err != nil {
		return accountError(err)
	}
	return c.JSON(200, wire.TelegramLinkChallenge{LinkToken: out.LinkToken, ExpiresAt: out.ExpiresAt})
}

func (a *API) ConfirmTelegramLink(c *echo.Context) error {
	if a.miniApp == nil {
		return unavailable()
	}
	in, err := decode[wire.MiniAppLinkInput](a, c, "MiniAppLinkInput")
	if err != nil {
		return err
	}
	out, err := a.miniApp.ConfirmLink(c.Request().Context(), telegram.MiniAppLinkInput{InitData: in.InitData, LinkToken: in.LinkToken, AcceptedTermsVersion: in.AcceptedTermsVersion, AcceptedPrivacyVersion: in.AcceptedPrivacyVersion}, c.RealIP())
	if errors.Is(err, telegram.ErrMiniAppData) {
		return failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return accountError(err)
	}
	return c.JSON(200, wire.TelegramLinkResult{Linked: wire.TelegramLinkResultLinked(out.Linked)})
}

func (a *API) UnlinkTelegram(c *echo.Context) error {
	if _, err := a.auth(c, true); err != nil {
		return err
	}
	raw, err := sessionRaw(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.CurrentPasswordInput](a, c, "CurrentPasswordInput")
	if err != nil {
		return err
	}
	out, err := a.accounts.UnlinkTelegram(c.Request().Context(), raw, accounts.CurrentPasswordInput{CurrentPassword: in.CurrentPassword}, c.RealIP())
	if err != nil {
		return accountError(err)
	}
	if out.Changed {
		c.SetCookie(&http.Cookie{Name: "__Host-session", Value: "", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/", MaxAge: -1})
	}
	return c.JSON(200, wire.TelegramUnlinkResult{Changed: out.Changed})
}
