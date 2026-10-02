package httpapi

import (
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func accessOperationID(c *echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("operation_id"))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, invalid()
	}
	return id, nil
}
func (a *API) CreateAccessOperation(c *echo.Context) error {
	actor, target, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.AccessOperationInput](a, c, "AccessOperationInput")
	if err != nil {
		return err
	}
	out, err := a.svc.CreateAccessOperation(c.Request().Context(), actor, target, key, in)
	if err != nil {
		return err
	}
	return c.JSON(202, out)
}
func (a *API) GetAccessOperation(c *echo.Context) error {
	account, err := a.operatorAuth(c, false)
	if err != nil {
		return err
	}
	target, err := resourceID(c)
	if err != nil {
		return err
	}
	id, err := accessOperationID(c)
	if err != nil {
		return err
	}
	out, err := a.svc.GetAccessOperation(c.Request().Context(), account.Account.AccountId, target, id)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) ReconcileAccessOperation(c *echo.Context) error {
	actor, target, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	id, err := accessOperationID(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.AccessReconcileInput](a, c, "AccessReconcileInput")
	if err != nil {
		return err
	}
	out, err := a.svc.ReconcileAccessOperation(c.Request().Context(), actor, target, id, key, in)
	if err != nil {
		return err
	}
	return c.JSON(202, out)
}
