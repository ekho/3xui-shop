package payments

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type StarsInvoice struct {
	Payload, Title, Description string
	Amount                      int64
}
type StarsGateway struct {
	BotID   int64
	Invoice func(context.Context, StarsInvoice) (string, error)
	Refund  func(context.Context, int64, string) error
}
type StarsCheckout struct {
	State string  `json:"state"`
	URL   *string `json:"url"`
}
type StarsPreCheckoutInput struct {
	BotID, PayerID, Amount int64
	Currency, Payload      string
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
	if err = s.pool.QueryRow(ctx, `SELECT bot_id,payload FROM stars_checkouts WHERE order_id=$1`, order).Scan(&bot, &payload); err != nil {
		return empty, unavailable()
	}
	if s.stars == nil || s.stars.BotID != bot {
		return empty, failure(409, "PAYMENT_METHOD_UNAVAILABLE")
	}
	link, err := s.stars.Invoice(ctx, StarsInvoice{Payload: payload, Amount: p.amount, Title: "VPN subscription", Description: fmt.Sprintf("%d days, %d devices", out.Quote.PeriodDays, out.Quote.Devices)})
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
	if err = tx.QueryRow(ctx, `SELECT payer_id,bot_id,EXISTS(SELECT 1 FROM stars_refunds WHERE order_id=$1) FROM stars_checkouts WHERE order_id=$1`, id).Scan(&payer, &bot, &refunded); errors.Is(err, pgx.ErrNoRows) {
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
	return !unresolved, nil
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
