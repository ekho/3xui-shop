package payments

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/payments/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

func (s *Service) methodEnabled(method string) bool {
	c := s.config()
	switch method {
	case "yoomoney":
		return c.YooMoneyEnabled
	case "manual":
		return c.ManualEnabled && validText(c.ManualCardDetails, 1, 2000)
	case "yookassa":
		return c.YooKassaEnabled
	default:
		return false
	}
}

func (s *Service) ReportManualPayment(ctx context.Context, account, id, key uuid.UUID) (PurchaseOrder, error) {
	var empty PurchaseOrder
	if account == uuid.Nil || id == uuid.Nil || key == uuid.Nil {
		return empty, failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	a, err := s.lockAccount(ctx, tx, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return empty, unavailable()
	}
	if a.Kind != "web" || a.VerifiedAt == nil {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	if a.Restricted {
		return empty, failure(403, "ACCOUNT_RESTRICTED")
	}
	q := store.New(tx)
	principal := "account:" + account.String()
	operation := "reportManualPayment"
	hash := bodyHash(id)
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: operation, Key: key}); err != nil {
		return empty, unavailable()
	}
	if prior, found, e := replay[PurchaseOrder](ctx, q, principal, operation, key, hash); found || e != nil {
		return prior, e
	}
	p, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2 FOR UPDATE", id, account))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	if p.method != "manual" || p.paymentStatus != "pending" || !p.active || p.review || p.manualDecision != nil {
		return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
	}
	if p.manualReported == nil {
		now := s.now().UTC().Truncate(time.Microsecond)
		if !now.Before(p.expires) || now.Before(p.created) {
			return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
		}
		if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET manual_reported_at=$2 WHERE id=$1", id, now); err != nil {
			return empty, unavailable()
		}
		if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: account, CreatedAt: now, Action: "manual_payment_reported"}); err != nil {
			return empty, unavailable()
		}
		p.manualReported = &now
	}
	out, err := s.publicPurchase(ctx, p)
	if err != nil {
		return empty, err
	}
	if err = s.saveIdempotency(ctx, q, principal, operation, key, hash, out); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, unavailable()
	}
	return out, nil
}

func (s *Service) DecideManualPayment(ctx context.Context, actor, target, id, key uuid.UUID, in ManualPaymentDecisionInput) (PurchaseOrder, error) {
	var empty PurchaseOrder
	if actor == uuid.Nil || target == uuid.Nil || id == uuid.Nil || key == uuid.Nil || !validText(in.Reason, 1, 1000) || (in.Decision != "approve" && in.Decision != "reject") {
		return empty, failure(400, "INVALID_INPUT")
	}
	var amount int64
	if in.Decision == "approve" {
		if in.ConfirmedAmountMinor == nil || len(*in.ConfirmedAmountMinor) == 0 || len(*in.ConfirmedAmountMinor) > 19 || (*in.ConfirmedAmountMinor)[0] == '0' {
			return empty, failure(400, "INVALID_INPUT")
		}
		for _, c := range *in.ConfirmedAmountMinor {
			if c < '0' || c > '9' {
				return empty, failure(400, "INVALID_INPUT")
			}
		}
		var err error
		amount, err = strconv.ParseInt(*in.ConfirmedAmountMinor, 10, 64)
		if err != nil || amount <= 0 {
			return empty, failure(400, "INVALID_INPUT")
		}
	} else if in.ConfirmedAmountMinor != nil {
		return empty, failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.authority.LockOperatorPair(ctx, tx, actor, target); err != nil {
		return empty, err
	}
	q := store.New(tx)
	principal := "operator-account:" + actor.String()
	operation := "decideManualPayment"
	hash := bodyHash(struct {
		Target, ID uuid.UUID
		Input      ManualPaymentDecisionInput
	}{target, id, in})
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: operation, Key: key}); err != nil {
		return empty, unavailable()
	}
	if prior, found, e := replay[PurchaseOrder](ctx, q, principal, operation, key, hash); found || e != nil {
		return prior, e
	}
	p, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2 FOR UPDATE", id, target))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	if p.method != "manual" || p.manualReported == nil || p.manualDecision != nil || p.paymentStatus != "pending" || !p.active || p.review || p.fulfillmentStatus != "not_started" {
		return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
	}
	if in.Decision == "approve" && amount != p.amount {
		return empty, failure(400, "INVALID_INPUT")
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	reason := strings.TrimSpace(in.Reason)
	if now.Before(*p.manualReported) {
		return empty, unavailable()
	}
	state := "rejected"
	status := "canceled"
	if in.Decision == "approve" {
		if s.queue == nil || s.queue() == nil {
			return empty, unavailable()
		}
		var otherPaid bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE account_id=$1 AND id<>$2 AND payment_status='paid')", target, id).Scan(&otherPaid); err != nil {
			return empty, unavailable()
		}
		if otherPaid {
			return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
		}
		funding := "manual:" + id.String()
		if _, err = tx.Exec(ctx, `INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,created_at)
	 VALUES($1,$2,$3,$4,$4,'643','manual_confirmation',false,false,$3)`, funding, id, now, amount); err != nil {
			return empty, unavailable()
		}
		if _, err = tx.Exec(ctx, `UPDATE purchase_orders SET manual_decision='approved',manual_actor_id=$2,manual_decided_at=$3,manual_reason=$4,
	 payment_status='paid',paid_at=$3,active=false,fulfillment_status='queued',funding_operation_id=$5 WHERE id=$1`, id, actor, now, reason, funding); err != nil {
			return empty, unavailable()
		}
		if _, err = s.queue().InsertTx(ctx, tx, PurchaseArgs{OrderID: id}, &river.InsertOpts{Queue: "provision", MaxAttempts: 1000000}); err != nil {
			return empty, unavailable()
		}
		state, status = "approved", "paid"
		p.fundingID = &funding
		p.fulfillmentStatus = "queued"
	} else {
		if _, err = tx.Exec(ctx, `UPDATE purchase_orders SET manual_decision='rejected',manual_actor_id=$2,manual_decided_at=$3,manual_reason=$4,
	 payment_status='canceled',active=false WHERE id=$1`, id, actor, now, reason); err != nil {
			return empty, unavailable()
		}
	}
	if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: target, CreatedAt: now, Action: "manual_payment_" + state, OperatorAccountID: &actor, Reason: &reason}); err != nil {
		return empty, unavailable()
	}
	p.manualDecision, p.manualDecided, p.manualActor, p.manualReason = &state, &now, &actor, &reason
	p.paymentStatus, p.active = status, false
	out, err := s.publicPurchase(ctx, p)
	if err != nil {
		return empty, err
	}
	if err = s.saveIdempotency(ctx, q, principal, operation, key, hash, out); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, unavailable()
	}
	return out, nil
}

func (s *Service) ManualPaymentRequests(ctx context.Context, actor uuid.UUID, after *uuid.UUID) (ManualPaymentPage, error) {
	out := ManualPaymentPage{Items: []ManualPaymentItem{}}
	if err := s.authority.RequireOperator(ctx, actor); err != nil {
		return out, err
	}
	var at *time.Time
	afterID := uuid.Nil
	if after != nil {
		if *after == uuid.Nil {
			return out, failure(400, "INVALID_INPUT")
		}
		var when time.Time
		err := s.pool.QueryRow(ctx, "SELECT manual_reported_at FROM purchase_orders WHERE id=$1 AND payment_method='manual' AND manual_reported_at IS NOT NULL", *after).Scan(&when)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, failure(400, "INVALID_INPUT")
		}
		if err != nil {
			return out, unavailable()
		}
		at, afterID = &when, *after
	}
	rows, err := s.pool.Query(ctx, "SELECT "+purchaseColumns+` FROM purchase_orders WHERE payment_method='manual' AND manual_reported_at IS NOT NULL AND manual_decision IS NULL
	 AND payment_status='pending' AND active AND NOT review_required AND fulfillment_status='not_started'
	 AND ($1::timestamptz IS NULL OR (manual_reported_at,id)>($1,$2)) ORDER BY manual_reported_at,id LIMIT 51`, at, afterID)
	if err != nil {
		return out, unavailable()
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanPurchase(rows)
		if err != nil {
			return out, unavailable()
		}
		order, err := s.publicPurchase(ctx, p)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, ManualPaymentItem{AccountId: p.account, Order: order})
	}
	if rows.Err() != nil {
		return out, unavailable()
	}
	if len(out.Items) > 50 {
		out.HasMore = true
		out.Items = out.Items[:50]
		id := out.Items[49].Order.OrderId
		out.NextCursor = &id
	}
	return out, nil
}
