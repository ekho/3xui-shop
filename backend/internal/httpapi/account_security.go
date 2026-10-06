package httpapi

import (
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
	"net/http"
	"time"
)

func (a *API) RequestPasswordReset(c *echo.Context) error {
	in, err := decode[wire.PasswordResetInput](a, c, "PasswordResetInput")
	if err != nil {
		return err
	}
	out, err := a.requestPasswordReset(c.Request().Context(), in, c.RealIP())
	if err != nil {
		return err
	}
	return c.JSON(202, out)
}
func (a *API) CompletePasswordReset(c *echo.Context) error {
	in, err := decode[wire.PasswordResetCompleteInput](a, c, "PasswordResetCompleteInput")
	if err != nil {
		return err
	}
	if err = a.completePasswordReset(c.Request().Context(), in, c.RealIP()); err != nil {
		return err
	}
	return c.NoContent(204)
}

func sessionRaw(c *echo.Context) (string, error) {
	cookie, err := c.Cookie("__Host-session")
	if err != nil {
		return "", &apiError{Status: 401, Code: "INVALID_CREDENTIALS"}
	}
	return cookie.Value, nil
}
func (a *API) GetSessionContext(c *echo.Context) error {
	raw, err := sessionRaw(c)
	if err != nil {
		return err
	}
	out, err := a.getSessionContext(c.Request().Context(), raw)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) GetAccountSecurity(c *echo.Context) error {
	raw, err := sessionRaw(c)
	if err != nil {
		return err
	}
	out, err := a.getAccountSecurity(c.Request().Context(), raw)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func rotateCookie(c *echo.Context, rotation accounts.SessionRotation) error {
	c.SetCookie(&http.Cookie{Name: "__Host-session", Value: rotation.Raw, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/", Expires: rotation.AbsoluteExpiresAt, MaxAge: max(0, int(time.Until(rotation.AbsoluteExpiresAt).Seconds()))})
	return c.NoContent(204)
}
func (a *API) ChangePassword(c *echo.Context) error {
	in, err := decode[wire.PasswordChangeInput](a, c, "PasswordChangeInput")
	if err != nil {
		return err
	}
	if _, err = a.auth(c, true); err != nil {
		return err
	}
	raw, err := sessionRaw(c)
	if err != nil {
		return err
	}
	rotation, err := a.changePassword(c.Request().Context(), raw, in, c.RealIP())
	if err != nil {
		return err
	}
	return rotateCookie(c, rotation)
}
func (a *API) RevokeOtherSessions(c *echo.Context) error {
	in, err := decode[wire.CurrentPasswordInput](a, c, "CurrentPasswordInput")
	if err != nil {
		return err
	}
	if _, err = a.auth(c, true); err != nil {
		return err
	}
	raw, err := sessionRaw(c)
	if err != nil {
		return err
	}
	rotation, err := a.revokeOtherSessions(c.Request().Context(), raw, in, c.RealIP())
	if err != nil {
		return err
	}
	return rotateCookie(c, rotation)
}

func (a *API) RequestEmailChange(c *echo.Context) error {
	in, err := decode[wire.EmailChangeInput](a, c, "EmailChangeInput")
	if err != nil {
		return err
	}
	if _, err = a.auth(c, true); err != nil {
		return err
	}
	raw, err := sessionRaw(c)
	if err != nil {
		return err
	}
	out, err := a.requestEmailChange(c.Request().Context(), raw, in, c.RealIP())
	if err != nil {
		return err
	}
	return c.JSON(202, out)
}
func (a *API) ConfirmEmailChange(c *echo.Context) error {
	in, err := decode[wire.EmailChangeConfirmInput](a, c, "EmailChangeConfirmInput")
	if err != nil {
		return err
	}
	out, err := a.confirmEmailChange(c.Request().Context(), in, c.RealIP())
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) CancelEmailChange(c *echo.Context) error {
	if err := requireEmptyBody(c); err != nil {
		return err
	}
	if _, err := a.auth(c, true); err != nil {
		return err
	}
	raw, err := sessionRaw(c)
	if err != nil {
		return err
	}
	if err = a.cancelEmailChange(c.Request().Context(), raw); err != nil {
		return err
	}
	return c.NoContent(204)
}
