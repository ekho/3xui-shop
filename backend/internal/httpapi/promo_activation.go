package httpapi

import (
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
)

func (a *API) ActivatePromocode(c *echo.Context) error {
	auth, err := a.auth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.ActivatePromocodeInput](a, c, "ActivatePromocodeInput")
	if err != nil {
		return err
	}
	out, err := a.bonusesOwner.ActivatePromocode(c.Request().Context(), auth.Account.ID, key, bonuses.ActivatePromocodeInput{Code: in.Code})
	if err != nil {
		return promocodeError(err)
	}
	return c.JSON(201, out)
}

func (a *API) GetPromocodeActivation(c *echo.Context) error {
	auth, err := a.auth(c, false)
	if err != nil {
		return err
	}
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	out, err := a.bonusesOwner.GetPromocodeActivation(c.Request().Context(), auth.Account.ID, id)
	if err != nil {
		return promocodeError(err)
	}
	return c.JSON(200, out)
}
