package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"example.com/cabinet/backend/internal/modules/payments/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const starsSubscriptionPeriod int64 = 2592000

type StarsSubscription struct {
	State                  string     `json:"state"`
	OrderId                *uuid.UUID `json:"order_id"`
	ProviderState          *string    `json:"provider_state"`
	ControlState           *string    `json:"control_state"`
	PaidUntil              *time.Time `json:"paid_until"`
	PeriodPhase            string     `json:"period_phase"`
	CanCancel              bool       `json:"can_cancel"`
	CanResume              bool       `json:"can_resume"`
	ExternalBillingBlocked bool       `json:"external_billing_blocked"`
	NeedsReview            bool       `json:"needs_review"`
}

// RequireStarsCancellationTx only records the owner's intent. The current process
// performs native I/O after commit, using captured payer/first charge identities.
func (s *Service) RequireStarsCancellationTx(ctx context.Context, tx pgx.Tx, account uuid.UUID, reason string) error {
	if tx == nil || account == uuid.Nil || !validText(reason, 1, 1000) {
		return failure(400, "INVALID_INPUT")
	}
	rows, err := tx.Query(ctx, `SELECT s.first_receipt_id FROM stars_subscriptions s JOIN purchase_orders p ON p.id=s.root_order_id WHERE p.account_id=$1 AND (s.desired_action<>'cancel' OR EXISTS(SELECT 1 FROM stars_subscription_controls c WHERE c.id=s.latest_control_id AND c.source='client')) ORDER BY s.first_receipt_id FOR UPDATE OF s`, account)
	if err != nil {
		return unavailable()
	}
	var keys []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return unavailable()
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return unavailable()
	}
	for _, key := range keys {
		if err = s.setStarsControlTx(ctx, tx, key, uuid.New(), "cancel", reason, "policy", nil); err != nil {
			return err
		}
	}
	return nil
}

func validStarsRecurringPeriod(in StarsPaymentInput) bool {
	return in.Recurring && in.SubscriptionExpiresAt > in.At.Unix() && in.SubscriptionExpiresAt <= in.At.Add(30*24*time.Hour+5*time.Minute).Unix()
}

// Every genuine first charge is its own billing identity, including unexpected
// extra starts. Only the canonical, eligible first receipt funds one cycle.
func (s *Service) recordStarsFirstSubscriptionTx(ctx context.Context, tx pgx.Tx, p purchaseRow, in StarsPaymentInput, key, reason string) error {
	if !in.Recurring || !in.FirstRecurring {
		return nil
	}
	var canonicalExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stars_subscriptions WHERE root_order_id=$1 AND canonical)`, p.id).Scan(&canonicalExists); err != nil {
		return unavailable()
	}
	canonical := reason == "" && !canonicalExists
	var paidUntil *time.Time
	if validStarsRecurringPeriod(in) {
		value := time.Unix(in.SubscriptionExpiresAt, 0).UTC()
		paidUntil = &value
	}
	provider := "active"
	if paidUntil == nil {
		provider = "unknown"
	}
	var current *uuid.UUID
	if canonical {
		value := p.id
		current = &value
	}
	if _, err := tx.Exec(ctx, `INSERT INTO stars_subscriptions(first_receipt_id,root_order_id,bot_id,payer_id,first_charge_id,canonical,provider_state,paid_until,current_cycle_order_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, key, p.id, in.BotID, in.PayerID, in.ChargeID, canonical, provider, paidUntil, current, s.now()); err != nil {
		return unavailable()
	}
	if canonical {
		if _, err := tx.Exec(ctx, `INSERT INTO stars_subscription_cycles(order_id,root_order_id,subscription_receipt_id,receipt_id,paid_until) VALUES($1,$1,$2,$2,$3)`, p.id, key, paidUntil); err != nil {
			return unavailable()
		}
	} else {
		if err := s.RequireStarsCancellationTx(ctx, tx, p.account, "Unexpected first recurring charge"); err != nil {
			return err
		}
	}
	return nil
}

func recurringQuote(raw []byte) bool {
	var quote PurchaseQuote
	return json.Unmarshal(raw, &quote) == nil && quote.StarsRecurring
}

type starsCycle struct {
	subscription starsSubscriptionRow
	previous     uuid.UUID
}

// Prepare a native cycle under the same charge/account transaction. Invoice
// identity and quote stay at the root; only the received payment gets a child.
func (s *Service) prepareStarsCycleTx(ctx context.Context, tx pgx.Tx, root purchaseRow, bot, payer int64, in StarsPaymentInput, key string, refunded bool) (purchaseRow, *starsCycle, string, error) {
	refuse := func(reason string) (purchaseRow, *starsCycle, string, error) { return root, nil, reason, nil }
	if bot != in.BotID || payer != in.PayerID {
		return refuse("payment_identity_mismatch")
	}
	if in.Currency != "XTR" || in.Amount != root.amount || !validStarsRecurringPeriod(in) || in.At.Before(root.created.Add(-5*time.Minute)) {
		return refuse("invalid_recurring_payment")
	}
	var quote PurchaseQuote
	if json.Unmarshal(root.quote, &quote) != nil || !quote.StarsRecurring || quote.PeriodDays != 30 || quote.Currency != "XTR" || root.amount > 10000 {
		return refuse("invalid_quote")
	}
	subs, err := s.starsSubscriptionsTx(ctx, tx, root.account)
	if err != nil {
		return root, nil, "", err
	}
	if len(subs) != 1 || !subs[0].canonical || subs[0].root != root.id || subs[0].bot != bot || subs[0].payer != payer || subs[0].paidUntil == nil || subs[0].current == nil {
		return refuse("stars_cycle_ambiguous")
	}
	sub := subs[0]
	if in.SubscriptionExpiresAt <= sub.paidUntil.Unix() || in.At.Before(sub.paidUntil.Add(-5*time.Minute)) {
		return refuse("stars_cycle_period_invalid")
	}
	var duplicate bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stars_subscription_cycles WHERE root_order_id=$1 AND paid_until=$2)`, root.id, time.Unix(in.SubscriptionExpiresAt, 0).UTC()).Scan(&duplicate); err != nil {
		return root, nil, "", unavailable()
	}
	if duplicate {
		return refuse("stars_cycle_period_duplicate")
	}
	a, err := s.accountByIDTx(ctx, tx, root.account)
	if err != nil {
		return root, nil, "", unavailable()
	}
	if !s.config().StarsEnabled {
		return refuse("stars_disabled")
	}
	if s.stars == nil || s.stars.BotID != bot || a.LegacyUserID != nil || !purchaseSourceEligible(a, "telegram_stars") || a.TelegramID == nil || *a.TelegramID != payer || a.Restricted || a.VpnBanned || a.AssignedPanelID == nil || *a.AssignedPanelID != s.config().PanelID || (stringValue(a.AccessProfile) != "regular" && stringValue(a.AccessProfile) != "euru") {
		return refuse("account_not_eligible")
	}
	if !refunded && (sub.desired == "cancel" || sub.botCanceled || sub.desired == "resume" && sub.control != "confirmed") {
		return refuse("stars_cycle_canceled")
	}
	previous, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2", *sub.current, root.account))
	if errors.Is(err, pgx.ErrNoRows) {
		return refuse("stars_cycle_source_invalid")
	}
	if err != nil {
		return root, nil, "", unavailable()
	}
	if previous.accessID == nil || previous.fulfillmentStatus != "applied" || previous.fullyRefunded {
		return refuse("stars_cycle_source_unresolved")
	}
	source, err := s.vpn.CurrentPlanSourceTx(ctx, tx, root.account)
	if err != nil {
		return root, nil, "", unavailable()
	}
	if source == nil || source.OperationID != *previous.accessID {
		return refuse("stars_cycle_source_changed")
	}
	blocked, err := s.purchaseHistoryBlockedTx(ctx, tx, root.account, root.id, "renew", "telegram_stars")
	if err != nil {
		return root, nil, "", err
	}
	if blocked || root.review {
		return refuse("order_requires_review")
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE account_id=$1 AND active AND payment_status='pending') OR EXISTS(SELECT 1 FROM stars_refunds WHERE order_id=$2 AND receipt_operation_id<>$3)`, root.account, root.id, key).Scan(&blocked); err != nil {
		return root, nil, "", unavailable()
	}
	if blocked {
		return refuse("stars_cycle_money_unresolved")
	}
	blocked, err = s.vpn.UnresolvedTx(ctx, tx, root.account)
	if err != nil {
		return root, nil, "", unavailable()
	}
	if blocked {
		return refuse("stars_cycle_access_unresolved")
	}
	child := root
	child.id, child.key = uuid.New(), uuid.NewSHA1(uuid.Nil, []byte(key))
	child.hash = bodyHash(struct {
		Root    uuid.UUID
		Receipt string
	}{root.id, key})
	child.action, child.paymentStatus, child.fulfillmentStatus = "renew", "pending", "not_started"
	child.active, child.review, child.fullyRefunded = false, false, false
	child.accessID, child.fundingID = nil, nil
	child.created, child.expires = in.At, in.At.Add(30*time.Minute)
	if _, err = tx.Exec(ctx, `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,created_at,expires_at,payment_method,action,active) VALUES($1,$2,$3,$4,$5,$6,'STARS',$7,$8,'telegram_stars','renew',false)`, child.id, child.account, child.key, child.hash, child.quote, child.amount, child.created, child.expires); err != nil {
		return root, nil, "", unavailable()
	}
	return child, &starsCycle{subscription: sub, previous: *previous.accessID}, "", nil
}

// The declared prior applied source is checked again before every native write.
// A same-plan operator change, refund or newer mandatory cancel closes the chain.
func (s *Service) starsCyclePolicyTx(ctx context.Context, tx pgx.Tx, p purchaseRow, root, previous uuid.UUID) (string, error) {
	subs, err := s.starsSubscriptionsTx(ctx, tx, p.account)
	if err != nil {
		return "", err
	}
	if len(subs) != 1 || !subs[0].canonical || subs[0].root != root || subs[0].current == nil || *subs[0].current != p.id || subs[0].desired == "resume" && subs[0].control != "confirmed" {
		return "stars_cycle_not_eligible", nil
	}
	var q store.DBTX = s.pool
	if tx != nil {
		q = tx
	}
	if subs[0].desired == "cancel" || subs[0].botCanceled {
		if subs[0].latest == nil {
			return "stars_cycle_not_eligible", nil
		}
		var policy bool
		if err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stars_subscription_controls WHERE id=$1 AND subscription_receipt_id=$2 AND source='policy')`, *subs[0].latest, subs[0].key).Scan(&policy); err != nil {
			return "", unavailable()
		}
		if policy {
			return "stars_cycle_not_eligible", nil
		}
		// Client cancellation keeps this already accepted paid period. Future
		// charges remain closed in prepareStarsCycleTx.
	}
	var blocked bool
	if err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stars_refunds WHERE order_id=$1) OR EXISTS(SELECT 1 FROM purchase_orders WHERE account_id=$2 AND active AND payment_status='pending')`, root, p.account).Scan(&blocked); err != nil {
		return "", unavailable()
	}
	if blocked {
		return "stars_cycle_money_unresolved", nil
	}
	blocked, err = s.purchaseHistoryBlockedTx(ctx, tx, p.account, p.id, "renew", "telegram_stars")
	if err != nil {
		return "", err
	}
	if blocked {
		return "order_requires_review", nil
	}
	source, err := s.vpn.CurrentPlanSourceTx(ctx, tx, p.account)
	if err != nil {
		return "", unavailable()
	}
	if source == nil || source.OperationID != previous && (p.accessID == nil || source.OperationID != *p.accessID) {
		return "stars_cycle_source_changed", nil
	}
	return "", nil
}
