package httpapi

import (
	"errors"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func (a *API) ReadOperatorStatistics(c *echo.Context) error {
	actor, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	in, err := decode[wire.StatisticsInput](a, c, "StatisticsInput")
	if err != nil {
		return err
	}
	if in.CampaignId != nil && *in.CampaignId == uuid.Nil {
		return invalid()
	}
	out, err := a.auditReports.Statistics(c.Request().Context(), actor.Account.AccountId, in.CampaignId)
	if err != nil {
		var e *auditreports.Error
		if errors.As(err, &e) {
			return failure(e.Status, e.Code)
		}
		return accountError(err)
	}
	return c.JSON(200, out)
}
