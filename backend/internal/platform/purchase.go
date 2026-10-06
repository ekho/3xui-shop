package platform

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func paymentError(err error) error {
	var domain *payments.Error
	if errors.As(err, &domain) {
		return &Error{Status: domain.Status, Code: domain.Code, Message: domain.Code}
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
func (s *Service) PaymentMethods(ctx context.Context, account uuid.UUID) (wire.PaymentMethods, error) {
	p, err := s.payments.PaymentMethods(ctx, account)
	out := wire.PaymentMethods{Methods: []wire.PaymentMethod{}}
	for _, method := range p.Methods {
		out.Methods = append(out.Methods, wire.PaymentMethod{Currency: wire.PaymentMethodCurrency(method.Currency), Id: wire.PaymentMethodId(method.Id)})
	}
	return out, paymentError(err)
}
func (s *Service) CreatePurchaseOrder(ctx context.Context, account, key uuid.UUID, in wire.PurchaseOrderInput) (wire.PurchaseOrder, error) {
	out, err := s.payments.CreatePurchaseOrder(ctx, account, key, payments.PurchaseOrderInput{Action: string(in.Action), PaymentMethod: string(in.PaymentMethod), PaymentType: string(in.PaymentType), PeriodDays: in.PeriodDays, PlanId: in.PlanId, Revision: in.Revision})
	return purchaseOrderResult(out), paymentError(err)
}
func (s *Service) PurchaseOrder(ctx context.Context, account, id uuid.UUID) (wire.PurchaseOrder, error) {
	out, err := s.payments.PurchaseOrder(ctx, account, id)
	return purchaseOrderResult(out), paymentError(err)
}
func (s *Service) CurrentPurchaseOrder(ctx context.Context, account uuid.UUID) (wire.CurrentPurchaseOrder, error) {
	out, err := s.payments.CurrentPurchaseOrder(ctx, account)
	return currentPurchaseResult(out), paymentError(err)
}
func (s *Service) CancelPurchaseOrder(ctx context.Context, account, id, key uuid.UUID) (wire.PurchaseOrder, error) {
	out, err := s.payments.CancelPurchaseOrder(ctx, account, id, key)
	return purchaseOrderResult(out), paymentError(err)
}
func (s *Service) OperatorPurchaseOrder(ctx context.Context, actor, target uuid.UUID) (wire.CurrentPurchaseOrder, error) {
	out, err := s.payments.OperatorPurchaseOrder(ctx, actor, target)
	return currentPurchaseResult(out), paymentError(err)
}
func (s *Service) ReconcilePurchaseOrder(ctx context.Context, actor, target, id, key uuid.UUID, in wire.PurchaseReconcileInput) (wire.PurchaseOrder, error) {
	out, err := s.payments.ReconcilePurchaseOrder(ctx, actor, target, id, key, payments.PurchaseReconcileInput{Reason: in.Reason})
	return purchaseOrderResult(out), paymentError(err)
}
func (s *Service) FulfillPurchase(ctx context.Context, id uuid.UUID) error {
	return paymentError(s.payments.FulfillPurchase(ctx, id))
}
func (s *Service) CheckPurchaseAccess(ctx context.Context, tx pgx.Tx, order, account, operation uuid.UUID) (string, error) {
	reason, err := s.payments.CheckPurchaseAccess(ctx, tx, order, account, operation)
	return reason, paymentError(err)
}
func (s *Service) RecordPurchaseAccessTx(ctx context.Context, tx pgx.Tx, operation uuid.UUID, status, reason string) error {
	return paymentError(s.payments.RecordPurchaseAccessTx(ctx, tx, operation, status, reason))
}
