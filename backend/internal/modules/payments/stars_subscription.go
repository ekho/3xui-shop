package payments

import (
	"context"
	"encoding/json"
	"time"

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
	rows, err := tx.Query(ctx, `SELECT s.first_receipt_id FROM stars_subscriptions s JOIN purchase_orders p ON p.id=s.root_order_id WHERE p.account_id=$1 AND s.desired_action<>'cancel' ORDER BY s.first_receipt_id FOR UPDATE OF s`, account)
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
		id := uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO stars_subscription_controls(id,subscription_receipt_id,idempotency_key,action,reason,source,created_at) VALUES($1,$2,$3,'cancel',$4,'policy',$5)`, id, key, uuid.New(), reason, s.now()); err != nil {
			return unavailable()
		}
		if _, err = tx.Exec(ctx, `UPDATE stars_subscriptions SET desired_action='cancel',control_state='pending',latest_control_id=$2 WHERE first_receipt_id=$1`, key, id); err != nil {
			return unavailable()
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
