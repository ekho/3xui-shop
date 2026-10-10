package httpapi

import (
	"errors"

	"example.com/cabinet/backend/internal/modules/operations"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
)

func maintenanceError(err error) error {
	var domain *operations.Error
	if errors.As(err, &domain) {
		return failure(domain.Status, domain.Code)
	}
	return unavailable()
}

func (a *API) GetMaintenance(c *echo.Context) error {
	if c.Request().Header.Get("Authorization") != "" {
		if _, err := a.miniAppAuth(c, false); err != nil {
			return err
		}
	}
	status, err := a.maintenance.Status(c.Request().Context())
	if err != nil {
		return maintenanceError(err)
	}
	return c.JSON(200, status)
}

func (a *API) GetOperatorMaintenance(c *echo.Context) error {
	if _, err := a.operatorAuth(c, false); err != nil {
		return err
	}
	return a.GetMaintenance(c)
}

func (a *API) SetOperatorMaintenance(c *echo.Context) error {
	auth, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.MaintenanceInput](a, c, "MaintenanceInput")
	if err != nil {
		return err
	}
	status, err := a.maintenance.Set(c.Request().Context(), auth.Account.AccountId, key, operations.Input{Enabled: in.Enabled, ExpectedRevision: in.ExpectedRevision, Reason: in.Reason, Confirmed: bool(in.Confirmed)})
	if err != nil {
		return maintenanceError(err)
	}
	return c.JSON(200, status)
}
