package httpapi

import (
	"errors"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
)

func promocodeError(err error) error {
	var e *bonuses.Error
	if errors.As(err, &e) {
		return &apiError{Status: e.Status, Code: e.Code, Message: e.Code}
	}
	return accountError(err)
}

func (a *API) ListOperatorPromocodes(c *echo.Context) error {
	actor, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	in, err := decode[wire.PromocodeListInput](a, c, "PromocodeListInput")
	if err != nil {
		return err
	}
	out, err := a.bonusesOwner.ListPromocodes(c.Request().Context(), actor.Account.AccountId, in.Page, in.PerPage)
	if err != nil {
		return promocodeError(err)
	}
	return c.JSON(200, out)
}
func (a *API) GetOperatorPromocode(c *echo.Context) error {
	actor, err := a.operatorAuth(c, false)
	if err != nil {
		return err
	}
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	out, err := a.bonusesOwner.GetPromocode(c.Request().Context(), actor.Account.AccountId, id)
	if err != nil {
		return promocodeError(err)
	}
	return c.JSON(200, out)
}
func (a *API) CreatePromocode(c *echo.Context) error {
	actor, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.CreatePromocodeInput](a, c, "CreatePromocodeInput")
	if err != nil {
		return err
	}
	out, err := a.bonusesOwner.CreatePromocode(c.Request().Context(), actor.Account.AccountId, key, bonuses.CreatePromocodeInput{DurationDays: in.DurationDays, Reason: in.Reason})
	if err != nil {
		return promocodeError(err)
	}
	return c.JSON(201, out)
}
func (a *API) EditPromocode(c *echo.Context) error {
	actor, id, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.EditPromocodeInput](a, c, "EditPromocodeInput")
	if err != nil {
		return err
	}
	out, err := a.bonusesOwner.EditPromocode(c.Request().Context(), actor, id, key, bonuses.EditPromocodeInput{DurationDays: in.DurationDays, ExpectedRevision: in.ExpectedRevision, Reason: in.Reason})
	if err != nil {
		return promocodeError(err)
	}
	return c.JSON(200, out)
}
func (a *API) DeletePromocode(c *echo.Context) error {
	actor, id, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.DeletePromocodeInput](a, c, "DeletePromocodeInput")
	if err != nil {
		return err
	}
	out, err := a.bonusesOwner.DeletePromocode(c.Request().Context(), actor, id, key, bonuses.DeletePromocodeInput{ExpectedRevision: in.ExpectedRevision, Reason: in.Reason})
	if err != nil {
		return promocodeError(err)
	}
	return c.JSON(200, out)
}
