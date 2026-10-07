package httpapi

import (
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"io"
	"mime"
	"net/url"
	"strings"
	"unicode/utf8"
)

func (a *API) GetPaymentMethods(c *echo.Context) error {
	account, err := a.auth(c, false)
	if err != nil {
		return err
	}
	out, err := a.paymentMethods(c.Request().Context(), account.Account.ID)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

func (a *API) GetRenewalOffer(c *echo.Context) error {
	account, err := a.auth(c, false)
	if err != nil {
		return err
	}
	p, err := a.payments.RenewalOffer(c.Request().Context(), account.Account.ID)
	if err != nil {
		return paymentError(err)
	}
	return c.JSON(200, wire.CataloguePlanSnapshot{PlanId: p.PlanId, Revision: p.Revision, Devices: p.Devices, Hidden: p.Hidden, Periods: p.Periods, Prices: cataloguePrices(p.Prices), Profile: wire.CataloguePlanSnapshotProfile(p.Profile), TrafficGb: p.TrafficGb})
}

func (a *API) GetPlanChangeContext(c *echo.Context) error {
	account, err := a.auth(c, false)
	if err != nil {
		return err
	}
	p, err := a.payments.PlanChangeContext(c.Request().Context(), account.Account.ID)
	if err != nil {
		return paymentError(err)
	}
	return c.JSON(200, wire.PlanChangeContext{CurrentPlanId: p.CurrentPlanId, SourceAccessOperationId: p.SourceAccessOperationId})
}

func (a *API) CreatePurchaseOrder(c *echo.Context) error {
	account, err := a.auth(c, true)
	if err != nil {
		return err
	}
	key, err := idempotencyKey(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.PurchaseOrderInput](a, c, "PurchaseOrderInput")
	if err != nil {
		return err
	}
	out, err := a.createPurchaseOrder(c.Request().Context(), account.Account.ID, key, in)
	if err != nil {
		return err
	}
	return c.JSON(201, out)
}

func (a *API) GetCurrentPurchaseOrder(c *echo.Context) error {
	account, err := a.auth(c, false)
	if err != nil {
		return err
	}
	out, err := a.currentPurchaseOrder(c.Request().Context(), account.Account.ID)
	if err != nil {
		return err
	}
	if c.Request().Header.Get("Authorization") != "" {
		if out.Order != nil {
			projected := miniAppReadOnlyOrder(c, *out.Order)
			out.Order = &projected
		}
		allowed := false
		out.CanPurchase = &allowed
	}
	return c.JSON(200, out)
}

func (a *API) GetPurchaseOrder(c *echo.Context) error {
	account, err := a.auth(c, false)
	if err != nil {
		return err
	}
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	out, err := a.purchaseOrder(c.Request().Context(), account.Account.ID, id)
	if err != nil {
		return err
	}
	return c.JSON(200, miniAppReadOnlyOrder(c, out))
}

func (a *API) CancelPurchaseOrder(c *echo.Context) error {
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
	_, err = decode[wire.PurchaseCancelInput](a, c, "PurchaseCancelInput")
	if err != nil {
		return err
	}
	out, err := a.cancelPurchaseOrder(c.Request().Context(), account.Account.ID, id, key)
	if err != nil {
		return err
	}
	return c.JSON(200, miniAppReadOnlyOrder(c, out))
}

func (a *API) GetOperatorPurchaseOrder(c *echo.Context) error {
	actor, err := a.operatorAuth(c, false)
	if err != nil {
		return err
	}
	target, err := resourceID(c)
	if err != nil {
		return err
	}
	out, err := a.operatorPurchaseOrder(c.Request().Context(), actor.Account.AccountId, target)
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

func (a *API) ReconcilePurchaseOrder(c *echo.Context) error {
	actor, target, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("order_id"))
	if err != nil || id == uuid.Nil {
		return invalid()
	}
	in, err := decode[wire.PurchaseReconcileInput](a, c, "PurchaseReconcileInput")
	if err != nil {
		return err
	}
	out, err := a.reconcilePurchaseOrder(c.Request().Context(), actor, target, id, key, in)
	if err != nil {
		return err
	}
	return c.JSON(202, out)
}

func (a *API) ReceiveYooMoney(c *echo.Context) error {
	media, params, err := mime.ParseMediaType(c.Request().Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" || params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") {
		return invalid()
	}
	data, err := io.ReadAll(io.LimitReader(c.Request().Body, 16385))
	if err != nil || len(data) == 0 || len(data) > 16384 || !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return invalid()
	}
	for _, field := range strings.Split(string(data), "&") {
		if field == "" || !strings.Contains(field, "=") || strings.HasPrefix(field, "=") {
			return invalid()
		}
	}
	fields, err := url.ParseQuery(string(data))
	if err != nil {
		return invalid()
	}
	for key, values := range fields {
		if key == "" || !utf8.ValidString(key) || len(values) != 1 || !utf8.ValidString(values[0]) || strings.ContainsRune(key, '\x00') || strings.ContainsRune(values[0], '\x00') {
			return invalid()
		}
	}
	if err = a.receiveYooMoney(c.Request().Context(), fields); err != nil {
		return err
	}
	return c.NoContent(200)
}

func (a *API) ReceiveYooKassa(c *echo.Context) error {
	if !a.payments.YooKassaSourceAllowed(c.RealIP()) {
		return paymentError(&payments.Error{Status: 403, Code: "INVALID_CREDENTIALS"})
	}
	in, err := decode[wire.YooKassaNotification](a, c, "YooKassaNotification")
	if err != nil {
		return err
	}
	if err = a.payments.ReceiveYooKassa(c.Request().Context(), in.Object.Id.String()); err != nil {
		return paymentError(err)
	}
	return c.NoContent(200)
}

func (a *API) ReceiveCryptomus(c *echo.Context) error {
	if !a.payments.CryptomusSourceAllowed(c.RealIP()) {
		return paymentError(&payments.Error{Status: 403, Code: "INVALID_CREDENTIALS"})
	}
	media, _, err := mime.ParseMediaType(c.Request().Header.Get("Content-Type"))
	if err != nil || media != "application/json" || c.Request().URL.RawQuery != "" {
		return paymentError(&payments.Error{Status: 400, Code: "INVALID_INPUT"})
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request().Body, 16385))
	if err != nil || len(raw) == 0 || len(raw) > 16384 || !utf8.Valid(raw) {
		return paymentError(&payments.Error{Status: 400, Code: "INVALID_INPUT"})
	}
	if err = a.payments.ReceiveCryptomus(c.Request().Context(), raw); err != nil {
		return paymentError(err)
	}
	return c.NoContent(200)
}

func (a *API) ReceiveHeleket(c *echo.Context) error {
	if !a.payments.HeleketSourceAllowed(c.RealIP()) {
		return paymentError(&payments.Error{Status: 403, Code: "INVALID_CREDENTIALS"})
	}
	media, _, err := mime.ParseMediaType(c.Request().Header.Get("Content-Type"))
	if err != nil || media != "application/json" || c.Request().URL.RawQuery != "" {
		return paymentError(&payments.Error{Status: 400, Code: "INVALID_INPUT"})
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request().Body, 16385))
	if err != nil || len(raw) == 0 || len(raw) > 16384 || !utf8.Valid(raw) {
		return paymentError(&payments.Error{Status: 400, Code: "INVALID_INPUT"})
	}
	if err = a.payments.ReceiveHeleket(c.Request().Context(), raw); err != nil {
		return paymentError(err)
	}
	return c.NoContent(200)
}
