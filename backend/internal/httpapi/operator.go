package httpapi

import (
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func (a *API) operatorAuth(c *echo.Context, write bool) (wire.AccountResult, error) {
	account, err := a.auth(c, write)
	if err != nil {
		return account, err
	}
	if err = a.requireSupportOperator(c.Request().Context(), account.Account.AccountId); err != nil {
		return account, err
	}
	return account, nil
}
func (a *API) GetOperatorSession(c *echo.Context) error {
	account, err := a.operatorAuth(c, false)
	if err != nil {
		return err
	}
	return c.JSON(200, wire.OperatorSession{Account: account.Account, CsrfToken: account.CsrfToken})
}
func (a *API) SearchOperatorClients(c *echo.Context) error {
	account, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	in, err := decode[wire.OperatorSearchInput](a, c, "OperatorSearchInput")
	if err != nil {
		return err
	}
	out, err := a.searchOperatorClients(c.Request().Context(), account.Account.AccountId, in)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) GetOperatorClient(c *echo.Context) error {
	account, err := a.operatorAuth(c, false)
	if err != nil {
		return err
	}
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	out, err := a.operatorClient(c.Request().Context(), account.Account.AccountId, id)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) GetOperatorClientHistory(c *echo.Context) error {
	account, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.OperatorHistoryInput](a, c, "OperatorHistoryInput")
	if err != nil {
		return err
	}
	out, err := a.operatorClientHistory(c.Request().Context(), account.Account.AccountId, id, in)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) GetOperatorClientKey(c *echo.Context) error {
	account, err := a.operatorAuth(c, false)
	if err != nil {
		return err
	}
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	out, err := a.operatorClientKey(c.Request().Context(), account.Account.AccountId, id)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) SetOperatorRestriction(c *echo.Context) error {
	actor, id, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.OperatorRestrictionInput](a, c, "OperatorRestrictionInput")
	if err != nil {
		return err
	}
	out, err := a.setOperatorRestriction(c.Request().Context(), actor, id, key, in)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) operatorAction(c *echo.Context) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	account, err := a.operatorAuth(c, true)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	id, err := resourceID(c)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	return account.Account.AccountId, id, key, nil
}
func (a *API) DecideOperatorTrial(c *echo.Context) error {
	actor, id, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.OperatorDecisionInput](a, c, "OperatorDecisionInput")
	if err != nil {
		return err
	}
	out, _, err := a.decideOperatorTrial(c.Request().Context(), actor, id, key, in)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
func (a *API) ReconsiderOperatorTrial(c *echo.Context) error {
	actor, id, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.OperatorReasonInput](a, c, "OperatorReasonInput")
	if err != nil {
		return err
	}
	out, _, err := a.reconsiderOperatorTrial(c.Request().Context(), actor, id, key, in.Reason)
	if err != nil {
		return err
	}
	return c.JSON(201, out)
}
func (a *API) ReconcileOperatorTrial(c *echo.Context) error {
	actor, id, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.OperatorReasonInput](a, c, "OperatorReasonInput")
	if err != nil {
		return err
	}
	out, _, err := a.reconcileOperatorTrial(c.Request().Context(), actor, id, key, in.Reason)
	if err != nil {
		return err
	}
	return c.JSON(202, out)
}
func (a *API) CreateOperatorTelegramTrial(c *echo.Context) error {
	account, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.OperatorTelegramTrialInput](a, c, "OperatorTelegramTrialInput")
	if err != nil {
		return err
	}
	out, created, err := a.createTelegramTrial(c.Request().Context(), account.Account.AccountId, key, in)
	if err != nil {
		return err
	}
	if created {
		return c.JSON(201, out)
	}
	return c.JSON(200, out)
}
