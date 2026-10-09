package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments/internal/store"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/vpn"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type purchaseRow struct {
	id, account, key                              uuid.UUID
	hash, quote                                   []byte
	amount                                        int64
	paymentType, paymentStatus, fulfillmentStatus string
	active, review, fullyRefunded                 bool
	accessID                                      *uuid.UUID
	fundingID                                     *string
	created, expires                              time.Time
	method                                        string
	action                                        string
	manualDetails, manualDecision, manualReason   *string
	manualReported, manualDecided                 *time.Time
	manualActor                                   *uuid.UUID
}

const purchaseColumns = "id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,payment_status,fulfillment_status,active,review_required,access_operation_id,funding_operation_id,created_at,expires_at,payment_method,manual_details,manual_reported_at,manual_decision,manual_decided_at,manual_actor_id,manual_reason,action,(" + purchaseRefundClosed + ")"
const purchaseFundingCheck = `SELECT EXISTS(SELECT 1 FROM purchase_orders p JOIN purchase_receipts r ON r.operation_id=p.funding_operation_id AND r.order_id=p.id
 WHERE p.id=$1 AND p.payment_status='paid' AND r.review_reason IS NULL AND r.gross_minor=p.amount_minor
 AND NOT r.codepro AND NOT r.unaccepted
 AND NOT EXISTS(SELECT 1 FROM purchase_refunds f WHERE f.receipt_operation_id=r.operation_id)
 AND ((r.net_minor>0 AND r.net_minor<=r.gross_minor AND r.currency='643'
       AND ((p.payment_method='yoomoney' AND r.notification_type IN ('p2p-incoming','card-incoming')
             AND r.occurred_at>=p.created_at-interval '5 minutes' AND r.occurred_at<=p.expires_at)
         OR (p.payment_method='manual' AND r.notification_type='manual_confirmation' AND r.operation_id='manual:'||p.id::text
             AND r.net_minor=r.gross_minor AND p.manual_decision='approved' AND p.manual_actor_id IS NOT NULL
             AND p.manual_reported_at>=p.created_at AND p.manual_reported_at<p.expires_at
             AND p.manual_decided_at>=p.manual_reported_at AND r.occurred_at=p.manual_decided_at)))
   OR (p.payment_method='yookassa' AND r.notification_type='yookassa.succeeded' AND r.currency='RUB'
       AND (r.net_minor IS NULL OR (r.net_minor>0 AND r.net_minor<=r.gross_minor))
       AND r.occurred_at>=p.created_at-interval '5 minutes' AND r.occurred_at<=p.expires_at
       AND EXISTS(SELECT 1 FROM yookassa_checkouts c WHERE c.order_id=p.id
           AND r.operation_id='yookassa:'||c.payment_id::text
           AND r.provider_data->>'provider'='yookassa' AND r.provider_data->>'payment_id'=c.payment_id::text
           AND r.provider_data->>'shop_id'=c.shop_id AND r.provider_data->>'test'=c.test_mode::text
           AND r.provider_data->>'order_id'=p.id::text AND r.provider_data->>'status'='succeeded'
           AND r.provider_data->>'paid'='true' AND r.provider_data->>'amount_minor'=r.gross_minor::text
           AND r.provider_data->>'refund_minor'='0'))
   OR (p.payment_method IN ('cryptomus','heleket') AND p.quote->>'currency'='USD' AND r.currency='USD' AND r.net_minor IS NULL
       AND r.notification_type IN (p.payment_method||'.paid',p.payment_method||'.paid_over')
       AND r.occurred_at>=p.created_at-interval '5 minutes' AND r.occurred_at<=p.expires_at
       AND EXISTS(SELECT 1 FROM (SELECT order_id,merchant_id,first_attempt_at,invoice_id,'cryptomus' AS provider FROM cryptomus_checkouts
                     UNION ALL SELECT order_id,merchant_id,first_attempt_at,invoice_id,'heleket' FROM heleket_checkouts) c
           WHERE c.order_id=p.id AND c.provider=p.payment_method
           AND c.first_attempt_at IS NOT NULL AND r.operation_id=c.provider||':'||c.invoice_id::text
           AND r.provider_data->>'provider'=c.provider AND r.provider_data->>'invoice_id'=c.invoice_id::text
           AND r.provider_data->>'merchant_id'=c.merchant_id AND r.provider_data->>'order_id'=p.id::text
           AND r.notification_type=c.provider||'.'||(r.provider_data->>'status')
           AND r.provider_data->>'payment_status'=r.provider_data->>'status'
           AND r.provider_data->'is_final'='true'::jsonb AND r.provider_data->>'currency'='USD'
           AND r.provider_data->>'amount_minor'=r.gross_minor::text
           AND r.provider_data->>'payer_currency' ~ '^[A-Z0-9]{1,16}$'
           AND CASE WHEN r.provider_data->>'payment_amount' ~ '^[0-9]{1,40}(\.[0-9]{1,40})?$'
                     AND r.provider_data->>'payer_amount' ~ '^[0-9]{1,40}(\.[0-9]{1,40})?$'
                    THEN (r.provider_data->>'payment_amount')::numeric >= (r.provider_data->>'payer_amount')::numeric
                         AND (r.provider_data->>'payer_amount')::numeric>0 ELSE false END))
   OR (p.payment_method='telegram_stars' AND p.payment_type='STARS' AND p.quote->>'currency'='XTR'
       AND r.notification_type='telegram_stars.paid' AND r.currency='XTR' AND r.net_minor IS NULL
       AND r.occurred_at>=p.created_at-interval '5 minutes' AND r.occurred_at<=p.expires_at
       AND NOT EXISTS(SELECT 1 FROM stars_refunds f WHERE f.receipt_operation_id=r.operation_id)
       AND EXISTS(SELECT 1 FROM stars_checkouts c WHERE c.order_id=COALESCE((SELECT cy.root_order_id FROM stars_subscription_cycles cy WHERE cy.order_id=p.id),p.id)
         AND r.provider_data->>'provider'='telegram_stars'
         AND r.provider_data->>'bot_id'=c.bot_id::text AND r.provider_data->>'payer_id'=c.payer_id::text
         AND r.provider_data->>'payload'=c.payload AND r.provider_data->>'currency'='XTR'
         AND r.provider_data->>'amount_minor'=r.gross_minor::text
         AND ((c.subscription_period=0 AND r.provider_data->>'recurring'='false' AND r.provider_data->>'first_recurring'='false'
               AND r.provider_data->>'subscription_expires_at'='0')
           OR (c.subscription_period=2592000 AND p.quote->>'stars_recurring'='true' AND p.quote->>'period_days'='30' AND p.amount_minor<=10000
               AND r.provider_data->>'recurring'='true'
               AND EXISTS(SELECT 1 FROM stars_subscription_cycles cy JOIN stars_subscriptions sub ON sub.first_receipt_id=cy.subscription_receipt_id
                   JOIN purchase_orders root ON root.id=cy.root_order_id
                   WHERE cy.order_id=p.id AND cy.root_order_id=c.order_id AND cy.receipt_id=r.operation_id
                   AND sub.canonical AND sub.root_order_id=c.order_id AND sub.bot_id=c.bot_id AND sub.payer_id=c.payer_id
                   AND ((p.id=c.order_id AND p.action='purchase' AND cy.subscription_receipt_id=r.operation_id
                         AND cy.previous_access_operation_id IS NULL AND r.provider_data->>'first_recurring'='true')
                     OR (p.id<>c.order_id AND p.action='renew' AND p.quote=root.quote AND p.account_id=root.account_id
                         AND cy.previous_access_operation_id IS NOT NULL AND r.provider_data->>'first_recurring'='false'))
                   AND r.provider_data->>'subscription_expires_at'=extract(epoch FROM cy.paid_until)::bigint::text)))
         AND r.operation_id='stars:'||encode(sha256(convert_to(c.bot_id::text||':'||(r.provider_data->>'charge_id'),'UTF8')),'hex')))))`

func scanPurchase(row pgx.Row) (purchaseRow, error) {
	var p purchaseRow
	err := row.Scan(&p.id, &p.account, &p.key, &p.hash, &p.quote, &p.amount, &p.paymentType, &p.paymentStatus, &p.fulfillmentStatus, &p.active, &p.review, &p.accessID, &p.fundingID, &p.created, &p.expires, &p.method, &p.manualDetails, &p.manualReported, &p.manualDecision, &p.manualDecided, &p.manualActor, &p.manualReason, &p.action, &p.fullyRefunded)
	return p, err
}

func (s *Service) publicPurchase(ctx context.Context, p purchaseRow) (PurchaseOrder, error) {
	var quote PurchaseQuote
	if json.Unmarshal(p.quote, &quote) != nil {
		return PurchaseOrder{}, unavailable()
	}
	now := s.now().UTC()
	expired := !now.Before(p.expires)
	if p.method == "manual" && p.manualReported != nil && p.manualDecision == nil {
		expired = false
	}
	canPay := p.active && p.paymentStatus == "pending" && !expired && p.manualReported == nil && s.methodEnabled(p.method)
	if canPay {
		reason, err := s.purchasePolicyTx(ctx, nil, p)
		if err != nil {
			return PurchaseOrder{}, err
		}
		canPay = reason == "" && !p.review
	}
	out := PurchaseOrder{OrderId: p.id, Action: p.action, PaymentMethod: p.method, PaymentType: p.paymentType, Quote: quote, PaymentStatus: p.paymentStatus, FulfillmentStatus: p.fulfillmentStatus, ReviewRequired: p.review, CreatedAt: p.created, ExpiresAt: p.expires, Expired: expired, CanPay: canPay, CanCancel: p.active && p.paymentStatus == "pending" && !expired && p.manualReported == nil, AccessOperationId: p.accessID}
	out.FullyRefunded = p.fullyRefunded
	if p.method == "manual" {
		if p.manualDetails == nil {
			return PurchaseOrder{}, unavailable()
		}
		state := "not_reported"
		if p.manualReported != nil {
			state = "pending"
		}
		if p.manualDecision != nil {
			state = *p.manualDecision
		}
		out.ManualPayment = &ManualPayment{State: state, Instructions: *p.manualDetails, CanReport: p.active && p.paymentStatus == "pending" && p.manualReported == nil && !expired && !p.review, ReportedAt: p.manualReported, DecidedAt: p.manualDecided, Reason: p.manualReason}
	}
	if p.method == "yookassa" {
		var state string
		var link *string
		if err := s.pool.QueryRow(ctx, "SELECT state,confirmation_url FROM yookassa_checkouts WHERE order_id=$1", p.id).Scan(&state, &link); err != nil {
			return PurchaseOrder{}, unavailable()
		}
		if !canPay || p.review {
			state, link = "unavailable", nil
		}
		if state != "ready" {
			link = nil
		}
		out.CanPay = canPay && !p.review && state == "ready" && link != nil
		out.YooKassaCheckout = &YooKassaCheckout{State: state, URL: link}
	}
	if p.method == "cryptomus" || p.method == "heleket" {
		var state string
		var link *string
		if err := s.pool.QueryRow(ctx, "SELECT state,checkout_url FROM "+cryptoProvider(p.method).table()+" WHERE order_id=$1", p.id).Scan(&state, &link); err != nil {
			return PurchaseOrder{}, unavailable()
		}
		if !canPay || p.review {
			state, link = "unavailable", nil
		}
		if state != "ready" {
			link = nil
		}
		out.CanPay = canPay && !p.review && state == "ready" && link != nil
		if p.method == "heleket" {
			out.HeleketCheckout = &HeleketCheckout{State: state, URL: link}
		} else {
			out.CryptomusCheckout = &CryptomusCheckout{State: state, URL: link}
		}
	}
	if p.method == "telegram_stars" {
		var link *string
		err := s.pool.QueryRow(ctx, `SELECT invoice_url FROM stars_checkouts WHERE order_id=$1`, p.id).Scan(&link)
		if errors.Is(err, pgx.ErrNoRows) {
			var cycle bool
			if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stars_subscription_cycles WHERE order_id=$1 AND root_order_id<>order_id)`, p.id).Scan(&cycle); err != nil || !cycle {
				return PurchaseOrder{}, unavailable()
			}
			return out, nil // A native renewal cycle never creates another invoice.
		}
		if err != nil {
			return PurchaseOrder{}, unavailable()
		}
		state := "preparing"
		if link != nil {
			state = "ready"
		}
		if !canPay {
			state, link = "unavailable", nil
		}
		out.StarsCheckout = &StarsCheckout{State: state, URL: link}
	}
	if canPay && p.method == "yoomoney" {
		out.Checkout = &YooMoneyCheckout{Action: "https://yoomoney.ru/quickpay/confirm", Method: "POST", Fields: YooMoneyCheckoutFields{Receiver: s.config().YooMoneyWalletID, QuickpayForm: "button", PaymentType: p.paymentType, Sum: fmt.Sprintf("%d.%02d", p.amount/100, p.amount%100), Label: p.id, SuccessURL: strings.TrimRight(s.config().CabinetOrigin, "/") + "/orders/" + p.id.String()}}
	}
	return out, nil
}

func (s *Service) PaymentMethods(ctx context.Context, account uuid.UUID) (PaymentMethods, error) {
	out := PaymentMethods{Methods: []PaymentMethod{}}
	a, err := s.accountByID(ctx, account)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, failure(401, "INVALID_CREDENTIALS")
		}
		return out, unavailable()
	}
	if a.Kind != "web" || a.VerifiedAt == nil {
		return out, failure(401, "INVALID_CREDENTIALS")
	}
	if a.Restricted {
		return out, failure(403, "ACCOUNT_RESTRICTED")
	}
	if s.config().YooMoneyEnabled {
		out.Methods = append(out.Methods, PaymentMethod{Id: "yoomoney", Currency: "RUB"})
	}
	if s.methodEnabled("yookassa") {
		out.Methods = append(out.Methods, PaymentMethod{Id: "yookassa", Currency: "RUB"})
	}
	if s.methodEnabled("cryptomus") {
		out.Methods = append(out.Methods, PaymentMethod{Id: "cryptomus", Currency: "USD"})
	}
	if s.methodEnabled("heleket") {
		out.Methods = append(out.Methods, PaymentMethod{Id: "heleket", Currency: "USD"})
	}
	if s.methodEnabled("manual") {
		out.Methods = append(out.Methods, PaymentMethod{Id: "manual", Currency: "RUB"})
	}
	return out, nil
}

func (s *Service) CreatePurchaseOrder(ctx context.Context, account, key uuid.UUID, in PurchaseOrderInput) (PurchaseOrder, error) {
	var empty PurchaseOrder
	if in.StarsRecurring && (in.Action != "purchase" || in.PaymentMethod != "telegram_stars" || in.PeriodDays != 30) {
		return empty, failure(400, "INVALID_INPUT")
	}
	if account == uuid.Nil || key == uuid.Nil || (in.Action != "purchase" && in.Action != "renew" && in.Action != "change_plan") || (in.Action == "change_plan") != (in.SourceAccessOperationId != nil) || (in.SourceAccessOperationId != nil && *in.SourceAccessOperationId == uuid.Nil) || !((in.PaymentMethod == "yoomoney" && (in.PaymentType == "AC" || in.PaymentType == "PC")) || (in.PaymentMethod == "manual" && in.PaymentType == "MANUAL") || (in.PaymentMethod == "yookassa" && in.PaymentType == "YOOKASSA") || (in.PaymentMethod == "cryptomus" && in.PaymentType == "CRYPTOMUS") || (in.PaymentMethod == "heleket" && in.PaymentType == "HELEKET") || (in.PaymentMethod == "telegram_stars" && in.PaymentType == "STARS")) || in.PlanId == uuid.Nil || in.Revision < 1 || in.PeriodDays < 1 || in.PeriodDays > 106751 {
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
	if !purchaseSourceEligible(pre, in.PaymentMethod) {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	prior, err := scanPurchase(preTx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE account_id=$1 AND idempotency_key=$2", account, key))
	if err == nil {
		if !bytes.Equal(prior.hash, hash) {
			return empty, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		return s.publicPurchase(ctx, prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, unavailable()
	}
	if !s.methodEnabled(in.PaymentMethod) {
		return empty, failure(409, "PAYMENT_METHOD_UNAVAILABLE")
	}
	eligible, err := s.purchaseBillingEligibleTx(ctx, preTx, pre, purchaseRow{method: in.PaymentMethod, action: in.Action})
	if err != nil {
		return empty, err
	}
	if !eligible {
		return empty, failure(409, "EXTERNAL_BILLING_UNVERIFIED")
	}
	if in.Action != "purchase" {
		if err = s.requireOrderPlan(ctx, preTx, account, in.Action, in.PlanId, in.SourceAccessOperationId); err != nil {
			return empty, err
		}
	}
	if pre.Restricted {
		return empty, failure(403, "ACCOUNT_RESTRICTED")
	}
	if pre.VpnBanned || stringValue(pre.AccessProfile) == "unlimited" || (pre.HadSubscription && pre.AssignedPanelID == nil) {
		return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
	}
	if err = preTx.Rollback(ctx); err != nil {
		return empty, unavailable()
	}
	panelID := stringValue(pre.AssignedPanelID)
	if panelID == "" {
		server, err := owner.AvailableServer(ctx)
		if err != nil {
			return empty, unavailable()
		}
		panelID = server.ID
	}
	panel, err := owner.PanelFor(ctx, panelID)
	if err != nil {
		return empty, unavailable()
	}
	defer panel.Close()
	v, err := panel.GetClient(ctx, pre.PanelKey)
	if err != nil {
		return empty, unavailable()
	}
	if pre.AssignedPanelID != nil {
		if v == nil || v.VPNID != pre.VpnID || v.SubID != pre.SubID || v.ExpiryTimeMS <= 0 || (!v.Enabled && v.ExpiryTimeMS > s.now().UnixMilli() && (in.Action == "purchase" || !subscriptions.CanActivateRenewal(v, s.now()))) || (stringValue(pre.AccessProfile) != "regular" && stringValue(pre.AccessProfile) != "euru") {
			return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
		}
		ids, e := panel.ProfileInboundIDs(ctx, stringValue(pre.AccessProfile))
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
	if !purchaseSourceEligible(a, in.PaymentMethod) {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	if a.PanelKey != pre.PanelKey || a.VpnID != pre.VpnID || a.SubID != pre.SubID || !reflect.DeepEqual(a.AssignedPanelID, pre.AssignedPanelID) || !reflect.DeepEqual(a.AccessProfile, pre.AccessProfile) || a.HadSubscription != pre.HadSubscription || a.VpnBanned != pre.VpnBanned || a.Restricted != pre.Restricted || !reflect.DeepEqual(a.TelegramID, pre.TelegramID) || a.TelegramLoginDisabled != pre.TelegramLoginDisabled || !reflect.DeepEqual(a.LegacyUserID, pre.LegacyUserID) {
		return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
	}
	prior, err = scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE account_id=$1 AND idempotency_key=$2", account, key))
	if err == nil {
		if !bytes.Equal(prior.hash, hash) {
			return empty, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		return s.publicPurchase(ctx, prior)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, unavailable()
	}
	if !s.methodEnabled(in.PaymentMethod) {
		return empty, failure(409, "PAYMENT_METHOD_UNAVAILABLE")
	}
	eligible, err = s.purchaseBillingEligibleTx(ctx, tx, a, purchaseRow{method: in.PaymentMethod, action: in.Action})
	if err != nil {
		return empty, err
	}
	if !eligible {
		return empty, failure(409, "EXTERNAL_BILLING_UNVERIFIED")
	}
	if in.Action != "purchase" {
		if err = s.requireOrderPlan(ctx, tx, account, in.Action, in.PlanId, in.SourceAccessOperationId); err != nil {
			return empty, err
		}
	}
	if a.Restricted {
		return empty, failure(403, "ACCOUNT_RESTRICTED")
	}
	if a.VpnBanned || stringValue(a.AccessProfile) == "unlimited" || (a.HadSubscription && a.AssignedPanelID == nil) {
		return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
	}
	blocked, err := s.purchaseHistoryBlockedTx(ctx, tx, account, uuid.Nil, in.Action, in.PaymentMethod)
	if err != nil {
		return empty, err
	}
	unresolved, err := s.vpn.UnresolvedTx(ctx, tx, account)
	if err != nil {
		return empty, unavailable()
	}
	if blocked || unresolved {
		return empty, failure(409, "PURCHASE_NOT_ELIGIBLE")
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET active=false WHERE account_id=$1 AND active AND payment_status='pending' AND manual_reported_at IS NULL AND expires_at<=$2", account, now); err != nil {
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
	if plan.Archived || (plan.Hidden && in.Action != "renew") || plan.Revision != in.Revision || (profile != "regular" && profile != "euru") {
		return empty, failure(409, "PURCHASE_PLAN_CONFLICT")
	}
	terms := plan.Terms
	if err != nil || (terms.Hidden && in.Action != "renew") || terms.Hidden != plan.Hidden || terms.Devices < 1 || terms.Devices > 10000 || string(terms.Profile) != profile || terms.TrafficGb < 0 || terms.TrafficGb > math.MaxInt64/(1024*1024*1024) {
		return empty, unavailable()
	}
	currency := "RUB"
	if in.PaymentMethod == "cryptomus" || in.PaymentMethod == "heleket" {
		currency = "USD"
	}
	if in.PaymentMethod == "telegram_stars" {
		currency = "XTR"
	}
	var amount int64
	found := false
	for _, price := range terms.Prices {
		if price.PeriodDays == in.PeriodDays && price.Currency == currency {
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
	if in.StarsRecurring && amount > 10000 {
		return empty, failure(400, "INVALID_INPUT")
	}
	quote := PurchaseQuote{PlanId: in.PlanId, Revision: in.Revision, PeriodDays: in.PeriodDays, Devices: int64(terms.Devices), TrafficGb: int64(terms.TrafficGb), Profile: profile, AmountMinor: strconv.FormatInt(amount, 10), Currency: currency, SourceAccessOperationId: in.SourceAccessOperationId, StarsRecurring: in.StarsRecurring}
	quoteRaw, _ := json.Marshal(quote)
	id := uuid.New()
	expires := now.Add(30 * time.Minute)
	var details *string
	if in.PaymentMethod == "manual" {
		value := s.config().ManualCardDetails
		details = &value
	}
	_, err = tx.Exec(ctx, "INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,created_at,expires_at,payment_method,manual_details,action) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)", id, account, key, hash, quoteRaw, amount, string(in.PaymentType), now, expires, in.PaymentMethod, details, in.Action)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
		}
		return empty, unavailable()
	}
	if in.PaymentMethod == "telegram_stars" {
		var period int64
		if in.StarsRecurring {
			period = starsSubscriptionPeriod
		}
		if _, err = tx.Exec(ctx, `INSERT INTO stars_checkouts(order_id,bot_id,payer_id,payload,subscription_period) VALUES($1,$2,$3,$4,$5)`, id, s.stars.BotID, *a.TelegramID, "stars:v1:"+id.String(), period); err != nil {
			return empty, unavailable()
		}
	}
	if in.PaymentMethod == "yookassa" {
		if err = s.queueYooKassaTx(ctx, tx, id, quote); err != nil {
			return empty, err
		}
	}
	if in.PaymentMethod == "cryptomus" {
		if err = s.queueCryptomusTx(ctx, tx, id, quote); err != nil {
			return empty, err
		}
	}
	if in.PaymentMethod == "heleket" {
		if err = s.queueHeleketTx(ctx, tx, id, quote); err != nil {
			return empty, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, unavailable()
	}
	return s.publicPurchase(ctx, purchaseRow{id: id, account: account, key: key, hash: hash, quote: quoteRaw, amount: amount, paymentType: string(in.PaymentType), paymentStatus: "pending", fulfillmentStatus: "not_started", active: true, created: now, expires: expires, method: in.PaymentMethod, manualDetails: details, action: in.Action})
}

func (s *Service) PurchaseOrder(ctx context.Context, account, id uuid.UUID) (PurchaseOrder, error) {
	var empty PurchaseOrder
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
	return s.publicPurchase(ctx, p)
}

func (s *Service) CurrentPurchaseOrder(ctx context.Context, account uuid.UUID) (CurrentPurchaseOrder, error) {
	var out CurrentPurchaseOrder
	if err := s.requirePaymentReader(ctx, account); err != nil {
		return out, err
	}
	p, err := scanPurchase(s.pool.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE account_id=$1 ORDER BY CASE WHEN NOT ("+purchaseRefundClosed+") AND (review_required OR fulfillment_status='needs_review') THEN 0 WHEN NOT ("+purchaseRefundClosed+") AND payment_status='paid' AND fulfillment_status<>'applied' THEN 1 WHEN active AND payment_status='pending' THEN 2 ELSE 3 END,created_at DESC,id DESC LIMIT 1", account))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, unavailable()
	}
	order, err := s.publicPurchase(ctx, p)
	if err != nil {
		return out, err
	}
	out.Order = &order
	if p.paymentStatus == "paid" && p.fulfillmentStatus == "applied" || order.FullyRefunded {
		reason, err := s.purchasePolicyTx(ctx, nil, purchaseRow{account: account, action: "purchase"})
		if err != nil {
			return out, err
		}
		unresolved, err := s.vpn.UnresolvedTx(ctx, nil, account)
		if err != nil {
			return out, unavailable()
		}
		allowed := reason == "" && !unresolved
		out.CanPurchase = &allowed
	}
	return out, nil
}

func (s *Service) CancelPurchaseOrder(ctx context.Context, account, id, key uuid.UUID) (PurchaseOrder, error) {
	var empty PurchaseOrder
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
		return s.publicPurchase(ctx, p)
	}
	if p.paymentStatus != "pending" || !p.active || p.manualReported != nil || !s.now().Before(p.expires) {
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
	return s.publicPurchase(ctx, p)
}

func (s *Service) OperatorPurchaseOrder(ctx context.Context, actor, target uuid.UUID) (CurrentPurchaseOrder, error) {
	var out CurrentPurchaseOrder
	if err := s.authority.RequireOperator(ctx, actor); err != nil {
		return out, err
	}
	if _, err := s.accountByID(ctx, target); errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	} else if err != nil {
		return out, unavailable()
	}
	p, err := scanPurchase(s.pool.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE account_id=$1 ORDER BY CASE WHEN NOT ("+purchaseRefundClosed+") AND (review_required OR fulfillment_status='needs_review') THEN 0 WHEN NOT ("+purchaseRefundClosed+") AND payment_status='paid' AND fulfillment_status<>'applied' THEN 1 WHEN active AND payment_status='pending' THEN 2 ELSE 3 END,created_at DESC,id DESC LIMIT 1", target))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, unavailable()
	}
	order, err := s.publicPurchase(ctx, p)
	if err != nil {
		return out, err
	}
	out.Order = &order
	return out, nil
}

func (s *Service) ReconcilePurchaseOrder(ctx context.Context, actor, target, id, key uuid.UUID, in PurchaseReconcileInput) (PurchaseOrder, error) {
	var empty PurchaseOrder
	if actor == uuid.Nil || target == uuid.Nil || id == uuid.Nil || key == uuid.Nil || !validText(strings.TrimSpace(in.Reason), 1, 1000) {
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
	hash := bodyHash(struct {
		Target, ID uuid.UUID
		Input      PurchaseReconcileInput
	}{target, id, in})
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "reconcilePurchaseOrder", Key: key}); err != nil {
		return empty, unavailable()
	}
	if prior, found, e := replay[PurchaseOrder](ctx, q, principal, "reconcilePurchaseOrder", key, hash); found || e != nil {
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
	if reason, err := s.purchasePolicyTx(ctx, tx, p); err != nil {
		return empty, err
	} else if reason != "" {
		return empty, failure(409, "PURCHASE_ORDER_CONFLICT")
	}
	if err = tx.QueryRow(ctx, `UPDATE purchase_orders p SET fulfillment_status='queued',
		review_required=EXISTS(SELECT 1 FROM purchase_receipts r WHERE r.order_id=p.id AND r.review_reason IS NOT NULL AND NOT EXISTS(SELECT 1 FROM purchase_refunds f WHERE f.receipt_operation_id=r.operation_id)),
		review_reason=(SELECT r.review_reason FROM purchase_receipts r WHERE r.order_id=p.id AND r.review_reason IS NOT NULL AND NOT EXISTS(SELECT 1 FROM purchase_refunds f WHERE f.receipt_operation_id=r.operation_id) ORDER BY r.created_at,r.operation_id LIMIT 1)
		WHERE p.id=$1 RETURNING p.review_required`, id).Scan(&p.review); err != nil {
		return empty, unavailable()
	}
	if s.queue == nil || s.queue() == nil {
		return empty, unavailable()
	}
	if _, err = s.queue().InsertTx(ctx, tx, PurchaseArgs{OrderID: id}, &river.InsertOpts{Queue: "provision", MaxAttempts: 1000000}); err != nil {
		return empty, unavailable()
	}
	auditReason := strings.TrimSpace(in.Reason)
	if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: "purchase_reconcile_requested", AccountID: target, OperatorAccountID: &actor, Reason: &auditReason}); err != nil {
		return empty, unavailable()
	}
	p.fulfillmentStatus = "queued"
	out, err := s.publicPurchase(ctx, p)
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
	p, err := scanPurchase(q.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 AND account_id=$2 AND payment_status='paid' AND access_operation_id=$3", order, account, operation))
	if errors.Is(err, pgx.ErrNoRows) {
		return "purchase_provenance_invalid", nil
	}
	if err != nil {
		return "", err
	}
	var valid bool
	if err := q.QueryRow(ctx, purchaseFundingCheck, order).Scan(&valid); err != nil || !valid {
		return "purchase_funding_invalid", err
	}
	return s.purchasePolicyTx(ctx, tx, p)
}

// RecordPurchaseAccessTx shares the access owner's Tx; no partial outcome may commit.
func (s *Service) RecordPurchaseAccessTx(ctx context.Context, tx pgx.Tx, operation uuid.UUID, status, reason string) error {
	if tx == nil || status != "queued" && status != "applied" && status != "needs_review" {
		return unavailable()
	}
	query := `UPDATE purchase_orders p SET fulfillment_status=$2,
		review_required=EXISTS(SELECT 1 FROM purchase_receipts r WHERE r.order_id=p.id AND r.review_reason IS NOT NULL AND NOT EXISTS(SELECT 1 FROM purchase_refunds f WHERE f.receipt_operation_id=r.operation_id)),
		review_reason=(SELECT r.review_reason FROM purchase_receipts r WHERE r.order_id=p.id AND r.review_reason IS NOT NULL AND NOT EXISTS(SELECT 1 FROM purchase_refunds f WHERE f.receipt_operation_id=r.operation_id) ORDER BY r.created_at,r.operation_id LIMIT 1)
		WHERE p.access_operation_id=$1 AND p.payment_status='paid'`
	args := []any{operation, status}
	if status == "needs_review" {
		query = "UPDATE purchase_orders SET fulfillment_status=$2,review_required=true,review_reason=$3 WHERE access_operation_id=$1 AND payment_status='paid'"
		args = append(args, reason)
	}
	var order, account uuid.UUID
	if err := tx.QueryRow(ctx, query+" RETURNING id,account_id", args...).Scan(&order, &account); err != nil {
		return unavailable()
	}
	if status != "queued" {
		a, err := s.authority.LookupTx(ctx, tx, account)
		if err != nil {
			return unavailable()
		}
		if a.TelegramID != nil && !a.TelegramLoginDisabled {
			notice := notifications.ClientNotice{AccountID: account, TelegramID: *a.TelegramID, CredentialVersion: a.CredentialVersion, Locale: a.Locale, EventKey: "purchase:" + operation.String() + ":" + status, Route: "orders:" + order.String()}
			if s.notifications.EnqueueClientTx(ctx, tx, notice, s.now()) != nil {
				return unavailable()
			}
		}
	}
	return nil
}
