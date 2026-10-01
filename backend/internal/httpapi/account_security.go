package httpapi

import (
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
)

func (a *API) RequestPasswordReset(c *echo.Context) error {
	in, err := decode[wire.PasswordResetInput](a, c, "PasswordResetInput")
	if err != nil {
		return err
	}
	out, err := a.svc.RequestPasswordReset(c.Request().Context(), in, c.RealIP())
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
	if err = a.svc.CompletePasswordReset(c.Request().Context(), in, c.RealIP()); err != nil {
		return err
	}
	return c.NoContent(204)
}
