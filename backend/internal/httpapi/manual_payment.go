package httpapi

import (
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func (a *API) ReportManualPayment(c *echo.Context) error {
	account, err := a.auth(c, true)
	if err != nil {
		return err
	}
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	if _, err = decode[wire.ManualPaymentReportInput](a, c, "ManualPaymentReportInput"); err != nil {
		return err
	}
	out, err := a.payments.ReportManualPayment(c.Request().Context(), account.Account.AccountId, id, key)
	if err != nil {
		return paymentError(err)
	}
	return c.JSON(200, purchaseOrderResult(out))
}

func (a *API) DecideManualPayment(c *echo.Context) error {
	actor, target, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("order_id"))
	if err != nil || id == uuid.Nil {
		return invalid()
	}
	in, err := decode[wire.ManualPaymentDecisionInput](a, c, "ManualPaymentDecisionInput")
	if err != nil {
		return err
	}
	out, err := a.payments.DecideManualPayment(c.Request().Context(), actor, target, id, key, payments.ManualPaymentDecisionInput{Decision: string(in.Decision), Reason: in.Reason, ConfirmedAmountMinor: in.ConfirmedAmountMinor})
	if err != nil {
		return paymentError(err)
	}
	status := 200
	if in.Decision == "approve" {
		status = 202
	}
	return c.JSON(status, purchaseOrderResult(out))
}

func (a *API) GetManualPaymentRequests(c *echo.Context) error {
	actor, err := a.operatorAuth(c, false)
	if err != nil {
		return err
	}
	var after *uuid.UUID
	if value := c.QueryParam("after"); value != "" {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return invalid()
		}
		after = &id
	}
	page, err := a.payments.ManualPaymentRequests(c.Request().Context(), actor.Account.AccountId, after)
	if err != nil {
		return paymentError(err)
	}
	out := wire.ManualPaymentPage{Items: []wire.ManualPaymentItem{}, HasMore: page.HasMore, NextCursor: page.NextCursor}
	for _, item := range page.Items {
		out.Items = append(out.Items, wire.ManualPaymentItem{AccountId: item.AccountId, Order: purchaseOrderResult(item.Order)})
	}
	return c.JSON(200, out)
}
