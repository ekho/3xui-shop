package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

type YooKassaArgs struct {
	OrderID uuid.UUID `json:"order_id"`
}

func (YooKassaArgs) Kind() string { return "yookassa_payment" }

type YooKassaWorker struct {
	river.WorkerDefaults[YooKassaArgs]
	Service *Service
}

func (w *YooKassaWorker) Work(ctx context.Context, job *river.Job[YooKassaArgs]) error {
	return w.Service.SyncYooKassa(ctx, job.Args.OrderID)
}
func (w *YooKassaWorker) Timeout(*river.Job[YooKassaArgs]) time.Duration { return 20 * time.Second }
func (w *YooKassaWorker) NextRetry(*river.Job[YooKassaArgs]) time.Time {
	return time.Now().Add(10 * time.Second)
}

func (s *Service) queueYooKassaTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, quote PurchaseQuote) error {
	c := s.config()
	amountMinor, err := strconv.ParseInt(quote.AmountMinor, 10, 64)
	if err != nil || amountMinor <= 0 {
		return unavailable()
	}
	amount := map[string]string{"value": fmt.Sprintf("%d.%02d", amountMinor/100, amountMinor%100), "currency": "RUB"}
	request := map[string]any{"amount": amount, "capture": true, "save_payment_method": false,
		"confirmation": map[string]string{"type": "redirect", "return_url": strings.TrimRight(c.CabinetOrigin, "/") + "/orders/" + id.String()},
		"metadata":     map[string]string{"order_id": id.String()},
		"receipt":      map[string]any{"customer": map[string]string{"email": c.ShopEmail}, "items": []any{map[string]any{"description": fmt.Sprintf("Access: %d devices, %d days", quote.Devices, quote.PeriodDays), "quantity": "1", "amount": amount, "vat_code": 1}}}}
	body, err := json.Marshal(request)
	if err != nil {
		return unavailable()
	}
	if _, err = tx.Exec(ctx, "INSERT INTO yookassa_checkouts(order_id,shop_id,test_mode,request) VALUES($1,$2,$3,$4)", id, c.YooKassaShopID, c.YooKassaTestMode, body); err != nil {
		return unavailable()
	}
	if s.queue == nil || s.queue() == nil {
		return unavailable()
	}
	_, err = s.queue().InsertTx(ctx, tx, YooKassaArgs{OrderID: id}, &river.InsertOpts{Queue: "payments", MaxAttempts: 1000000})
	if err != nil {
		return unavailable()
	}
	return nil
}

// Current provider addresses from the official webhook contract, checked 2026-10-06.
func (s *Service) YooKassaSourceAllowed(raw string) bool {
	ip, err := netip.ParseAddr(raw)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if s.config().YooKassaTestMode && (ip.IsLoopback() || ip.IsPrivate()) {
		return true
	}
	for _, cidr := range []string{"185.71.76.0/27", "185.71.77.0/27", "77.75.153.0/25", "77.75.156.11/32", "77.75.156.35/32", "77.75.154.128/25", "2a02:5180::/32"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return true
		}
	}
	return false
}

type kassaAmount struct{ Value, Currency string }
type kassaPayment struct {
	ID                     string `json:"id"`
	Status                 string `json:"status"`
	Paid, Test, Refundable *bool
	Amount                 kassaAmount
	Income                 *kassaAmount `json:"income_amount"`
	Refunded               *kassaAmount `json:"refunded_amount"`
	Created                string       `json:"created_at"`
	Captured               string       `json:"captured_at"`
	Recipient              struct {
		AccountID string `json:"account_id"`
	}
	Metadata     map[string]string
	Confirmation struct {
		Type string
		URL  string `json:"confirmation_url"`
	}
}
type kassaRow struct {
	order   uuid.UUID
	shop    string
	test    bool
	request []byte
	first   *time.Time
	id      *uuid.UUID
}

func (s *Service) kassaCheckout(ctx context.Context, order uuid.UUID) (kassaRow, error) {
	c := kassaRow{order: order}
	err := s.pool.QueryRow(ctx, "SELECT shop_id,test_mode,request,first_attempt_at,payment_id FROM yookassa_checkouts WHERE order_id=$1", order).Scan(&c.shop, &c.test, &c.request, &c.first, &c.id)
	return c, err
}
func validKassaURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 2000 && u.Scheme == "https" && u.User == nil && strings.EqualFold(u.Hostname(), "yoomoney.ru") && (u.Port() == "" || u.Port() == "443") && !strings.ContainsAny(raw, "\r\n\x00")
}
func (s *Service) kassaRequest(ctx context.Context, c kassaRow, method, path string, body []byte) (kassaPayment, int, error) {
	var out kassaPayment
	req, err := http.NewRequestWithContext(ctx, method, "https://api.yookassa.ru/v3/payments"+path, bytes.NewReader(body))
	if err != nil {
		return out, 0, unavailable()
	}
	req.SetBasicAuth(c.shop, s.config().YooKassaToken)
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotence-Key", c.order.String())
	}
	r, err := s.http.Do(req)
	if err != nil {
		return out, 0, unavailable()
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return out, r.StatusCode, unavailable()
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(data) == 0 || len(data) > 65536 || !utf8.Valid(data) || json.Unmarshal(data, &out) != nil {
		return out, r.StatusCode, unavailable()
	}
	return out, r.StatusCode, nil
}
func (s *Service) kassaReview(ctx context.Context, order uuid.UUID, reason string, observation []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	var account uuid.UUID
	if err = tx.QueryRow(ctx, "SELECT account_id FROM purchase_orders WHERE id=$1", order).Scan(&account); err != nil {
		return unavailable()
	}
	if _, err = s.lockAccount(ctx, tx, account); err != nil {
		return unavailable()
	}
	if err = s.kassaReviewTx(ctx, tx, order, reason, observation); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) kassaReviewTx(ctx context.Context, tx pgx.Tx, order uuid.UUID, reason string, observation []byte) error {
	var err error
	if _, err = tx.Exec(ctx, "UPDATE purchase_receipts SET review_reason=$2 WHERE operation_id=(SELECT funding_operation_id FROM purchase_orders WHERE id=$1)", order, reason); err != nil {
		return unavailable()
	}
	if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET active=false,review_required=true,review_reason=$2,fulfillment_status=CASE WHEN access_operation_id IS NULL THEN 'needs_review' ELSE fulfillment_status END WHERE id=$1", order, reason); err != nil {
		return unavailable()
	}
	if _, err = tx.Exec(ctx, "UPDATE yookassa_checkouts SET state='unavailable',observation=COALESCE($2,observation) WHERE order_id=$1", order, observation); err != nil {
		return unavailable()
	}
	return nil
}

// SQL commits the frozen request/first attempt before POST; no transaction spans HTTP.
func (s *Service) SyncYooKassa(ctx context.Context, order uuid.UUID) error {
	c, err := s.kassaCheckout(ctx, order)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	config := s.config()
	if config.YooKassaShopID != c.shop || config.YooKassaTestMode != c.test {
		return s.kassaReview(ctx, order, "provider_config_changed", nil)
	}
	if config.YooKassaToken == "" {
		return unavailable()
	}
	if c.id == nil {
		p, err := scanPurchase(s.pool.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1", order))
		if err != nil {
			return unavailable()
		}
		if p.review {
			return nil
		}
		if c.first == nil && (!p.active || !s.now().Before(p.expires)) {
			_, err = s.pool.Exec(ctx, "UPDATE yookassa_checkouts SET state='unavailable' WHERE order_id=$1 AND first_attempt_at IS NULL", order)
			return err
		}
		if c.first == nil && !config.YooKassaEnabled {
			return river.JobSnooze(10 * time.Second)
		}
		if err = s.pool.QueryRow(ctx, "UPDATE yookassa_checkouts SET first_attempt_at=COALESCE(first_attempt_at,$2) WHERE order_id=$1 RETURNING first_attempt_at", order, s.now().UTC().Truncate(time.Microsecond)).Scan(&c.first); err != nil {
			return unavailable()
		}
		if !s.now().Before(c.first.Add(24 * time.Hour)) {
			return s.kassaReview(ctx, order, "provider_creation_unknown", nil)
		}
		payment, status, err := s.kassaRequest(ctx, c, "POST", "", c.request)
		if err != nil {
			if status == 400 {
				return s.kassaReview(ctx, order, "provider_request_rejected", nil)
			}
			return err
		}
		id, err := uuid.Parse(payment.ID)
		if err != nil || id == uuid.Nil || len(payment.ID) != 36 || payment.Recipient.AccountID != c.shop || payment.Test == nil || *payment.Test != c.test || payment.Metadata["order_id"] != order.String() {
			return s.kassaReview(ctx, order, "provider_identity_mismatch", nil)
		}
		var link *string
		state := "preparing"
		if payment.Confirmation.Type == "redirect" && validKassaURL(payment.Confirmation.URL) {
			link = &payment.Confirmation.URL
			state = "ready"
		}
		result, err := s.pool.Exec(ctx, "UPDATE yookassa_checkouts SET payment_id=$2,confirmation_url=COALESCE(confirmation_url,$3),state=$4 WHERE order_id=$1 AND (payment_id IS NULL OR payment_id=$2)", order, id, link, state)
		if err != nil || result.RowsAffected() != 1 {
			return s.kassaReview(ctx, order, "provider_identity_conflict", nil)
		}
		c.id = &id
	}
	waiting, err := s.refreshKassa(ctx, c)
	if err != nil {
		return err
	}
	if waiting {
		if c.first != nil && !s.now().Before(c.first.Add(24*time.Hour)) {
			return s.kassaReview(ctx, order, "provider_status_unknown", nil)
		}
		return river.JobSnooze(10 * time.Second)
	}
	return nil
}

func (s *Service) ReceiveYooKassa(ctx context.Context, raw string) error {
	id, err := uuid.Parse(raw)
	if err != nil || len(raw) != 36 {
		return failure(400, "INVALID_INPUT")
	}
	var order uuid.UUID
	err = s.pool.QueryRow(ctx, "SELECT order_id FROM yookassa_checkouts WHERE payment_id=$1", id).Scan(&order)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	c, err := s.kassaCheckout(ctx, order)
	if err != nil {
		return unavailable()
	}
	if s.config().YooKassaShopID != c.shop || s.config().YooKassaTestMode != c.test {
		return s.kassaReview(ctx, order, "provider_config_changed", nil)
	}
	if s.config().YooKassaToken == "" {
		return unavailable()
	}
	_, err = s.refreshKassa(ctx, c)
	return err
}

func (s *Service) refreshKassa(ctx context.Context, c kassaRow) (bool, error) {
	p, _, err := s.kassaRequest(ctx, c, "GET", "/"+c.id.String(), nil)
	if err != nil {
		return false, err
	}
	return s.recordKassa(ctx, c, p)
}

func (s *Service) recordKassa(ctx context.Context, c kassaRow, payment kassaPayment) (bool, error) {
	// Retain typed financial facts, never raw customer/card/provider JSON.
	id, identityErr := uuid.Parse(payment.ID)
	gross, amountErr := minorUnits(payment.Amount.Value)
	created, timeErr := time.Parse(time.RFC3339Nano, payment.Created)
	validStatus := payment.Status == "pending" || payment.Status == "waiting_for_capture" || payment.Status == "succeeded" || payment.Status == "canceled"
	merchant, merchantErr := strconv.ParseUint(payment.Recipient.AccountID, 10, 64)
	meta, metaErr := uuid.Parse(payment.Metadata["order_id"])
	observation, _ := json.Marshal(map[string]any{"provider": "yookassa", "payment_id": c.id.String(), "valid_status": validStatus, "merchant_matches": payment.Recipient.AccountID == c.shop, "order_matches": metaErr == nil && meta == c.order, "test": payment.Test, "paid": payment.Paid})
	if identityErr != nil || id != *c.id || len(payment.ID) != 36 || amountErr != nil || timeErr != nil || !validStatus || payment.Paid == nil || payment.Test == nil || payment.Refundable == nil || merchantErr != nil || merchant == 0 || len(payment.Recipient.AccountID) > 20 || metaErr != nil || len(payment.Amount.Currency) != 3 || created.After(s.now().Add(5*time.Minute)) {
		return false, s.kassaReview(ctx, c.order, "provider_observation_invalid", observation)
	}
	created = created.UTC().Truncate(time.Microsecond)
	if payment.Status != "succeeded" || !*payment.Paid {
		if payment.Recipient.AccountID != c.shop || *payment.Test != c.test || meta != c.order {
			return false, s.kassaReview(ctx, c.order, "provider_identity_mismatch", observation)
		}
		if payment.Status == "succeeded" {
			return false, s.kassaReview(ctx, c.order, "provider_not_paid", observation)
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return false, unavailable()
		}
		defer tx.Rollback(ctx)
		var account uuid.UUID
		if err = tx.QueryRow(ctx, "SELECT account_id FROM purchase_orders WHERE id=$1", c.order).Scan(&account); err != nil {
			return false, unavailable()
		}
		if _, err = s.lockAccount(ctx, tx, account); err != nil {
			return false, unavailable()
		}
		if _, err = scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 FOR UPDATE", c.order)); err != nil {
			return false, unavailable()
		}
		var hadReceipt bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_receipts WHERE operation_id=$1)", "yookassa:"+id.String()).Scan(&hadReceipt); err != nil {
			return false, unavailable()
		}
		if hadReceipt {
			// An older nonterminal GET may arrive after the settled GET.
			if payment.Status == "pending" || payment.Status == "waiting_for_capture" {
				return false, tx.Commit(ctx)
			}
			if err = s.kassaReviewTx(ctx, tx, c.order, "provider_status_conflict", observation); err != nil {
				return false, err
			}
			return false, tx.Commit(ctx)
		}
		if payment.Status == "canceled" {
			if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET payment_status='canceled',active=false WHERE id=$1 AND payment_status='pending'", c.order); err != nil {
				return false, unavailable()
			}
			if _, err = tx.Exec(ctx, "UPDATE yookassa_checkouts SET state='unavailable',observation=$2 WHERE order_id=$1", c.order, observation); err != nil {
				return false, unavailable()
			}
			return false, tx.Commit(ctx)
		}
		if payment.Confirmation.Type == "redirect" && validKassaURL(payment.Confirmation.URL) {
			if _, err = tx.Exec(ctx, "UPDATE yookassa_checkouts SET confirmation_url=COALESCE(confirmation_url,$2),state='ready' WHERE order_id=$1", c.order, payment.Confirmation.URL); err != nil {
				return false, unavailable()
			}
		}
		return true, tx.Commit(ctx)
	}
	captured, err := time.Parse(time.RFC3339Nano, payment.Captured)
	if err != nil || captured.Before(created) || captured.After(s.now().Add(5*time.Minute)) {
		return false, s.kassaReview(ctx, c.order, "provider_capture_invalid", observation)
	}
	captured = captured.UTC().Truncate(time.Microsecond)
	var net *int64
	if payment.Income != nil {
		value, err := minorUnits(payment.Income.Value)
		if err != nil {
			return false, s.kassaReview(ctx, c.order, "provider_income_invalid", observation)
		}
		net = &value
	}
	var refund int64
	if payment.Refunded != nil {
		refund, err = minorUnits(payment.Refunded.Value)
		if err != nil {
			return false, s.kassaReview(ctx, c.order, "provider_refund_invalid", observation)
		}
	}
	proof, _ := json.Marshal(map[string]any{"provider": "yookassa", "payment_id": id.String(), "shop_id": payment.Recipient.AccountID, "test": *payment.Test, "order_id": meta.String(), "status": payment.Status, "paid": *payment.Paid, "amount_minor": strconv.FormatInt(gross, 10), "refund_minor": strconv.FormatInt(refund, 10), "created_at": created.Format(time.RFC3339Nano)})
	return s.settleKassaTx(ctx, c, payment, gross, net, refund, created, captured, proof)
}

func (s *Service) settleKassaTx(ctx context.Context, c kassaRow, payment kassaPayment, gross int64, net *int64, refund int64, created, captured time.Time, proof []byte) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, unavailable()
	}
	defer tx.Rollback(ctx)
	var account uuid.UUID
	if err = tx.QueryRow(ctx, "SELECT account_id FROM purchase_orders WHERE id=$1", c.order).Scan(&account); err != nil {
		return false, unavailable()
	}
	if _, err = s.lockAccount(ctx, tx, account); err != nil {
		return false, unavailable()
	}
	p, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 FOR UPDATE", c.order))
	if err != nil {
		return false, unavailable()
	}
	var firstIncome *int64
	if err = tx.QueryRow(ctx, "SELECT income_minor FROM yookassa_checkouts WHERE order_id=$1 FOR UPDATE", c.order).Scan(&firstIncome); err != nil {
		return false, unavailable()
	}
	operation := "yookassa:" + c.id.String()
	invalidAmounts := payment.Amount.Currency != "RUB" || net != nil && (*net <= 0 || *net > gross || payment.Income.Currency != "RUB") || refund != 0 || payment.Refunded != nil && payment.Refunded.Currency != "RUB"
	var originalOrder uuid.UUID
	var originalTime time.Time
	var originalGross int64
	var originalNet *int64
	var originalCurrency, originalType string
	var originalProof []byte
	err = tx.QueryRow(ctx, "SELECT order_id,occurred_at,gross_minor,net_minor,currency,notification_type,provider_data FROM purchase_receipts WHERE operation_id=$1 FOR UPDATE", operation).Scan(&originalOrder, &originalTime, &originalGross, &originalNet, &originalCurrency, &originalType, &originalProof)
	conflict := err == nil && (invalidAmounts || originalOrder != c.order || !originalTime.Equal(captured) || originalGross != gross || originalCurrency != payment.Amount.Currency || originalType != "yookassa.succeeded" || !equalKassaProof(originalProof, proof) || originalNet != nil && net != nil && *originalNet != *net || firstIncome != nil && net != nil && *firstIncome != *net)
	if err == nil {
		if conflict {
			if _, err = tx.Exec(ctx, "UPDATE purchase_receipts SET review_reason='conflicting_operation_id' WHERE operation_id=$1", operation); err != nil {
				return false, unavailable()
			}
			if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET review_required=true,review_reason='conflicting_operation_id',active=false,fulfillment_status=CASE WHEN access_operation_id IS NULL THEN 'needs_review' ELSE fulfillment_status END WHERE id=$1 OR id=$2", c.order, originalOrder); err != nil {
				return false, unavailable()
			}
			if _, err = tx.Exec(ctx, "UPDATE yookassa_checkouts SET state='unavailable',observation=$2 WHERE order_id=$1", c.order, proof); err != nil {
				return false, unavailable()
			}
		} else if _, err = tx.Exec(ctx, "UPDATE yookassa_checkouts SET income_minor=COALESCE(income_minor,$2),observation=$3 WHERE order_id=$1", c.order, net, proof); err != nil {
			return false, unavailable()
		}
		return false, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, unavailable()
	}
	var otherPaid bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE account_id=$1 AND id<>$2 AND payment_status='paid')", account, c.order).Scan(&otherPaid); err != nil {
		return false, unavailable()
	}
	review := ""
	switch {
	case p.method != "yookassa", payment.Recipient.AccountID != c.shop, *payment.Test != c.test, payment.Metadata["order_id"] != c.order.String():
		review = "payment_method_or_identity_mismatch"
	case gross != p.amount, invalidAmounts, created.Before(p.created.Add(-5 * time.Minute)):
		review = "payment_mismatch"
	case p.paymentStatus == "canceled", captured.After(p.expires):
		review = "late_or_canceled"
	case p.review:
		review = "order_requires_review"
	case p.paymentStatus == "paid", otherPaid:
		review = "another_first_payment"
	}
	result, err := tx.Exec(ctx, "INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,review_reason,created_at,provider_data) VALUES($1,$2,$3,$4,$5,$6,'yookassa.succeeded',false,false,NULLIF($7,''),$8,$9) ON CONFLICT DO NOTHING", operation, c.order, captured, gross, net, payment.Amount.Currency, review, s.now(), proof)
	if err != nil || result.RowsAffected() != 1 {
		return false, unavailable()
	}
	if _, err = tx.Exec(ctx, "UPDATE yookassa_checkouts SET state='unavailable',income_minor=COALESCE(income_minor,$2),observation=$3 WHERE order_id=$1", c.order, net, proof); err != nil {
		return false, unavailable()
	}
	if review != "" {
		owned := payment.Recipient.AccountID == c.shop && *payment.Test == c.test && payment.Metadata["order_id"] == c.order.String() && gross > 0
		if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET payment_status=CASE WHEN payment_status='paid' OR $2 THEN 'paid' ELSE payment_status END,paid_at=CASE WHEN paid_at IS NOT NULL THEN paid_at WHEN $2 THEN $3 ELSE NULL END,active=false,fulfillment_status=CASE WHEN access_operation_id IS NULL THEN 'needs_review' ELSE fulfillment_status END,review_required=true,review_reason=$4 WHERE id=$1", c.order, owned, captured, review); err != nil {
			return false, unavailable()
		}
	} else {
		if s.queue == nil || s.queue() == nil {
			return false, unavailable()
		}
		if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET payment_status='paid',paid_at=$2,active=false,fulfillment_status='queued',funding_operation_id=$3 WHERE id=$1", c.order, captured, operation); err != nil {
			return false, unavailable()
		}
		if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET active=false WHERE account_id=$1 AND id<>$2", account, c.order); err != nil {
			return false, unavailable()
		}
		if _, err = s.queue().InsertTx(ctx, tx, PurchaseArgs{OrderID: c.order}, &river.InsertOpts{Queue: "provision", MaxAttempts: 1000000}); err != nil {
			return false, unavailable()
		}
	}
	return false, tx.Commit(ctx)
}
func equalKassaProof(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	left, _ := json.Marshal(x)
	right, _ := json.Marshal(y)
	return bytes.Equal(left, right)
}
