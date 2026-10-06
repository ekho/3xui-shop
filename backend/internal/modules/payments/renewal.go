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
)

// Until C35 owns authoritative recurrence states, only unlinked new web
// accounts prove they cannot also be billed by the legacy Stars handler.
func independentBilling(a accounts.Snapshot) bool {
	return a.Kind == "web" && a.TelegramID == nil && a.LegacyUserID == nil
}

func renewalError(err error) error {
	var e *subscriptions.Error
	if errors.As(err, &e) {
		return failure(e.Status, e.Code)
	}
	return err
}

func (s *Service) requireRenewalPlan(ctx context.Context, tx pgx.Tx, account, plan uuid.UUID) error {
	id, err := s.subscriptions.RenewalPlanIDTx(ctx, tx, account)
	if err != nil {
		return renewalError(err)
	}
	if id != plan {
		return failure(409, "RENEWAL_NOT_ELIGIBLE")
	}
	return nil
}

// Completed payments are reusable only for renewal or proved starter clearing.
// Review and unresolved funding stay blocked regardless of the current plan.
func (s *Service) purchaseHistoryBlockedTx(ctx context.Context, tx pgx.Tx, account, except uuid.UUID, action string) (bool, error) {
	ignoreApplied := action == "renew"
	if !ignoreApplied {
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
	err := q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE account_id=$1 AND id<>$2 AND (review_required OR fulfillment_status='needs_review' OR payment_status='paid' AND (NOT $3::boolean OR fulfillment_status<>'applied')))", account, except, ignoreApplied).Scan(&blocked)
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
	if !independentBilling(a) {
		return "external_billing_unverified", nil
	}
	if a.Restricted || a.VpnBanned || stringValue(a.AccessProfile) == "unlimited" {
		return "account_not_eligible", nil
	}
	if p.action == "purchase" {
		blocked, err := s.purchaseHistoryBlockedTx(ctx, tx, p.account, p.id, p.action)
		if err != nil {
			return "", err
		}
		if blocked {
			return "another_first_payment", nil
		}
	}
	if p.action == "renew" {
		var quote PurchaseQuote
		if json.Unmarshal(p.quote, &quote) != nil {
			return "invalid_quote", nil
		}
		err = s.requireRenewalPlan(ctx, tx, p.account, quote.PlanId)
		if err != nil {
			var e *Error
			if errors.As(err, &e) && e.Status < 500 {
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
		if price.Currency == "RUB" {
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
