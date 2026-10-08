package httpapi

import (
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
)

func (a *API) GetStarsSubscription(c *echo.Context) error {
	auth, err := a.auth(c, false)
	if err != nil {
		return err
	}
	out, err := a.payments.StarsSubscription(c.Request().Context(), auth.Account.ID)
	if err != nil {
		return paymentError(err)
	}
	return c.JSON(200, out)
}
func (a *API) ControlStarsSubscription(c *echo.Context) error {
	auth, err := a.auth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.StarsSubscriptionControlInput](a, c, "StarsSubscriptionControlInput")
	if err != nil {
		return err
	}
	out, err := a.payments.ControlStarsSubscription(c.Request().Context(), auth.Account.ID, key, payments.StarsSubscriptionControlInput{Action: string(in.Action), Confirmed: bool(in.Confirmed)})
	if err != nil {
		return paymentError(err)
	}
	return c.JSON(200, out)
}
