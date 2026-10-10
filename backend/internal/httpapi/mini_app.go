package httpapi

import (
	"crypto/subtle"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"strings"
)

// Keep bearer grants explicit: new API routes never inherit Telegram privileges.
func miniAppRouteAllowed(path, method string) bool {
	if method == "GET" {
		if path == "/api/v1/reminders" || path == "/api/v1/notices" {
			return true
		}
		switch path {
		case "/api/v1/promocodes/activations/:id", "/api/v1/maintenance", "/api/v1/stars-subscription", "/api/v1/payment-methods", "/api/v1/telegram/mini-app/account", "/api/v1/auth/session", "/api/v1/me/identity", "/api/v1/subscription", "/api/v1/subscription/renewal", "/api/v1/subscription/plan-change", "/api/v1/subscription/key", "/api/v1/trial-requests/current", "/api/v1/catalogue", "/api/v1/orders/current", "/api/v1/orders/:id", "/api/v1/support", "/api/v1/support/messages/:id/attachment":
			return true
		}
	}
	if method == "POST" {
		if path == "/api/v1/reminders/preferences" || path == "/api/v1/reminders/:id/dismiss" || path == "/api/v1/notices/preferences" || path == "/api/v1/notices/:id/dismiss" {
			return true
		}
		switch path {
		case "/api/v1/promocodes/activate", "/api/v1/stars-subscription/control", "/api/v1/orders", "/api/v1/orders/:id/stars-invoice", "/api/v1/telegram/mini-app/logout", "/api/v1/telegram/initial-email", "/api/v1/telegram/initial-email/confirm", "/api/v1/trial-requests", "/api/v1/trials/activate", "/api/v1/payment-history", "/api/v1/support/history", "/api/v1/support/messages", "/api/v1/support/read", "/api/v1/support/state", "/api/v1/orders/:id/cancel":
			return true
		}
	}
	return false
}
func miniAppRaw(c *echo.Context) (string, error) {
	header := c.Request().Header.Get("Authorization")
	if len(c.Request().Header.Values("Authorization")) != 1 || !strings.HasPrefix(header, "Bearer mini_") {
		return "", failure(401, "INVALID_CREDENTIALS")
	}
	return strings.TrimPrefix(header, "Bearer "), nil
}
func (a *API) miniAppAuth(c *echo.Context, allowRestricted bool) (accounts.Authentication, error) {
	raw, err := miniAppRaw(c)
	if err != nil {
		return accounts.Authentication{}, err
	}
	out, err := a.accounts.AuthenticateTelegram(c.Request().Context(), raw, allowRestricted)
	return out, accountError(err)
}
func (a *API) miniAppAccount(c *echo.Context, out accounts.Authentication) (wire.MiniAppAccountResult, error) {
	capabilities, err := a.trialCapabilities(c.Request().Context(), out.Account)
	if err != nil {
		return wire.MiniAppAccountResult{}, err
	}
	account := out.Account
	var email *openapi_types.Email
	if account.EmailKey != nil {
		v := openapi_types.Email(*account.EmailKey)
		email = &v
	}
	return wire.MiniAppAccountResult{Account: wire.MiniAppAccount{AccountId: account.ID, Email: email, EmailVerified: account.VerifiedAt != nil, DisplayName: account.DisplayName, TelegramId: *account.TelegramID, TelegramLinked: true, Locale: wire.MiniAppAccountLocale(account.Locale)}, CsrfToken: out.CsrfToken, Capabilities: capabilities}, nil
}
func (a *API) CreateMiniAppSession(c *echo.Context) error {
	if a.miniApp == nil {
		return unavailable()
	}
	in, err := decode[wire.MiniAppSessionInput](a, c, "MiniAppSessionInput")
	if err != nil {
		return err
	}
	terms, privacy := "", ""
	if in.AcceptedTermsVersion != nil {
		terms = *in.AcceptedTermsVersion
	}
	if in.AcceptedPrivacyVersion != nil {
		privacy = *in.AcceptedPrivacyVersion
	}
	out, raw, err := a.miniApp.Login(c.Request().Context(), in.InitData, terms, privacy, c.RealIP())
	if errors.Is(err, telegram.ErrMiniAppData) {
		return failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return accountError(err)
	}
	profile, err := a.miniAppAccount(c, out)
	if err != nil {
		return err
	}
	return c.JSON(200, wire.MiniAppSessionResult{Account: profile.Account, CsrfToken: profile.CsrfToken, Capabilities: profile.Capabilities, SessionToken: raw})
}
func (a *API) GetMiniAppAccount(c *echo.Context) error {
	auth, err := a.miniAppAuth(c, false)
	if err != nil {
		return err
	}
	out, err := a.miniAppAccount(c, auth)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) LogoutMiniAppAccount(c *echo.Context) error {
	if err := requireEmptyBody(c); err != nil {
		return err
	}
	raw, err := miniAppRaw(c)
	if err != nil {
		return err
	}
	auth, err := a.accounts.AuthenticateTelegram(c.Request().Context(), raw, true)
	var domain *accounts.Error
	if errors.As(err, &domain) && domain.Status == 401 {
		return c.NoContent(204)
	}
	if err != nil {
		return accountError(err)
	}
	if subtle.ConstantTimeCompare([]byte(c.Request().Header.Get("X-CSRF-Token")), []byte(auth.CsrfToken)) != 1 {
		return failure(403, "INVALID_CREDENTIALS")
	}
	if err = a.accounts.LogoutTelegram(c.Request().Context(), raw); err != nil {
		return accountError(err)
	}
	return c.NoContent(204)
}

func miniAppReadOnlyOrder(c *echo.Context, out wire.PurchaseOrder) wire.PurchaseOrder {
	if c.Request().Header.Get("Authorization") != "" {
		if out.PaymentMethod != "telegram_stars" {
			out.CanPay = false
		}
		if out.ManualPayment != nil {
			manual := *out.ManualPayment
			manual.CanReport = false
			out.ManualPayment = &manual
		}
		out.Checkout = nil
		out.YookassaCheckout = nil
		out.CryptomusCheckout = nil
		out.HeleketCheckout = nil
	}
	if c.Request().Header.Get("Authorization") == "" && out.StarsCheckout != nil {
		out.StarsCheckout = nil
		out.CanPay = false
	}
	return out
}
