package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type StarsRefundInput struct {
	ReceiptOperationId string `json:"receipt_operation_id"`
	Reason             string `json:"reason"`
	ConfirmFull        bool   `json:"confirm_full"`
	KeepAccess         bool   `json:"keep_access"`
}
type StarsRefund struct {
	State  string         `json:"state"`
	Refund *PaymentRefund `json:"refund"`
}
type starsRefundRow struct {
	order                            uuid.UUID
	bot, payer, amount               int64
	charge, payload, currency, state string
	actor, key                       *uuid.UUID
	reason                           *string
	hash, proof                      []byte
	created                          time.Time
}

const starsRefundColumns = `order_id,bot_id,payer_id,amount,charge_id,payload,currency,state,operator_account_id,idempotency_key,reason,body_hash,proof,created_at`

func scanStarsRefund(row pgx.Row) (starsRefundRow, error) {
	var r starsRefundRow
	err := row.Scan(&r.order, &r.bot, &r.payer, &r.amount, &r.charge, &r.payload, &r.currency, &r.state, &r.actor, &r.key, &r.reason, &r.hash, &r.proof, &r.created)
	return r, err
}

// A negative observation can precede its receipt. It blocks funding immediately;
// the common immutable refund ledger is completed only when both proofs match.
func (s *Service) completeStarsRefundTx(ctx context.Context, tx pgx.Tx, p purchaseRow, key string, owner *vpn.AccessOwner) error {
	f, err := scanStarsRefund(tx.QueryRow(ctx, "SELECT "+starsRefundColumns+" FROM stars_refunds WHERE receipt_operation_id=$1", key))
	if err != nil {
		return unavailable()
	}
	if f.state != "confirmed" {
		return nil
	}
	var proof []byte
	var gross int64
	var currency string
	err = tx.QueryRow(ctx, `SELECT provider_data,gross_minor,currency FROM purchase_receipts WHERE operation_id=$1 AND order_id=$2`, key, p.id).Scan(&proof, &gross, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	var paid starsProof
	if json.Unmarshal(proof, &paid) != nil || f.order != p.id || paid.Provider != "telegram_stars" || paid.BotID != f.bot || paid.PayerID != f.payer || paid.ChargeID != f.charge || paid.Payload != f.payload || f.amount != gross || currency != "XTR" || f.currency != "XTR" {
		_, err = tx.Exec(ctx, `UPDATE purchase_orders SET review_required=true,review_reason='refund_proof_mismatch',active=false,fulfillment_status=CASE WHEN access_operation_id IS NULL THEN 'needs_review' ELSE fulfillment_status END WHERE id=$1`, p.id)
		if err != nil {
			return unavailable()
		}
		return nil
	}
	reason := "Telegram refund notification"
	if f.reason != nil {
		reason = *f.reason
	}
	id := uuid.New()
	result, err := tx.Exec(ctx, `INSERT INTO purchase_refunds(id,order_id,receipt_operation_id,payment_method,reference,returned_amount,returned_currency,reason,operator_account_id,created_at,source) VALUES($1,$2,$3,'telegram_stars',$3,$4,'XTR',$5,$6,$7,'telegram') ON CONFLICT(receipt_operation_id) DO NOTHING`, id, p.id, key, strconv.FormatInt(gross, 10), reason, f.actor, s.now())
	if err != nil {
		return unavailable()
	}
	if p.fundingID != nil && *p.fundingID == key && p.fulfillmentStatus != "applied" {
		if p.accessID != nil {
			if err = s.vpn.RetirePurchaseAccessTx(ctx, tx, owner, p.account, p.id, *p.accessID); err != nil {
				return unavailable()
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE purchase_orders SET active=false,fulfillment_status='needs_review',review_required=true,review_reason='funding_refunded' WHERE id=$1`, p.id); err != nil {
			return unavailable()
		}
	}
	if result.RowsAffected() == 1 {
		if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: id, AccountID: p.account, CreatedAt: s.now(), Action: "stars_refund_confirmed", OperatorAccountID: f.actor, AccessOperationID: p.accessID, Reason: &reason}); err != nil {
			return unavailable()
		}
	}
	return nil
}
func (s *Service) RecordStarsRefund(ctx context.Context, in StarsPaymentInput) error {
	if !validStarsPayment(in) || in.Currency != "XTR" {
		return failure(409, "STARS_UNSUPPORTED_PAYMENT")
	}
	key := starsReceiptKey(in.BotID, in.ChargeID)
	proof := starsPaymentProof(in)
	id, _ := starsOrderID(in.Payload)
	var account uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT account_id FROM purchase_orders WHERE id=$1`, id).Scan(&account)
	if errors.Is(err, pgx.ErrNoRows) {
		return failure(409, "STARS_UNSUPPORTED_PAYMENT")
	}
	if err != nil {
		return unavailable()
	}
	owner, err := s.vpn.OpenAccessOwner(ctx, account)
	if err != nil {
		return unavailable()
	}
	defer owner.Release()
	if err = owner.TryLock(ctx); err != nil {
		return unavailable() // The durable event is retried after the active writer.
	}
	tx, err := owner.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	if err = starsLockCharge(ctx, tx, key); err != nil {
		return unavailable()
	}
	p, _, _, err := s.starsOrderTx(ctx, tx, in)
	if err != nil {
		return err
	}
	old, err := scanStarsRefund(tx.QueryRow(ctx, "SELECT "+starsRefundColumns+" FROM stars_refunds WHERE receipt_operation_id=$1 FOR UPDATE", key))
	observed := false
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		_, err = tx.Exec(ctx, `INSERT INTO stars_refunds(receipt_operation_id,order_id,bot_id,payer_id,charge_id,payload,amount,currency,state,proof,created_at,confirmed_at) VALUES($1,$2,$3,$4,$5,$6,$7,'XTR','confirmed',$8,$9,$10)`, key, p.id, in.BotID, in.PayerID, in.ChargeID, in.Payload, in.Amount, proof, s.now(), in.At)
		observed = true
	case err != nil:
		return unavailable()
	case old.order != p.id || old.bot != in.BotID || old.payer != in.PayerID || old.charge != in.ChargeID || old.payload != in.Payload || old.amount != in.Amount || old.currency != in.Currency:
		_, err = tx.Exec(ctx, `UPDATE purchase_orders SET review_required=true,review_reason='refund_proof_mismatch',active=false,fulfillment_status=CASE WHEN access_operation_id IS NULL THEN 'needs_review' ELSE fulfillment_status END WHERE id=$1 OR id=$2`, p.id, old.order)
	case old.state != "confirmed":
		_, err = tx.Exec(ctx, `UPDATE stars_refunds SET state='confirmed',proof=$2,confirmed_at=$3 WHERE receipt_operation_id=$1`, key, proof, in.At)
		observed = true
	}
	if err != nil {
		return unavailable()
	}
	if observed {
		if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: p.account, CreatedAt: s.now(), Action: "stars_refund_observed", OperatorAccountID: old.actor, Reason: &key}); err != nil {
			return unavailable()
		}
	}
	if err = s.completeStarsRefundTx(ctx, tx, p, key, owner); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
}

var starsKey = regexp.MustCompile(`^stars:[0-9a-f]{64}$`)

func (s *Service) lockStarsRefundAuthority(ctx context.Context, tx pgx.Tx, actor, target uuid.UUID) error {
	a, err := s.authority.LockOperatorPair(ctx, tx, actor, target)
	if err != nil {
		return err
	}
	protected, err := s.authority.OperatorRoleExists(ctx, tx, target)
	if err != nil {
		return err
	}
	if actor == target || protected || a.TelegramID != nil && s.authority.OperatorAllowed(*a.TelegramID) {
		return failure(403, "OPERATOR_ACCOUNT_PROTECTED")
	}
	return nil
}

func (s *Service) starsRefundResult(ctx context.Context, key string) (StarsRefund, error) {
	var out StarsRefund
	if err := s.pool.QueryRow(ctx, `SELECT state FROM stars_refunds WHERE receipt_operation_id=$1`, key).Scan(&out.State); err != nil {
		return out, unavailable()
	}
	f, err := scanRefund(s.pool.QueryRow(ctx, "SELECT "+refundColumns+" FROM purchase_refunds f JOIN purchase_receipts r ON r.operation_id=f.receipt_operation_id WHERE f.receipt_operation_id=$1", key))
	if err == nil {
		out.Refund = &f
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, unavailable()
	}
	return out, nil
}

// RefundStarsPurchase persists authority and intent before the payout. An
// ambiguous attempt is observed, never blindly repeated with a different key.
func (s *Service) RefundStarsPurchase(ctx context.Context, actor, target, order, key uuid.UUID, in StarsRefundInput) (StarsRefund, error) {
	var empty StarsRefund
	if actor == uuid.Nil || target == uuid.Nil || order == uuid.Nil || key == uuid.Nil || !starsKey.MatchString(in.ReceiptOperationId) || !validText(in.Reason, 1, 1000) || !in.ConfirmFull || !in.KeepAccess {
		return empty, failure(400, "INVALID_INPUT")
	}
	if err := s.authority.RequireOperator(ctx, actor); err != nil {
		return empty, err
	}
	owner, err := s.vpn.OpenAccessOwner(ctx, target)
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
	tx, err := owner.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	if err = starsLockCharge(ctx, tx, in.ReceiptOperationId); err != nil {
		return empty, unavailable()
	}
	if err = s.lockStarsRefundAuthority(ctx, tx, actor, target); err != nil {
		return empty, err
	}
	p, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2 FOR UPDATE", order, target))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	var raw []byte
	var gross int64
	var currency string
	err = tx.QueryRow(ctx, `SELECT provider_data,gross_minor,currency FROM purchase_receipts WHERE order_id=$1 AND operation_id=$2`, order, in.ReceiptOperationId).Scan(&raw, &gross, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	var proof starsProof
	if p.method != "telegram_stars" || json.Unmarshal(raw, &proof) != nil || proof.Provider != "telegram_stars" || proof.BotID <= 0 || proof.PayerID <= 0 || proof.Amount != strconv.FormatInt(gross, 10) || currency != "XTR" || proof.Currency != "XTR" || proof.Payload != "stars:v1:"+order.String() || starsReceiptKey(proof.BotID, proof.ChargeID) != in.ReceiptOperationId {
		return empty, failure(409, "PAYMENT_REFUND_CONFLICT")
	}
	hash := bodyHash(struct {
		Target, Order uuid.UUID
		Input         StarsRefundInput
	}{target, order, in})
	prior, err := scanStarsRefund(tx.QueryRow(ctx, "SELECT "+starsRefundColumns+" FROM stars_refunds WHERE receipt_operation_id=$1 FOR UPDATE", in.ReceiptOperationId))
	if err == nil {
		if prior.actor != nil && (*prior.actor != actor || !bytes.Equal(prior.hash, hash)) {
			return empty, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		if prior.state == "confirmed" {
			if err = s.completeStarsRefundTx(ctx, tx, p, in.ReceiptOperationId, owner); err != nil {
				return empty, err
			}
			err = tx.Commit(ctx)
		} else {
			err = tx.Rollback(ctx)
		}
		if err != nil {
			return empty, unavailable()
		}
		return s.starsRefundResult(ctx, in.ReceiptOperationId)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, unavailable()
	}
	if s.stars == nil || s.stars.BotID != proof.BotID || s.stars.Ready != nil && !s.stars.Ready() {
		return empty, unavailable()
	}
	reason := strings.TrimSpace(in.Reason)
	_, err = tx.Exec(ctx, `INSERT INTO stars_refunds(receipt_operation_id,order_id,bot_id,payer_id,charge_id,payload,amount,currency,state,operator_account_id,reason,idempotency_key,body_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,'XTR','pending',$8,$9,$10,$11,$12)`, in.ReceiptOperationId, order, proof.BotID, proof.PayerID, proof.ChargeID, proof.Payload, gross, actor, reason, key, hash, s.now())
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return empty, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		return empty, unavailable()
	}
	if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: target, CreatedAt: s.now(), Action: "stars_refund_requested", OperatorAccountID: &actor, Reason: &reason}); err != nil {
		return empty, unavailable()
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, unavailable()
	}
	// Recheck actor/target immediately before the provider side effect.
	check, checkErr := owner.Begin(ctx)
	if checkErr == nil {
		checkErr = s.lockStarsRefundAuthority(ctx, check, actor, target)
		closeErr := check.Rollback(ctx)
		if checkErr == nil {
			checkErr = closeErr
		}
	}
	payoutErr := checkErr
	if payoutErr == nil {
		payoutErr = s.stars.Refund(ctx, proof.PayerID, proof.ChargeID)
	}
	// Completion may outlive a disconnected browser; never lose a confirmed payout.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	tx, err = owner.Begin(finish)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(finish)
	if err = starsLockCharge(finish, tx, in.ReceiptOperationId); err != nil {
		return empty, unavailable()
	}
	if _, err = s.lockAccount(finish, tx, target); err != nil {
		return empty, unavailable()
	}
	p, err = scanPurchase(tx.QueryRow(finish, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 FOR UPDATE", order))
	if err != nil {
		return empty, unavailable()
	}
	if payoutErr == nil {
		nativeProof, _ := json.Marshal(map[string]any{"provider": "telegram_stars", "result": true, "bot_id": proof.BotID, "payer_id": proof.PayerID, "charge_id": proof.ChargeID})
		_, err = tx.Exec(finish, `UPDATE stars_refunds SET state='confirmed',proof=$2,confirmed_at=$3 WHERE receipt_operation_id=$1 AND state<>'confirmed'`, in.ReceiptOperationId, nativeProof, s.now())
	} else {
		_, err = tx.Exec(finish, `UPDATE stars_refunds SET state='uncertain' WHERE receipt_operation_id=$1 AND state='pending'`, in.ReceiptOperationId)
	}
	if err != nil {
		return empty, unavailable()
	}
	if err = s.completeStarsRefundTx(finish, tx, p, in.ReceiptOperationId, owner); err != nil {
		return empty, err
	}
	if payoutErr != nil && p.fulfillmentStatus != "applied" {
		if _, err = tx.Exec(finish, `UPDATE purchase_orders SET review_required=true,review_reason='stars_refund_uncertain',fulfillment_status='needs_review',active=false WHERE id=$1`, order); err != nil {
			return empty, unavailable()
		}
	}
	if err = tx.Commit(finish); err != nil {
		return empty, unavailable()
	}
	return s.starsRefundResult(finish, in.ReceiptOperationId)
}
