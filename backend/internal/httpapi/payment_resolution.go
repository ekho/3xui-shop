package httpapi

import (
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func (a *API) GetOperatorPaymentCase(c *echo.Context) error {
	actor, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	target, err := resourceID(c)
	if err != nil {
		return err
	}
	order, err := uuid.Parse(c.Param("order_id"))
	if err != nil || order == uuid.Nil {
		return invalid()
	}
	in, err := decode[wire.PaymentCaseInput](a, c, "PaymentCaseInput")
	if err != nil {
		return err
	}
	out, err := a.payments.OperatorPaymentCase(c.Request().Context(), actor.Account.AccountId, target, order, in.ReceiptOperationId)
	if err != nil {
		return paymentError(err)
	}
	result := wire.PaymentCase{Order: purchaseOrderResult(out.Order), FinancialReviewOpen: out.FinancialReviewOpen, CanConfirmRefund: out.CanConfirmRefund}
	if out.Receipt != nil {
		page := paymentHistoryResult(payments.PaymentHistoryPage{Receipts: []payments.HistoryReceipt{*out.Receipt}})
		result.Receipt = &page.Receipts[0]
	}
	if out.Refund != nil {
		r := paymentRefundResult(*out.Refund)
		result.Refund = &r
	}
	return c.JSON(200, result)
}

func (a *API) ConfirmPurchaseRefund(c *echo.Context) error {
	actor, target, key, err := a.operatorAction(c)
	if err != nil {
		return err
	}
	order, err := uuid.Parse(c.Param("order_id"))
	if err != nil || order == uuid.Nil {
		return invalid()
	}
	in, err := decode[wire.PurchaseRefundInput](a, c, "PurchaseRefundInput")
	if err != nil {
		return err
	}
	out, err := a.payments.ConfirmPurchaseRefund(c.Request().Context(), actor, target, order, key, payments.PurchaseRefundInput{ReceiptOperationId: in.ReceiptOperationId, Reference: in.Reference, Reason: in.Reason, ConfirmFull: bool(in.ConfirmFull), KeepAccess: bool(in.KeepAccess), ReturnedAmount: in.ReturnedAmount})
	if err != nil {
		return paymentError(err)
	}
	return c.JSON(201, paymentRefundResult(out))
}

func paymentRefundResult(f payments.PaymentRefund) wire.PaymentRefund {
	return wire.PaymentRefund{RefundId: f.RefundId, OrderId: f.OrderId, OperatorAccountId: f.OperatorAccountId, ReceiptOperationId: f.ReceiptOperationId, PaymentMethod: wire.PaymentRefundPaymentMethod(f.PaymentMethod), CreatedAt: f.CreatedAt, ReceiptGrossMinor: f.ReceiptGrossMinor, ReceiptCurrency: wire.PaymentRefundReceiptCurrency(f.ReceiptCurrency), ReturnedAmount: f.ReturnedAmount, ReturnedCurrency: f.ReturnedCurrency, Reference: f.Reference, Reason: f.Reason, Source: "operator"}
}
