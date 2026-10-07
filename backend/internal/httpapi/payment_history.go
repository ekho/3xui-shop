package httpapi

import (
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/wire"
	"github.com/labstack/echo/v5"
	"strconv"
)

func (a *API) GetPaymentHistory(c *echo.Context) error {
	account, err := a.auth(c, true)
	if err != nil {
		return err
	}
	in, err := decode[wire.PaymentHistoryInput](a, c, "PaymentHistoryInput")
	if err != nil {
		return err
	}
	out, err := a.payments.PaymentHistory(c.Request().Context(), account.Account.ID, payments.PaymentHistoryInput{Kind: string(in.Kind), BeforeCreatedAt: in.BeforeCreatedAt, BeforeId: in.BeforeId, LegacySourceId: in.LegacySourceId})
	if err != nil {
		return paymentError(err)
	}
	return c.JSON(200, paymentHistoryResult(out))
}
func (a *API) GetOperatorPaymentHistory(c *echo.Context) error {
	actor, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	id, err := resourceID(c)
	if err != nil {
		return err
	}
	in, err := decode[wire.PaymentHistoryInput](a, c, "PaymentHistoryInput")
	if err != nil {
		return err
	}
	out, err := a.payments.OperatorPaymentHistory(c.Request().Context(), actor.Account.AccountId, id, payments.PaymentHistoryInput{Kind: string(in.Kind), BeforeCreatedAt: in.BeforeCreatedAt, BeforeId: in.BeforeId, LegacySourceId: in.LegacySourceId})
	if err != nil {
		return paymentError(err)
	}
	return c.JSON(200, paymentHistoryResult(out))
}
func paymentHistoryResult(page payments.PaymentHistoryPage) wire.PaymentHistoryPage {
	out := wire.PaymentHistoryPage{Kind: wire.PaymentHistoryPageKind(page.Kind), HasMore: page.HasMore, Orders: []wire.PaymentHistoryOrder{}, Receipts: []wire.PaymentHistoryReceipt{}, LegacyTransactions: []wire.LegacyPaymentHistoryItem{}}
	if page.Kind == "refunds" {
		refunds := []wire.PaymentRefund{}
		for _, f := range page.Refunds {
			refunds = append(refunds, paymentRefundResult(f))
		}
		out.Refunds = &refunds
	}
	for _, p := range page.Orders {
		out.Orders = append(out.Orders, wire.PaymentHistoryOrder{OrderId: p.OrderId, Action: wire.PaymentHistoryOrderAction(p.Action), PaymentMethod: wire.PaymentHistoryOrderPaymentMethod(p.PaymentMethod), PaymentType: wire.PaymentHistoryOrderPaymentType(p.PaymentType), PaymentStatus: wire.PaymentHistoryOrderPaymentStatus(p.PaymentStatus), FulfillmentStatus: wire.PaymentHistoryOrderFulfillmentStatus(p.FulfillmentStatus), ReviewRequired: p.ReviewRequired, ReviewReason: p.ReviewReason, AccessOperationId: p.AccessOperationId, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt, Quote: wire.PurchaseQuote{AmountMinor: p.Quote.AmountMinor, Currency: wire.PurchaseQuoteCurrency(p.Quote.Currency), Devices: p.Quote.Devices, PeriodDays: p.Quote.PeriodDays, PlanId: p.Quote.PlanId, Profile: wire.PurchaseQuoteProfile(p.Quote.Profile), Revision: p.Quote.Revision, SourceAccessOperationId: p.Quote.SourceAccessOperationId, TrafficGb: p.Quote.TrafficGb}})
	}
	for _, r := range page.Receipts {
		item := wire.PaymentHistoryReceipt{OperationId: r.OperationId, OrderId: r.OrderId, PaymentMethod: wire.PaymentHistoryReceiptPaymentMethod(r.PaymentMethod), CreatedAt: r.CreatedAt, OccurredAt: r.OccurredAt, GrossMinor: r.GrossMinor, NetMinor: r.NetMinor, RawCurrency: r.RawCurrency, Source: wire.PaymentHistoryReceiptSource(r.Source), FundsOrder: r.FundsOrder, ReviewRequired: r.ReviewRequired, ReviewReason: r.ReviewReason, Codepro: r.Codepro, Unaccepted: r.Unaccepted}
		if r.Currency != nil {
			value := wire.PaymentHistoryReceiptCurrency(*r.Currency)
			item.Currency = &value
		}
		if c := r.CryptoAmounts; c != nil {
			item.CryptoAmounts = &wire.PaymentHistoryCryptoAmounts{PaymentAmount: c.PaymentAmount, PayerAmount: c.PayerAmount, MerchantAmount: c.MerchantAmount, PayerCurrency: c.PayerCurrency}
		}
		out.Receipts = append(out.Receipts, item)
	}
	for _, l := range page.LegacyTransactions {
		item := wire.LegacyPaymentHistoryItem{SourceId: l.SourceId, CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt, PaymentStatus: wire.LegacyPaymentHistoryItemPaymentStatus(l.PaymentStatus), FulfillmentStatus: "unknown"}
		if l.PaymentMethod != nil {
			value := wire.LegacyPaymentHistoryItemPaymentMethod(*l.PaymentMethod)
			item.PaymentMethod = &value
		}
		if q := l.Quote; q != nil {
			item.Quote = &wire.LegacyPaymentQuote{Action: wire.LegacyPaymentQuoteAction(q.Action), AmountMinor: q.AmountMinor, Currency: wire.LegacyPaymentQuoteCurrency(q.Currency), Devices: strconv.FormatInt(q.Devices, 10), PeriodDays: strconv.FormatInt(q.PeriodDays, 10), TrafficGb: strconv.FormatInt(q.TrafficGb, 10)}
		}
		out.LegacyTransactions = append(out.LegacyTransactions, item)
	}
	return out
}
