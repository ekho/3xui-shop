package tests

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func nativeReferredFixture(t *testing.T) (*fixture, *nativeBot, ed25519.PrivateKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := openMode(t, true, pub)
	f.cfg.ReferredTrial = bonuses.ReferredTrialConfig{Enabled: true, PeriodDays: 7}
	f.cfg.Subscriptions.TrialTrafficGB, f.cfg.Subscriptions.TrialDevices = 27, 2
	f.cfg.Bonuses = bonuses.RewardConfig{Enabled: false}
	f.panel.mu.Lock()
	f.panel.loseReply = false
	f.panel.mu.Unlock()
	nativeRewardModules(t, f, key)
	return f, &nativeBot{}, key
}

func nativeReferredActivate(t *testing.T, f *fixture, auth wire.MiniAppSessionResult, key string, want int) wire.TrialRequest {
	t.Helper()
	status, body, _ := f.send(t, f.public.Client(), "POST", "/api/v1/trials/activate", map[string]string{}, auth.CsrfToken, key, false, auth.SessionToken)
	if status != want {
		t.Fatalf("signed trial activation: want %d, got %d", want, status)
	}
	var out wire.TrialRequest
	if status < 300 && (json.Unmarshal(body, &out) != nil || out.OperationId == nil) {
		t.Fatal("signed activation lost request/operation")
	}
	return out
}

func nativeReferredFacts(t *testing.T, f *fixture, account uuid.UUID) (uuid.UUID, int64, int64, int64, int, bool, int, int) {
	t.Helper()
	var op uuid.UUID
	var period, traffic, devices int64
	var reserve int
	var granted bool
	var grants, jobs int
	err := f.env.Pool.QueryRow(context.Background(), `SELECT o.id,o.period_days,o.traffic_gb,o.devices,r.referred_bonus_days,r.referred_rewarded_at IS NOT NULL,
	 (SELECT count(*) FROM trial_grants WHERE account_id=$1),
	 (SELECT count(*) FROM river_job WHERE kind='trial_provision' AND args->>'operation_id'=o.id::text)
	 FROM trial_operations o JOIN referrals r ON r.referred_account_id=o.account_id WHERE o.account_id=$1`, account).Scan(&op, &period, &traffic, &devices, &reserve, &granted, &grants, &jobs)
	if err != nil {
		t.Fatal("durable referred trial facts unavailable", err)
	}
	return op, period, traffic, devices, reserve, granted, grants, jobs
}

func nativeReferredNoPurchase(t *testing.T, f *fixture, account uuid.UUID) {
	t.Helper()
	var orders, rewards int
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM purchase_orders WHERE account_id=$1),
	 (SELECT count(*) FROM referrer_rewards WHERE account_id=$1 OR account_id IN (SELECT referrer_account_id FROM referrals WHERE referred_account_id=$1))`, account).Scan(&orders, &rewards); err != nil || orders != 0 || rewards != 0 {
		t.Fatal("trial created paid order or purchase reward", err)
	}
}

func TestNativeReferredTrialReservationRecovery(t *testing.T) {
	f, bot, key := nativeReferredFixture(t)
	_, stop := launchNative(t, f, bot, true, false, true)
	inviter, _, inviterID := f.signupAccount(t, nativeEmail("trial-inviter"))
	code := nativeReferralCode(t, nativeReferralRead(t, f, inviter))
	tg := int64(uuid.New().ID()) + 1000000000
	bot.reason(tg, "/start "+code)
	wait(t, func() bool { return bot.message(tg, "Подписка,").ID > 0 })
	auth := nativeReferralMini(t, f, key, tg, code)
	account := auth.Account.AccountId
	if later := nativeReferralMini(t, f, key, tg, "ignored-late-source"); later.Account.AccountId != account {
		t.Fatal("later signed launch changed original account")
	}
	if auth.Capabilities.TrialMode == nil || *auth.Capabilities.TrialMode != "activate" {
		t.Fatal("signed original Telegram account lost automatic trial")
	}
	var original, source string
	var parent uuid.UUID
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT COALESCE(a.original_kind,a.kind),a.telegram_start_param,r.referrer_account_id FROM accounts a JOIN referrals r ON r.referred_account_id=a.id WHERE a.id=$1`, account).Scan(&original, &source, &parent); err != nil || original != "telegram" || source != code || parent != inviterID {
		t.Fatal("first signed Telegram inviter not fixed", err)
	}
	firstKey := uuid.NewString()
	first := nativeReferredActivate(t, f, auth, firstKey, 201)
	op, period, traffic, devices, reserve, granted, grants, jobs := nativeReferredFacts(t, f, account)
	if op != *first.OperationId || period != 7 || traffic != 27 || devices != 2 || reserve != 7 || granted || grants != 1 || jobs != 1 {
		t.Fatal("one full seven-day reserved benefit with configured limits missing")
	}
	var identity string
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT vpn_id::text||':'||sub_id||':'||panel_key FROM accounts WHERE id=$1`, account).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	// A changed policy after reservation must not rewrite a pending operation.
	stop()
	f.cfg.ReferredTrial.Enabled, f.cfg.ReferredTrial.PeriodDays = false, 11
	f.cfg.Subscriptions.TrialTrafficGB, f.cfg.Subscriptions.TrialDevices = 0, 1
	nativeRewardModules(t, f, key)
	_, stop = launchNative(t, f, bot, true, true, true)
	replay := nativeReferredActivate(t, f, auth, firstKey, 200)
	if replay.RequestId != first.RequestId || *replay.OperationId != op {
		t.Fatal("restart replay changed request or operation")
	}
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _, _ := f.send(t, f.public.Client(), "POST", "/api/v1/trials/activate", map[string]string{}, auth.CsrfToken, uuid.NewString(), false, auth.SessionToken)
			results <- status
		}()
	}
	wg.Wait()
	close(results)
	for status := range results {
		if status != 409 {
			t.Fatal("distinct concurrent key reissued trial", status)
		}
	}
	wait(t, func() bool { return applied(f, op) })
	gotOp, gotPeriod, gotTraffic, gotDevices, gotReserve, gotGranted, gotGrants, gotJobs := nativeReferredFacts(t, f, account)
	if gotOp != op || gotPeriod != 7 || gotTraffic != 27 || gotDevices != 2 || gotReserve != 7 || !gotGranted || gotGrants != 1 || gotJobs != 1 {
		t.Fatal("restart or concurrent replay changed retained benefit")
	}
	var after string
	var reservedAudit, grantedAudit int
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT vpn_id::text||':'||sub_id||':'||panel_key,
	 (SELECT count(*) FROM audit_events WHERE request_id=$3 AND action='referral_trial_reserved'),
	 (SELECT count(*) FROM audit_events WHERE operation_id=$2 AND action='referral_trial_granted') FROM accounts WHERE id=$1`, account, op, first.RequestId).Scan(&after, &reservedAudit, &grantedAudit); err != nil || after != identity || reservedAudit != 1 || grantedAudit != 1 {
		t.Fatal("restart changed identity or audit proof", err)
	}
	var targetRaw []byte
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT target FROM trial_operations WHERE id=$1`, op).Scan(&targetRaw); err != nil {
		t.Fatal(err)
	}
	var target vpn.ProvisionTarget
	if json.Unmarshal(targetRaw, &target) != nil || target.DeviceCount != 2 || target.TrafficLimitBytes != 27*1024*1024*1024 {
		t.Fatal("TLS panel target lost reserved limits")
	}
	panelClient := vpn.NewPanelClient(f.cfg.VPN.Panel)
	defer panelClient.Close()
	var started time.Time
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT first_started_at FROM trial_operations WHERE id=$1`, op).Scan(&started); err != nil {
		t.Fatal("trial start not retained", err)
	}
	view, err := panelClient.GetClient(context.Background(), target.PanelKey)
	if err != nil || view == nil || view.ExpiryTimeMS != started.Add(7*24*time.Hour).UnixMilli() || view.ExpiryTimeMS != target.ExpiryTimeMS || view.VPNID != target.VPNID || view.SubID != target.SubID || view.LimitIP != 3 || view.TrafficLimitBytes != 27*1024*1024*1024 {
		t.Fatal("TLS panel readback lost seven-day expiry, limits or identity", err)
	}
	nativeReferredNoPurchase(t, f, account)
	stop()
}

func TestNativeReferredTrialSourceAndFlags(t *testing.T) {
	for _, scenario := range []struct {
		name                            string
		kind                            string
		referred, enabled, trialEnabled bool
		want                            int64
	}{
		{"telegram_referred", "telegram", true, true, true, 7},
		{"telegram_flag_off", "telegram", true, false, true, 3},
		{"telegram_unknown", "telegram", false, true, true, 3},
		{"telegram_self", "telegram_self", false, true, true, 3},
		{"telegram_legacy", "telegram_legacy", true, true, true, 0},
		{"web_referred", "web", true, true, true, 3},
		{"trial_disabled", "telegram", true, true, false, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f, bot, key := nativeReferredFixture(t)
			f.cfg.ReferredTrial.Enabled = scenario.enabled
			if scenario.name == "telegram_referred" {
				f.cfg.Bonuses.Enabled = true
			}
			f.cfg.Subscriptions.TrialEnabled = scenario.trialEnabled
			nativeRewardModules(t, f, key)
			_, stop := launchNative(t, f, bot, true, false, true)
			inviter, _, _ := f.signupAccount(t, nativeEmail("source-inviter"))
			code := nativeReferralCode(t, nativeReferralRead(t, f, inviter))
			if !scenario.referred {
				code = "unknown-owned-source"
			}
			if strings.HasPrefix(scenario.kind, "telegram") {
				tg := int64(uuid.New().ID()) + 1000000000
				if scenario.kind == "telegram_self" {
					code = fmt.Sprint(tg)
				}
				auth := nativeReferralMini(t, f, key, tg, code)
				if scenario.kind == "telegram_legacy" {
					if _, err := f.env.Pool.Exec(context.Background(), `UPDATE accounts SET legacy_user_id=$1 WHERE id=$2`, tg, auth.Account.AccountId); err != nil {
						t.Fatal("owned legacy history fixture", err)
					}
				}
				if !scenario.trialEnabled || scenario.kind == "telegram_legacy" {
					status, _, _ := f.send(t, f.public.Client(), "POST", "/api/v1/trials/activate", map[string]string{}, auth.CsrfToken, uuid.NewString(), false, auth.SessionToken)
					if status != 403 {
						t.Fatal("disabled or legacy automatic trial must be denied", status)
					}
					var requests, grants, reservations int
					if err := f.env.Pool.QueryRow(context.Background(), `SELECT
					 (SELECT count(*) FROM trial_requests WHERE account_id=$1),
					 (SELECT count(*) FROM trial_grants WHERE account_id=$1),
					 (SELECT count(*) FROM referrals WHERE referred_account_id=$1 AND (referred_bonus_days IS NOT NULL OR referred_rewarded_at IS NOT NULL))`, auth.Account.AccountId).Scan(&requests, &grants, &reservations); err != nil || requests != 0 || grants != 0 || reservations != 0 {
						t.Fatal("disabled trial left request, grant, or referral reservation", err)
					}
					return
				}
				nativeReferredActivate(t, f, auth, uuid.NewString(), 201)
				var period int64
				if err := f.env.Pool.QueryRow(context.Background(), `SELECT period_days FROM trial_operations WHERE account_id=$1`, auth.Account.AccountId).Scan(&period); err != nil || period != scenario.want {
					t.Fatal("Telegram source/flag selected wrong duration", period, err)
				}
				nativeReferredNoPurchase(t, f, auth.Account.AccountId)
			} else {
				email := nativeEmail("web-source")
				client, csrf, account := f.signupAccount(t, email, code)
				tg := int64(uuid.New().ID()) + 1000000000
				csrf = nativeReferralLink(t, f, client, csrf, email, key, tg, code)
				linked := nativeReferralMini(t, f, key, tg, code)
				if linked.Account.AccountId != account || linked.Capabilities.TrialMode != nil {
					t.Fatal("original web policy changed after Telegram link")
				}
				status, raw, _ := f.send(t, client, "POST", "/api/v1/trial-requests", map[string]string{"comment": "Owned web request"}, csrf, uuid.NewString(), false)
				var request wire.TrialRequest
				if status != 201 || json.Unmarshal(raw, &request) != nil {
					t.Fatal("linked original web manual request", status)
				}
				card := cardFor(t, bot, 101, request.RequestId)
				bot.callback(101, card.ID, "a", request.RequestId, "")
				wait(t, func() bool { return trialStatus(f, request.RequestId) == "approved" })
				var period int64
				if err := f.env.Pool.QueryRow(context.Background(), `SELECT period_days FROM trial_operations WHERE account_id=$1`, account).Scan(&period); err != nil || period != 3 {
					t.Fatal("web manual trial became referred automatic benefit", period, err)
				}
			}
			stop()
		})
	}
}

func TestNativeReferredTrialFinalAuditRollback(t *testing.T) {
	f, bot, key := nativeReferredFixture(t)
	_, stop := launchNative(t, f, bot, true, false, true)
	inviter, _, _ := f.signupAccount(t, nativeEmail("audit-inviter"))
	code := nativeReferralCode(t, nativeReferralRead(t, f, inviter))
	auth := nativeReferralMini(t, f, key, int64(uuid.New().ID())+1000000000, code)
	request := nativeReferredActivate(t, f, auth, uuid.NewString(), 201)
	op := *request.OperationId
	if _, err := f.env.Pool.Exec(context.Background(), `CREATE FUNCTION reject_owned_referred_trial_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='referral_trial_granted' THEN RAISE EXCEPTION 'owned final audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_owned_referred_trial_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_owned_referred_trial_audit()`); err != nil {
		t.Fatal(err)
	}
	stop()
	nativeRewardModules(t, f, key)
	_, stop = launchNative(t, f, bot, true, true, true)
	wait(t, func() bool {
		return trialStatus(f, request.RequestId) == "approved" && trialOperationStatus(f, op) == "needs_review"
	})
	var grantStatus string
	var rewarded bool
	var audit int
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT g.status,r.referred_rewarded_at IS NOT NULL,
	 (SELECT count(*) FROM audit_events WHERE action='referral_trial_granted' AND operation_id=$2)
	 FROM trial_grants g JOIN referrals r ON r.referred_account_id=g.account_id WHERE g.account_id=$1`, auth.Account.AccountId, op).Scan(&grantStatus, &rewarded, &audit); err != nil || grantStatus != "reserved" || rewarded || audit != 0 {
		t.Fatal("failed final audit partially committed referral grant", err)
	}
	var targetRaw []byte
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT target FROM trial_operations WHERE id=$1`, op).Scan(&targetRaw); err != nil || len(targetRaw) == 0 {
		t.Fatal("uncertain panel target not retained", err)
	}
	var target vpn.ProvisionTarget
	if json.Unmarshal(targetRaw, &target) != nil {
		t.Fatal("retained target invalid")
	}
	client := vpn.NewPanelClient(f.cfg.VPN.Panel)
	defer client.Close()
	view, err := client.GetClient(context.Background(), target.PanelKey)
	if err != nil || view == nil || view.VPNID != target.VPNID || view.SubID != target.SubID || view.ExpiryTimeMS != target.ExpiryTimeMS {
		t.Fatal("external panel write not retained before audit rollback", err)
	}
	if _, err := f.env.Pool.Exec(context.Background(), `DROP TRIGGER reject_owned_referred_trial_audit ON audit_events; DROP FUNCTION reject_owned_referred_trial_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Subscriptions.ReconcileTrialOperation(context.Background(), op, uuid.New(), subscriptions.ReconcileInput{OperatorTgId: 101, Reason: "Owned referral trial audit recovery"}); err != nil {
		t.Fatal("operator reconcile failed", err)
	}
	wait(t, func() bool { return applied(f, op) })
	_, period, _, _, reserve, granted, grants, jobs := nativeReferredFacts(t, f, auth.Account.AccountId)
	if period != 7 || reserve != 7 || !granted || grants != 1 || jobs < 1 {
		t.Fatal("reconcile lost original benefit or grant")
	}
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE action='referral_trial_granted' AND operation_id=$1`, op).Scan(&audit); err != nil || audit != 1 {
		t.Fatal("reconcile did not create exactly one grant proof", err)
	}
	var current []byte
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT target FROM trial_operations WHERE id=$1`, op).Scan(&current); err != nil || !reflect.DeepEqual(current, targetRaw) {
		t.Fatal("reconcile recomputed external target", err)
	}
	nativeReferredNoPurchase(t, f, auth.Account.AccountId)
	stop()
}

func TestNativeReferredTrialQueueRollback(t *testing.T) {
	f, bot, key := nativeReferredFixture(t)
	_, stop := launchNative(t, f, bot, true, false, true)
	inviter, _, _ := f.signupAccount(t, nativeEmail("queue-inviter"))
	code := nativeReferralCode(t, nativeReferralRead(t, f, inviter))
	auth := nativeReferralMini(t, f, key, int64(uuid.New().ID())+1000000000, code)
	account := auth.Account.AccountId
	ctx := context.Background()
	if _, err := f.env.Pool.Exec(ctx, `CREATE FUNCTION reject_owned_referred_trial_job() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='trial_provision' THEN RAISE EXCEPTION 'owned trial queue failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_owned_referred_trial_job BEFORE INSERT ON river_job FOR EACH ROW EXECUTE FUNCTION reject_owned_referred_trial_job()`); err != nil {
		t.Fatal("owned queue failure fixture unavailable", err)
	}
	idempotency := uuid.NewString()
	if status, _, _ := f.send(t, f.public.Client(), "POST", "/api/v1/trials/activate", map[string]string{}, auth.CsrfToken, idempotency, false, auth.SessionToken); status != 503 {
		t.Fatal("rejected River insert did not fail activation atomically", status)
	}
	var requests, operations, grants, reserved, audits int
	if err := f.env.Pool.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM trial_requests WHERE account_id=$1),
	 (SELECT count(*) FROM trial_operations WHERE account_id=$1),
	 (SELECT count(*) FROM trial_grants WHERE account_id=$1),
	 (SELECT count(*) FROM referrals WHERE referred_account_id=$1 AND (referred_bonus_days IS NOT NULL OR referred_rewarded_at IS NOT NULL)),
	 (SELECT count(*) FROM audit_events WHERE account_id=$1 AND action LIKE 'referral_trial_%')`, account).Scan(&requests, &operations, &grants, &reserved, &audits); err != nil || requests != 0 || operations != 0 || grants != 0 || reserved != 0 || audits != 0 {
		t.Fatal("failed queue insert left partial trial/referral state", err)
	}
	if _, err := f.env.Pool.Exec(ctx, `DROP TRIGGER reject_owned_referred_trial_job ON river_job; DROP FUNCTION reject_owned_referred_trial_job()`); err != nil {
		t.Fatal("owned queue failure fixture removal failed", err)
	}
	request := nativeReferredActivate(t, f, auth, idempotency, 201)
	op, period, traffic, devices, reserve, granted, count, jobs := nativeReferredFacts(t, f, account)
	if op != *request.OperationId || period != 7 || traffic != 27 || devices != 2 || reserve != 7 || granted || count != 1 || jobs != 1 {
		t.Fatal("retry after atomic queue rollback lost original benefit")
	}
	nativeReferredNoPurchase(t, f, account)
	stop()
}

func TestNativeReferredTrialUncertainPanelWrite(t *testing.T) {
	f, bot, key := nativeReferredFixture(t)
	_, stop := launchNative(t, f, bot, true, false, true)
	inviter, _, _ := f.signupAccount(t, nativeEmail("uncertain-inviter"))
	code := nativeReferralCode(t, nativeReferralRead(t, f, inviter))
	auth := nativeReferralMini(t, f, key, int64(uuid.New().ID())+1000000000, code)
	request := nativeReferredActivate(t, f, auth, uuid.NewString(), 201)
	op := *request.OperationId
	stop()
	f.panel.mu.Lock()
	f.panel.loseReply = true
	f.panel.mu.Unlock()
	var readUnavailable atomic.Bool
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if readUnavailable.Load() && r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/panel/api/clients/get/") {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		f.panel.serve(w, r)
		if r.Method == http.MethodPost && r.URL.Path == "/panel/api/clients/add" {
			readUnavailable.Store(true)
		}
	}))
	t.Cleanup(provider.Close)
	f.cfg.VPN.Panel.PanelURL = provider.URL
	f.cfg.VPN.Panel.PanelRootCAs = x509.NewCertPool()
	f.cfg.VPN.Panel.PanelRootCAs.AddCert(provider.Certificate())
	nativeRewardModules(t, f, key)
	_, stop = launchNative(t, f, bot, true, true, true)
	wait(t, func() bool { return trialOperationStatus(f, op) == "needs_review" })
	var raw []byte
	var grantStatus string
	var rewarded bool
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT o.target,g.status,r.referred_rewarded_at IS NOT NULL
	 FROM trial_operations o JOIN trial_grants g ON g.operation_id=o.id JOIN referrals r ON r.referred_account_id=o.account_id WHERE o.id=$1`, op).Scan(&raw, &grantStatus, &rewarded); err != nil || len(raw) == 0 || grantStatus != "reserved" || rewarded {
		t.Fatal("uncertain write prematurely confirmed referral benefit", err)
	}
	var target vpn.ProvisionTarget
	if json.Unmarshal(raw, &target) != nil {
		t.Fatal("uncertain write lost target")
	}
	readUnavailable.Store(false)
	panelClient := vpn.NewPanelClient(f.cfg.VPN.Panel)
	defer panelClient.Close()
	view, err := panelClient.GetClient(context.Background(), target.PanelKey)
	if err != nil || view == nil || view.VPNID != target.VPNID || view.SubID != target.SubID || view.ExpiryTimeMS != target.ExpiryTimeMS {
		t.Fatal("uncertain write did not retain external target", err)
	}
	if status, _, _ := f.send(t, f.public.Client(), "GET", "/api/v1/subscription/key", nil, "", "", false, auth.SessionToken); status == 200 {
		t.Fatal("uncertain write exposed key before operator reconciliation")
	}
	f.panel.mu.Lock()
	adds := f.panel.adds
	f.panel.mu.Unlock()
	if _, err := f.svc.Subscriptions.ReconcileTrialOperation(context.Background(), op, uuid.New(), subscriptions.ReconcileInput{OperatorTgId: 101, Reason: "Owned uncertain referral trial recovery"}); err != nil {
		t.Fatal("uncertain trial reconcile failed", err)
	}
	wait(t, func() bool { return applied(f, op) })
	_, period, traffic, devices, reserve, granted, grants, jobs := nativeReferredFacts(t, f, auth.Account.AccountId)
	if period != 7 || traffic != 27 || devices != 2 || reserve != 7 || !granted || grants != 1 || jobs < 1 {
		t.Fatal("uncertain recovery changed trial snapshot or grant")
	}
	var after []byte
	var audit int
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT target,(SELECT count(*) FROM audit_events WHERE operation_id=$1 AND action='referral_trial_granted') FROM trial_operations WHERE id=$1`, op).Scan(&after, &audit); err != nil || !reflect.DeepEqual(raw, after) || audit != 1 {
		t.Fatal("uncertain recovery changed target or repeated grant proof", err)
	}
	f.panel.mu.Lock()
	afterAdds := f.panel.adds
	f.panel.mu.Unlock()
	if afterAdds != adds {
		t.Fatal("uncertain recovery repeated panel add")
	}
	nativeReferredNoPurchase(t, f, auth.Account.AccountId)
	stop()
}

func trialOperationStatus(f *fixture, operation uuid.UUID) string {
	var status string
	_ = f.env.Pool.QueryRow(context.Background(), `SELECT status FROM trial_operations WHERE id=$1`, operation).Scan(&status)
	return status
}

// The account's email is granted through the same Mini App flow used by the cabinet.
func TestNativeReferredTrialTelegramEmailAlias(t *testing.T) {
	f, bot, key := nativeReferredFixture(t)
	_, stop := launchNative(t, f, bot, true, false, true)
	inviter, _, inviterID := f.signupAccount(t, nativeEmail("email-inviter"))
	code := nativeReferralCode(t, nativeReferralRead(t, f, inviter))
	tg := int64(uuid.New().ID()) + 1000000000
	auth := nativeReferralMini(t, f, key, tg, code)
	account := auth.Account.AccountId
	email := nativeEmail("telegram-email")
	status, body, _ := f.send(t, f.public.Client(), "POST", "/api/v1/telegram/initial-email", map[string]string{"email": email}, auth.CsrfToken, "", false, auth.SessionToken)
	var challenge struct {
		ChallengeID uuid.UUID `json:"challenge_id"`
	}
	if status != 202 || json.Unmarshal(body, &challenge) != nil || challenge.ChallengeID == uuid.Nil {
		t.Fatal("Telegram initial email request", status)
	}
	var codeProof string
	wait(t, func() bool {
		for _, letter := range f.letters(t, email) {
			if !strings.Contains(letter, "To: "+email) {
				continue
			}
			if match := regexp.MustCompile(`Code: ([0-9]{8})`).FindStringSubmatch(letter); len(match) == 2 {
				codeProof = match[1]
				return true
			}
		}
		return false
	})
	status, body, _ = f.send(t, f.public.Client(), "POST", "/api/v1/telegram/initial-email/confirm", map[string]any{"challenge_id": challenge.ChallengeID, "code": codeProof, "new_password": "fixture password with Unicode ✨", "accepted_terms_version": "1", "accepted_privacy_version": "1"}, auth.CsrfToken, "", false, auth.SessionToken)
	if status != 200 {
		t.Fatal("Telegram email confirmation", status)
	}
	var kind, source string
	var parent uuid.UUID
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT a.original_kind,a.telegram_start_param,r.referrer_account_id FROM accounts a JOIN referrals r ON r.referred_account_id=a.id WHERE a.id=$1 AND a.email_key=$2`, account, email).Scan(&kind, &source, &parent); err != nil || kind != "telegram" || source != code || parent != inviterID {
		t.Fatal("email conversion changed original Telegram inviter", err)
	}
	client := *f.public.Client()
	client.Jar, _ = cookiejar.New(nil)
	status, raw, _ := f.send(t, &client, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "fixture password with Unicode ✨"}, "", "", false)
	var web wire.LoginResult
	if status != 200 || json.Unmarshal(raw, &web) != nil || web.Account.AccountId != account {
		t.Fatal("Telegram-to-web login changed account", status)
	}
	reopened := nativeReferralMini(t, f, key, tg, "ignored-late-source")
	if reopened.Account.AccountId != account || reopened.Capabilities.TrialMode == nil || *reopened.Capabilities.TrialMode != "activate" {
		t.Fatal("email alias lost automatic Telegram policy")
	}
	first := nativeReferredActivate(t, f, reopened, uuid.NewString(), 201)
	if _, period, _, _, reserve, _, _, _ := nativeReferredFacts(t, f, account); period != 7 || reserve != 7 {
		t.Fatal("same-account email alias changed referral benefit")
	}
	if status, _, _ := f.send(t, &client, "POST", "/api/v1/me/telegram/unlink", wire.CurrentPasswordInput{CurrentPassword: "fixture password with Unicode ✨"}, web.CsrfToken, "", false); status != 200 {
		t.Fatal("same-account unlink", status)
	}
	status, raw, _ = f.send(t, &client, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "fixture password with Unicode ✨"}, "", "", false)
	if status != 200 || json.Unmarshal(raw, &web) != nil || web.Account.AccountId != account {
		t.Fatal("same-account web relogin after unlink", status)
	}
	other := int64(uuid.New().ID()) + 1000000000
	nativeReferralLink(t, f, &client, web.CsrfToken, email, key, other, "ignored-alternative-source")
	linked := nativeReferralMini(t, f, key, other, "ignored-alternative-source")
	if linked.Account.AccountId != account {
		t.Fatal("alternative Telegram alias changed account")
	}
	if replay := nativeReferredActivate(t, f, linked, uuid.NewString(), 409); replay.OperationId != nil {
		t.Fatal("alternative alias unexpectedly returned new operation")
	}
	var count int
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT count(*) FROM trial_operations WHERE account_id=$1 AND id=$2`, account, *first.OperationId).Scan(&count); err != nil || count != 1 {
		t.Fatal("alias changed original trial operation", err)
	}
	stop()
}
