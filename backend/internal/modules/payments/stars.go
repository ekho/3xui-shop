package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/accounts"
	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/payments/internal/store"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type StarsInvoice struct {
	Payload, Title, Description string
	Amount                      int64
	SubscriptionPeriod          int64
}
type StarsGateway struct {
	BotID            int64
	Invoice          func(context.Context, StarsInvoice) (string, error)
	Refund           func(context.Context, int64, string) error
	Ready            func() bool
	EditSubscription func(context.Context, int64, string, bool) error
}
type StarsCheckout struct {
	State string  `json:"state"`
	URL   *string `json:"url"`
}
type StarsPreCheckoutInput struct {
	BotID, PayerID, Amount int64
	Currency, Payload      string
	QueryID                string
}

type StarsPaymentInput struct {
	StarsPreCheckoutInput
	ChargeID, ProviderChargeID string
	At                         time.Time
	Recurring, FirstRecurring  bool
	SubscriptionExpiresAt      int64
}
type starsProof struct {
	Provider              string `json:"provider"`
	BotID                 int64  `json:"bot_id"`
	PayerID               int64  `json:"payer_id"`
	Payload               string `json:"payload"`
	ChargeID              string `json:"charge_id"`
	ProviderChargeID      string `json:"provider_charge_id"`
	Amount                string `json:"amount_minor"`
	Currency              string `json:"currency"`
	Recurring             bool   `json:"recurring"`
	FirstRecurring        bool   `json:"first_recurring"`
	SubscriptionExpiresAt int64  `json:"subscription_expires_at"`
}

func starsReceiptKey(bot int64, charge string) string {
	hash := sha256.Sum256([]byte(strconv.FormatInt(bot, 10) + ":" + charge))
	return "stars:" + hex.EncodeToString(hash[:])
}
func validStarsPayment(in StarsPaymentInput) bool {
	return validStarsPaymentBase(in) && !in.Recurring && !in.FirstRecurring && in.SubscriptionExpiresAt == 0
}
func validStarsPaymentBase(in StarsPaymentInput) bool {
	_, valid := starsOrderID(in.Payload)
	return valid && in.BotID > 0 && in.PayerID > 0 && in.PayerID <= 1<<52-1 && in.Amount > 0 && validText(in.Currency, 1, 16) && len(in.ChargeID) > 0 && len(in.ChargeID) <= 4096 && utf8.ValidString(in.ChargeID) && !strings.ContainsRune(in.ChargeID, '\x00') && len(in.ProviderChargeID) <= 4096 && utf8.ValidString(in.ProviderChargeID) && !strings.ContainsRune(in.ProviderChargeID, '\x00') && in.At.Unix() > 0 && in.At.Year() <= 9999
}
func starsPaymentProof(in StarsPaymentInput) []byte {
	raw, _ := json.Marshal(starsProof{Provider: "telegram_stars", BotID: in.BotID, PayerID: in.PayerID, Payload: in.Payload, ChargeID: in.ChargeID, ProviderChargeID: in.ProviderChargeID, Amount: strconv.FormatInt(in.Amount, 10), Currency: in.Currency, Recurring: in.Recurring, FirstRecurring: in.FirstRecurring, SubscriptionExpiresAt: in.SubscriptionExpiresAt})
	return raw
}
func starsLockCharge(ctx context.Context, tx pgx.Tx, key string) error {
	return store.New(tx).LockIdempotency(ctx, store.LockIdempotencyParams{Principal: "telegram-stars", Operation: "charge", Key: uuid.NewSHA1(uuid.Nil, []byte(key))})
}
func (s *Service) starsOrderTx(ctx context.Context, tx pgx.Tx, in StarsPaymentInput) (purchaseRow, int64, int64, error) {
	var empty purchaseRow
	id, _ := starsOrderID(in.Payload)
	var account uuid.UUID
	err := tx.QueryRow(ctx, `SELECT account_id FROM purchase_orders WHERE id=$1`, id).Scan(&account)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, 0, 0, failure(409, "STARS_UNSUPPORTED_PAYMENT")
	}
	if err != nil {
		return empty, 0, 0, unavailable()
	}
	if _, err = s.lockAccount(ctx, tx, account); err != nil {
		return empty, 0, 0, unavailable()
	}
	p, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return empty, 0, 0, unavailable()
	}
	var bot, payer int64
	err = tx.QueryRow(ctx, `SELECT bot_id,payer_id FROM stars_checkouts WHERE order_id=$1`, id).Scan(&bot, &payer)
	if errors.Is(err, pgx.ErrNoRows) || p.method != "telegram_stars" {
		return empty, 0, 0, failure(409, "STARS_UNSUPPORTED_PAYMENT")
	}
	if err != nil {
		return empty, 0, 0, unavailable()
	}
	return p, bot, payer, nil
}

// RecordStarsPayment is reachable only through the trusted Telegram adapter.
// Current sales/identity errors retain the charge and prevent automatic issue.
func (s *Service) RecordStarsPayment(ctx context.Context, in StarsPaymentInput) error {
	if !validStarsPaymentBase(in) {
		return failure(409, "STARS_UNSUPPORTED_PAYMENT")
	}
	in.At = in.At.UTC().Truncate(time.Microsecond)
	key := starsReceiptKey(in.BotID, in.ChargeID)
	proof := starsPaymentProof(in)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	if err = starsLockCharge(ctx, tx, key); err != nil {
		return unavailable()
	}
	p, bot, payer, err := s.starsOrderTx(ctx, tx, in)
	if err != nil {
		return err
	}
	if !recurringQuote(p.quote) && !validStarsPayment(in) {
		return failure(409, "STARS_UNSUPPORTED_PAYMENT")
	}
	var oldOrder uuid.UUID
	var oldAt time.Time
	var oldAmount int64
	var oldCurrency string
	var oldProof []byte
	var oldReview *string
	err = tx.QueryRow(ctx, `SELECT order_id,occurred_at,gross_minor,currency,provider_data,review_reason FROM purchase_receipts WHERE operation_id=$1 FOR UPDATE`, key).Scan(&oldOrder, &oldAt, &oldAmount, &oldCurrency, &oldProof, &oldReview)
	if err == nil {
		var saved, current starsProof
		conflict := json.Unmarshal(oldProof, &saved) != nil || json.Unmarshal(proof, &current) != nil || saved != current || oldOrder != p.id || !oldAt.Equal(in.At) || oldAmount != in.Amount || oldCurrency != in.Currency
		if conflict {
			if _, err = tx.Exec(ctx, `UPDATE purchase_receipts SET review_reason='conflicting_operation_id' WHERE operation_id=$1`, key); err != nil {
				return unavailable()
			}
			if _, err = tx.Exec(ctx, `UPDATE purchase_orders SET review_required=true,review_reason='conflicting_operation_id',active=false,fulfillment_status=CASE WHEN access_operation_id IS NULL THEN 'needs_review' ELSE fulfillment_status END WHERE id=$1 OR id=$2`, p.id, oldOrder); err != nil {
				return unavailable()
			}
			if oldReview == nil || *oldReview != "conflicting_operation_id" {
				if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: p.account, CreatedAt: s.now(), Action: "stars_payment_conflict", Reason: &key}); err != nil {
					return unavailable()
				}
			}
		}
		if err = tx.Commit(ctx); err != nil {
			return unavailable()
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return unavailable()
	}
	reason := ""
	switch {
	case bot != in.BotID || payer != in.PayerID:
		reason = "payment_identity_mismatch"
	case in.Amount != p.amount || in.Currency != "XTR" || in.At.Before(p.created.Add(-5*time.Minute)):
		reason = "payment_mismatch"
	case recurringQuote(p.quote) && (!in.FirstRecurring || !validStarsRecurringPeriod(in)):
		reason = "invalid_recurring_payment"
	case in.At.After(p.expires) || p.paymentStatus == "canceled":
		reason = "late_or_canceled"
	case p.review:
		reason = "order_requires_review"
	case p.paymentStatus == "paid":
		reason = "another_first_payment"
	case !s.config().StarsEnabled:
		reason = "stars_disabled"
	}
	if reason == "" {
		reason, err = s.purchasePolicyTx(ctx, tx, p)
		if err != nil {
			return err
		}
	}
	var refunded bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stars_refunds WHERE receipt_operation_id=$1)`, key).Scan(&refunded); err != nil {
		return unavailable()
	}
	if refunded {
		reason = "funding_refunded"
	}
	_, err = tx.Exec(ctx, `INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,review_reason,created_at,provider_data) VALUES($1,$2,$3,$4,NULL,$5,'telegram_stars.paid',false,false,NULLIF($6,''),$7,$8)`, key, p.id, in.At, in.Amount, in.Currency, reason, s.now(), proof)
	if err != nil {
		return unavailable()
	}
	if recurringQuote(p.quote) {
		if err = s.recordStarsFirstSubscriptionTx(ctx, tx, p, in, key, reason); err != nil {
			return err
		}
	}
	if reason != "" {
		_, err = tx.Exec(ctx, `UPDATE purchase_orders SET payment_status='paid',paid_at=COALESCE(paid_at,$2),active=false,review_required=true,review_reason=$3,fulfillment_status=CASE WHEN access_operation_id IS NULL THEN 'needs_review' ELSE fulfillment_status END WHERE id=$1`, p.id, in.At, reason)
	} else {
		if s.queue == nil || s.queue() == nil {
			return unavailable()
		}
		_, err = tx.Exec(ctx, `UPDATE purchase_orders SET payment_status='paid',paid_at=$2,active=false,fulfillment_status='queued',funding_operation_id=$3 WHERE id=$1`, p.id, in.At, key)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE purchase_orders SET active=false WHERE account_id=$1 AND id<>$2`, p.account, p.id)
		}
		if err == nil {
			_, err = s.queueFundedPurchaseTx(ctx, tx, p)
		}
	}
	if err != nil {
		return unavailable()
	}
	if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: p.account, CreatedAt: s.now(), Action: "stars_payment_received", Reason: &key}); err != nil {
		return unavailable()
	}
	if refunded {
		if err = s.completeStarsRefundTx(ctx, tx, p, key, nil); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
}

// ConfigureStars is called once by root composition, before serving requests.
func (s *Service) ConfigureStars(g StarsGateway) {
	if g.BotID > 0 && g.Invoice != nil && g.Refund != nil {
		s.stars = &g
	}
}
func purchaseSourceEligible(a accounts.Snapshot, method string) bool {
	if method != "telegram_stars" {
		return a.Kind == "web" && a.VerifiedAt != nil
	}
	return accounts.SourceEligible(a) && a.TelegramID != nil && *a.TelegramID > 0 && !a.TelegramLoginDisabled && a.TermsVersion != nil && a.PrivacyVersion != nil
}
func purchaseBillingEligible(a accounts.Snapshot, method string) bool {
	if method != "telegram_stars" {
		return independentBilling(a)
	}
	return a.LegacyUserID == nil && purchaseSourceEligible(a, method)
}
func (s *Service) StarsPaymentMethods(ctx context.Context, account uuid.UUID) (PaymentMethods, error) {
	out := PaymentMethods{Methods: []PaymentMethod{}}
	a, err := s.accountByID(ctx, account)
	if err != nil {
		return out, unavailable()
	}
	if !purchaseSourceEligible(a, "telegram_stars") {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	if a.Restricted {
		return out, failure(403, "ACCOUNT_RESTRICTED")
	}
	if s.methodEnabled("telegram_stars") {
		out.Methods = append(out.Methods, PaymentMethod{Id: "telegram_stars", Currency: "XTR"})
	}
	return out, nil
}

var starsSlug = regexp.MustCompile(`^/\$[A-Za-z0-9_-]{1,256}$`)

func validStarsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "t.me" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.RawPath == "" && starsSlug.MatchString(u.Path)
}
func starsOrderID(payload string) (uuid.UUID, bool) {
	raw := strings.TrimPrefix(payload, "stars:v1:")
	id, err := uuid.Parse(raw)
	return id, err == nil && id != uuid.Nil && payload == "stars:v1:"+id.String()
}
func (s *Service) CreateStarsInvoice(ctx context.Context, account, order uuid.UUID) (PurchaseOrder, error) {
	var empty PurchaseOrder
	if account == uuid.Nil || order == uuid.Nil {
		return empty, failure(400, "INVALID_INPUT")
	}
	owner, err := s.vpn.OpenAccessOwner(ctx, account)
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
	p, err := scanPurchase(s.pool.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2", order, account))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	out, err := s.publicPurchase(ctx, p)
	if err != nil {
		return empty, err
	}
	if p.method != "telegram_stars" || !out.CanPay {
		return empty, failure(409, "PAYMENT_METHOD_UNAVAILABLE")
	}
	if out.StarsCheckout.URL != nil {
		return out, nil
	}
	var payload string
	var bot int64
	var period int64
	if err = s.pool.QueryRow(ctx, `SELECT bot_id,payload,subscription_period FROM stars_checkouts WHERE order_id=$1`, order).Scan(&bot, &payload, &period); err != nil {
		return empty, unavailable()
	}
	if s.stars == nil || s.stars.BotID != bot {
		return empty, failure(409, "PAYMENT_METHOD_UNAVAILABLE")
	}
	link, err := s.stars.Invoice(ctx, StarsInvoice{Payload: payload, Amount: p.amount, Title: "VPN subscription", Description: fmt.Sprintf("%d days, %d devices", out.Quote.PeriodDays, out.Quote.Devices), SubscriptionPeriod: period})
	if err != nil || !validStarsURL(link) {
		return empty, unavailable()
	}
	tx, err := owner.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.lockAccount(ctx, tx, account); err != nil {
		return empty, unavailable()
	}
	p, err = scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2 FOR UPDATE", order, account))
	if err != nil {
		return empty, unavailable()
	}
	reason, err := s.purchasePolicyTx(ctx, tx, p)
	if err != nil {
		return empty, err
	}
	if reason != "" || !s.methodEnabled(p.method) || !p.active || p.review || p.paymentStatus != "pending" || !s.now().Before(p.expires) {
		return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
	}
	if _, err = tx.Exec(ctx, `UPDATE stars_checkouts SET invoice_url=$2 WHERE order_id=$1 AND invoice_url IS NULL`, order, link); err != nil {
		return empty, unavailable()
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, unavailable()
	}
	return s.publicPurchase(ctx, p)
}
func (s *Service) CheckStarsPreCheckout(ctx context.Context, in StarsPreCheckoutInput) (bool, error) {
	id, valid := starsOrderID(in.Payload)
	if !valid || in.BotID <= 0 || in.PayerID <= 0 || in.Amount <= 0 || in.Currency != "XTR" || !s.methodEnabled("telegram_stars") || s.stars.BotID != in.BotID {
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, unavailable()
	}
	defer tx.Rollback(ctx)
	var account uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT account_id FROM purchase_orders WHERE id=$1`, id).Scan(&account); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, unavailable()
	}
	if _, err = s.lockAccount(ctx, tx, account); err != nil {
		return false, unavailable()
	}
	p, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return false, unavailable()
	}
	var payer, bot int64
	var refunded bool
	var period int64
	var reserved *string
	if err = tx.QueryRow(ctx, `SELECT payer_id,bot_id,EXISTS(SELECT 1 FROM stars_refunds WHERE order_id=$1),subscription_period,pre_checkout_id FROM stars_checkouts WHERE order_id=$1`, id).Scan(&payer, &bot, &refunded, &period, &reserved); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, unavailable()
	}
	if p.method != "telegram_stars" || p.action != "purchase" || payer != in.PayerID || bot != in.BotID || p.amount != in.Amount || !p.active || p.review || p.paymentStatus != "pending" || !s.now().Before(p.expires) || refunded {
		return false, nil
	}
	reason, err := s.purchasePolicyTx(ctx, tx, p)
	if err != nil || reason != "" {
		return false, err
	}
	unresolved, err := s.vpn.UnresolvedTx(ctx, tx, account)
	if err != nil {
		return false, unavailable()
	}
	if unresolved {
		return false, nil
	}
	if period != 0 {
		if len(in.QueryID) > 128 || !validText(in.QueryID, 1, 128) || reserved != nil && *reserved != in.QueryID {
			return false, nil
		}
		if reserved == nil {
			if _, err = tx.Exec(ctx, `UPDATE stars_checkouts SET pre_checkout_id=$2 WHERE order_id=$1 AND pre_checkout_id IS NULL`, id, in.QueryID); err != nil {
				return false, unavailable()
			}
		}
		if err = tx.Commit(ctx); err != nil {
			return false, unavailable()
		}
	}
	return true, nil
}

func (s *Service) CanPurchaseStars(ctx context.Context, account uuid.UUID) (bool, error) {
	if !s.methodEnabled("telegram_stars") {
		return false, nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, unavailable()
	}
	defer tx.Rollback(ctx)
	reason, err := s.purchasePolicyTx(ctx, tx, purchaseRow{account: account, method: "telegram_stars", action: "purchase"})
	if err != nil || reason != "" {
		return false, err
	}
	unresolved, err := s.vpn.UnresolvedTx(ctx, tx, account)
	if err != nil {
		return false, unavailable()
	}
	return !unresolved, nil
}
