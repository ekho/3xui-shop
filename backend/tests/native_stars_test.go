package tests

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func nativeStarsFixture(t *testing.T) (*fixture, *nativeBot, ed25519.PrivateKey, uuid.UUID) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := openMode(t, true, pub)
	f.cfg.Payments.StarsEnabled = true
	plan := uuid.New()
	terms := wire.CataloguePlanSnapshot{PlanId: plan, Revision: 1, Devices: 2, TrafficGb: 15, Profile: "regular", Periods: []int64{30}, Prices: []wire.CataloguePrice{{PeriodDays: 30, Currency: "RUB", AmountMinor: "0"}, {PeriodDays: 30, Currency: "USD", AmountMinor: "0"}, {PeriodDays: 30, Currency: "XTR", AmountMinor: "100"}}}
	raw, _ := json.Marshal(terms)
	ctx := context.Background()
	if _, err = f.env.Pool.Exec(ctx, `INSERT INTO catalogue_plans(id,current_revision,current_devices,current_profile,current_hidden) VALUES($1,1,2,'regular',false)`, plan); err != nil {
		t.Fatal(err)
	}
	if _, err = f.env.Pool.Exec(ctx, `INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,1,$2,false,'legacy_import',$3)`, plan, raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	return f, &nativeBot{}, key, plan
}

func nativeStarsInit(key ed25519.PrivateKey, tg int64) string {
	return testkit.SignedMiniAppData(key, 123456789, time.Now(), fmt.Sprintf(`{"id":%d,"first_name":"Owned Stars client","language_code":"en"}`, tg), "")
}

func nativeStarsOrder(t *testing.T, f *fixture, key ed25519.PrivateKey, tg int64, plan uuid.UUID, recurring ...bool) (wire.MiniAppSessionResult, wire.PurchaseOrder) {
	t.Helper()
	status, body, _ := f.send(t, f.public.Client(), "POST", "/api/v1/telegram/mini-app/session", map[string]string{"init_data": nativeStarsInit(key, tg), "accepted_terms_version": "1", "accepted_privacy_version": "1"}, "", "", false)
	var auth wire.MiniAppSessionResult
	if status != 200 || json.Unmarshal(body, &auth) != nil {
		t.Fatal("native Stars signed session", status)
	}
	input := map[string]any{"action": "purchase", "payment_method": "telegram_stars", "payment_type": "STARS", "plan_id": plan, "revision": 1, "period_days": 30}
	if len(recurring) > 0 && recurring[0] {
		input["stars_recurring"] = true
	}
	status, body, _ = f.send(t, f.public.Client(), "POST", "/api/v1/orders", input, auth.CsrfToken, uuid.NewString(), false, auth.SessionToken)
	var order wire.PurchaseOrder
	if status != 201 || json.Unmarshal(body, &order) != nil || order.Quote.Currency != "XTR" || order.Quote.AmountMinor != "100" {
		t.Fatal("native Stars order", status)
	}
	status, body, _ = f.send(t, f.public.Client(), "POST", "/api/v1/orders/"+order.OrderId.String()+"/stars-invoice", map[string]string{}, auth.CsrfToken, "", false, auth.SessionToken)
	if status != 200 || json.Unmarshal(body, &order) != nil || order.StarsCheckout == nil || order.StarsCheckout.Url == nil {
		t.Fatal("native Stars invoice", status)
	}
	return auth, order
}

func nativeStarsProof(tg int64, order uuid.UUID, charge string, at time.Time) payments.StarsPaymentInput {
	return payments.StarsPaymentInput{StarsPreCheckoutInput: payments.StarsPreCheckoutInput{BotID: 123456789, PayerID: tg, Amount: 100, Currency: "XTR", Payload: "stars:v1:" + order.String()}, ChargeID: charge, At: at.UTC().Truncate(time.Second)}
}

func (b *nativeBot) preCheckout(in payments.StarsPaymentInput) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sequence++
	b.updates = append(b.updates, map[string]any{"update_id": b.sequence, "pre_checkout_query": map[string]any{"id": fmt.Sprint(b.sequence), "from": map[string]any{"id": in.PayerID, "is_bot": false, "language_code": "en"}, "currency": in.Currency, "total_amount": in.Amount, "invoice_payload": in.Payload}})
}

func (b *nativeBot) starsMoney(in payments.StarsPaymentInput, refund bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sequence++
	field := "successful_payment"
	if refund {
		field = "refunded_payment"
	}
	payment := map[string]any{"currency": in.Currency, "total_amount": in.Amount, "invoice_payload": in.Payload, "telegram_payment_charge_id": in.ChargeID, "provider_payment_charge_id": in.ProviderChargeID}
	if !refund && in.Recurring {
		payment["is_recurring"], payment["is_first_recurring"], payment["subscription_expiration_date"] = true, in.FirstRecurring, in.SubscriptionExpiresAt
	}
	b.updates = append(b.updates, map[string]any{"update_id": b.sequence, "message": map[string]any{"message_id": b.sequence, "date": in.At.Unix(), "from": map[string]any{"id": in.PayerID, "is_bot": false, "language_code": "en"}, "chat": map[string]any{"id": in.PayerID, "type": "private"}, field: payment}})
}

func assertNativeStarsPanel(t *testing.T, f *fixture, order uuid.UUID) vpn.AccessTarget {
	t.Helper()
	var raw []byte
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT a.target FROM access_operations a JOIN purchase_orders p ON p.access_operation_id=a.id WHERE p.id=$1`, order).Scan(&raw); err != nil {
		t.Fatal("native Stars target missing")
	}
	var target vpn.AccessTarget
	if json.Unmarshal(raw, &target) != nil {
		t.Fatal("native Stars target invalid")
	}
	client := vpn.NewPanelClient(f.cfg.VPN.Panel)
	defer client.Close()
	view, err := client.GetClient(context.Background(), target.PanelKey)
	expected := vpn.ProvisionTarget{PanelKey: target.PanelKey, VPNID: target.VPNID, SubID: target.SubID, ExpiryTimeMS: target.ExpiryTimeMS, DeviceCount: target.DeviceCount, TrafficLimitBytes: target.TrafficLimitBytes, Banned: target.Banned}
	if err != nil || !vpn.Matches(view, expected, time.Now()) || !reflect.DeepEqual(view.InboundIDs, target.InboundIDs) {
		t.Fatal("native Stars panel readback changed frozen identity/access")
	}
	return target
}

func TestNativeStarsRecurring(t *testing.T) {
	f, bot, key, plan := nativeStarsFixture(t)
	ctx := context.Background()
	var clock atomic.Int64
	clock.Store(time.Now().Unix())
	f.cfg.Accounts.Now = func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	runtime, stop := launchNative(t, f, bot, true, true, true)
	wait(t, runtime.StarsGateway().Ready)
	tg := int64(uuid.New().ID()) + 1000000000
	auth, first := nativeStarsOrder(t, f, key, tg, plan, true)
	bot.mu.Lock()
	periods := append([]int64(nil), bot.starsPeriods...)
	bot.mu.Unlock()
	if !reflect.DeepEqual(periods, []int64{2592000}) || first.Quote.StarsRecurring == nil || !*first.Quote.StarsRecurring {
		t.Fatal("native recurring invoice period/explicit quote")
	}
	proof := nativeStarsProof(tg, first.OrderId, "owned-native-first-recurring", f.cfg.Accounts.Now())
	proof.Recurring, proof.FirstRecurring, proof.SubscriptionExpiresAt = true, true, clock.Load()+2592000
	bot.preCheckout(proof)
	wait(t, func() bool {
		bot.mu.Lock()
		defer bot.mu.Unlock()
		return len(bot.starsPreChecks) == 1 && bot.starsPreChecks[0]
	})
	bot.starsMoney(proof, false)
	wait(t, func() bool {
		var applied bool
		return f.env.Pool.QueryRow(ctx, `SELECT fulfillment_status='applied' FROM purchase_orders WHERE id=$1`, first.OrderId).Scan(&applied) == nil && applied
	})
	firstTarget := assertNativeStarsPanel(t, f, first.OrderId)
	clock.Store(proof.SubscriptionExpiresAt)
	next := nativeStarsProof(tg, first.OrderId, "owned-native-next-recurring", f.cfg.Accounts.Now())
	next.Recurring, next.SubscriptionExpiresAt = true, proof.SubscriptionExpiresAt+2592000
	bot.starsMoney(next, false)
	var child uuid.UUID
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT cy.order_id FROM stars_subscription_cycles cy JOIN purchase_orders p ON p.id=cy.order_id WHERE cy.root_order_id=$1 AND cy.paid_until=$2 AND p.fulfillment_status='applied'`, first.OrderId, time.Unix(next.SubscriptionExpiresAt, 0).UTC()).Scan(&child) == nil
	})
	nextTarget := assertNativeStarsPanel(t, f, child)
	if child == first.OrderId || nextTarget.VPNID != firstTarget.VPNID || nextTarget.SubID != firstTarget.SubID || nextTarget.ExpiryTimeMS != firstTarget.ExpiryTimeMS+2592000000 {
		t.Fatal("native cycle changed identity or lost/doubled paid period")
	}
	bot.starsMoney(proof, false)
	bot.starsMoney(next, false)
	wait(t, func() bool { bot.mu.Lock(); defer bot.mu.Unlock(); return len(bot.updates) == 0 })
	var orders, receipts, cycles, accesses int
	if err := f.env.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM purchase_orders WHERE account_id=$1),(SELECT count(*) FROM purchase_receipts r JOIN purchase_orders p ON p.id=r.order_id WHERE p.account_id=$1),(SELECT count(*) FROM stars_subscription_cycles WHERE root_order_id=$2),(SELECT count(*) FROM access_operations WHERE account_id=$1)`, auth.Account.AccountId, first.OrderId).Scan(&orders, &receipts, &cycles, &accesses); err != nil || orders != 2 || receipts != 2 || cycles != 2 || accesses != 2 {
		t.Fatal("native cycle replay created extra money/access", err)
	}
	// The original Mini session expired when the owner clock crossed 30 days.
	status, body, _ := f.send(t, f.public.Client(), "POST", "/api/v1/telegram/mini-app/session", map[string]string{"init_data": nativeStarsInit(key, tg)}, "", "", false)
	if status != 200 || json.Unmarshal(body, &auth) != nil {
		t.Fatal("native renewed own session", status)
	}
	var state wire.StarsSubscription
	status, body, _ = f.send(t, f.public.Client(), "GET", "/api/v1/stars-subscription", nil, "", "", false, auth.SessionToken)
	if status != 200 || json.Unmarshal(body, &state) != nil || state.State != "active" || state.PaidUntil == nil || state.PaidUntil.Unix() != next.SubscriptionExpiresAt {
		t.Fatal("native current recurring period", status)
	}
	bot.mu.Lock()
	bot.sequence++
	bot.updates = append(bot.updates, map[string]any{"update_id": bot.sequence, "subscription": map[string]any{"user": map[string]any{"id": tg, "is_bot": false}, "invoice_payload": proof.Payload, "state": "failed"}})
	bot.mu.Unlock()
	wait(t, func() bool {
		var provider string
		return f.env.Pool.QueryRow(ctx, `SELECT provider_state FROM stars_subscriptions WHERE root_order_id=$1 AND canonical`, first.OrderId).Scan(&provider) == nil && provider == "failed"
	})
	status, body, _ = f.send(t, f.public.Client(), "GET", "/api/v1/stars-subscription", nil, "", "", false, auth.SessionToken)
	if status != 200 || json.Unmarshal(body, &state) != nil || state.State != "failed" || state.PaidUntil == nil || state.PaidUntil.Unix() != next.SubscriptionExpiresAt {
		t.Fatal("native provider status changed paid-period facts", status)
	}
	assertNativeStarsPanel(t, f, child)
	status, body, _ = f.send(t, f.public.Client(), "POST", "/api/v1/stars-subscription/control", map[string]any{"action": "cancel", "confirmed": true}, auth.CsrfToken, uuid.NewString(), false, auth.SessionToken)
	if status != 200 || json.Unmarshal(body, &state) != nil || state.State != "canceled" || state.ExternalBillingBlocked {
		t.Fatal("native proven cancellation", status)
	}
	bot.mu.Lock()
	edits := append([]nativeStarsEdit(nil), bot.starsEdits...)
	bot.mu.Unlock()
	if len(edits) != 1 || edits[0].Payer != tg || edits[0].Charge != proof.ChargeID || !edits[0].Canceled {
		t.Fatal("native cancel changed captured first identity")
	}
	assertNativeStarsPanel(t, f, child)
	stop()
	// Restart the current modules/native poll/stdlib schedulers with the same DB.
	queue, err := river.NewClient(riverpgxv5.New(f.env.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var handler http.Handler
	f.public = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(f.public.Close)
	f.cfg.HTTP.CabinetOrigin = f.public.URL
	f.svc = app.NewModules(f.env.Pool, f.env.Redis, queue, &f.cfg)
	f.svc.MiniApp = telegram.NewMiniApp(123456789, key.Public().(ed25519.PublicKey), f.svc.Accounts, time.Now)
	handler = httpapi.New(f.svc, f.env.Pool, f.cfg.HTTP)
	runtime, stop = launchNative(t, f, bot, true, true, true)
	wait(t, runtime.StarsGateway().Ready)
	status, body, _ = f.send(t, f.public.Client(), "GET", "/api/v1/stars-subscription", nil, "", "", false, auth.SessionToken)
	if status != 200 || json.Unmarshal(body, &state) != nil || state.State != "canceled" || state.ExternalBillingBlocked || state.PaidUntil == nil || state.PaidUntil.Unix() != next.SubscriptionExpiresAt {
		t.Fatal("native restart lost cancel/period proof", status)
	}
	// Telegram can deliver another genuine first charge on the original payload.
	extra := proof
	extra.ChargeID = "owned-native-extra-first"
	bot.starsMoney(extra, false)
	wait(t, func() bool {
		var count int
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM stars_subscriptions WHERE root_order_id=$1`, first.OrderId).Scan(&count) == nil && count == 2
	})
	if err = f.svc.Payments.ReconcileStarsSubscriptions(ctx); err != nil {
		t.Fatal("native extra cancellation", err)
	}
	var canceled, grants int
	if err = f.env.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM stars_subscriptions WHERE root_order_id=$1 AND bot_canceled AND control_state='confirmed'),(SELECT count(*) FROM access_operations WHERE account_id=$2)`, first.OrderId, auth.Account.AccountId).Scan(&canceled, &grants); err != nil || canceled != 2 || grants != 2 {
		t.Fatal("native extra identity was not individually canceled", err)
	}
	bot.mu.Lock()
	known := map[string]bool{}
	for _, edit := range bot.starsEdits {
		if edit.Payer != tg || !edit.Canceled {
			bot.mu.Unlock()
			t.Fatal("native extra cancel changed payer/action")
		}
		known[edit.Charge] = true
	}
	bot.mu.Unlock()
	if !known[proof.ChargeID] || !known[extra.ChargeID] || len(known) != 2 {
		t.Fatal("native extra first charge cancellation proof missing")
	}
	assertNativeStarsPanel(t, f, child)
	stop()
}

// Catches ACK without durable money, losing queued funding on restart, a second
// identity after a panel write/local commit failure, and early negative funding.
func TestNativeTrialStarsRestartAndRefund(t *testing.T) {
	f, bot, key, plan := nativeStarsFixture(t)
	ctx := context.Background()
	runtime, stop := launchNative(t, f, bot, true, false, true)
	wait(t, runtime.StarsGateway().Ready)
	tg := int64(uuid.New().ID()) + 1000000000
	auth, order := nativeStarsOrder(t, f, key, tg, plan)
	proof := nativeStarsProof(tg, order.OrderId, "owned-native-charge", time.Now())
	bot.preCheckout(proof)
	wait(t, func() bool {
		bot.mu.Lock()
		defer bot.mu.Unlock()
		return len(bot.starsPreChecks) == 1 && bot.starsPreChecks[0]
	})
	bot.starsMoney(proof, false)
	wait(t, func() bool {
		var count int
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM purchase_receipts WHERE order_id=$1`, order.OrderId).Scan(&count) == nil && count == 1
	})
	earlyAuth, early := nativeStarsOrder(t, f, key, tg+1, plan)
	negative := nativeStarsProof(tg+1, early.OrderId, "owned-native-early-refund", time.Now())
	bot.starsMoney(negative, true)
	bot.starsMoney(negative, false)
	wait(t, func() bool {
		var count int
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM purchase_refunds WHERE order_id=$1`, early.OrderId).Scan(&count) == nil && count == 1
	})
	retiredAuth, retired := nativeStarsOrder(t, f, key, tg+2, plan)
	retiredProof := nativeStarsProof(tg+2, retired.OrderId, "owned-native-prepared-refund", time.Now())
	bot.starsMoney(retiredProof, false)
	wait(t, func() bool {
		var count int
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM purchase_receipts WHERE order_id=$1`, retired.OrderId).Scan(&count) == nil && count == 1
	})
	if err := f.svc.Payments.FulfillPurchase(ctx, retired.OrderId); err != nil {
		t.Fatal("native refunded access preparation", err)
	}
	var retiredEvidence string
	if err := f.env.Pool.QueryRow(ctx, `SELECT a.target::text||':'||a.completed_steps::text FROM access_operations a JOIN purchase_orders p ON p.access_operation_id=a.id WHERE p.id=$1 AND a.status='pending'`, retired.OrderId).Scan(&retiredEvidence); err != nil {
		t.Fatal("native pending target missing", err)
	}
	bot.starsMoney(retiredProof, true)
	wait(t, func() bool {
		var done bool
		return f.env.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM purchase_orders p JOIN access_operations a ON a.id=p.access_operation_id JOIN purchase_refunds r ON r.order_id=p.id WHERE p.id=$1 AND a.status='skipped')`, retired.OrderId).Scan(&done) == nil && done
	})
	var before string
	if err := f.env.Pool.QueryRow(ctx, `SELECT vpn_id::text||':'||sub_id||':'||panel_key FROM accounts WHERE id=$1`, auth.Account.AccountId).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var queued bool
	if err := f.env.Pool.QueryRow(ctx, `SELECT p.fulfillment_status='queued' AND p.access_operation_id IS NULL AND (SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=p.id::text)=1 FROM purchase_orders p WHERE p.id=$1`, order.OrderId).Scan(&queued); err != nil || !queued {
		t.Fatal("native Stars money/job not durable before restart", err)
	}
	stop()
	if _, err := f.env.Pool.Exec(ctx, `CREATE FUNCTION fail_stars_apply() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='purchase' AND NEW.status='applied' THEN RAISE EXCEPTION 'owned Stars commit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_stars_apply BEFORE UPDATE ON access_operations FOR EACH ROW EXECUTE FUNCTION fail_stars_apply()`); err != nil {
		t.Fatal(err)
	}
	queue, err := river.NewClient(riverpgxv5.New(f.env.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var handler http.Handler
	f.public = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(f.public.Close)
	f.cfg.HTTP.CabinetOrigin = f.public.URL
	f.svc = app.NewModules(f.env.Pool, f.env.Redis, queue, &f.cfg)
	f.svc.MiniApp = telegram.NewMiniApp(123456789, key.Public().(ed25519.PublicKey), f.svc.Accounts, time.Now)
	handler = httpapi.New(f.svc, f.env.Pool, f.cfg.HTTP)
	launchNative(t, f, bot, false, true)
	if err := f.svc.Payments.RecordStarsRefund(ctx, retiredProof); err != nil {
		t.Fatal("native retired refund replay after restart", err)
	}
	var retainedEvidence string
	if err := f.env.Pool.QueryRow(ctx, `SELECT a.target::text||':'||a.completed_steps::text FROM access_operations a JOIN purchase_orders p ON p.access_operation_id=a.id WHERE p.id=$1 AND a.status='skipped'`, retired.OrderId).Scan(&retainedEvidence); err != nil || retainedEvidence != retiredEvidence {
		t.Fatal("native refund lost frozen access evidence", err)
	}
	if unresolved, err := f.svc.VPN.UnresolvedTx(ctx, nil, retiredAuth.Account.AccountId); err != nil || unresolved {
		t.Fatal("native provider refund kept account blocked after restart", err)
	}
	wait(t, func() bool {
		var state string
		return f.env.Pool.QueryRow(ctx, `SELECT fulfillment_status FROM purchase_orders WHERE id=$1`, order.OrderId).Scan(&state) == nil && state == "needs_review"
	})
	target := assertNativeStarsPanel(t, f, order.OrderId)
	if err = f.svc.Payments.RecordStarsPayment(ctx, proof); err != nil {
		t.Fatal("native Stars duplicate after restart", err)
	}
	if _, err = f.env.Pool.Exec(ctx, `DROP TRIGGER fail_stars_apply ON access_operations; DROP FUNCTION fail_stars_apply()`); err != nil {
		t.Fatal(err)
	}
	_, _, operatorTrial := f.signup(t, nativeEmail("stars-recovery-operator"))
	var actor uuid.UUID
	if err = f.env.Pool.QueryRow(ctx, `SELECT account_id FROM trial_requests WHERE id=$1`, operatorTrial.RequestId).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Subscriptions.ReconcileAccessOperation(ctx, actor, auth.Account.AccountId, target.OperationID, uuid.New(), subscriptions.AccessReconcileInput{Reason: "Owned Stars unchanged target recovery"}); err != nil {
		t.Fatal("native Stars recovery", err)
	}
	wait(t, func() bool {
		var state string
		return f.env.Pool.QueryRow(ctx, `SELECT fulfillment_status FROM purchase_orders WHERE id=$1`, order.OrderId).Scan(&state) == nil && state == "applied"
	})
	if restored := assertNativeStarsPanel(t, f, order.OrderId); !reflect.DeepEqual(target, restored) {
		t.Fatal("native Stars recovery rewrote target")
	}
	if err = f.svc.Payments.RecordStarsRefund(ctx, proof); err != nil {
		t.Fatal("native Stars matching refund", err)
	}
	assertNativeStarsPanel(t, f, order.OrderId)
	var after string
	var receipts, operations, grants, earlyOperations int
	var retained bool
	err = f.env.Pool.QueryRow(ctx, `SELECT vpn_id::text||':'||sub_id||':'||panel_key,(SELECT count(*) FROM purchase_receipts WHERE order_id=$2),(SELECT count(*) FROM access_operations WHERE purchase_order_id=$2),(SELECT count(*) FROM trial_grants WHERE account_id=$1),(SELECT count(*) FROM access_operations WHERE account_id=$3),EXISTS(SELECT 1 FROM purchase_orders p JOIN purchase_refunds r ON r.order_id=p.id WHERE p.id=$2 AND p.fulfillment_status='applied' AND r.source='telegram' AND r.operator_account_id IS NULL AND r.returned_amount='100' AND r.returned_currency='XTR') FROM accounts WHERE id=$1`, auth.Account.AccountId, order.OrderId, earlyAuth.Account.AccountId).Scan(&after, &receipts, &operations, &grants, &earlyOperations, &retained)
	if err != nil || after != before || receipts != 1 || operations != 1 || grants != 0 || earlyOperations != 0 || !retained {
		t.Fatal("native Stars duplicated access or lost honest refund/identity", err)
	}
	status, _, response := f.send(t, f.public.Client(), "GET", "/api/v1/subscription/key", nil, "", "", false, auth.SessionToken)
	if status != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("native Stars current connection unavailable", status)
	}
}

// Only Telegram SDK/transport are stubbed: actual browser calls the current Go
// graph, its durable jobs write the owned TLS panel, and the SDK is not proof.
func TestNativeTrialStarsBrowser(t *testing.T) {
	nativeStarsBrowser(t, false)
}

func TestNativeStarsRecurringBrowser(t *testing.T) {
	nativeStarsBrowser(t, true)
}

func nativeStarsBrowser(t *testing.T, recurring bool) {
	if os.Getenv("RUN_BROWSER_TESTS") != "1" {
		t.Skip("requires RUN_BROWSER_TESTS=1")
	}
	f, bot, key, _ := nativeStarsFixture(t)
	runtime, _ := launchNative(t, f, bot, true, true, true)
	wait(t, runtime.StarsGateway().Ready)
	tg := int64(uuid.New().ID()) + 1000000000
	control := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/complete" {
			http.NotFound(w, r)
			return
		}
		bot.mu.Lock()
		payload := ""
		if len(bot.starsInvoices) == 1 {
			payload = bot.starsInvoices[0]
		}
		bot.mu.Unlock()
		id, err := uuid.Parse(strings.TrimPrefix(payload, "stars:v1:"))
		if err != nil {
			http.Error(w, "fixture invoice unavailable", 409)
			return
		}
		proof := nativeStarsProof(tg, id, "owned-browser-stars-charge", time.Now())
		if recurring {
			proof.Recurring, proof.FirstRecurring, proof.SubscriptionExpiresAt = true, true, proof.At.Unix()+2592000
		}
		bot.starsMoney(proof, false)
		w.WriteHeader(204)
	}))
	t.Cleanup(control.Close)
	file := filepath.Join(t.TempDir(), "stars-init.json")
	data, _ := json.Marshal(map[string]any{"init_data": nativeStarsInit(key, tg), "control_url": control.URL, "recurring": recurring})
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("npm", "run", "test:e2e", "--", "--grep", "Telegram Stars real")
	cmd.Dir = filepath.Join(f.root, "web")
	cmd.Env = append(os.Environ(), "E2E_MODE=real", "TEST_ORIGIN="+f.public.URL, "TEST_STARS_INIT_FILE="+file)
	if out, err := cmd.CombinedOutput(); err != nil {
		var payment, fulfillment, orderReason, access, accessReason string
		if e := f.env.Pool.QueryRow(context.Background(), `SELECT p.payment_status,p.fulfillment_status,COALESCE(p.review_reason,''),COALESCE(a.status,''),COALESCE(a.review_reason,'') FROM purchase_orders p JOIN accounts c ON c.id=p.account_id LEFT JOIN access_operations a ON a.id=p.access_operation_id WHERE c.telegram_id=$1`, tg).Scan(&payment, &fulfillment, &orderReason, &access, &accessReason); e == nil {
			t.Logf("owned Stars checkpoint payment=%s fulfillment=%s order_reason=%s access=%s access_reason=%s", payment, fulfillment, orderReason, access, accessReason)
		}
		t.Fatalf("owned Stars browser failed: %s", out)
	}
	var order uuid.UUID
	var count int
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT p.id,(SELECT count(*) FROM access_operations WHERE purchase_order_id=p.id) FROM purchase_orders p JOIN accounts a ON a.id=p.account_id WHERE a.telegram_id=$1 AND p.payment_method='telegram_stars' AND p.fulfillment_status='applied'`, tg).Scan(&order, &count); err != nil || count != 1 {
		t.Fatal("native Stars browser did not grant once", err)
	}
	assertNativeStarsPanel(t, f, order)
	if recurring {
		var enabled bool
		if err := f.env.Pool.QueryRow(context.Background(), `SELECT desired_action='resume' AND control_state='confirmed' AND NOT bot_canceled FROM stars_subscriptions WHERE root_order_id=$1`, order).Scan(&enabled); err != nil || !enabled {
			t.Fatal("native browser did not retain confirmed renewal permission", err)
		}
		bot.mu.Lock()
		periods := append([]int64(nil), bot.starsPeriods...)
		edits := append([]nativeStarsEdit(nil), bot.starsEdits...)
		bot.mu.Unlock()
		if !reflect.DeepEqual(periods, []int64{2592000}) || len(edits) != 2 || !edits[0].Canceled || edits[1].Canceled || edits[0].Payer != tg || edits[1].Payer != tg || edits[0].Charge != edits[1].Charge {
			t.Fatal("native browser recurring period/control proof")
		}
	}
}
