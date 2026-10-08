package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/payments/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type starsSubscriptionRow struct {
	key              string
	root             uuid.UUID
	bot, payer       int64
	charge           string
	canonical        bool
	provider         string
	paidUntil        *time.Time
	current          *uuid.UUID
	desired, control string
	botCanceled      bool
	latest           *uuid.UUID
	proof            []byte
}

const starsSubscriptionColumns = "s.first_receipt_id,s.root_order_id,s.bot_id,s.payer_id,s.first_charge_id,s.canonical,s.provider_state,s.paid_until,s.current_cycle_order_id,s.desired_action,s.control_state,s.bot_canceled,s.latest_control_id,s.native_proof"

func scanStarsSubscription(row pgx.Row) (starsSubscriptionRow, error) {
	var s starsSubscriptionRow
	err := row.Scan(&s.key, &s.root, &s.bot, &s.payer, &s.charge, &s.canonical, &s.provider, &s.paidUntil, &s.current, &s.desired, &s.control, &s.botCanceled, &s.latest, &s.proof)
	return s, err
}
func (s *Service) starsSubscriptionsTx(ctx context.Context, tx pgx.Tx, account uuid.UUID) ([]starsSubscriptionRow, error) {
	var q store.DBTX = s.pool
	if tx != nil {
		q = tx
	}
	rows, err := q.Query(ctx, "SELECT "+starsSubscriptionColumns+" FROM stars_subscriptions s JOIN purchase_orders p ON p.id=s.root_order_id WHERE p.account_id=$1 ORDER BY s.canonical DESC,p.created_at DESC,s.first_receipt_id", account)
	if err != nil {
		return nil, unavailable()
	}
	defer rows.Close()
	var result []starsSubscriptionRow
	for rows.Next() {
		item, err := scanStarsSubscription(rows)
		if err != nil {
			return nil, unavailable()
		}
		result = append(result, item)
	}
	if rows.Err() != nil {
		return nil, unavailable()
	}
	return result, nil
}

type starsControlProof struct {
	Provider  string    `json:"provider"`
	Result    bool      `json:"result"`
	BotID     int64     `json:"bot_id"`
	PayerID   int64     `json:"payer_id"`
	ChargeID  string    `json:"charge_id"`
	Canceled  bool      `json:"is_canceled"`
	ControlID uuid.UUID `json:"control_id"`
}

func starsCancelProved(row starsSubscriptionRow) bool {
	var proof starsControlProof
	return row.botCanceled && row.desired == "cancel" && row.control == "confirmed" && row.latest != nil && json.Unmarshal(row.proof, &proof) == nil && proof.Provider == "telegram_stars" && proof.Result && proof.Canceled && proof.BotID == row.bot && proof.PayerID == row.payer && proof.ChargeID == row.charge && proof.ControlID == *row.latest
}
func (s *Service) starsBillingBlockedTx(ctx context.Context, tx pgx.Tx, a accounts.Snapshot) (bool, error) {
	if a.LegacyUserID != nil {
		return true, nil
	}
	rows, err := s.starsSubscriptionsTx(ctx, tx, a.ID)
	if err != nil {
		return true, err
	}
	for _, item := range rows {
		if !starsCancelProved(item) {
			return true, nil
		}
	}
	var q store.DBTX = s.pool
	if tx != nil {
		q = tx
	}
	var unknown bool
	err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stars_checkouts c JOIN purchase_orders p ON p.id=c.order_id WHERE p.account_id=$1 AND c.subscription_period=2592000 AND
 ((c.pre_checkout_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM purchase_receipts r WHERE r.order_id=p.id))
 OR EXISTS(SELECT 1 FROM purchase_receipts r WHERE r.order_id=p.id AND NOT EXISTS(SELECT 1 FROM stars_subscriptions sub WHERE sub.first_receipt_id=r.operation_id))))`, a.ID).Scan(&unknown)
	if err != nil {
		return true, unavailable()
	}
	return unknown, nil
}
func (s *Service) ExternalBillingEligibleTx(ctx context.Context, tx pgx.Tx, a accounts.Snapshot) (bool, error) {
	if a.Kind != "web" || a.VerifiedAt == nil || a.LegacyUserID != nil {
		return false, nil
	}
	blocked, err := s.starsBillingBlockedTx(ctx, tx, a)
	return !blocked, err
}
func (s *Service) CanUnlinkTelegramTx(ctx context.Context, tx pgx.Tx, account uuid.UUID) (bool, error) {
	var a accounts.Snapshot
	var err error
	if tx == nil {
		a, err = s.accountByID(ctx, account)
	} else {
		a, err = s.accountByIDTx(ctx, tx, account)
	}
	if err != nil {
		return false, unavailable()
	}
	if a.Kind != "web" || a.VerifiedAt == nil || !a.PasswordSet || a.LegacyUserID != nil {
		return false, nil
	}
	blocked, err := s.starsBillingBlockedTx(ctx, tx, a)
	return !blocked, err
}
func (s *Service) starsResumeEligibleTx(ctx context.Context, tx pgx.Tx, a accounts.Snapshot, sub starsSubscriptionRow, pending bool) (bool, error) {
	if !sub.canonical || sub.current == nil || a.LegacyUserID != nil || a.Restricted || a.VpnBanned || !purchaseSourceEligible(a, "telegram_stars") || a.TelegramID == nil || *a.TelegramID != sub.payer || a.AssignedPanelID == nil || *a.AssignedPanelID != s.config().PanelID || (stringValue(a.AccessProfile) != "regular" && stringValue(a.AccessProfile) != "euru") || !s.config().StarsEnabled || s.stars == nil || s.stars.BotID != sub.bot || s.stars.EditSubscription == nil || s.stars.Ready != nil && !s.stars.Ready() {
		return false, nil
	}
	if !pending {
		blocked, err := s.starsBillingBlockedTx(ctx, tx, a)
		if err != nil {
			return false, err
		}
		if blocked || !starsCancelProved(sub) {
			return false, nil
		}
	} else {
		var proof starsControlProof
		if sub.desired != "resume" || !sub.botCanceled || json.Unmarshal(sub.proof, &proof) != nil || proof.Provider != "telegram_stars" || !proof.Result || !proof.Canceled || proof.BotID != sub.bot || proof.PayerID != sub.payer || proof.ChargeID != sub.charge {
			return false, nil
		}
		var baseline bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stars_subscription_controls WHERE id=$1 AND subscription_receipt_id=$2 AND state='confirmed' AND action='cancel' AND proof=$3::jsonb)`, proof.ControlID, sub.key, sub.proof).Scan(&baseline); err != nil {
			return false, unavailable()
		}
		if !baseline {
			return false, nil
		}
	}
	other, err := s.starsSubscriptionsTx(ctx, tx, a.ID)
	if err != nil {
		return false, err
	}
	for _, item := range other {
		if !item.canonical || item.key != sub.key && !starsCancelProved(item) {
			return false, nil
		}
	}
	var q store.DBTX = s.pool
	if tx != nil {
		q = tx
	}
	var blocked bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE purchase_orders.account_id=$1 AND
 (purchase_orders.review_required AND NOT (`+purchaseRefundClosed+`) OR purchase_orders.active AND purchase_orders.payment_status='pending' OR purchase_orders.payment_status='paid' AND purchase_orders.fulfillment_status<>'applied' AND NOT (`+purchaseRefundClosed+`)))`, a.ID).Scan(&blocked); err != nil {
		return false, unavailable()
	}
	if blocked {
		return false, nil
	}
	if err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stars_refunds f JOIN purchase_orders p ON p.id=f.order_id WHERE p.account_id=$1 AND (f.state<>'confirmed' OR f.order_id=$2))`, a.ID, sub.root).Scan(&blocked); err != nil {
		return false, unavailable()
	}
	if blocked {
		return false, nil
	}
	blocked, err = s.vpn.UnresolvedTx(ctx, tx, a.ID)
	if err != nil {
		return false, unavailable()
	}
	if blocked {
		return false, nil
	}
	source, err := s.vpn.CurrentPlanSourceTx(ctx, tx, a.ID)
	if err != nil {
		return false, unavailable()
	}
	if source == nil {
		return false, nil
	}
	var operation *uuid.UUID
	var status string
	if err = q.QueryRow(ctx, `SELECT access_operation_id,fulfillment_status FROM purchase_orders WHERE id=$1 AND account_id=$2 AND NOT (`+purchaseRefundClosed+`)`, *sub.current, a.ID).Scan(&operation, &status); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, unavailable()
	}
	return status == "applied" && operation != nil && *operation == source.OperationID, nil
}

func (s *Service) StarsSubscription(ctx context.Context, account uuid.UUID) (StarsSubscription, error) {
	out := StarsSubscription{State: "none", PeriodPhase: "none"}
	if account == uuid.Nil {
		return out, failure(400, "INVALID_INPUT")
	}
	a, err := s.accountByID(ctx, account)
	if err != nil {
		return out, unavailable()
	}
	rows, err := s.starsSubscriptionsTx(ctx, nil, account)
	if err != nil {
		return out, err
	}
	out.ExternalBillingBlocked, err = s.starsBillingBlockedTx(ctx, nil, a)
	if err != nil {
		return out, err
	}
	if a.LegacyUserID != nil {
		out.State = "legacy_unknown"
	}
	if len(rows) == 0 {
		if out.ExternalBillingBlocked && out.State == "none" {
			out.State = "unknown"
			out.NeedsReview = true
		}
		return out, nil
	}
	chosen := rows[0]
	out.OrderId = &chosen.root
	out.ProviderState = &chosen.provider
	out.ControlState = &chosen.control
	out.PaidUntil = chosen.paidUntil
	if chosen.paidUntil != nil {
		out.PeriodPhase = "current"
		if s.now().After(*chosen.paidUntil) {
			out.PeriodPhase = "grace"
			if s.now().After(chosen.paidUntil.Add(24 * time.Hour)) {
				out.PeriodPhase = "lapsed"
			}
		}
	}
	out.CanResume, err = s.starsResumeEligibleTx(ctx, nil, a, chosen, false)
	if err != nil {
		return out, err
	}
	for _, item := range rows {
		out.CanCancel = out.CanCancel || !starsCancelProved(item)
	}
	if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE purchase_orders.account_id=$1 AND purchase_orders.review_required AND NOT (`+purchaseRefundClosed+`))`, account).Scan(&out.NeedsReview); err != nil {
		return out, unavailable()
	}
	for _, item := range rows {
		out.NeedsReview = out.NeedsReview || !item.canonical || item.control == "uncertain" || item.control == "rejected"
	}
	if a.LegacyUserID != nil {
		return out, nil
	}
	switch {
	case !out.ExternalBillingBlocked && starsCancelProved(chosen):
		out.State = "canceled"
	case starsCancelProved(chosen):
		out.State = "unknown"
	case chosen.desired == "cancel" && !starsCancelProved(chosen):
		out.State = "cancel_" + chosen.control
	case chosen.desired == "resume" && chosen.control == "confirmed" && chosen.provider != "active":
		out.State = "resume_allowed"
	case chosen.desired == "resume" && chosen.control != "confirmed":
		out.State = "resume_" + chosen.control
	case chosen.provider == "canceled":
		out.State = "user_canceled"
	default:
		out.State = chosen.provider
	}
	return out, nil
}

type StarsSubscriptionUpdateInput struct {
	BotID, PayerID, UpdateID int64
	Payload, State           string
	At                       time.Time
}

func (s *Service) RecordStarsSubscriptionUpdate(ctx context.Context, in StarsSubscriptionUpdateInput) error {
	root, valid := starsOrderID(in.Payload)
	if !valid || in.BotID <= 0 || in.PayerID <= 0 || in.PayerID > 1<<52-1 || in.UpdateID < 0 || in.UpdateID == math.MaxInt64 || (in.State != "active" && in.State != "canceled" && in.State != "failed") {
		return failure(409, "STARS_UNSUPPORTED_PAYMENT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	var account uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT account_id FROM purchase_orders WHERE id=$1`, root).Scan(&account); errors.Is(err, pgx.ErrNoRows) {
		return failure(409, "STARS_UNSUPPORTED_PAYMENT")
	} else if err != nil {
		return unavailable()
	}
	if _, err = s.lockAccount(ctx, tx, account); err != nil {
		return unavailable()
	}
	rows, err := s.starsSubscriptionsTx(ctx, tx, account)
	if err != nil {
		return err
	}
	var matches []starsSubscriptionRow
	for _, item := range rows {
		if item.root == root && item.bot == in.BotID && item.payer == in.PayerID {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return failure(409, "STARS_UNSUPPORTED_PAYMENT")
	}
	principal := fmt.Sprintf("telegram-stars:%d", in.BotID)
	key := uuid.NewSHA1(uuid.Nil, []byte(strconv.FormatInt(in.UpdateID, 10)))
	hash := bodyHash(struct {
		BotID, PayerID int64
		Payload, State string
	}{in.BotID, in.PayerID, in.Payload, in.State})
	q := store.New(tx)
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "starsSubscriptionUpdate", Key: key}); err != nil {
		return unavailable()
	}
	if _, found, e := replay[struct{}](ctx, q, principal, "starsSubscriptionUpdate", key, hash); found || e != nil {
		return e
	}
	state := in.State
	if len(matches) > 1 {
		state = "unknown"
	}
	for _, item := range matches {
		if state == "active" && (item.desired == "cancel" || item.botCanceled) {
			continue
		}
		if _, err = tx.Exec(ctx, `UPDATE stars_subscriptions SET provider_state=$2 WHERE first_receipt_id=$1`, item.key, state); err != nil {
			return unavailable()
		}
	}
	if err = s.saveIdempotency(ctx, q, principal, "starsSubscriptionUpdate", key, hash, struct{}{}); err != nil {
		return err
	}
	if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: account, CreatedAt: s.now(), Action: "stars_subscription_updated"}); err != nil {
		return unavailable()
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}

// ponytail: O(n) retained subscriptions per pass; paginate when a pass approaches 15 minutes.
func (s *Service) ReconcileStarsSubscriptions(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT p.account_id FROM stars_subscriptions s JOIN purchase_orders p ON p.id=s.root_order_id ORDER BY p.account_id`)
	if err != nil {
		return unavailable()
	}
	var accountsIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return unavailable()
		}
		accountsIDs = append(accountsIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return unavailable()
	}
	failed := false
	for _, id := range accountsIDs {
		if ctx.Err() != nil {
			return nil
		}
		tx, e := s.pool.Begin(ctx)
		if e != nil {
			failed = true
			continue
		}
		a, e := s.lockAccount(ctx, tx, id)
		reason := ""
		if e == nil {
			subs, lookupErr := s.starsSubscriptionsTx(ctx, tx, id)
			e = lookupErr
			switch {
			case !s.config().StarsEnabled:
				reason = "Stars disabled"
			case a.Restricted || a.VpnBanned || a.TelegramLoginDisabled || stringValue(a.AccessProfile) == "unlimited":
				reason = "Account access restricted"
			}
			if reason == "" {
				for _, sub := range subs {
					if sub.paidUntil != nil && s.now().After(sub.paidUntil.Add(24*time.Hour)) {
						reason = "Stars paid period lapsed"
						break
					}
					if sub.canonical && sub.current != nil {
						var operation *uuid.UUID
						var status string
						e = tx.QueryRow(ctx, `SELECT access_operation_id,fulfillment_status FROM purchase_orders WHERE id=$1`, *sub.current).Scan(&operation, &status)
						if e != nil {
							break
						}
						if status == "applied" {
							current, sourceErr := s.vpn.CurrentPlanSourceTx(ctx, tx, id)
							e = sourceErr
							if e != nil {
								break
							}
							if operation == nil || current == nil || current.OperationID != *operation {
								reason = "Paid plan source changed"
								break
							}
						}
					}
				}
			}
			if e == nil && reason != "" {
				e = s.RequireStarsCancellationTx(ctx, tx, id, reason)
			}
		}
		if e == nil {
			e = tx.Commit(ctx)
		} else {
			tx.Rollback(ctx)
		}
		if e != nil {
			failed = true
			continue
		}
		if e = s.applyStarsControls(ctx, id); e != nil {
			failed = true
		}
	}
	if failed {
		return unavailable()
	}
	return nil
}
func (s *Service) RunStarsSubscriptionScheduler(ctx context.Context) error {
	for ctx.Err() == nil {
		if err := s.ReconcileStarsSubscriptions(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("Stars subscription reconciliation unavailable")
		}
		timer := time.NewTimer(15 * time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}
