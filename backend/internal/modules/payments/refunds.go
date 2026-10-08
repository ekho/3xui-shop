package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/payments/internal/store"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type PurchaseRefundInput struct {
	ReceiptOperationId string  `json:"receipt_operation_id"`
	Reference          string  `json:"reference"`
	Reason             string  `json:"reason"`
	ConfirmFull        bool    `json:"confirm_full"`
	KeepAccess         bool    `json:"keep_access"`
	ReturnedAmount     *string `json:"returned_amount,omitempty"`
}
type PaymentRefund struct {
	RefundId           uuid.UUID  `json:"refund_id"`
	OrderId            uuid.UUID  `json:"order_id"`
	OperatorAccountId  *uuid.UUID `json:"operator_account_id"`
	ReceiptOperationId string     `json:"receipt_operation_id"`
	PaymentMethod      string     `json:"payment_method"`
	CreatedAt          time.Time  `json:"created_at"`
	ReceiptGrossMinor  string     `json:"receipt_gross_minor"`
	ReceiptCurrency    string     `json:"receipt_currency"`
	ReturnedAmount     string     `json:"returned_amount"`
	ReturnedCurrency   string     `json:"returned_currency"`
	Reference          string     `json:"reference"`
	Reason             string     `json:"reason"`
	Source             string     `json:"source"`
}
type PaymentCase struct {
	Order               PurchaseOrder
	Receipt             *HistoryReceipt
	Refund              *PaymentRefund
	FinancialReviewOpen bool
	CanConfirmRefund    bool
	CanRefundStars      bool
	StarsRefundState    *string
}

var refundReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Every known receipt must be closed;
// a separately arriving late receipt cannot inherit an earlier refund.
const purchaseRefundClosed = `EXISTS(SELECT 1 FROM purchase_receipts r WHERE r.order_id=purchase_orders.id)
 AND NOT EXISTS(SELECT 1 FROM purchase_receipts r WHERE r.order_id=purchase_orders.id
 AND NOT EXISTS(SELECT 1 FROM purchase_refunds f WHERE f.receipt_operation_id=r.operation_id))`

const refundColumns = `f.id,f.order_id,f.operator_account_id,f.receipt_operation_id,f.payment_method,f.created_at,
 r.gross_minor,r.currency,f.returned_amount,f.returned_currency,f.reference,f.reason,f.source`

func scanRefund(row pgx.Row) (PaymentRefund, error) {
	var f PaymentRefund
	var gross int64
	err := row.Scan(&f.RefundId, &f.OrderId, &f.OperatorAccountId, &f.ReceiptOperationId, &f.PaymentMethod, &f.CreatedAt, &gross, &f.ReceiptCurrency, &f.ReturnedAmount, &f.ReturnedCurrency, &f.Reference, &f.Reason, &f.Source)
	f.ReceiptGrossMinor = strconv.FormatInt(gross, 10)
	if f.ReceiptCurrency == "643" {
		f.ReceiptCurrency = "RUB"
	}
	return f, err
}

func refundableReceipt(r HistoryReceipt) bool {
	gross, err := strconv.ParseInt(r.GrossMinor, 10, 64)
	if err != nil || gross <= 0 || r.Currency == nil || r.Codepro || r.Unaccepted {
		return false
	}
	switch r.PaymentMethod {
	case "yoomoney", "manual":
		if *r.Currency != "RUB" || r.NetMinor == nil {
			return false
		}
		net, err := strconv.ParseInt(*r.NetMinor, 10, 64)
		return err == nil && net > 0 && net <= gross
	case "yookassa":
		return *r.Currency == "RUB"
	case "cryptomus", "heleket":
		if *r.Currency != "USD" || r.CryptoAmounts == nil || !cryptoTicker.MatchString(r.CryptoAmounts.PayerCurrency) {
			return false
		}
		paid, ok := cryptoNumber(r.CryptoAmounts.PaymentAmount)
		payer, payerOK := cryptoNumber(r.CryptoAmounts.PayerAmount)
		return ok && payerOK && payer.Sign() > 0 && paid.Cmp(payer) >= 0
	}
	return false
}

func (s *Service) OperatorPaymentCase(ctx context.Context, actor, target, order uuid.UUID, receipt *string) (PaymentCase, error) {
	var out PaymentCase
	if actor == uuid.Nil || target == uuid.Nil || order == uuid.Nil || receipt != nil && !validText(*receipt, 1, 128) {
		return out, failure(400, "INVALID_INPUT")
	}
	if err := s.authority.RequireOperator(ctx, actor); err != nil {
		return out, err
	}
	p, err := scanPurchase(s.pool.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2", order, target))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, unavailable()
	}
	out.Order, err = s.publicPurchase(ctx, p)
	if err != nil {
		return out, err
	}
	out.FinancialReviewOpen = !out.Order.FullyRefunded && (p.review || p.fulfillmentStatus == "needs_review" || p.paymentStatus == "paid" && p.fulfillmentStatus != "applied")
	r, err := scanHistoryReceipt(s.pool.QueryRow(ctx, "SELECT "+receiptColumns+` FROM purchase_receipts r JOIN purchase_orders p ON p.id=r.order_id
 WHERE p.id=$1 AND p.account_id=$2 AND ($3::text IS NULL OR r.operation_id=$3)
 ORDER BY COALESCE(p.funding_operation_id=r.operation_id,false) DESC,r.created_at DESC,r.operation_id DESC LIMIT 1`, order, target, receipt))
	if errors.Is(err, pgx.ErrNoRows) {
		if receipt != nil {
			return out, failure(404, "INVALID_INPUT")
		}
		return out, nil
	}
	if err != nil {
		return out, unavailable()
	}
	out.Receipt = &r
	f, err := scanRefund(s.pool.QueryRow(ctx, "SELECT "+refundColumns+" FROM purchase_refunds f JOIN purchase_receipts r ON r.operation_id=f.receipt_operation_id WHERE f.order_id=$1 AND f.receipt_operation_id=$2", order, r.OperationId))
	if err == nil {
		out.Refund = &f
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, unavailable()
	}
	out.CanConfirmRefund = out.Refund == nil && refundableReceipt(r)
	if p.method == "telegram_stars" {
		var state string
		err := s.pool.QueryRow(ctx, `SELECT state FROM stars_refunds WHERE receipt_operation_id=$1`, r.OperationId).Scan(&state)
		if err == nil {
			out.StarsRefundState = &state
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return out, unavailable()
		}
		out.CanRefundStars = out.Refund == nil && out.StarsRefundState == nil && r.Currency != nil && *r.Currency == "XTR" && s.stars != nil && (s.stars.Ready == nil || s.stars.Ready())
		if out.CanRefundStars {
			protected, err := s.authority.OperatorRoleExists(ctx, nil, target)
			if err != nil {
				return out, err
			}
			a, err := s.accountByID(ctx, target)
			if err != nil {
				return out, unavailable()
			}
			var proof []byte
			if err = s.pool.QueryRow(ctx, `SELECT provider_data FROM purchase_receipts WHERE operation_id=$1 AND order_id=$2`, r.OperationId, order).Scan(&proof); err != nil {
				return out, unavailable()
			}
			var native starsProof
			out.CanRefundStars = !protected && actor != target && (a.TelegramID == nil || !s.authority.OperatorAllowed(*a.TelegramID)) && json.Unmarshal(proof, &native) == nil && native.BotID == s.stars.BotID
		}
	}
	return out, nil
}

// ConfirmPurchaseRefund records a trusted operator's external confirmation.
// It has no network/payout side effect and never modifies current panel access.
func (s *Service) ConfirmPurchaseRefund(ctx context.Context, actor, target, order, key uuid.UUID, in PurchaseRefundInput) (PaymentRefund, error) {
	var empty PaymentRefund
	if actor == uuid.Nil || target == uuid.Nil || order == uuid.Nil || key == uuid.Nil || !validText(in.ReceiptOperationId, 1, 128) || !refundReference.MatchString(in.Reference) || !validText(in.Reason, 1, 1000) || !in.ConfirmFull || !in.KeepAccess {
		return empty, failure(400, "INVALID_INPUT")
	}
	if err := s.authority.RequireOperator(ctx, actor); err != nil {
		return empty, err
	}
	owner, err := s.vpn.OpenAccessOwner(ctx, target)
	if err != nil {
		return empty, unavailable()
	}
	defer owner.Release()
	if err = owner.TryLock(ctx); err != nil {
		if errors.Is(err, vpn.ErrBusy) {
			return empty, failure(409, "ACCOUNT_ACCESS_BUSY")
		}
		return empty, unavailable()
	}
	tx, err := owner.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.authority.LockOperatorPair(ctx, tx, actor, target); err != nil {
		return empty, err
	}
	q := store.New(tx)
	principal := "operator-account:" + actor.String()
	operation := "confirmPurchaseRefund"
	hash := bodyHash(struct {
		Target, Order uuid.UUID
		Input         PurchaseRefundInput
	}{target, order, in})
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: operation, Key: key}); err != nil {
		return empty, unavailable()
	}
	if prior, found, e := replay[PaymentRefund](ctx, q, principal, operation, key, hash); found || e != nil {
		return prior, e
	}
	p, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2 FOR UPDATE", order, target))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	r, err := scanHistoryReceipt(tx.QueryRow(ctx, "SELECT "+receiptColumns+" FROM purchase_receipts r JOIN purchase_orders p ON p.id=r.order_id WHERE p.id=$1 AND p.account_id=$2 AND r.operation_id=$3", order, target, in.ReceiptOperationId))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	if !refundableReceipt(r) {
		return empty, failure(409, "PAYMENT_REFUND_CONFLICT")
	}
	var amount, currency string
	if c := r.CryptoAmounts; c != nil && (r.PaymentMethod == "cryptomus" || r.PaymentMethod == "heleket") {
		if in.ReturnedAmount == nil {
			return empty, failure(400, "INVALID_INPUT")
		}
		returned, ok := cryptoNumber(*in.ReturnedAmount)
		paid, _ := cryptoNumber(c.PaymentAmount)
		if !ok || returned.Sign() <= 0 || returned.Cmp(paid) > 0 {
			return empty, failure(400, "INVALID_INPUT")
		}
		amount, currency = *in.ReturnedAmount, c.PayerCurrency
	} else {
		if in.ReturnedAmount != nil {
			return empty, failure(400, "INVALID_INPUT")
		}
		gross, _ := strconv.ParseInt(r.GrossMinor, 10, 64)
		amount, currency = fmt.Sprintf("%d.%02d", gross/100, gross%100), "RUB"
	}
	out := PaymentRefund{RefundId: uuid.New(), OrderId: order, OperatorAccountId: &actor, ReceiptOperationId: r.OperationId, PaymentMethod: r.PaymentMethod, CreatedAt: s.now().UTC().Truncate(time.Microsecond), ReceiptGrossMinor: r.GrossMinor, ReceiptCurrency: *r.Currency, ReturnedAmount: amount, ReturnedCurrency: currency, Reference: in.Reference, Reason: strings.TrimSpace(in.Reason), Source: "operator"}
	_, err = tx.Exec(ctx, `INSERT INTO purchase_refunds(id,order_id,operator_account_id,receipt_operation_id,payment_method,created_at,returned_amount,returned_currency,reference,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, out.RefundId, order, actor, r.OperationId, r.PaymentMethod, out.CreatedAt, amount, currency, out.Reference, out.Reason)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return empty, failure(409, "PAYMENT_REFUND_CONFLICT")
		}
		return empty, unavailable()
	}
	if p.fundingID != nil && *p.fundingID == r.OperationId && p.fulfillmentStatus != "applied" {
		if p.accessID != nil {
			if err = s.vpn.RetirePurchaseAccessTx(ctx, tx, owner, target, order, *p.accessID); err != nil {
				return empty, unavailable()
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE purchase_orders SET active=false,fulfillment_status='needs_review',review_required=true,review_reason='funding_refunded' WHERE id=$1`, order); err != nil {
			return empty, unavailable()
		}
	}
	// operation_id belongs to trial_operations. The audit ID is the refund ID;
	// the immutable payments record supplies its order/receipt provenance.
	if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: out.RefundId, CreatedAt: out.CreatedAt, Action: "purchase_refund_confirmed", AccountID: target, OperatorAccountID: &actor, AccessOperationID: p.accessID, Reason: &out.Reason}); err != nil {
		return empty, unavailable()
	}
	if err = s.saveIdempotency(ctx, q, principal, operation, key, hash, out); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, unavailable()
	}
	return out, nil
}
