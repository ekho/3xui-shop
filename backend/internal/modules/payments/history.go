package payments

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type PaymentHistoryInput struct {
	Kind            string
	BeforeCreatedAt *time.Time
	BeforeId        *string
}
type PaymentHistoryPage struct {
	Kind               string
	Orders             []HistoryOrder
	Receipts           []HistoryReceipt
	LegacyTransactions []LegacyPayment
	HasMore            bool
}
type HistoryOrder struct {
	OrderId                            uuid.UUID
	Action, PaymentMethod, PaymentType string
	Quote                              PurchaseQuote
	PaymentStatus, FulfillmentStatus   string
	ReviewRequired                     bool
	ReviewReason                       *string
	AccessOperationId                  *uuid.UUID
	CreatedAt, ExpiresAt               time.Time
}
type HistoryReceipt struct {
	OperationId                string
	OrderId                    uuid.UUID
	PaymentMethod              string
	CreatedAt, OccurredAt      time.Time
	GrossMinor                 string
	NetMinor, Currency         *string
	RawCurrency, Source        string
	FundsOrder, ReviewRequired bool
	ReviewReason               *string
	Codepro, Unaccepted        bool
	CryptoAmounts              *CryptoAmounts
}
type CryptoAmounts struct{ PaymentAmount, PayerAmount, MerchantAmount, PayerCurrency string }
type LegacyPayment struct {
	SourceId                         string
	CreatedAt, UpdatedAt             time.Time
	PaymentStatus, FulfillmentStatus string
	PaymentMethod                    *string
	Quote                            *LegacyQuote
}
type LegacyQuote struct {
	Action, AmountMinor, Currency  string
	Devices, PeriodDays, TrafficGb int64
}

func historyInput(in PaymentHistoryInput) error {
	if in.Kind != "orders" && in.Kind != "receipts" && in.Kind != "legacy" {
		return failure(400, "INVALID_INPUT")
	}
	if (in.BeforeCreatedAt == nil) != (in.BeforeId == nil) {
		return failure(400, "INVALID_INPUT")
	}
	if in.BeforeCreatedAt == nil {
		return nil
	}
	t := *in.BeforeCreatedAt
	if t.IsZero() || t.Year() < 1 || t.Year() > 9999 || t.Nanosecond()%1000 != 0 || !validText(*in.BeforeId, 1, 128) {
		return failure(400, "INVALID_INPUT")
	}
	switch in.Kind {
	case "orders":
		id, err := uuid.Parse(*in.BeforeId)
		if err != nil || id == uuid.Nil {
			return failure(400, "INVALID_INPUT")
		}
	case "legacy":
		id, err := strconv.ParseInt(*in.BeforeId, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != *in.BeforeId {
			return failure(400, "INVALID_INPUT")
		}
	}
	return nil
}

func (s *Service) PaymentHistory(ctx context.Context, account uuid.UUID, in PaymentHistoryInput) (PaymentHistoryPage, error) {
	a, err := s.accountByID(ctx, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return PaymentHistoryPage{}, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return PaymentHistoryPage{}, err
	}
	if !accounts.SourceEligible(a) {
		return PaymentHistoryPage{}, failure(401, "INVALID_CREDENTIALS")
	}
	if a.Restricted {
		return PaymentHistoryPage{}, failure(403, "ACCOUNT_RESTRICTED")
	}
	return s.paymentHistory(ctx, account, in)
}
func (s *Service) OperatorPaymentHistory(ctx context.Context, actor, target uuid.UUID, in PaymentHistoryInput) (PaymentHistoryPage, error) {
	if err := s.authority.RequireOperator(ctx, actor); err != nil {
		return PaymentHistoryPage{}, err
	}
	if _, err := s.accountByID(ctx, target); errors.Is(err, pgx.ErrNoRows) {
		return PaymentHistoryPage{}, failure(404, "INVALID_INPUT")
	} else if err != nil {
		return PaymentHistoryPage{}, err
	}
	return s.paymentHistory(ctx, target, in)
}
func (s *Service) paymentHistory(ctx context.Context, account uuid.UUID, in PaymentHistoryInput) (PaymentHistoryPage, error) {
	out := PaymentHistoryPage{Kind: in.Kind, Orders: []HistoryOrder{}, Receipts: []HistoryReceipt{}, LegacyTransactions: []LegacyPayment{}}
	if err := historyInput(in); err != nil {
		return out, err
	}
	switch in.Kind {
	case "orders":
		rows, err := s.pool.Query(ctx, `SELECT id,action,payment_method,payment_type,quote,payment_status,fulfillment_status,review_required,review_reason,access_operation_id,created_at,expires_at FROM purchase_orders
 WHERE account_id=$1 AND ($2::timestamptz IS NULL OR (created_at,id)<($2,$3::uuid)) ORDER BY created_at DESC,id DESC LIMIT 51`, account, in.BeforeCreatedAt, in.BeforeId)
		if err != nil {
			return out, unavailable()
		}
		defer rows.Close()
		for rows.Next() {
			var order HistoryOrder
			var raw []byte
			if err = rows.Scan(&order.OrderId, &order.Action, &order.PaymentMethod, &order.PaymentType, &raw, &order.PaymentStatus, &order.FulfillmentStatus, &order.ReviewRequired, &order.ReviewReason, &order.AccessOperationId, &order.CreatedAt, &order.ExpiresAt); err != nil || json.Unmarshal(raw, &order.Quote) != nil {
				return out, unavailable()
			}
			out.Orders = append(out.Orders, order)
		}
		if rows.Err() != nil {
			return out, unavailable()
		}
		if len(out.Orders) > 50 {
			out.HasMore = true
			out.Orders = out.Orders[:50]
		}
	case "receipts":
		rows, err := s.pool.Query(ctx, `SELECT r.operation_id,r.order_id,
 CASE WHEN r.provider_data->>'provider' IN ('yookassa','cryptomus','heleket') THEN r.provider_data->>'provider'
 WHEN r.notification_type='manual_confirmation' THEN 'manual' ELSE 'yoomoney' END,
 r.created_at,r.occurred_at,r.gross_minor,r.net_minor,r.currency,r.notification_type,COALESCE(p.funding_operation_id=r.operation_id,false),r.review_reason,r.codepro,r.unaccepted,
 CASE WHEN r.provider_data->>'provider' IN ('cryptomus','heleket') THEN r.provider_data->>'payment_amount' END,
 CASE WHEN r.provider_data->>'provider' IN ('cryptomus','heleket') THEN r.provider_data->>'payer_amount' END,
 CASE WHEN r.provider_data->>'provider' IN ('cryptomus','heleket') THEN r.provider_data->>'merchant_amount' END,
 CASE WHEN r.provider_data->>'provider' IN ('cryptomus','heleket') THEN r.provider_data->>'payer_currency' END
 FROM purchase_receipts r JOIN purchase_orders p ON p.id=r.order_id WHERE p.account_id=$1 AND ($2::timestamptz IS NULL OR (r.created_at,r.operation_id)<($2,$3::text)) ORDER BY r.created_at DESC,r.operation_id DESC LIMIT 51`, account, in.BeforeCreatedAt, in.BeforeId)
		if err != nil {
			return out, unavailable()
		}
		defer rows.Close()
		for rows.Next() {
			var receipt HistoryReceipt
			var gross int64
			var net *int64
			var notification string
			var paymentAmount, payerAmount, merchantAmount, payerCurrency *string
			if rows.Scan(&receipt.OperationId, &receipt.OrderId, &receipt.PaymentMethod, &receipt.CreatedAt, &receipt.OccurredAt, &gross, &net, &receipt.RawCurrency, &notification, &receipt.FundsOrder, &receipt.ReviewReason, &receipt.Codepro, &receipt.Unaccepted, &paymentAmount, &payerAmount, &merchantAmount, &payerCurrency) != nil {
				return out, unavailable()
			}
			receipt.GrossMinor = strconv.FormatInt(gross, 10)
			if net != nil {
				value := strconv.FormatInt(*net, 10)
				receipt.NetMinor = &value
			}
			switch receipt.RawCurrency {
			case "643", "RUB":
				value := "RUB"
				receipt.Currency = &value
			case "USD":
				value := "USD"
				receipt.Currency = &value
			}
			receipt.Source = "provider"
			if notification == "manual_confirmation" {
				receipt.Source = "operator"
			}
			receipt.ReviewRequired = receipt.ReviewReason != nil || receipt.Codepro || receipt.Unaccepted
			if paymentAmount != nil && payerAmount != nil && merchantAmount != nil && payerCurrency != nil {
				receipt.CryptoAmounts = &CryptoAmounts{*paymentAmount, *payerAmount, *merchantAmount, *payerCurrency}
			}
			out.Receipts = append(out.Receipts, receipt)
		}
		if rows.Err() != nil {
			return out, unavailable()
		}
		if len(out.Receipts) > 50 {
			out.HasMore = true
			out.Receipts = out.Receipts[:50]
		}
	case "legacy":
		rows, err := s.pool.Query(ctx, `SELECT source_id,created_at,updated_at,status,subscription,source_tg_id FROM legacy_payment_transactions
 WHERE account_id=$1 AND ($2::timestamptz IS NULL OR (created_at,source_id)<($2,$3::bigint)) ORDER BY created_at DESC,source_id DESC LIMIT 51`, account, in.BeforeCreatedAt, in.BeforeId)
		if err != nil {
			return out, unavailable()
		}
		defer rows.Close()
		for rows.Next() {
			var item LegacyPayment
			var id, tgID int64
			var packed string
			if rows.Scan(&id, &item.CreatedAt, &item.UpdatedAt, &item.PaymentStatus, &packed, &tgID) != nil {
				return out, unavailable()
			}
			item.SourceId = strconv.FormatInt(id, 10)
			item.FulfillmentStatus = "unknown"
			item.PaymentMethod, item.Quote = legacyPaymentQuote(packed, tgID)
			out.LegacyTransactions = append(out.LegacyTransactions, item)
		}
		if rows.Err() != nil {
			return out, unavailable()
		}
		if len(out.LegacyTransactions) > 50 {
			out.HasMore = true
			out.LegacyTransactions = out.LegacyTransactions[:50]
		}
	}
	return out, nil
}

func legacyPaymentQuote(packed string, tgID int64) (*string, *LegacyQuote) {
	p := strings.Split(packed, ":")
	if len(p) != 9 || p[0] != "subscription" || (p[2] != "0" && p[2] != "1") || (p[3] != "0" && p[3] != "1") {
		return nil, nil
	}
	id, err := strconv.ParseInt(p[4], 10, 64)
	if err != nil || (id != 0 && id != tgID) {
		return nil, nil
	}
	devices, err := strconv.ParseInt(p[5], 10, 64)
	if err != nil || devices <= 0 {
		return nil, nil
	}
	days, err := strconv.ParseInt(p[6], 10, 64)
	if err != nil || days <= 0 {
		return nil, nil
	}
	traffic, err := strconv.ParseInt(p[7], 10, 64)
	if err != nil || traffic < 0 {
		return nil, nil
	}
	amount, err := minorUnits(p[8])
	if err != nil {
		return nil, nil
	}
	method, currency := "", ""
	switch p[1] {
	case "pay_yoomoney":
		method, currency = "yoomoney", "RUB"
	case "pay_yookassa":
		method, currency = "yookassa", "RUB"
	case "pay_manual":
		method, currency = "manual", "RUB"
	case "pay_cryptomus":
		method, currency = "cryptomus", "USD"
	case "pay_heleket":
		method, currency = "heleket", "USD"
	case "pay_telegram_stars":
		if amount%100 != 0 {
			return nil, nil
		}
		method, currency = "telegram_stars", "XTR"
		amount /= 100
	default:
		return nil, nil
	}
	action := "purchase"
	if p[2] == "1" {
		action = "renew"
	} else if p[3] == "1" {
		action = "change_plan"
	}
	return &method, &LegacyQuote{Action: action, AmountMinor: strconv.FormatInt(amount, 10), Currency: currency, Devices: devices, PeriodDays: days, TrafficGb: traffic}
}
