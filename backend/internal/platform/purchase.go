package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
	"math"
	"strconv"
	"strings"
	"time"
)

type purchaseRow struct {
	id, account, key                              uuid.UUID
	hash, quote                                   []byte
	amount                                        int64
	paymentType, paymentStatus, fulfillmentStatus string
	active, review                                bool
	accessID                                      *uuid.UUID
	fundingID                                     *string
	created, expires                              time.Time
}

const purchaseColumns = "id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,payment_status,fulfillment_status,active,review_required,access_operation_id,funding_operation_id,created_at,expires_at"
const purchaseFundingCheck = "SELECT EXISTS(SELECT 1 FROM purchase_orders p JOIN purchase_receipts r ON r.operation_id=p.funding_operation_id AND r.order_id=p.id WHERE p.id=$1 AND p.payment_status='paid' AND r.review_reason IS NULL AND r.gross_minor=p.amount_minor AND r.net_minor>0 AND r.net_minor<=r.gross_minor AND r.currency='643' AND r.notification_type IN ('p2p-incoming','card-incoming') AND NOT r.codepro AND NOT r.unaccepted AND r.occurred_at>=p.created_at-interval '5 minutes' AND r.occurred_at<=p.expires_at)"

func scanPurchase(row pgx.Row) (purchaseRow, error) {
	var p purchaseRow
	err := row.Scan(&p.id, &p.account, &p.key, &p.hash, &p.quote, &p.amount, &p.paymentType, &p.paymentStatus, &p.fulfillmentStatus, &p.active, &p.review, &p.accessID, &p.fundingID, &p.created, &p.expires)
	return p, err
}

func (s *Service) publicPurchase(p purchaseRow) (wire.PurchaseOrder, error) {
	var quote wire.PurchaseQuote
	if json.Unmarshal(p.quote, &quote) != nil {
		return wire.PurchaseOrder{}, unavailable()
	}
	now := s.now().UTC()
	expired := !now.Before(p.expires)
	canPay := p.active && p.paymentStatus == "pending" && !expired && s.cfg.YooMoneyEnabled
	out := wire.PurchaseOrder{OrderId: p.id, Action: "purchase", PaymentMethod: "yoomoney", PaymentType: wire.PurchaseOrderPaymentType(p.paymentType), Quote: quote, PaymentStatus: wire.PurchaseOrderPaymentStatus(p.paymentStatus), FulfillmentStatus: wire.PurchaseOrderFulfillmentStatus(p.fulfillmentStatus), ReviewRequired: p.review, CreatedAt: p.created, ExpiresAt: p.expires, Expired: expired, CanPay: canPay, CanCancel: p.active && p.paymentStatus == "pending" && !expired, AccessOperationId: p.accessID}
	if canPay {
		out.Checkout = &wire.YooMoneyCheckout{Action: "https://yoomoney.ru/quickpay/confirm", Method: "POST", Fields: wire.YooMoneyCheckoutFields{Receiver: s.cfg.YooMoneyWalletID, QuickpayForm: "button", PaymentType: wire.YooMoneyCheckoutFieldsPaymentType(p.paymentType), Sum: fmt.Sprintf("%d.%02d", p.amount/100, p.amount%100), Label: p.id, SuccessURL: strings.TrimRight(s.cfg.CabinetOrigin, "/") + "/orders/" + p.id.String()}}
	}
	return out, nil
}

func (s *Service) PaymentMethods(ctx context.Context, account uuid.UUID) (wire.PaymentMethods, error) {
	out := wire.PaymentMethods{Methods: []wire.PaymentMethod{}}
	a, err := s.accountByID(ctx, account)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, failure(401, "INVALID_CREDENTIALS")
		}
		return out, unavailable()
	}
	if a.Kind != "web" || !a.VerifiedAt.Valid {
		return out, failure(401, "INVALID_CREDENTIALS")
	}
	if a.Restricted {
		return out, failure(403, "ACCOUNT_RESTRICTED")
	}
	if s.cfg.YooMoneyEnabled {
		out.Methods = append(out.Methods, wire.PaymentMethod{Id: "yoomoney", Currency: "RUB"})
	}
	return out, nil
}

func (s *Service) CreatePurchaseOrder(ctx context.Context, account, key uuid.UUID, in wire.PurchaseOrderInput) (wire.PurchaseOrder, error) {
	var empty wire.PurchaseOrder
	if account == uuid.Nil || key == uuid.Nil || in.Action != "purchase" || in.PaymentMethod != "yoomoney" || (in.PaymentType != "AC" && in.PaymentType != "PC") || in.PlanId == uuid.Nil || in.Revision < 1 || in.PeriodDays < 1 || in.PeriodDays > 106751 {
		return empty, failure(400, "INVALID_INPUT")
	}
	hash := bodyHash(in)
	owner, err := s.vpn.OpenAccessOwner(ctx, account)
	if err != nil {
		return empty, unavailable()
	}
	defer owner.Release()
	if err = owner.TryLock(ctx); err != nil {
		if errors.Is(err, vpn.ErrBusy) {
			return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
		}
		return empty, unavailable()
	}
	preTx, err := owner.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer preTx.Rollback(ctx)
	pre, err := s.accountByIDTx(ctx, preTx, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return empty, unavailable()
	}
	if pre.Kind != "web" || !pre.VerifiedAt.Valid {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	prior, err := scanPurchase(preTx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE account_id=$1 AND idempotency_key=$2", account, key))
	if err == nil {
		if !bytes.Equal(prior.hash, hash) {
			return empty, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		return s.publicPurchase(prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, unavailable()
	}
	if !s.cfg.YooMoneyEnabled {
		return empty, failure(409, "PAYMENT_METHOD_UNAVAILABLE")
	}
	if pre.Restricted {
		return empty, failure(403, "ACCOUNT_RESTRICTED")
	}
	if pre.VpnBanned || pre.AccessProfile.String == "unlimited" || (pre.HadSubscription && !pre.AssignedPanelID.Valid) || (pre.AssignedPanelID.Valid && pre.AssignedPanelID.String != s.cfg.PanelID) || s.cfg.PanelID == "" {
		return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
	}
	if err = preTx.Rollback(ctx); err != nil {
		return empty, unavailable()
	}
	panel := NewPanelClient(s.cfg)
	defer panel.Close()
	v, err := panel.GetClient(ctx, pre.PanelKey)
	if err != nil {
		return empty, unavailable()
	}
	if pre.AssignedPanelID.Valid {
		if v == nil || v.VPNID != pre.VpnID || v.SubID != pre.SubID || v.ExpiryTimeMS <= 0 || (!v.Enabled && v.ExpiryTimeMS > s.now().UnixMilli()) || (pre.AccessProfile.String != "regular" && pre.AccessProfile.String != "euru") {
			return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
		}
		ids, e := panel.ProfileInboundIDs(ctx, pre.AccessProfile.String)
		if e != nil {
			return empty, unavailable()
		}
		attach, detach, e := panel.MembershipDiff(ctx, v.InboundIDs, ids)
		if e != nil || len(attach) > 0 || len(detach) > 0 {
			return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
		}
	} else if v != nil {
		return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
	}
	tx, err := owner.Begin(ctx)
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
	if a.Kind != "web" || !a.VerifiedAt.Valid {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	if a.PanelKey != pre.PanelKey || a.VpnID != pre.VpnID || a.SubID != pre.SubID || a.AssignedPanelID != pre.AssignedPanelID || a.AccessProfile != pre.AccessProfile || a.HadSubscription != pre.HadSubscription || a.VpnBanned != pre.VpnBanned || a.Restricted != pre.Restricted {
		return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
	}
	prior, err = scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE account_id=$1 AND idempotency_key=$2", account, key))
	if err == nil {
		if !bytes.Equal(prior.hash, hash) {
			return empty, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		return s.publicPurchase(prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, unavailable()
	}
	if !s.cfg.YooMoneyEnabled {
		return empty, failure(409, "PAYMENT_METHOD_UNAVAILABLE")
	}
	if a.Restricted {
		return empty, failure(403, "ACCOUNT_RESTRICTED")
	}
	if a.VpnBanned || a.AccessProfile.String == "unlimited" || (a.HadSubscription && !a.AssignedPanelID.Valid) || (a.AssignedPanelID.Valid && a.AssignedPanelID.String != s.cfg.PanelID) {
		return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
	}
	var blocked bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE account_id=$1 AND (payment_status='paid' OR fulfillment_status='needs_review'))", account).Scan(&blocked); err != nil {
		return empty, unavailable()
	}
	unresolved, err := s.vpn.UnresolvedTx(ctx, tx, account)
	if err != nil {
		return empty, unavailable()
	}
	if blocked || unresolved {
		return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET active=false WHERE account_id=$1 AND active AND payment_status='pending' AND expires_at<=$2", account, now); err != nil {
		return empty, unavailable()
	}
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE account_id=$1 AND active)", account).Scan(&blocked); err != nil {
		return empty, unavailable()
	}
	if blocked {
		return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
	}
	plan, err := s.catalogue.LockCurrentPlan(ctx, tx, in.PlanId)
	if errors.Is(err, catalogue.ErrNotFound) {
		return empty, failure(409, "PURCHASE_PLAN_CONFLICT")
	}
	if err != nil && !errors.Is(err, catalogue.ErrInvalidTerms) {
		return empty, unavailable()
	}
	profile := plan.Profile
	if plan.Archived || plan.Hidden || plan.Revision != in.Revision || (profile != "regular" && profile != "euru") {
		return empty, failure(409, "PURCHASE_PLAN_CONFLICT")
	}
	terms := plan.Terms
	if err != nil || terms.Hidden || string(terms.Profile) != profile || terms.TrafficGb < 0 || terms.TrafficGb > math.MaxInt64/(1024*1024*1024) {
		return empty, unavailable()
	}
	var amount int64
	found := false
	for _, price := range terms.Prices {
		if price.PeriodDays == in.PeriodDays && price.Currency == "RUB" {
			amount, err = strconv.ParseInt(price.AmountMinor, 10, 64)
			if err != nil || amount <= 0 {
				return empty, failure(409, "PURCHASE_PLAN_CONFLICT")
			}
			found = true
			break
		}
	}
	if !found {
		return empty, failure(409, "PURCHASE_PLAN_CONFLICT")
	}
	quote := wire.PurchaseQuote{PlanId: in.PlanId, Revision: in.Revision, PeriodDays: in.PeriodDays, Devices: int64(terms.Devices), TrafficGb: int64(terms.TrafficGb), Profile: wire.PurchaseQuoteProfile(profile), AmountMinor: strconv.FormatInt(amount, 10), Currency: "RUB"}
	quoteRaw, _ := json.Marshal(quote)
	id := uuid.New()
	expires := now.Add(30 * time.Minute)
	_, err = tx.Exec(ctx, "INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)", id, account, key, hash, quoteRaw, amount, string(in.PaymentType), now, expires)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
		}
		return empty, unavailable()
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, unavailable()
	}
	return s.publicPurchase(purchaseRow{id: id, account: account, key: key, hash: hash, quote: quoteRaw, amount: amount, paymentType: string(in.PaymentType), paymentStatus: "pending", fulfillmentStatus: "not_started", active: true, created: now, expires: expires})
}

func (s *Service) PurchaseOrder(ctx context.Context, account, id uuid.UUID) (wire.PurchaseOrder, error) {
	var empty wire.PurchaseOrder
	if account == uuid.Nil || id == uuid.Nil {
		return empty, failure(400, "INVALID_INPUT")
	}
	p, err := scanPurchase(s.pool.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2", id, account))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	return s.publicPurchase(p)
}

func (s *Service) CurrentPurchaseOrder(ctx context.Context, account uuid.UUID) (wire.CurrentPurchaseOrder, error) {
	var out wire.CurrentPurchaseOrder
	if _, err := s.PaymentMethods(ctx, account); err != nil {
		return out, err
	}
	p, err := scanPurchase(s.pool.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE account_id=$1 ORDER BY CASE WHEN fulfillment_status='needs_review' THEN 0 WHEN payment_status='paid' THEN 1 WHEN active THEN 2 ELSE 3 END,created_at DESC,id DESC LIMIT 1", account))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, unavailable()
	}
	order, err := s.publicPurchase(p)
	if err != nil {
		return out, err
	}
	out.Order = &order
	return out, nil
}

func (s *Service) CancelPurchaseOrder(ctx context.Context, account, id, key uuid.UUID) (wire.PurchaseOrder, error) {
	var empty wire.PurchaseOrder
	if account == uuid.Nil || id == uuid.Nil || key == uuid.Nil {
		return empty, failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.lockAccount(ctx, tx, account); err != nil {
		return empty, failure(404, "INVALID_INPUT")
	}
	p, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2 FOR UPDATE", id, account))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	if p.paymentStatus == "canceled" {
		return s.publicPurchase(p)
	}
	if p.paymentStatus != "pending" || !p.active || !s.now().Before(p.expires) {
		return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
	}
	if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET payment_status='canceled',active=false WHERE id=$1", id); err != nil {
		return empty, unavailable()
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, unavailable()
	}
	p.paymentStatus = "canceled"
	p.active = false
	return s.publicPurchase(p)
}

func (s *Service) OperatorPurchaseOrder(ctx context.Context, actor, target uuid.UUID) (wire.CurrentPurchaseOrder, error) {
	var out wire.CurrentPurchaseOrder
	if err := s.RequireSupportOperator(ctx, actor); err != nil {
		return out, err
	}
	if _, err := s.accountByID(ctx, target); errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	} else if err != nil {
		return out, unavailable()
	}
	p, err := scanPurchase(s.pool.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE account_id=$1 ORDER BY CASE WHEN fulfillment_status='needs_review' THEN 0 WHEN payment_status='paid' THEN 1 WHEN active THEN 2 ELSE 3 END,created_at DESC,id DESC LIMIT 1", target))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, unavailable()
	}
	order, err := s.publicPurchase(p)
	if err != nil {
		return out, err
	}
	out.Order = &order
	return out, nil
}

func (s *Service) ReconcilePurchaseOrder(ctx context.Context, actor, target, id, key uuid.UUID, in wire.PurchaseReconcileInput) (wire.PurchaseOrder, error) {
	var empty wire.PurchaseOrder
	if actor == uuid.Nil || target == uuid.Nil || id == uuid.Nil || key == uuid.Nil || !validText(strings.TrimSpace(in.Reason), 1, 1000) {
		return empty, failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.lockOperatorPair(ctx, tx, actor, target); err != nil {
		return empty, err
	}
	q := store.New(tx)
	principal := "operator-account:" + actor.String()
	hash := bodyHash(struct {
		Target, ID uuid.UUID
		Input      wire.PurchaseReconcileInput
	}{target, id, in})
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "reconcilePurchaseOrder", Key: key}); err != nil {
		return empty, unavailable()
	}
	if prior, found, e := replay[wire.PurchaseOrder](ctx, q, principal, "reconcilePurchaseOrder", key, hash); found || e != nil {
		return prior, e
	}
	p, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2 FOR UPDATE", id, target))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	if p.paymentStatus != "paid" || p.fundingID == nil || p.accessID != nil || p.fulfillmentStatus != "needs_review" {
		return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
	}
	var funded bool
	if err = tx.QueryRow(ctx, purchaseFundingCheck, id).Scan(&funded); err != nil {
		return empty, unavailable()
	}
	if !funded {
		return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
	}
	if err = tx.QueryRow(ctx, `UPDATE purchase_orders p SET fulfillment_status='queued',
		review_required=EXISTS(SELECT 1 FROM purchase_receipts r WHERE r.order_id=p.id AND r.review_reason IS NOT NULL),
		review_reason=(SELECT r.review_reason FROM purchase_receipts r WHERE r.order_id=p.id AND r.review_reason IS NOT NULL ORDER BY r.created_at,r.operation_id LIMIT 1)
		WHERE p.id=$1 RETURNING p.review_required`, id).Scan(&p.review); err != nil {
		return empty, unavailable()
	}
	if s.queue == nil {
		return empty, unavailable()
	}
	if _, err = s.queue.InsertTx(ctx, tx, PurchaseArgs{OrderID: id}, &river.InsertOpts{Queue: "provision", MaxAttempts: 1000000}); err != nil {
		return empty, unavailable()
	}
	if _, err = tx.Exec(ctx, "INSERT INTO audit_events(id,created_at,action,account_id,operator_account_id,reason) VALUES($1,$2,'purchase_reconcile_requested',$3,$4,$5)", uuid.New(), s.now(), target, actor, strings.TrimSpace(in.Reason)); err != nil {
		return empty, unavailable()
	}
	p.fulfillmentStatus = "queued"
	out, err := s.publicPurchase(p)
	if err != nil {
		return empty, err
	}
	if err = s.saveIdempotency(ctx, q, principal, "reconcilePurchaseOrder", key, hash, out); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, unavailable()
	}
	return out, nil
}

// CheckPurchaseAccess is the money owner's guard, also used inside the caller's final Tx.
func (s *Service) CheckPurchaseAccess(ctx context.Context, tx pgx.Tx, order, account, operation uuid.UUID) (string, error) {
	var q store.DBTX = s.pool
	if tx != nil {
		q = tx
	}
	var valid bool
	if err := q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE id=$1 AND account_id=$2 AND payment_status='paid' AND access_operation_id=$3)", order, account, operation).Scan(&valid); err != nil || !valid {
		return "purchase_provenance_invalid", err
	}
	if err := q.QueryRow(ctx, purchaseFundingCheck, order).Scan(&valid); err != nil || !valid {
		return "purchase_funding_invalid", err
	}
	return "", nil
}

// RecordPurchaseAccessTx shares the access owner's Tx; no partial outcome may commit.
func (s *Service) RecordPurchaseAccessTx(ctx context.Context, tx pgx.Tx, operation uuid.UUID, status, reason string) error {
	if tx == nil || status != "queued" && status != "applied" && status != "needs_review" {
		return unavailable()
	}
	query := `UPDATE purchase_orders p SET fulfillment_status=$2,
		review_required=EXISTS(SELECT 1 FROM purchase_receipts r WHERE r.order_id=p.id AND r.review_reason IS NOT NULL),
		review_reason=(SELECT r.review_reason FROM purchase_receipts r WHERE r.order_id=p.id AND r.review_reason IS NOT NULL ORDER BY r.created_at,r.operation_id LIMIT 1)
		WHERE p.access_operation_id=$1 AND p.payment_status='paid'`
	args := []any{operation, status}
	if status == "needs_review" {
		query = "UPDATE purchase_orders SET fulfillment_status=$2,review_required=true,review_reason=$3 WHERE access_operation_id=$1 AND payment_status='paid'"
		args = append(args, reason)
	}
	n, err := tx.Exec(ctx, query, args...)
	if err != nil || n.RowsAffected() != 1 {
		return unavailable()
	}
	return nil
}
