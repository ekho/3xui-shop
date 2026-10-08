package payments

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/payments/internal/store"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// Until C35 owns authoritative recurrence states, only unlinked new web
// accounts prove they cannot also be billed by the legacy Stars handler.
func independentBilling(a accounts.Snapshot) bool {
	return a.Kind == "web" && a.TelegramID == nil && a.LegacyUserID == nil
}

// All five funding paths retain valid money proof before checking live access.
// An eligibility conflict needs review; it must not invalidate the receipt.
func (s *Service) queueFundedPurchaseTx(ctx context.Context, tx pgx.Tx, p purchaseRow) (string, error) {
	if p.action != "purchase" {
		reason, err := s.purchasePolicyTx(ctx, tx, p)
		if err != nil {
			return "", err
		}
		if reason != "" {
			_, err = tx.Exec(ctx, "UPDATE purchase_orders SET fulfillment_status='needs_review',review_required=true,review_reason=$2 WHERE id=$1", p.id, reason)
			return reason, err
		}
	}
	_, err := s.queue().InsertTx(ctx, tx, PurchaseArgs{OrderID: p.id}, &river.InsertOpts{Queue: "provision", MaxAttempts: 1000000})
	return "", err
}

func renewalError(err error) error {
	var e *subscriptions.Error
	if errors.As(err, &e) {
		return failure(e.Status, e.Code)
	}
	return err
}

func (s *Service) requireOrderPlan(ctx context.Context, tx pgx.Tx, account uuid.UUID, action string, plan uuid.UUID, source *uuid.UUID) error {
	if action == "purchase" {
		return nil
	}
	code := "RENEWAL_NOT_ELIGIBLE"
	if action == "change_plan" {
		code = "PLAN_CHANGE_NOT_ELIGIBLE"
	}
	current, err := s.subscriptions.CurrentPlanSourceTx(ctx, tx, account)
	if err != nil {
		mapped := renewalError(err)
		var e *Error
		if errors.As(mapped, &e) && e.Code == "RENEWAL_NOT_ELIGIBLE" {
			return failure(e.Status, code)
		}
		return mapped
	}
	if action == "renew" && *current.PlanID != plan || action == "change_plan" && (source == nil || *source != current.OperationID) {
		return failure(409, code)
	}
	return nil
}

// Completed payments are reusable for managing access or proved starter clearing.
// Review and unresolved funding stay blocked regardless of the current plan.
func (s *Service) purchaseHistoryBlockedTx(ctx context.Context, tx pgx.Tx, account, except uuid.UUID, action, method string) (bool, error) {
	ignoreApplied := action == "renew" || action == "change_plan"
	if !ignoreApplied && method != "telegram_stars" {
		var err error
		ignoreApplied, err = s.vpn.PlanClearedTx(ctx, tx, account)
		if err != nil {
			return false, unavailable()
		}
	}
	var q store.DBTX = s.pool
	if tx != nil {
		q = tx
	}
	var blocked bool
	err := q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE account_id=$1 AND id<>$2 AND ((NOT ("+purchaseRefundClosed+") AND (review_required OR fulfillment_status='needs_review' OR payment_status='paid' AND fulfillment_status<>'applied')) OR payment_status='paid' AND fulfillment_status='applied' AND NOT $3::boolean))", account, except, ignoreApplied).Scan(&blocked)
	if err != nil {
		return false, unavailable()
	}
	return blocked, nil
}

// The same live policy guards checkout, preparation, recovery and panel writes.
// A reason retains paid funds for review; a dependency error remains retryable.
func (s *Service) purchasePolicyTx(ctx context.Context, tx pgx.Tx, p purchaseRow) (string, error) {
	var a accounts.Snapshot
	var err error
	if tx == nil {
		a, err = s.accountByID(ctx, p.account)
	} else {
		a, err = s.accountByIDTx(ctx, tx, p.account)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "account_not_eligible", nil
	}
	if err != nil {
		return "", unavailable()
	}
	if !purchaseBillingEligible(a, p.method) {
		return "external_billing_unverified", nil
	}
	if !purchaseSourceEligible(a, p.method) || a.Restricted || a.VpnBanned || stringValue(a.AccessProfile) == "unlimited" {
		return "account_not_eligible", nil
	}
	if p.method == "telegram_stars" && !s.config().StarsEnabled {
		return "stars_disabled", nil
	}
	if p.method == "telegram_stars" && p.id != uuid.Nil {
		var payer, bot int64
		var q store.DBTX = s.pool
		if tx != nil {
			q = tx
		}
		if err := q.QueryRow(ctx, `SELECT payer_id,bot_id FROM stars_checkouts WHERE order_id=$1`, p.id).Scan(&payer, &bot); err != nil {
			return "", unavailable()
		}
		if p.action != "purchase" || a.TelegramID == nil || *a.TelegramID != payer {
			return "stars_identity_changed", nil
		}
	}
	if p.action == "purchase" {
		blocked, err := s.purchaseHistoryBlockedTx(ctx, tx, p.account, p.id, p.action, p.method)
		if err != nil {
			return "", err
		}
		if blocked {
			return "another_first_payment", nil
		}
	}
	if p.action == "renew" || p.action == "change_plan" {
		var quote PurchaseQuote
		if json.Unmarshal(p.quote, &quote) != nil || (p.action == "change_plan") != (quote.SourceAccessOperationId != nil) {
			return "invalid_quote", nil
		}
		err = s.requireOrderPlan(ctx, tx, p.account, p.action, quote.PlanId, quote.SourceAccessOperationId)
		if err != nil {
			var e *Error
			if errors.As(err, &e) && e.Status < 500 {
				if p.action == "change_plan" {
					return "plan_change_not_eligible", nil
				}
				return "renewal_not_eligible", nil
			}
			return "", err
		}
	}
	return "", nil
}

func (s *Service) RenewalOffer(ctx context.Context, account uuid.UUID) (catalogue.PlanSnapshot, error) {
	var empty catalogue.PlanSnapshot
	a, err := s.accountByID(ctx, account)
	if err != nil {
		return empty, unavailable()
	}
	if !independentBilling(a) {
		return empty, failure(409, "EXTERNAL_BILLING_UNVERIFIED")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	id, err := s.subscriptions.RenewalPlanIDTx(ctx, tx, account)
	if err != nil {
		return empty, renewalError(err)
	}
	plan, err := s.catalogue.CurrentPlanTx(ctx, tx, id)
	if errors.Is(err, catalogue.ErrNotFound) {
		return empty, failure(409, "RENEWAL_NOT_ELIGIBLE")
	}
	if err != nil {
		return empty, unavailable()
	}
	terms := plan.Terms
	if plan.Archived || (plan.Profile != "regular" && plan.Profile != "euru") || terms.Profile != plan.Profile || terms.Hidden != plan.Hidden || terms.Devices < 1 || terms.Devices > 10000 || terms.TrafficGb < 0 || terms.TrafficGb > 100000 {
		return empty, failure(409, "RENEWAL_NOT_ELIGIBLE")
	}
	offered := false
	for _, price := range terms.Prices {
		if price.Currency == "RUB" || price.Currency == "USD" {
			amount, e := strconv.ParseInt(price.AmountMinor, 10, 64)
			if e == nil && amount > 0 {
				offered = true
			}
		}
	}
	if !offered {
		return empty, failure(409, "RENEWAL_NOT_ELIGIBLE")
	}
	return catalogue.PlanSnapshot{PlanId: id, Revision: plan.Revision, Devices: terms.Devices, Hidden: terms.Hidden, Periods: terms.Periods, Prices: terms.Prices, Profile: terms.Profile, TrafficGb: terms.TrafficGb}, nil
}

func (s *Service) PlanChangeContext(ctx context.Context, account uuid.UUID) (PlanChangeContext, error) {
	var empty PlanChangeContext
	a, err := s.accountByID(ctx, account)
	if err != nil {
		return empty, unavailable()
	}
	if !independentBilling(a) {
		return empty, failure(409, "EXTERNAL_BILLING_UNVERIFIED")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	current, err := s.subscriptions.CurrentPlanSourceTx(ctx, tx, account)
	if err != nil {
		mapped := renewalError(err)
		var e *Error
		if errors.As(mapped, &e) && e.Code == "RENEWAL_NOT_ELIGIBLE" {
			return empty, failure(e.Status, "PLAN_CHANGE_NOT_ELIGIBLE")
		}
		return empty, mapped
	}
	return PlanChangeContext{CurrentPlanId: *current.PlanID, SourceAccessOperationId: current.OperationID}, nil
}
