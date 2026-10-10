package httpapi

import (
	"errors"

	"example.com/cabinet/backend/internal/modules/bonuses"
	"github.com/labstack/echo/v5"
)

func (a *API) GetReferrals(c *echo.Context) error {
	auth, err := a.auth(c, false)
	if err != nil {
		return err
	}
	if err = requireEmptyBody(c); err != nil {
		return err
	}
	out, err := a.bonuses.ReadReferrals(c.Request().Context(), auth.Account.ID, a.cfg.CabinetOrigin)
	if err != nil {
		var domain *bonuses.Error
		if errors.As(err, &domain) {
			return failure(domain.Status, domain.Code)
		}
		return unavailable()
	}
	return c.JSON(200, out)
}
