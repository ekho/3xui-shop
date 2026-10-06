package httpapi

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"net/url"
)

func paymentError(err error) error {
	var domain *payments.Error
	if errors.As(err, &domain) {
		return &apiError{Status: domain.Status, Code: domain.Code, Message: domain.Code}
	}
	return subscriptionError(err)
}

func purchaseOrderResult(p payments.PurchaseOrder) wire.PurchaseOrder {
	out := wire.PurchaseOrder{AccessOperationId: p.AccessOperationId, Action: wire.PurchaseOrderAction(p.Action), CanCancel: p.CanCancel, CanPay: p.CanPay, CreatedAt: p.CreatedAt, Expired: p.Expired, ExpiresAt: p.ExpiresAt, FulfillmentStatus: wire.PurchaseOrderFulfillmentStatus(p.FulfillmentStatus), OrderId: p.OrderId, PaymentMethod: wire.PurchaseOrderPaymentMethod(p.PaymentMethod), PaymentStatus: wire.PurchaseOrderPaymentStatus(p.PaymentStatus), PaymentType: wire.PurchaseOrderPaymentType(p.PaymentType), ReviewRequired: p.ReviewRequired,
		Quote: wire.PurchaseQuote{AmountMinor: p.Quote.AmountMinor, Currency: wire.PurchaseQuoteCurrency(p.Quote.Currency), Devices: p.Quote.Devices, PeriodDays: p.Quote.PeriodDays, PlanId: p.Quote.PlanId, Profile: wire.PurchaseQuoteProfile(p.Quote.Profile), Revision: p.Quote.Revision, TrafficGb: p.Quote.TrafficGb}}
	if c := p.Checkout; c != nil {
		out.Checkout = &wire.YooMoneyCheckout{Action: wire.YooMoneyCheckoutAction(c.Action), Method: wire.YooMoneyCheckoutMethod(c.Method), Fields: wire.YooMoneyCheckoutFields{Label: c.Fields.Label, PaymentType: wire.YooMoneyCheckoutFieldsPaymentType(c.Fields.PaymentType), QuickpayForm: wire.YooMoneyCheckoutFieldsQuickpayForm(c.Fields.QuickpayForm), Receiver: c.Fields.Receiver, SuccessURL: c.Fields.SuccessURL, Sum: c.Fields.Sum}}
	}
	return out
}

func currentPurchaseResult(p payments.CurrentPurchaseOrder) wire.CurrentPurchaseOrder {
	var out wire.CurrentPurchaseOrder
	if p.Order != nil {
		order := purchaseOrderResult(*p.Order)
		out.Order = &order
	}
	return out
}

func (a *API) paymentMethods(ctx context.Context, account uuid.UUID) (wire.PaymentMethods, error) {
	p, err := a.payments.PaymentMethods(ctx, account)
	out := wire.PaymentMethods{Methods: []wire.PaymentMethod{}}
	for _, method := range p.Methods {
		out.Methods = append(out.Methods, wire.PaymentMethod{Currency: wire.PaymentMethodCurrency(method.Currency), Id: wire.PaymentMethodId(method.Id)})
	}
	return out, paymentError(err)
}

func (a *API) createPurchaseOrder(ctx context.Context, account, key uuid.UUID, in wire.PurchaseOrderInput) (wire.PurchaseOrder, error) {
	out, err := a.payments.CreatePurchaseOrder(ctx, account, key, payments.PurchaseOrderInput{Action: string(in.Action), PaymentMethod: string(in.PaymentMethod), PaymentType: string(in.PaymentType), PeriodDays: in.PeriodDays, PlanId: in.PlanId, Revision: in.Revision})
	return purchaseOrderResult(out), paymentError(err)
}

func (a *API) purchaseOrder(ctx context.Context, account, id uuid.UUID) (wire.PurchaseOrder, error) {
	out, err := a.payments.PurchaseOrder(ctx, account, id)
	return purchaseOrderResult(out), paymentError(err)
}

func (a *API) currentPurchaseOrder(ctx context.Context, account uuid.UUID) (wire.CurrentPurchaseOrder, error) {
	out, err := a.payments.CurrentPurchaseOrder(ctx, account)
	return currentPurchaseResult(out), paymentError(err)
}

func (a *API) cancelPurchaseOrder(ctx context.Context, account, id, key uuid.UUID) (wire.PurchaseOrder, error) {
	out, err := a.payments.CancelPurchaseOrder(ctx, account, id, key)
	return purchaseOrderResult(out), paymentError(err)
}

func (a *API) operatorPurchaseOrder(ctx context.Context, actor, target uuid.UUID) (wire.CurrentPurchaseOrder, error) {
	out, err := a.payments.OperatorPurchaseOrder(ctx, actor, target)
	return currentPurchaseResult(out), paymentError(err)
}

func (a *API) reconcilePurchaseOrder(ctx context.Context, actor, target, id, key uuid.UUID, in wire.PurchaseReconcileInput) (wire.PurchaseOrder, error) {
	out, err := a.payments.ReconcilePurchaseOrder(ctx, actor, target, id, key, payments.PurchaseReconcileInput{Reason: in.Reason})
	return purchaseOrderResult(out), paymentError(err)
}

func (a *API) receiveYooMoney(ctx context.Context, fields url.Values) error {
	return paymentError(a.payments.ReceiveYooMoney(ctx, fields))
}
