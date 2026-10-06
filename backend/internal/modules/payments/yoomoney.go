package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func yooMoneySignature(fields url.Values, secret []byte) string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if key != "sign" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, rfc3986(key)+"="+rfc3986(fields.Get(key)))
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strings.Join(parts, "&")))
	return hex.EncodeToString(mac.Sum(nil))
}

func rfc3986(value string) string { return strings.ReplaceAll(url.QueryEscape(value), "+", "%20") }

func verifyYooMoneySignature(fields url.Values, secret []byte) bool {
	sign := fields.Get("sign")
	if len(sign) != 64 || len(secret) == 0 {
		return false
	}
	want, err := hex.DecodeString(sign)
	if err != nil {
		return false
	}
	got, _ := hex.DecodeString(yooMoneySignature(fields, secret))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func minorUnits(text string) (int64, error) {
	parts := strings.Split(text, ".")
	if len(parts) > 2 || len(parts[0]) == 0 || len(parts[0]) > 17 {
		return 0, failure(400, "INVALID_INPUT")
	}
	for _, part := range parts {
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return 0, failure(400, "INVALID_INPUT")
			}
		}
	}
	if len(parts) == 2 && (len(parts[1]) == 0 || len(parts[1]) > 2) {
		return 0, failure(400, "INVALID_INPUT")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, failure(400, "INVALID_INPUT")
	}
	fraction := "00"
	if len(parts) == 2 {
		fraction = parts[1] + strings.Repeat("0", 2-len(parts[1]))
	}
	frac, _ := strconv.ParseInt(fraction, 10, 64)
	if whole > (int64(^uint64(0)>>1)-frac)/100 {
		return 0, failure(400, "INVALID_INPUT")
	}
	return whole*100 + frac, nil
}

// ReceiveYooMoney accepts only already parsed, unique UTF-8 form fields.
func (s *Service) ReceiveYooMoney(ctx context.Context, fields url.Values) error {
	for _, values := range fields {
		if len(values) != 1 {
			return failure(400, "INVALID_INPUT")
		}
	}
	if !verifyYooMoneySignature(fields, s.config().YooMoneyNotificationSecret) {
		return failure(403, "INVALID_CREDENTIALS")
	}
	if fields.Get("test_notification") == "true" {
		return nil
	}
	id := fields.Get("operation_id")
	label := fields.Get("label")
	if len(id) < 1 || len(id) > 128 || len(label) == 0 || len(id) != len(strings.TrimSpace(id)) {
		return failure(400, "INVALID_INPUT")
	}
	orderID, err := uuid.Parse(label)
	if err != nil {
		return nil
	} // Authenticated notification for an older or unknown label.
	net, err := minorUnits(fields.Get("amount"))
	if err != nil {
		return err
	}
	gross, err := minorUnits(fields.Get("withdraw_amount"))
	if err != nil {
		return err
	}
	codepro, err := strconv.ParseBool(fields.Get("codepro"))
	if err != nil {
		return failure(400, "INVALID_INPUT")
	}
	unaccepted, err := strconv.ParseBool(fields.Get("unaccepted"))
	if err != nil {
		return failure(400, "INVALID_INPUT")
	}
	occurred, err := time.Parse(time.RFC3339, fields.Get("datetime"))
	if err != nil {
		return failure(400, "INVALID_INPUT")
	}
	occurred = occurred.UTC().Truncate(time.Microsecond)
	typ, currency := fields.Get("notification_type"), fields.Get("currency")
	if len(typ) < 1 || len(typ) > 64 || len(currency) < 1 || len(currency) > 16 {
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	var accountID uuid.UUID
	if err = tx.QueryRow(ctx, "SELECT account_id FROM purchase_orders WHERE id=$1", orderID).Scan(&accountID); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return unavailable()
	}
	if _, err = s.lockAccount(ctx, tx, accountID); err != nil {
		return unavailable()
	}
	p, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 FOR UPDATE", orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	if occurred.Before(p.created.Add(-5*time.Minute)) || occurred.After(s.now().Add(5*time.Minute)) {
		return failure(400, "INVALID_INPUT")
	}
	var existingOrder uuid.UUID
	var existingWhen time.Time
	var existingGross int64
	var existingNet *int64
	var existingCurrency, existingType string
	var existingCodepro, existingUnaccepted bool
	err = tx.QueryRow(ctx, "SELECT order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted FROM purchase_receipts WHERE operation_id=$1 FOR UPDATE", id).Scan(&existingOrder, &existingWhen, &existingGross, &existingNet, &existingCurrency, &existingType, &existingCodepro, &existingUnaccepted)
	if err == nil {
		if existingOrder != orderID || !existingWhen.Equal(occurred) || existingGross != gross || (existingNet == nil || *existingNet != net) || existingCurrency != currency || existingType != typ || existingCodepro != codepro || existingUnaccepted != unaccepted {
			if _, err = tx.Exec(ctx, "UPDATE purchase_receipts SET review_reason='conflicting_operation_id' WHERE operation_id=$1", id); err != nil {
				return unavailable()
			}
			if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET review_required=true,review_reason='conflicting_operation_id' WHERE id=$1 OR id=$2", orderID, existingOrder); err != nil {
				return unavailable()
			}
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return unavailable()
	}
	review := ""
	var otherPaid bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE account_id=$1 AND id<>$2 AND (review_required OR fulfillment_status='needs_review' OR (payment_status='paid' AND ($3::text='purchase' OR fulfillment_status<>'applied'))))", accountID, orderID, p.action).Scan(&otherPaid); err != nil {
		return unavailable()
	}
	switch {
	case p.method != "yoomoney":
		review = "payment_method_mismatch"
	case typ != "p2p-incoming" && typ != "card-incoming", currency != "643", codepro, unaccepted, net <= 0, net > gross, gross != p.amount:
		review = "payment_mismatch"
	case p.paymentStatus == "canceled", occurred.After(p.expires):
		review = "late_or_canceled"
	case p.paymentStatus == "paid":
		review = "additional_payment"
	case otherPaid:
		review = "another_first_payment"
	}
	result, err := tx.Exec(ctx, "INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,review_reason,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11) ON CONFLICT DO NOTHING", id, orderID, occurred, gross, net, currency, typ, codepro, unaccepted, review, s.now())
	if err != nil {
		return unavailable()
	}
	if result.RowsAffected() == 0 {
		return unavailable() // A concurrent same-ID insert committed; provider retries safely.
	}
	if review != "" {
		status := "paid"
		if codepro || unaccepted || p.method != "yoomoney" {
			status = "pending"
		} // Held or protected is not accessible money.
		if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET payment_status=CASE WHEN payment_method<>'yoomoney' OR payment_status='paid' THEN payment_status ELSE $2 END,paid_at=CASE WHEN paid_at IS NOT NULL THEN paid_at WHEN $2='paid' THEN $3 ELSE NULL END,active=false,fulfillment_status=CASE WHEN access_operation_id IS NULL THEN 'needs_review' ELSE fulfillment_status END,review_required=true,review_reason=$4 WHERE id=$1", orderID, status, occurred, review); err != nil {
			return unavailable()
		}
		if status == "paid" {
			if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET active=false WHERE account_id=$1 AND id<>$2", accountID, orderID); err != nil {
				return unavailable()
			}
		}
	} else {
		if s.queue == nil || s.queue() == nil {
			return unavailable()
		}
		if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET payment_status='paid',paid_at=$2,active=false,fulfillment_status='queued',funding_operation_id=$3 WHERE id=$1", orderID, occurred, id); err != nil {
			return unavailable()
		}
		if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET active=false WHERE account_id=$1 AND id<>$2", accountID, orderID); err != nil {
			return unavailable()
		}
		if _, err = s.queue().InsertTx(ctx, tx, PurchaseArgs{OrderID: orderID}, &river.InsertOpts{Queue: "provision", MaxAttempts: 1000000}); err != nil {
			return unavailable()
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
}
