package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"

	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func starsSubscriptionFixture(t *testing.T) (http.Handler, *regressionFixture, *testkit.Env, wire.MiniAppSessionResult, *app.Modules, payments.PurchaseOrder) {
	t.Helper()
	_, s, e, auth, plan := starsHTTPFixture(t)
	modules := app.NewModules(e.Pool, e.Redis, s.queue, s.cfg)
	modules.Payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) {
		return "https://t.me/$owned_invoice", nil
	}, Refund: func(context.Context, int64, string) error { return nil }})
	s.payments = modules.Payments
	s.vpn = modules.VPN
	h := New(modules, e.Pool, s.cfg.HTTP)
	panelFixture(t, s)
	order, err := s.payments.CreatePurchaseOrder(context.Background(), auth.Account.AccountId, uuid.New(), payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "telegram_stars", PaymentType: "STARS", PeriodDays: 30, PlanId: plan, Revision: 1, StarsRecurring: true})
	if err != nil {
		t.Fatal("recurring fixture", err)
	}
	in := starsPayment(order.OrderId, e.Clock())
	in.Recurring = true
	in.FirstRecurring = true
	in.SubscriptionExpiresAt = in.At.Add(30 * 24 * time.Hour).Unix()
	if err = s.payments.RecordStarsPayment(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if err = s.payments.FulfillPurchase(context.Background(), order.OrderId); err != nil {
		t.Fatal(err)
	}
	got, err := s.payments.PurchaseOrder(context.Background(), auth.Account.AccountId, order.OrderId)
	if err != nil || got.AccessOperationId == nil {
		t.Fatal("fixture access", err)
	}
	if err = s.vpn.ApplyAccess(context.Background(), *got.AccessOperationId); err != nil {
		t.Fatal(err)
	}
	return h, s, e, auth, modules, got
}
func starsSubscriptionState(t *testing.T, h http.Handler, s *regressionFixture, auth wire.MiniAppSessionResult) payments.StarsSubscription {
	t.Helper()
	r := starsRequest(h, s, auth, "GET", "/api/v1/stars-subscription", "", uuid.Nil)
	var out payments.StarsSubscription
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil {
		t.Fatal("own recurring state", r.Code)
	}
	return out
}

// Catches native failure/SDK state being mistaken for cancellation and paid access being erased.
func TestStarsSubscriptionControl(t *testing.T) {
	for _, mode := range []string{"confirmed", "uncertain", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			h, s, e, auth, _, order := starsSubscriptionFixture(t)
			calls := 0
			s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) { return "", nil }, Refund: func(context.Context, int64, string) error { return nil }, EditSubscription: func(_ context.Context, payer int64, charge string, cancel bool) error {
				calls++
				if payer != 701 || charge != "owned-stars-charge" {
					t.Fatal("cancellation lost original billing identity")
				}
				if mode == "uncertain" && calls == 1 {
					return errors.New("owned response loss")
				}
				if mode == "rejected" {
					return &payments.Error{Status: 409, Code: "STARS_CONTROL_REJECTED"}
				}
				return nil
			}})
			before := starsSubscriptionState(t, h, s, auth)
			if before.State != "active" || !before.CanCancel || before.CanResume || !before.ExternalBillingBlocked || before.PaidUntil == nil {
				t.Fatal("unpaid/active recurrence misrepresented")
			}
			key := uuid.New()
			r := starsRequest(h, s, auth, "POST", "/api/v1/stars-subscription/control", `{"action":"cancel","confirmed":true}`, key)
			if r.Code != 200 {
				t.Fatal("own cancel refused", r.Code)
			}
			state := starsSubscriptionState(t, h, s, auth)
			if state.ControlState == nil || *state.ControlState != mode || calls != 1 {
				t.Fatal("native result misrepresented")
			}
			if mode == "confirmed" {
				if state.State != "canceled" || state.ExternalBillingBlocked || !state.CanResume {
					t.Fatal("actual cancellation missing")
				}
				r = starsRequest(h, s, auth, "POST", "/api/v1/stars-subscription/control", `{"action":"cancel","confirmed":true}`, key)
				if r.Code != 200 || calls != 1 {
					t.Fatal("cancel replay repeated a confirmed setter")
				}
				r = starsRequest(h, s, auth, "POST", "/api/v1/stars-subscription/control", `{"action":"resume","confirmed":true}`, uuid.New())
				if r.Code != 200 {
					t.Fatal("resume refused", r.Code)
				}
				state = starsSubscriptionState(t, h, s, auth)
				if state.State != "resume_allowed" || !state.ExternalBillingBlocked || calls != 2 {
					t.Fatal("resume success forged provider active")
				}
			} else if !state.ExternalBillingBlocked || state.State == "canceled" || state.CanResume {
				t.Fatal("unproved cancellation opened billing")
			}
			if mode == "uncertain" {
				r = starsRequest(h, s, auth, "POST", "/api/v1/stars-subscription/control", `{"action":"cancel","confirmed":true}`, key)
				if r.Code != 200 || calls != 2 {
					t.Fatal("latest uncertain setter cannot recover")
				}
				state = starsSubscriptionState(t, h, s, auth)
				if state.State != "canceled" || state.ExternalBillingBlocked {
					t.Fatal("recovered native proof missing")
				}
			}
			var same bool
			if err := e.Pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM access_operations WHERE id=$1 AND status='applied' AND account_id=$2)`, *order.AccessOperationId, auth.Account.AccountId).Scan(&same); err != nil || !same {
				t.Fatal("native control erased paid access", err)
			}
		})
	}
}

// Catches accepting raw payout coordinates, wrong sessions/CSRF or unconfirmed control.
func TestStarsSubscriptionControlAuthority(t *testing.T) {
	h, s, e, auth, _, order := starsSubscriptionFixture(t)
	calls := 0
	s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) { return "", nil }, Refund: func(context.Context, int64, string) error { return nil }, EditSubscription: func(context.Context, int64, string, bool) error { calls++; return nil }})
	for _, in := range []struct {
		body string
		key  uuid.UUID
		csrf bool
		want int
	}{
		{`{"action":"cancel","confirmed":false}`, uuid.New(), true, 400},
		{`{"action":"cancel","confirmed":true,"payer_id":702}`, uuid.New(), true, 400},
		{`{"action":"cancel","confirmed":true}`, uuid.Nil, true, 400},
		{`{"action":"cancel","confirmed":true}`, uuid.New(), false, 403},
	} {
		session := auth
		if !in.csrf {
			session.CsrfToken = ""
		}
		r := starsRequest(h, s, session, "POST", "/api/v1/stars-subscription/control", in.body, in.key)
		if r.Code != in.want {
			t.Fatal("unsafe control status", r.Code, in.want)
		}
	}
	other := supportLogin(t, h, e, *s.cfg, "other-subscription@example.test")
	r := supportRequest(h, &other, "GET", "/api/v1/stars-subscription", "", nil, s.cfg.HTTP.CabinetOrigin, uuid.Nil)
	var state payments.StarsSubscription
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &state) != nil || state.OrderId != nil || state.State != "none" {
		t.Fatal("foreign own account saw native order", r.Code)
	}
	if calls != 0 {
		t.Fatal("unsafe control reached native billing")
	}
	if _, err := e.Pool.Exec(context.Background(), `UPDATE accounts SET legacy_user_id=991 WHERE id=$1`, auth.Account.AccountId); err != nil {
		t.Fatal(err)
	}
	state = starsSubscriptionState(t, h, s, auth)
	if state.State != "legacy_unknown" || !state.ExternalBillingBlocked || state.CanResume {
		t.Fatal("unknown legacy billing opened")
	}
	_ = order
}

// Catches an old successful resume overwriting a later required cancel under real account locks.
func TestStarsSubscriptionAuthorityRace(t *testing.T) {
	h, s, e, auth, modules, _ := starsSubscriptionFixture(t)
	ctx := context.Background()
	actor := verified(t, s, e, "recurring-race-operator@example.test")
	if err := modules.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	started, finish := make(chan struct{}), make(chan struct{})
	s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) { return "", nil }, Refund: func(context.Context, int64, string) error { return nil }, EditSubscription: func(_ context.Context, payer int64, charge string, cancel bool) error {
		if payer != 701 || charge != "owned-stars-charge" {
			t.Error("policy cancellation lost captured payer")
		}
		if !cancel {
			close(started)
			<-finish
		}
		return nil
	}})
	if r := starsRequest(h, s, auth, "POST", "/api/v1/stars-subscription/control", `{"action":"cancel","confirmed":true}`, uuid.New()); r.Code != 200 {
		t.Fatal(r.Code)
	}
	result := make(chan int, 1)
	go func() {
		r := starsRequest(h, s, auth, "POST", "/api/v1/stars-subscription/control", `{"action":"resume","confirmed":true}`, uuid.New())
		result <- r.Code
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("native resume not started")
	}
	if _, err := modules.Accounts.SetOperatorRestriction(ctx, actor, auth.Account.AccountId, uuid.New(), accounts.OperatorRestrictionInput{Restricted: true, Reason: "Owned race restriction"}); err != nil {
		close(finish)
		t.Fatal(err)
	}
	close(finish)
	select {
	case status := <-result:
		if status != 200 {
			t.Fatal("accepted native result lost", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native resume failed to finish")
	}
	var desired, control string
	var blocked bool
	if err := e.Pool.QueryRow(ctx, `SELECT desired_action,control_state,NOT bot_canceled FROM stars_subscriptions WHERE root_order_id=(SELECT id FROM purchase_orders WHERE account_id=$1 AND action='purchase') AND canonical`, auth.Account.AccountId).Scan(&desired, &control, &blocked); err != nil || desired != "cancel" || control == "confirmed" {
		t.Fatal("old resume overwrote required cancel", err)
	}
	type reconcile interface{ ReconcileStarsSubscriptions(context.Context) error }
	service, ok := any(s.payments).(reconcile)
	if !ok {
		t.Fatal("required cancellation scheduler missing")
	}
	if err := service.ReconcileStarsSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Pool.QueryRow(ctx, `SELECT bot_canceled FROM stars_subscriptions WHERE root_order_id=(SELECT id FROM purchase_orders WHERE account_id=$1 AND action='purchase') AND canonical`, auth.Account.AccountId).Scan(&blocked); err != nil || !blocked {
		t.Fatal("required cancel proof missing", err)
	}
}

// Catches elapsed time granting access or a provider error killing the process loop.
func TestStarsSubscriptionScheduler(t *testing.T) {
	h, s, e, auth, _, order := starsSubscriptionFixture(t)
	ctx := context.Background()
	type scheduler interface {
		ReconcileStarsSubscriptions(context.Context) error
		RunStarsSubscriptionScheduler(context.Context) error
	}
	owner, ok := any(s.payments).(scheduler)
	if !ok {
		t.Fatal("Stars lapse scheduler missing")
	}
	paidUntil := starsPayment(order.OrderId, e.Clock()).At.Add(30 * 24 * time.Hour)
	current := paidUntil.Add(24 * time.Hour)
	s.now = func() time.Time { return current }
	state, readErr := s.payments.StarsSubscription(ctx, auth.Account.AccountId)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if state.PeriodPhase != "grace" {
		t.Fatal("24h grace boundary")
	}
	calls := 0
	s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) { return "", nil }, Refund: func(context.Context, int64, string) error { return nil }, EditSubscription: func(context.Context, int64, string, bool) error {
		calls++
		return errors.New("owned provider unavailable")
	}})
	if err := owner.ReconcileStarsSubscriptions(ctx); err != nil {
		t.Fatal("grace reconciliation", err)
	}
	if calls != 0 {
		t.Fatal("cancel before grace ended")
	}
	current = current.Add(time.Second)
	if err := owner.ReconcileStarsSubscriptions(ctx); err == nil {
		t.Fatal("failed native lapse cancellation not reported")
	}
	state, readErr = s.payments.StarsSubscription(ctx, auth.Account.AccountId)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if state.PeriodPhase != "lapsed" || state.ControlState == nil || *state.ControlState != "uncertain" || !state.ExternalBillingBlocked || calls != 1 {
		t.Fatal("lapse/payment/native proof confused")
	}
	stop, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancel()
	if err := owner.RunStarsSubscriptionScheduler(stop); err != nil {
		t.Fatal("ordinary provider error killed scheduler", err)
	}
	if r := starsRequest(h, s, auth, "GET", "/healthz", "", uuid.Nil); r.Code != 200 {
		t.Fatal("native failure killed HTTP", r.Code)
	}
}

// Catches status replay changing authoritative state and native user cancel being treated as bot proof.
func TestStarsSubscriptionUpdates(t *testing.T) {
	h, s, _, auth, _, order := starsSubscriptionFixture(t)
	update := func(id int64, state string) map[string]any {
		return map[string]any{"update_id": id, "subscription": map[string]any{"user": map[string]any{"id": 701, "is_bot": false}, "invoice_payload": "stars:v1:" + order.OrderId.String(), "state": state}}
	}
	if err := starsRunUpdates(t, s, []map[string]any{update(100, "canceled")}); err != nil {
		t.Fatal(err)
	}
	state := starsSubscriptionState(t, h, s, auth)
	if state.ProviderState == nil || *state.ProviderState != "canceled" || !state.ExternalBillingBlocked || state.State == "canceled" {
		t.Fatal("user cancel forged bot proof")
	}
	if err := starsRunUpdates(t, s, []map[string]any{update(2, "active")}); err != nil {
		t.Fatal(err)
	}
	state = starsSubscriptionState(t, h, s, auth)
	if state.ProviderState == nil || *state.ProviderState != "active" {
		t.Fatal("smaller first-seen update rejected")
	}
	if err := starsRunUpdates(t, s, []map[string]any{update(100, "canceled")}); err != nil {
		t.Fatal(err)
	}
	state = starsSubscriptionState(t, h, s, auth)
	if state.ProviderState == nil || *state.ProviderState != "active" {
		t.Fatal("replay overwrote later first-seen state")
	}
}

func starsWebLogin(t *testing.T, h http.Handler, s *regressionFixture, e *testkit.Env, auth wire.MiniAppSessionResult, m *app.Modules) supportSession {
	t.Helper()
	ctx := context.Background()
	proof, err := m.Accounts.RequestInitialEmail(ctx, auth.SessionToken, accounts.InitialEmailInput{Email: "recurring-web@example.test"}, "127.0.0.1")
	if err != nil {
		t.Fatal("owned email request", err)
	}
	_, _, code := testkit.CredentialMailSecrets(t, e.Pool, s.cfg.Mail.MailKey, proof.ChallengeId)
	_, err = m.Accounts.CompleteInitialEmail(ctx, auth.SessionToken, accounts.InitialEmailCompleteInput{ChallengeId: proof.ChallengeId, Code: code, NewPassword: "long safe password", AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"}, "127.0.0.1")
	if err != nil {
		t.Fatal("owned email grant", err)
	}
	r := request(h, "POST", "/api/v1/auth/login", `{"email":"recurring-web@example.test","password":"long safe password"}`, s.cfg.HTTP.CabinetOrigin)
	var login wire.LoginResult
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &login) != nil || len(r.Result().Cookies()) != 1 {
		t.Fatal("owned web login", r.Code)
	}
	return supportSession{login.Account.AccountId, r.Result().Cookies()[0], login.CsrfToken}
}
func starsSetter(s *regressionFixture, edit func(context.Context, int64, string, bool) error) {
	s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) { return "", nil }, Refund: func(context.Context, int64, string) error { return nil }, EditSubscription: edit})
}

// Catches unlink/external billing opening before proof or staying closed after all native cancellations.
func TestStarsSubscriptionBillingHandoff(t *testing.T) {
	h, s, e, auth, m, order := starsSubscriptionFixture(t)
	ctx := context.Background()
	session := starsWebLogin(t, h, s, e, auth, m)
	calls := 0
	starsSetter(s, func(context.Context, int64, string, bool) error { calls++; return nil })
	identity, err := m.Accounts.GetIdentity(ctx, session.id)
	if err != nil || identity.CanUnlink {
		t.Fatal("active recurrence offered unlink", err)
	}
	if _, err = m.Accounts.UnlinkTelegram(ctx, session.cookie.Value, accounts.CurrentPasswordInput{CurrentPassword: "long safe password"}, "127.0.0.2"); err == nil {
		t.Fatal("active recurrence allowed unlink")
	}
	terms := catalogueTerms(2)
	terms.Prices[0].AmountMinor = "10000"
	terms.Prices[2].AmountMinor = "100"
	raw, _ := json.Marshal(terms)
	if _, err = e.Pool.Exec(ctx, `INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,2,$2,false,'legacy_import',$3);`, order.Quote.PlanId, raw, e.Clock()); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE catalogue_plans SET current_revision=2 WHERE id=$1`, order.Quote.PlanId); err != nil {
		t.Fatal(err)
	}
	s.cfg.Payments.ManualEnabled = true
	s.cfg.Payments.ManualCardDetails = "Owned transfer instructions"
	in := payments.PurchaseOrderInput{Action: "renew", PaymentMethod: "manual", PaymentType: "MANUAL", PeriodDays: 30, PlanId: order.Quote.PlanId, Revision: 2}
	if _, err = s.payments.CreatePurchaseOrder(ctx, session.id, uuid.New(), in); err == nil || err.Error() != "EXTERNAL_BILLING_UNVERIFIED" {
		t.Fatal("active recurrence allowed external checkout", err)
	}
	r := supportRequest(h, &session, "POST", "/api/v1/stars-subscription/control", "application/json", []byte(`{"action":"cancel","confirmed":true}`), s.cfg.HTTP.CabinetOrigin, uuid.New())
	if r.Code != 200 || calls != 1 {
		t.Fatal("web cancellation unavailable", r.Code)
	}
	if _, err = s.payments.RenewalOffer(ctx, session.id); err != nil {
		t.Fatal("proved cancellation kept external offer closed", err)
	}
	if _, err = s.payments.PlanChangeContext(ctx, session.id); err != nil {
		t.Fatal("proved cancellation kept plan change closed", err)
	}
	purchase, err := s.payments.CreatePurchaseOrder(ctx, session.id, uuid.New(), in)
	if err != nil {
		t.Fatal("proved cancellation kept checkout closed", err)
	}
	state, err := s.payments.StarsSubscription(ctx, session.id)
	if err != nil || state.CanResume {
		t.Fatal("pending external checkout allowed resume", err)
	}
	if _, err = s.payments.ControlStarsSubscription(ctx, session.id, uuid.New(), payments.StarsSubscriptionControlInput{Action: "resume", Confirmed: true}); err == nil || calls != 1 {
		t.Fatal("unsafe resume reached Telegram")
	}
	if _, err = s.payments.CancelPurchaseOrder(ctx, session.id, purchase.OrderId, uuid.New()); err != nil {
		t.Fatal(err)
	}
	identity, err = m.Accounts.GetIdentity(ctx, session.id)
	if err != nil || !identity.CanUnlink {
		t.Fatal("proved cancellation kept unlink closed", err)
	}
	if _, err = m.Accounts.UnlinkTelegram(ctx, session.cookie.Value, accounts.CurrentPasswordInput{CurrentPassword: "long safe password"}, "127.0.0.3"); err != nil {
		t.Fatal("proved cancellation could not unlink", err)
	}
	var captured bool
	if err = e.Pool.QueryRow(ctx, `SELECT payer_id=701 AND first_charge_id='owned-stars-charge' FROM stars_subscriptions WHERE root_order_id=$1`, order.OrderId).Scan(&captured); err != nil || !captured {
		t.Fatal("unlink erased billing identity", err)
	}
}

// Catches marking an unknown reserved checkout safe merely because its local order expired.
func TestStarsSubscriptionReservedHandoff(t *testing.T) {
	h, s, e, auth, plan := starsHTTPFixture(t)
	ctx := context.Background()
	m := app.NewModules(e.Pool, e.Redis, s.queue, s.cfg)
	s.payments = m.Payments
	h = New(m, e.Pool, s.cfg.HTTP)
	starsSetter(s, func(context.Context, int64, string, bool) error { return nil })
	order, err := s.payments.CreatePurchaseOrder(ctx, auth.Account.AccountId, uuid.New(), payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "telegram_stars", PaymentType: "STARS", PeriodDays: 30, PlanId: plan, Revision: 1, StarsRecurring: true})
	if err != nil {
		t.Fatal(err)
	}
	in := payments.StarsPreCheckoutInput{BotID: 123, PayerID: 701, Amount: 100, Currency: "XTR", Payload: "stars:v1:" + order.OrderId.String(), QueryID: "owned-reserved-start"}
	if ok, err := s.payments.CheckStarsPreCheckout(ctx, in); err != nil || !ok {
		t.Fatal("owned reservation", err)
	}
	session := starsWebLogin(t, h, s, e, auth, m)
	if _, err = s.payments.CancelPurchaseOrder(ctx, session.id, order.OrderId, uuid.New()); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return e.Clock().Add(32 * 24 * time.Hour) }
	state, err := s.payments.StarsSubscription(ctx, session.id)
	if err != nil || !state.ExternalBillingBlocked || state.State != "unknown" {
		t.Fatal("local cancel/time proved unknown money", err)
	}
	identity, err := m.Accounts.GetIdentity(ctx, session.id)
	if err != nil || identity.CanUnlink {
		t.Fatal("unknown reservation allowed unlink", err)
	}
}

// Catches policy state committing without its required cancellation intent.
func TestStarsSubscriptionPolicyHooks(t *testing.T) {
	for _, kind := range []string{"metadata-ban", "metadata-unlimited", "assign_plan", "starter_trial", "set_profile", "set_vpn_ban", "reset_traffic", "quarantine"} {
		t.Run(kind, func(t *testing.T) {
			h, s, e, auth, m, order := starsSubscriptionFixture(t)
			ctx := context.Background()
			actor := supportLogin(t, h, e, *s.cfg, "recurring-policy-operator@example.test")
			if err := m.Accounts.ChangeOperatorRole(ctx, actor.id, true); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "metadata-ban", "metadata-unlimited":
				tx, err := e.Pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if _, err = m.Accounts.Lock(ctx, tx, auth.Account.AccountId); err != nil {
					t.Fatal(err)
				}
				profile := "regular"
				if kind == "metadata-unlimited" {
					profile = "unlimited"
				}
				if err = m.Accounts.SetAccessMetadata(ctx, tx, auth.Account.AccountId, profile, kind == "metadata-ban"); err != nil {
					t.Fatal(err)
				}
				if tx.Commit(ctx) != nil {
					t.Fatal("owned metadata transaction")
				}
			case "quarantine":
				proof, err := m.Accounts.RequestOperatorRecovery(ctx, actor.cookie.Value, actor.id, auth.Account.AccountId, uuid.NewString(), accounts.OperatorRecoveryInput{Email: "recovered-recurring@example.test", CurrentPassword: "long safe password", Reason: "Owned support recovery", Confirmed: true}, "127.0.0.1")
				if err != nil {
					t.Fatal("owned quarantine", err)
				}
				_, _, code := testkit.CredentialMailSecrets(t, e.Pool, s.cfg.Mail.MailKey, proof.ChallengeId)
				if _, err = m.Accounts.CompleteIdentityRecovery(ctx, accounts.IdentityRecoveryCompleteInput{ChallengeId: &proof.ChallengeId, Code: &code, NewPassword: "long safe password", AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"}, "127.0.0.2"); err != nil {
					t.Fatal("owned recovery", err)
				}
			default:
				profile := "euru"
				banned := true
				days := int64(30)
				rev := int64(1)
				in := subscriptions.AccessOperationInput{Kind: kind, Reason: "Owned policy change"}
				if kind == "assign_plan" {
					in.PlanId = &order.Quote.PlanId
					in.Revision = &rev
					in.PeriodDays = &days
				}
				if kind == "set_profile" {
					in.Profile = &profile
				}
				if kind == "set_vpn_ban" {
					in.VpnBanned = &banned
				}
				if _, err := m.Subscriptions.CreateAccessOperation(ctx, actor.id, auth.Account.AccountId, uuid.New(), in); err != nil {
					t.Fatal("owned operator intent", err)
				}
			}
			var desired string
			if err := e.Pool.QueryRow(ctx, `SELECT desired_action FROM stars_subscriptions WHERE root_order_id=$1`, order.OrderId).Scan(&desired); err != nil {
				t.Fatal(err)
			}
			if kind == "reset_traffic" {
				if desired != "none" {
					t.Fatal("traffic-only maintenance canceled billing")
				}
				return
			}
			if desired != "cancel" {
				t.Fatal("policy committed without cancellation intent")
			}
			payer := int64(0)
			starsSetter(s, func(_ context.Context, id int64, charge string, canceled bool) error {
				if charge != "owned-stars-charge" || !canceled {
					t.Error("policy control changed native coordinates")
				}
				payer = id
				return nil
			})
			if err := s.payments.ReconcileStarsSubscriptions(ctx); err != nil {
				t.Fatal(err)
			}
			if payer != 701 {
				t.Fatal("policy/recovery lost captured payer")
			}
		})
	}
}

// Catches resuming billing while a refund is ambiguous or a replacement access
// command has not completed, and treating an extra first charge as one recurrence.
func TestStarsSubscriptionResumeGuards(t *testing.T) {
	for _, mode := range []string{"refund-uncertain", "operator-pending", "operator-applied", "starter-applied", "extra-first"} {
		t.Run(mode, func(t *testing.T) {
			_, s, e, auth, m, order := starsSubscriptionFixture(t)
			ctx := context.Background()
			calls := 0
			edit := func(context.Context, int64, string, bool) error { calls++; return nil }
			starsSetter(s, edit)
			if _, err := s.payments.ControlStarsSubscription(ctx, auth.Account.AccountId, uuid.New(), payments.StarsSubscriptionControlInput{Action: "cancel", Confirmed: true}); err != nil {
				t.Fatal(err)
			}
			actor := verified(t, s, e, "resume-guard-operator@example.test")
			if err := m.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "refund-uncertain":
				var receipt string
				if err := e.Pool.QueryRow(ctx, `SELECT funding_operation_id FROM purchase_orders WHERE id=$1`, order.OrderId).Scan(&receipt); err != nil {
					t.Fatal(err)
				}
				s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) { return "", nil }, EditSubscription: edit, Refund: func(context.Context, int64, string) error { return errors.New("owned lost refund reply") }})
				got, err := s.payments.RefundStarsPurchase(ctx, actor, auth.Account.AccountId, order.OrderId, uuid.New(), payments.StarsRefundInput{ReceiptOperationId: receipt, Reason: "Owned refund", ConfirmFull: true, KeepAccess: true})
				if err != nil || got.State != "uncertain" {
					t.Fatal("owned ambiguous refund", err)
				}
			case "operator-pending", "operator-applied", "starter-applied":
				days := int64(30)
				rev := int64(1)
				input := subscriptions.AccessOperationInput{Kind: "assign_plan", Reason: "Owned replacement", PlanId: &order.Quote.PlanId, Revision: &rev, PeriodDays: &days}
				if mode == "starter-applied" {
					input = subscriptions.AccessOperationInput{Kind: "starter_trial", Reason: "Owned starter"}
				}
				got, err := m.Subscriptions.CreateAccessOperation(ctx, actor, auth.Account.AccountId, uuid.New(), input)
				if err != nil || got.Status != "pending" {
					t.Fatal("owned pending replacement", err)
				}
				if mode != "operator-pending" {
					if err = m.VPN.ApplyAccess(ctx, got.OperationId); err != nil {
						t.Fatal("owned replacement apply", err)
					}
				}
			case "extra-first":
				in := starsPayment(order.OrderId, e.Clock())
				in.ChargeID = "owned-extra-first"
				in.Recurring = true
				in.FirstRecurring = true
				in.SubscriptionExpiresAt = in.At.Add(30 * 24 * time.Hour).Unix()
				if err := s.payments.RecordStarsPayment(ctx, in); err != nil {
					t.Fatal(err)
				}
				seen := map[string]bool{}
				starsSetter(s, func(_ context.Context, payer int64, charge string, cancel bool) error {
					calls++
					if payer != 701 || !cancel {
						t.Error("extra billing identity changed")
					}
					seen[charge] = true
					return nil
				})
				if _, err := s.payments.ControlStarsSubscription(ctx, auth.Account.AccountId, uuid.New(), payments.StarsSubscriptionControlInput{Action: "cancel", Confirmed: true}); err != nil {
					t.Fatal(err)
				}
				if !seen["owned-extra-first"] {
					t.Fatal("extra first charge was not canceled separately")
				}
			}
			state, err := s.payments.StarsSubscription(ctx, auth.Account.AccountId)
			if err != nil || state.CanResume {
				t.Fatal("unresolved money/access offered resume", err)
			}
			before := calls
			if _, err = s.payments.ControlStarsSubscription(ctx, auth.Account.AccountId, uuid.New(), payments.StarsSubscriptionControlInput{Action: "resume", Confirmed: true}); err == nil || calls != before {
				t.Fatal("unresolved money/access reached native resume")
			}
		})
	}
}

// Catches accepting an unrelated payer/bot as authoritative provider state.
func TestStarsSubscriptionUpdateAuthority(t *testing.T) {
	_, s, e, auth, _, order := starsSubscriptionFixture(t)
	ctx := context.Background()
	in := payments.StarsSubscriptionUpdateInput{BotID: 123, PayerID: 701, UpdateID: 50, Payload: "stars:v1:" + order.OrderId.String(), State: "failed", At: e.Clock()}
	for _, field := range []string{"bot", "payer", "root", "state"} {
		bad := in
		switch field {
		case "bot":
			bad.BotID = 124
		case "payer":
			bad.PayerID = 702
		case "root":
			bad.Payload = "stars:v1:" + uuid.NewString()
		case "state":
			bad.State = "unknown"
		}
		if err := s.payments.RecordStarsSubscriptionUpdate(ctx, bad); err == nil {
			t.Fatal("foreign status accepted", field)
		}
	}
	if err := s.payments.RecordStarsSubscriptionUpdate(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.At = in.At.Add(time.Hour)
	if err := s.payments.RecordStarsSubscriptionUpdate(ctx, in); err != nil {
		t.Fatal("observed time made an exact replay conflict", err)
	}
	starsSetter(s, func(context.Context, int64, string, bool) error { return nil })
	if _, err := s.payments.ControlStarsSubscription(ctx, auth.Account.AccountId, uuid.New(), payments.StarsSubscriptionControlInput{Action: "cancel", Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	in.UpdateID = 2
	in.State = "active"
	if err := s.payments.RecordStarsSubscriptionUpdate(ctx, in); err != nil {
		t.Fatal(err)
	}
	state, err := s.payments.StarsSubscription(ctx, auth.Account.AccountId)
	if err != nil || state.State != "canceled" || state.ExternalBillingBlocked || state.ProviderState == nil || *state.ProviderState != "failed" {
		t.Fatal("active update overrode actual bot cancellation", err)
	}
	var audits int
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE account_id=$1 AND action='stars_subscription_updated'`, auth.Account.AccountId).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("provider replay duplicated audit", err)
	}
}
