package httpapi

import (
	"errors"
	"example.com/cabinet/backend/internal/modules/campaigns"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
)

func campaignError(err error) error {
	var e *campaigns.Error
	if errors.As(err, &e) {
		return &apiError{Status: e.Status, Code: e.Code, Message: e.Code, RetryAfter: e.RetryAfter}
	}
	return accountError(err)
}
func (a *API) RecordCampaignVisit(c *echo.Context) error {
	in, err := decode[wire.CampaignVisitInput](a, c, "CampaignVisitInput")
	if err != nil {
		return err
	}
	if err = a.campaignsOwner.Visit(c.Request().Context(), in.Code, c.RealIP()); err != nil {
		return campaignError(err)
	}
	return c.NoContent(204)
}

func (a *API) GetOperatorCampaign(c *echo.Context) error {
	actor, err := a.operatorAuth(c, false)
	if err != nil {
		return err
	}
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	out, err := a.campaignsOwner.Detail(c.Request().Context(), actor.Account.AccountId, id)
	if err != nil {
		return campaignError(err)
	}
	return c.JSON(200, out)
}
func (a *API) ListOperatorCampaigns(c *echo.Context) error {
	actor, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	in, err := decode[wire.CampaignListInput](a, c, "CampaignListInput")
	if err != nil {
		return err
	}
	out, err := a.campaignsOwner.List(c.Request().Context(), actor.Account.AccountId, in.Page, in.PerPage)
	if err != nil {
		return campaignError(err)
	}
	return c.JSON(200, out)
}
func (a *API) CreateCampaign(c *echo.Context) error {
	actor, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.CampaignCreateInput](a, c, "CampaignCreateInput")
	if err != nil {
		return err
	}
	out, err := a.campaignsOwner.Create(c.Request().Context(), actor.Account.AccountId, key, campaigns.CreateInput{Name: in.Name, Reason: in.Reason})
	if err != nil {
		return campaignError(err)
	}
	return c.JSON(201, out)
}
func (a *API) SetCampaignState(c *echo.Context) error {
	actor, id, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.CampaignStateInput](a, c, "CampaignStateInput")
	if err != nil {
		return err
	}
	out, err := a.campaignsOwner.SetState(c.Request().Context(), actor, id, key, campaigns.StateInput{State: string(in.State), ExpectedRevision: in.ExpectedRevision, Reason: in.Reason})
	if err != nil {
		return campaignError(err)
	}
	return c.JSON(200, out)
}
