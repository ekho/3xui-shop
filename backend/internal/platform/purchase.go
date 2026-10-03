package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	a, err := store.New(s.pool).AccountByID(ctx, account)
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
	c, err := s.pool.Acquire(ctx)
	if err != nil {
		return empty, unavailable()
	}
	var ownerLocked bool
	if err = c.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended('account-access:'||$1::text,0))", account).Scan(&ownerLocked); err != nil || !ownerLocked {
		c.Release()
		if err != nil {
			return empty, unavailable()
		}
		return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
	}
	defer releaseOwner(c, account)
	pre, err := store.New(c).AccountByID(ctx, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return empty, unavailable()
	}
	if pre.Kind != "web" || !pre.VerifiedAt.Valid {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	prior, err := scanPurchase(c.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE account_id=$1 AND idempotency_key=$2", account, key))
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
	panel := NewPanelClient(s.cfg)
	defer panel.Close()
	v, err := panel.GetClient(ctx, pre.PanelKey)
	if err != nil {
		return empty, unavailable()
	}
	if pre.AssignedPanelID.Valid {
		if v == nil || v.VPNID != pre.VpnID || v.SubID != pre.SubID || v.ExpiryTimeMS <= 0 || (pre.AccessProfile.String != "regular" && pre.AccessProfile.String != "euru") {
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
	tx, err := c.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	a, err := store.New(tx).LockAccount(ctx, account)
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
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE account_id=$1 AND (payment_status='paid' OR fulfillment_status='needs_review')) OR EXISTS(SELECT 1 FROM access_operations WHERE account_id=$1 AND status IN ('pending','provisioning','needs_review')) OR EXISTS(SELECT 1 FROM trial_operations WHERE account_id=$1 AND status IN ('pending','provisioning','needs_review'))", account).Scan(&blocked); err != nil {
		return empty, unavailable()
	}
	if blocked {
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
	var termsRaw []byte
	var currentRev int64
	var archived, hidden bool
	var profile string
	err = tx.QueryRow(ctx, "SELECT p.current_revision,p.archived,p.current_hidden,p.current_profile,r.terms FROM catalogue_plans p JOIN catalogue_revisions r ON r.plan_id=p.id AND r.revision=p.current_revision WHERE p.id=$1 FOR UPDATE OF p", in.PlanId).Scan(&currentRev, &archived, &hidden, &profile, &termsRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(409, "PURCHASE_PLAN_CONFLICT")
	}
	if err != nil {
		return empty, unavailable()
	}
	if archived || hidden || currentRev != in.Revision || (profile != "regular" && profile != "euru") {
		return empty, failure(409, "PURCHASE_PLAN_CONFLICT")
	}
	var terms wire.CatalogueTerms
	if json.Unmarshal(termsRaw, &terms) != nil || terms.Hidden || string(terms.Profile) != profile || terms.TrafficGb < 0 || terms.TrafficGb > math.MaxInt64/(1024*1024*1024) {
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
	if _, err = store.New(tx).LockAccount(ctx, account); err != nil {
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
	if _, err := store.New(s.pool).AccountByID(ctx, target); errors.Is(err, pgx.ErrNoRows) {
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
