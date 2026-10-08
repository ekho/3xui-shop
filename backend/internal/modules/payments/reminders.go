package payments

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/notifications"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"
)

func starsReminderPeriod(row starsSubscriptionRow, now time.Time) notifications.ReminderStars {
	if !row.canonical || row.current == nil || *row.current == uuid.Nil || row.paidUntil == nil || row.provider == "unknown" || row.control == "uncertain" || row.control == "rejected" {
		return notifications.ReminderStars{}
	}
	lapsed := now.After(row.paidUntil.Add(24 * time.Hour))
	return notifications.ReminderStars{Known: true, SuppressExpiry: row.provider == "active" && !starsCancelProved(row) && !lapsed, Lapsed: lapsed, Period: row.current.String() + ":" + strconv.FormatInt(row.paidUntil.Unix(), 10), PaidUntil: row.paidUntil}
}

// ReminderPolicyTx reads proven billing only; it never reconciles/cancels subscriptions.
func (s *Service) ReminderPolicyTx(ctx context.Context, tx pgx.Tx, account uuid.UUID) (notifications.ReminderStars, error) {
	if tx == nil || account == uuid.Nil {
		return notifications.ReminderStars{}, failure(400, "INVALID_INPUT")
	}
	unknown := notifications.ReminderStars{}
	a, err := s.accountByIDTx(ctx, tx, account)
	if err != nil {
		return unknown, unavailable()
	}
	rows, err := s.starsSubscriptionsTx(ctx, tx, account)
	if err != nil {
		return unknown, err
	}
	if len(rows) == 0 {
		blocked, err := s.starsBillingBlockedTx(ctx, tx, a)
		return notifications.ReminderStars{Known: !blocked}, err
	}
	if len(rows) != 1 || a.LegacyUserID != nil || a.Restricted || a.VpnBanned || !purchaseSourceEligible(a, "telegram_stars") {
		return unknown, nil
	}
	row := rows[0]
	out := starsReminderPeriod(row, s.now())
	if !out.Known || a.TelegramID == nil || *a.TelegramID != row.payer || a.AssignedPanelID == nil || *a.AssignedPanelID != s.config().PanelID {
		return unknown, nil
	}
	unresolved, err := s.vpn.UnresolvedTx(ctx, tx, account)
	if err != nil {
		return unknown, unavailable()
	}
	if unresolved {
		return unknown, nil
	}
	source, err := s.vpn.CurrentPlanSourceTx(ctx, tx, account)
	if err != nil {
		return unknown, unavailable()
	}
	if source == nil {
		return unknown, nil
	}
	p, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2", *row.current, account))
	if errors.Is(err, pgx.ErrNoRows) {
		return unknown, nil
	}
	if err != nil {
		return unknown, unavailable()
	}
	var quote PurchaseQuote
	if p.method != "telegram_stars" || p.paymentStatus != "paid" || p.fulfillmentStatus != "applied" || p.review || p.fullyRefunded || p.accessID == nil || *p.accessID != source.OperationID || json.Unmarshal(p.quote, &quote) != nil || !quote.StarsRecurring || string(quote.Profile) != stringValue(a.AccessProfile) {
		return unknown, nil
	}
	var funded, cycle bool
	if err = tx.QueryRow(ctx, purchaseFundingCheck, p.id).Scan(&funded); err != nil {
		return unknown, unavailable()
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stars_subscription_cycles WHERE order_id=$1 AND root_order_id=$2 AND subscription_receipt_id=$3 AND paid_until=$4)`, p.id, row.root, row.key, row.paidUntil).Scan(&cycle); err != nil {
		return unknown, unavailable()
	}
	if !funded || !cycle {
		return unknown, nil
	}
	return out, nil
}
