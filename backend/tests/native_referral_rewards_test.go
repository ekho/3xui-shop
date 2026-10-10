package tests

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type nativeRewardFact struct {
	ID, Account, Order uuid.UUID
	Level              int
	Days, Payment      string
	Job                int64
	Access             *uuid.UUID
	Granted            bool
}

func TestNativeReferralRewardPrewriteGuards(t *testing.T) {
	for _, guard := range []string{"recipient-restricted", "source-refunded"} {
		t.Run(guard, func(t *testing.T) {
			f, bot, key, plan := nativeStarsFixture(t)
			ctx := context.Background()
			f.cfg.Bonuses = bonuses.RewardConfig{Enabled: true, LevelOneDays: 10, LevelTwoDays: 3}
			runtime, stop := launchNative(t, f, bot, true, false, true)
			wait(t, runtime.StarsGateway().Ready)
			inviter, _, aid := f.signupAccount(t, nativeEmail("guarded-referrer"))
			operator, csrf, operatorID := f.signupAccount(t, nativeEmail("guard-operator"))
			if f.svc.Accounts.ChangeOperatorRole(ctx, operatorID, true) != nil {
				t.Fatal("owned guard operator unavailable")
			}
			tg := int64(uuid.New().ID()) + 1000000000
			nativeReferralMini(t, f, key, tg, nativeReferralCode(t, nativeReferralRead(t, f, inviter)))
			_, order := nativeStarsOrder(t, f, key, tg, plan)
			proof := nativeStarsProof(tg, order.OrderId, "owned-guarded-charge", time.Now())
			bot.starsMoney(proof, false)
			var reward, access uuid.UUID
			wait(t, func() bool {
				return f.env.Pool.QueryRow(ctx, `SELECT id FROM referrer_rewards WHERE source_order_id=$1 AND account_id=$2`, order.OrderId, aid).Scan(&reward) == nil
			})
			var snooze *river.JobSnoozeError
			if err := f.svc.Bonuses.ProcessReward(ctx, reward); !errors.As(err, &snooze) || f.env.Pool.QueryRow(ctx, `SELECT access_operation_id FROM referrer_rewards WHERE id=$1`, reward).Scan(&access) != nil || access == uuid.Nil {
				t.Fatal("owned public reward preparation did not retain one intent", err)
			}
			frozen := nativeRewardTarget(t, f, access)
			wantReason := "referral_funding_invalid"
			if guard == "recipient-restricted" {
				wantReason = "bonus_guard_changed"
				status, _, _ := f.send(t, operator, "POST", "/api/v1/operator/clients/"+aid.String()+"/restriction", map[string]any{"restricted": true, "reason": "Owned prewrite reward permission guard"}, csrf, uuid.NewString(), false)
				if status != 200 {
					t.Fatal("native recipient restriction failed", status)
				}
			} else {
				bot.starsMoney(proof, true)
				wait(t, func() bool {
					var refunded bool
					return f.env.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM purchase_refunds WHERE order_id=$1 AND source='telegram')`, order.OrderId).Scan(&refunded) == nil && refunded
				})
			}
			stop()
			nativeRewardModules(t, f, key)
			_, stop = launchNative(t, f, bot, true, true, true)
			wait(t, func() bool {
				var held bool
				return f.env.Pool.QueryRow(ctx, `SELECT status='needs_review' AND review_reason=$2 AND NOT write_started AND NOT reset_started FROM access_operations WHERE id=$1`, access, wantReason).Scan(&held) == nil && held
			})
			var retained uuid.UUID
			var granted bool
			if f.env.Pool.QueryRow(ctx, `SELECT access_operation_id,rewarded_at IS NOT NULL FROM referrer_rewards WHERE id=$1`, reward).Scan(&retained, &granted) != nil || retained != access || granted || !reflect.DeepEqual(nativeRewardTarget(t, f, access), frozen) {
				t.Fatal("prewrite guard replaced target or declared reward granted")
			}
			f.panel.mu.Lock()
			written := f.panel.clients[frozen.PanelKey] != nil
			f.panel.mu.Unlock()
			if written {
				t.Fatal("native access worker wrote a denied reward")
			}
			bot.starsMoney(proof, false)
			wait(t, func() bool { bot.mu.Lock(); defer bot.mu.Unlock(); return len(bot.updates) == 0 })
			var rewards, grantAudit int
			if f.env.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM referrer_rewards WHERE source_order_id=$1),(SELECT count(*) FROM audit_events WHERE access_operation_id=$2 AND action='referral_reward_granted')`, order.OrderId, access).Scan(&rewards, &grantAudit) != nil || rewards != 1 || grantAudit != 0 {
				t.Fatal("denied reward replay duplicated source or grant proof")
			}
			if guard == "recipient-restricted" {
				// The common compensation port also serves bonuses without a referral source.
				_, _, ordinary := f.signupAccount(t, nativeEmail("ordinary-bonus"))
				op, err := f.svc.Subscriptions.GrantBonusDays(ctx, ordinary, uuid.New(), 2, "Owned ordinary bonus", func(context.Context, pgx.Tx, subscriptions.AccessOperation) error { return nil })
				if err != nil || op.OperatorAccountId != nil || op.Kind != "compensate" {
					t.Fatal("ordinary shared bonus port rejected", err)
				}
				wait(t, func() bool {
					var applied bool
					return f.env.Pool.QueryRow(ctx, `SELECT status='applied' AND operator_account_id IS NULL AND NOT EXISTS(SELECT 1 FROM referrer_rewards WHERE access_operation_id=$1) FROM access_operations WHERE id=$1`, op.OperationId).Scan(&applied) == nil && applied
				})
				target := nativeRewardTarget(t, f, op.OperationId)
				panelClient := vpn.NewPanelClient(f.cfg.VPN.Panel)
				defer panelClient.Close()
				view, err := panelClient.GetClient(ctx, target.PanelKey)
				expected := vpn.ProvisionTarget{PanelKey: target.PanelKey, VPNID: target.VPNID, SubID: target.SubID, ExpiryTimeMS: target.ExpiryTimeMS, DeviceCount: target.DeviceCount, TrafficLimitBytes: target.TrafficLimitBytes, Banned: target.Banned}
				if err != nil || !vpn.Matches(view, expected, time.Now()) || target.TrafficLimitBytes != 0 {
					t.Fatal("unrelated bonus lost native access readback")
				}
			}
			stop()
		})
	}
}

func nativeRewardFacts(t *testing.T, f *fixture, order uuid.UUID) []nativeRewardFact {
	t.Helper()
	rows, err := f.env.Pool.Query(context.Background(), `SELECT r.id,r.account_id,r.source_order_id,r.reward_level,r.amount::integer::text,r.payment_id,j.id,r.access_operation_id,r.rewarded_at IS NOT NULL FROM referrer_rewards r JOIN river_job j ON j.kind='referral_reward' AND j.args->>'reward_id'=r.id::text WHERE r.source_order_id=$1 ORDER BY r.reward_level`, order)
	if err != nil {
		t.Fatal("owned reward facts unavailable", err)
	}
	defer rows.Close()
	var facts []nativeRewardFact
	for rows.Next() {
		var fact nativeRewardFact
		if err = rows.Scan(&fact.ID, &fact.Account, &fact.Order, &fact.Level, &fact.Days, &fact.Payment, &fact.Job, &fact.Access, &fact.Granted); err != nil {
			t.Fatal("owned reward facts invalid", err)
		}
		facts = append(facts, fact)
	}
	if rows.Err() != nil || len(facts) != 2 {
		t.Fatal("two durable reward records/jobs missing")
	}
	return facts
}

func nativeRewardModules(t *testing.T, f *fixture, key ed25519.PrivateKey) {
	t.Helper()
	f.public.Close()
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
}

func nativeRewardTarget(t *testing.T, f *fixture, operation uuid.UUID) vpn.AccessTarget {
	t.Helper()
	var raw []byte
	var target vpn.AccessTarget
	if f.env.Pool.QueryRow(context.Background(), `SELECT target FROM access_operations WHERE id=$1`, operation).Scan(&raw) != nil || json.Unmarshal(raw, &target) != nil {
		t.Fatal("owned reward access target unavailable")
	}
	return target
}

func nativeRewardRetry(t *testing.T, f *fixture, facts []nativeRewardFact) {
	t.Helper()
	for _, fact := range facts {
		if !fact.Granted {
			if _, err := f.workers.JobRetry(context.Background(), fact.Job); err != nil {
				t.Fatal("owned durable reward retry unavailable", err)
			}
		}
	}
}

func TestNativeReferralRewardsRecovery(t *testing.T) {
	f, bot, key, plan := nativeStarsFixture(t)
	ctx := context.Background()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		rows, err := f.env.Pool.Query(ctx, `SELECT r.reward_level,r.access_operation_id IS NOT NULL,COALESCE(a.status,''),COALESCE(a.review_reason,''),r.rewarded_at IS NOT NULL FROM referrer_rewards r LEFT JOIN access_operations a ON a.id=r.access_operation_id WHERE r.source_order_id IS NOT NULL ORDER BY r.reward_level`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var level int
				var attached, granted bool
				var status, reason string
				if rows.Scan(&level, &attached, &status, &reason, &granted) == nil {
					t.Log("owned reward state", level, attached, status, reason, granted)
				}
			}
		}
		jobs, err := f.env.Pool.Query(ctx, `SELECT kind,state,attempt,attempted_at IS NOT NULL,scheduled_at<=now() FROM river_job WHERE queue='provision' ORDER BY id`)
		if err == nil {
			defer jobs.Close()
			for jobs.Next() {
				var kind, state string
				var attempt int
				var attempted, due bool
				if jobs.Scan(&kind, &state, &attempt, &attempted, &due) == nil {
					t.Log("owned worker state", kind, state, attempt, attempted, due)
				}
			}
		}
	})
	var clock atomic.Int64
	clock.Store(time.Now().UTC().Truncate(time.Second).Unix())
	f.cfg.Accounts.Now = func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	f.cfg.Bonuses = bonuses.RewardConfig{Enabled: true, LevelOneDays: 10, LevelTwoDays: 3}
	// Only this synthetic TLS panel loses the reply after applying one update.
	var offline, loseUpdate, readbackOffline atomic.Bool
	var updateKey atomic.Value
	updateKey.Store("")
	var newClientKey atomic.Value
	newClientKey.Store("")
	var updates, resets atomic.Int64
	f.panel.mu.Lock()
	f.panel.loseReply = false
	f.panel.mu.Unlock()
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() || readbackOffline.Load() && r.URL.Path == "/panel/api/clients/get/"+updateKey.Load().(string) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/panel/api/clients/resetTraffic/"+updateKey.Load().(string) || r.URL.Path == "/panel/api/clients/resetTraffic/"+newClientKey.Load().(string) {
			resets.Add(1)
		}
		if r.Method == http.MethodPost && r.URL.Path == "/panel/api/clients/update/"+updateKey.Load().(string) {
			updates.Add(1)
			if loseUpdate.CompareAndSwap(true, false) {
				f.panel.serve(httptest.NewRecorder(), r)
				readbackOffline.Store(true)
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					conn.Close()
				}
				return
			}
		}
		f.panel.serve(w, r)
	}))
	t.Cleanup(provider.Close)
	f.cfg.VPN.Panel.PanelURL = provider.URL
	f.cfg.VPN.Panel.PanelRootCAs = x509.NewCertPool()
	f.cfg.VPN.Panel.PanelRootCAs.AddCert(provider.Certificate())
	nativeRewardModules(t, f, key)
	_, stop := launchNative(t, f, bot, true, true, true)
	emailA, emailB := nativeEmail("reward-ancestor"), nativeEmail("reward-inviter")
	a, csrfA, aid := f.signupAccount(t, emailA)
	codeA := nativeReferralCode(t, nativeReferralRead(t, f, a))
	b, csrfB, bid := f.signupAccount(t, emailB, codeA)
	inviter, err := f.svc.Accounts.Lookup(ctx, bid)
	if err != nil {
		t.Fatal("owned inviter identity unavailable")
	}
	newClientKey.Store(inviter.PanelKey)
	codeB := nativeReferralCode(t, nativeReferralRead(t, f, b))
	if f.svc.Accounts.ChangeOperatorRole(ctx, aid, true) != nil {
		t.Fatal("owned operator unavailable")
	}
	status, raw, _ := f.send(t, a, "POST", "/api/v1/operator/clients/"+aid.String()+"/access-operations", map[string]any{"kind": "assign_plan", "reason": "Owned referral baseline", "plan_id": plan, "revision": 1, "period_days": 30}, csrfA, uuid.NewString(), false)
	var baseline subscriptions.AccessOperation
	if status != 202 || json.Unmarshal(raw, &baseline) != nil {
		t.Fatal("native owned baseline rejected", status)
	}
	wait(t, func() bool {
		var applied bool
		return f.env.Pool.QueryRow(ctx, `SELECT status='applied' FROM access_operations WHERE id=$1`, baseline.OperationId).Scan(&applied) == nil && applied
	})
	before := nativeRewardTarget(t, f, baseline.OperationId)
	updateKey.Store(before.PanelKey)
	f.panel.mu.Lock()
	f.panel.usedTraffic = 8192
	f.panel.mu.Unlock()
	stop()
	nativeRewardModules(t, f, key)
	runtime, stop := launchNative(t, f, bot, true, false, true)
	wait(t, runtime.StarsGateway().Ready)
	tgC := int64(uuid.New().ID()) + 1000000000
	sourceC := nativeReferralMini(t, f, key, tgC, codeB)
	authC, order := nativeStarsOrder(t, f, key, tgC, plan)
	if authC.Account.AccountId != sourceC.Account.AccountId {
		t.Fatal("signed purchase changed original source identity")
	}
	var count int
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM referrer_rewards WHERE source_order_id=$1`, order.OrderId).Scan(&count) != nil || count != 0 {
		t.Fatal("unconfirmed invoice created rewards")
	}
	proof := nativeStarsProof(tgC, order.OrderId, "owned-referral-charge", f.cfg.Accounts.Now())
	bot.starsMoney(proof, false)
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM referrer_rewards WHERE source_order_id=$1 AND rewarded_at IS NULL`, order.OrderId).Scan(&count) == nil && count == 2
	})
	facts := nativeRewardFacts(t, f, order.OrderId)
	if facts[0].Account != bid || facts[0].Days != "10" || facts[0].Level != 1 || facts[1].Account != aid || facts[1].Days != "3" || facts[1].Level != 2 || facts[0].Payment != "order:"+order.OrderId.String() || facts[1].Payment != facts[0].Payment || facts[0].Access != nil || facts[1].Access != nil {
		t.Fatal("confirmed funding did not retain two source rewards")
	}
	if aRead, bRead := nativeReferralRead(t, f, a), nativeReferralRead(t, f, b); aRead.Levels[1].PendingDays != "3" || bRead.Levels[0].PendingDays != "10" || aRead.Levels[1].GrantedDays != "0" || bRead.Levels[0].GrantedDays != "0" {
		t.Fatal("HTTP pending reward facts missing")
	}
	tgA, tgB := int64(uuid.New().ID())+1000000000, int64(uuid.New().ID())+1000000000
	nativeReferralLink(t, f, a, csrfA, emailA, key, tgA, codeB)
	codeC := nativeReferralCode(t, nativeReferralRead(t, f, f.public.Client(), authC.SessionToken))
	nativeReferralLink(t, f, b, csrfB, emailB, key, tgB, codeC)
	var originalA, originalB, originalC string
	var inviterB, inviterC uuid.UUID
	if f.env.Pool.QueryRow(ctx, `SELECT COALESCE(a.original_kind,a.kind),COALESCE(b.original_kind,b.kind),COALESCE(c.original_kind,c.kind),rb.referrer_account_id,rc.referrer_account_id FROM accounts a,accounts b,accounts c,referrals rb,referrals rc WHERE a.id=$1 AND b.id=$2 AND c.id=$3 AND rb.referred_account_id=b.id AND rc.referred_account_id=c.id`, aid, bid, authC.Account.AccountId).Scan(&originalA, &originalB, &originalC, &inviterB, &inviterC) != nil || originalA != "web" || originalB != "web" || originalC != "telegram" || inviterB != aid || inviterC != bid {
		t.Fatal("later signed linking changed original channel/referral graph")
	}
	bot.reason(tgB, "/referrals")
	wait(t, func() bool { return bot.message(tgB, "Ожидает дней: 10").ID > 0 })
	nativeClientCallback(bot, tgB, nativeMessage{ID: 7001, Chat: tgB, Markup: json.RawMessage(`{"inline_keyboard":[[{"text":"Invitations","callback_data":"referral"}]]}`)}, "referral")
	wait(t, func() bool { return bot.message(tgB, "Pending days: 10").ID > 0 })
	bot.starsMoney(proof, false)
	bot.starsMoney(proof, false)
	wait(t, func() bool { bot.mu.Lock(); defer bot.mu.Unlock(); return len(bot.updates) == 0 })
	if !reflect.DeepEqual(nativeRewardFacts(t, f, order.OrderId), facts) {
		t.Fatal("callback replay/link-account changed reward IDs/jobs")
	}
	stop()
	// Both jobs survive provider failure and a new application/worker instance.
	offline.Store(true)
	nativeRewardModules(t, f, key)
	_, stop = launchNative(t, f, bot, true, true, true)
	nativeRewardRetry(t, f, facts)
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='referral_reward' AND attempted_at IS NOT NULL AND state IN ('retryable','scheduled')`).Scan(&count) == nil && count == 2
	})
	if !reflect.DeepEqual(nativeRewardFacts(t, f, order.OrderId), facts) {
		t.Fatal("provider failure marked rewards granted or changed durable facts")
	}
	if status, _, _ := f.send(t, f.public.Client(), "GET", "/healthz", nil, "", "", false); status != 200 {
		t.Fatal("provider outage stopped the Go application", status)
	}
	stop()
	offline.Store(false)
	loseUpdate.Store(true)
	nativeRewardModules(t, f, key)
	_, stop = launchNative(t, f, bot, true, true, true)
	nativeRewardRetry(t, f, facts)
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM referrer_rewards r JOIN access_operations a ON a.id=r.access_operation_id WHERE r.source_order_id=$1 AND ((r.reward_level=1 AND r.rewarded_at IS NOT NULL AND a.status='applied') OR (r.reward_level=2 AND r.rewarded_at IS NULL AND a.status='needs_review'))`, order.OrderId).Scan(&count) == nil && count == 2
	})
	retained := nativeRewardFacts(t, f, order.OrderId)
	if retained[0].Access == nil || retained[1].Access == nil || !retained[0].Granted || retained[1].Granted {
		t.Fatal("late new-client issue or ambiguous existing-client issue changed grant proof")
	}
	frozen := nativeRewardTarget(t, f, *retained[1].Access)
	newTarget := nativeRewardTarget(t, f, *retained[0].Access)
	if frozen.VPNID != before.VPNID || frozen.SubID != before.SubID || frozen.PanelKey != before.PanelKey || frozen.DeviceCount != before.DeviceCount || frozen.TrafficLimitBytes != before.TrafficLimitBytes || frozen.ExpiryTimeMS != before.ExpiryTimeMS+int64(3*24*time.Hour/time.Millisecond) || newTarget.TrafficLimitBytes != 0 || newTarget.DeviceCount != f.cfg.Subscriptions.TrialDevices {
		t.Fatal("reward changed existing access identity/limits or new-client bonus policy")
	}
	var created time.Time
	if f.env.Pool.QueryRow(ctx, `SELECT created_at FROM access_operations WHERE id=$1`, *retained[0].Access).Scan(&created) != nil || newTarget.ExpiryTimeMS != created.UnixMilli()+int64(10*24*time.Hour/time.Millisecond) {
		t.Fatal("late issue lost configured bonus period")
	}
	stop()
	clock.Add(int64(2 * time.Hour / time.Second))
	readbackOffline.Store(false)
	nativeRewardModules(t, f, key)
	runtime, stop = launchNative(t, f, bot, true, true, true)
	wait(t, runtime.StarsGateway().Ready)
	nativeRewardRetry(t, f, retained)
	authA := nativeReferralMini(t, f, key, tgA, codeB)
	authB := nativeReferralMini(t, f, key, tgB, codeC)
	if authA.Account.AccountId != aid || authB.Account.AccountId != bid || !reflect.DeepEqual(nativeRewardFacts(t, f, order.OrderId), retained) || !reflect.DeepEqual(nativeRewardTarget(t, f, *retained[1].Access), frozen) {
		t.Fatal("worker restart lost reward/access identity or recomputed absolute expiry")
	}
	path := "/api/v1/operator/clients/" + aid.String() + "/access-operations/" + retained[1].Access.String() + "/reconcile"
	input := map[string]any{"reason": "Owned referral readback recovery", "acknowledge_reset_cost": false}
	for _, person := range []struct {
		client *http.Client
		email  string
		csrf   *string
	}{{a, emailA, &csrfA}, {b, emailB, &csrfB}} {
		status, raw, _ = f.send(t, person.client, "POST", "/api/v1/auth/login", map[string]string{"email": person.email, "password": "fixture password with Unicode ✨"}, "", "", false)
		var login wire.LoginResult
		if status != 200 || json.Unmarshal(raw, &login) != nil {
			t.Fatal("owned current cookie session unavailable", status)
		}
		*person.csrf = login.CsrfToken
	}
	if status, _, _ := f.send(t, b, "POST", path, input, csrfB, uuid.NewString(), false); status != 403 {
		t.Fatal("customer reconciled another account's reward", status)
	}
	status, _, _ = f.send(t, a, "POST", path, input, csrfA, uuid.NewString(), false)
	if status != 200 && status != 202 {
		t.Fatal("protected existing access reconciliation failed", status)
	}
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM referrer_rewards WHERE source_order_id=$1 AND rewarded_at IS NOT NULL`, order.OrderId).Scan(&count) == nil && count == 2
	})
	final := nativeRewardFacts(t, f, order.OrderId)
	for i := range final {
		prior := retained[i]
		prior.Granted = true
		if !reflect.DeepEqual(final[i], prior) {
			t.Fatal("recovery changed stable source/reward/job/access IDs")
		}
	}
	if aRead, bRead := nativeReferralRead(t, f, f.public.Client(), authA.SessionToken), nativeReferralRead(t, f, f.public.Client(), authB.SessionToken); aRead.Levels[1].GrantedDays != "3" || aRead.Levels[1].PendingDays != "0" || bRead.Levels[0].GrantedDays != "10" || bRead.Levels[0].PendingDays != "0" {
		t.Fatal("HTTP pending-to-granted proof did not follow applied access")
	}
	panelClient := vpn.NewPanelClient(f.cfg.VPN.Panel)
	defer panelClient.Close()
	for _, target := range []vpn.AccessTarget{frozen, newTarget} {
		view, err := panelClient.GetClient(ctx, target.PanelKey)
		expected := vpn.ProvisionTarget{PanelKey: target.PanelKey, VPNID: target.VPNID, SubID: target.SubID, ExpiryTimeMS: target.ExpiryTimeMS, DeviceCount: target.DeviceCount, TrafficLimitBytes: target.TrafficLimitBytes, Banned: target.Banned}
		if err != nil || !vpn.Matches(view, expected, f.cfg.Accounts.Now()) || !reflect.DeepEqual(view.InboundIDs, target.InboundIDs) || view.UsedTraffic == nil || *view.UsedTraffic != 8192 {
			t.Fatal("native provider readback changed granted expiry/limits/traffic/identity")
		}
	}
	if updates.Load() != 1 || resets.Load() != 0 {
		t.Fatal("ambiguous recovery repeated period update or reset bonus traffic", updates.Load(), resets.Load())
	}
	bot.starsMoney(proof, false)
	wait(t, func() bool { bot.mu.Lock(); defer bot.mu.Unlock(); return len(bot.updates) == 0 })
	if !reflect.DeepEqual(nativeRewardFacts(t, f, order.OrderId), final) {
		t.Fatal("post-recovery callback repeated reward delivery")
	}
	var rewards, jobs, accesses, requested, applied, receipts, atomicGrants, self int
	if f.env.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM referrer_rewards WHERE source_order_id=$1),(SELECT count(*) FROM river_job WHERE kind='referral_reward'),(SELECT count(*) FROM access_operations WHERE kind='compensate' AND operator_account_id IS NULL),(SELECT count(*) FROM audit_events WHERE action='access_requested' AND access_operation_id IN (SELECT access_operation_id FROM referrer_rewards WHERE source_order_id=$1)),(SELECT count(*) FROM audit_events WHERE action='access_applied' AND access_operation_id IN (SELECT access_operation_id FROM referrer_rewards WHERE source_order_id=$1)),(SELECT count(*) FROM purchase_receipts WHERE order_id=$1),(SELECT count(*) FROM referrer_rewards r JOIN access_operations a ON a.id=r.access_operation_id WHERE r.source_order_id=$1 AND r.rewarded_at=a.updated_at AND a.status='applied'),(SELECT count(*) FROM referrals WHERE referred_account_id=referrer_account_id)`, order.OrderId).Scan(&rewards, &jobs, &accesses, &requested, &applied, &receipts, &atomicGrants, &self) != nil || rewards != 2 || jobs != 2 || accesses != 2 || requested != 2 || applied != 2 || receipts != 1 || atomicGrants != 2 || self != 0 {
		t.Fatal("native reward proof/audit multiplicity changed", rewards, jobs, accesses, requested, applied, receipts, atomicGrants, self)
	}
}

type nativePreparedReward struct {
	f                       *fixture
	bot                     *nativeBot
	key                     ed25519.PrivateKey
	stop                    func()
	operator                *http.Client
	csrf                    string
	account, reward, access uuid.UUID
	order                   uuid.UUID
}

func nativePrepareReward(t *testing.T, configure func(*fixture)) nativePreparedReward {
	t.Helper()
	f, bot, key, plan := nativeStarsFixture(t)
	f.cfg.Bonuses = bonuses.RewardConfig{Enabled: true, LevelOneDays: 10, LevelTwoDays: 3}
	if configure != nil {
		configure(f)
	}
	nativeRewardModules(t, f, key)
	runtime, stop := launchNative(t, f, bot, true, false, true)
	wait(t, runtime.StarsGateway().Ready)
	inviter, _, account := f.signupAccount(t, nativeEmail("pending-referral"))
	operator, csrf, operatorID := f.signupAccount(t, nativeEmail("recovery-operator"))
	ctx := context.Background()
	if f.svc.Accounts.ChangeOperatorRole(ctx, operatorID, true) != nil {
		t.Fatal("owned recovery operator unavailable")
	}
	tg := int64(uuid.New().ID()) + 1000000000
	nativeReferralMini(t, f, key, tg, nativeReferralCode(t, nativeReferralRead(t, f, inviter)))
	_, order := nativeStarsOrder(t, f, key, tg, plan)
	bot.starsMoney(nativeStarsProof(tg, order.OrderId, "owned-pending-reward-charge", time.Now()), false)
	var reward, access uuid.UUID
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT id FROM referrer_rewards WHERE source_order_id=$1 AND account_id=$2`, order.OrderId, account).Scan(&reward) == nil
	})
	var snooze *river.JobSnoozeError
	if err := f.svc.Bonuses.ProcessReward(ctx, reward); !errors.As(err, &snooze) || f.env.Pool.QueryRow(ctx, `SELECT access_operation_id FROM referrer_rewards WHERE id=$1`, reward).Scan(&access) != nil || access == uuid.Nil {
		t.Fatal("owned public reward preparation did not retain one intent", err)
	}
	return nativePreparedReward{f: f, bot: bot, key: key, stop: stop, operator: operator, csrf: csrf, account: account, reward: reward, access: access, order: order.OrderId}
}

func TestNativeReferralRewardProfileReadRetry(t *testing.T) {
	var unavailable atomic.Bool
	p := nativePrepareReward(t, func(f *fixture) {
		f.panel.mu.Lock()
		f.panel.loseReply = false
		f.panel.mu.Unlock()
		provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if unavailable.Load() && r.Method == http.MethodGet && r.URL.Path == "/panel/api/inbounds/list" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			f.panel.serve(w, r)
		}))
		t.Cleanup(provider.Close)
		f.cfg.VPN.Panel.PanelURL = provider.URL
		f.cfg.VPN.Panel.PanelRootCAs = x509.NewCertPool()
		f.cfg.VPN.Panel.PanelRootCAs.AddCert(provider.Certificate())
	})
	ctx := context.Background()
	frozen := nativeRewardTarget(t, p.f, p.access)
	p.stop()
	unavailable.Store(true)
	nativeRewardModules(t, p.f, p.key)
	_, stop := launchNative(t, p.f, p.bot, true, true, true)
	defer stop()
	var status, reason, jobState string
	var write, reset, granted bool
	wait(t, func() bool {
		return p.f.env.Pool.QueryRow(ctx, `SELECT a.status,COALESCE(a.review_reason,''),a.write_started,a.reset_started,r.rewarded_at IS NOT NULL,j.state FROM access_operations a JOIN referrer_rewards r ON r.access_operation_id=a.id JOIN river_job j ON j.kind='access_operation' AND j.args->>'operation_id'=a.id::text WHERE a.id=$1 AND (a.status='needs_review' OR (a.status='pending' AND a.attempts>0 AND j.state='retryable'))`, p.access).Scan(&status, &reason, &write, &reset, &granted, &jobState) == nil
	})
	if status != "pending" || jobState != "retryable" || write || reset || granted {
		t.Fatal("prewrite profile HTTP503 requires automatic retry with retained intent", status, reason, write, reset, granted, jobState)
	}
	if !reflect.DeepEqual(nativeRewardTarget(t, p.f, p.access), frozen) {
		t.Fatal("prewrite outage changed the retained absolute target")
	}
	unavailable.Store(false)
	var job int64
	wait(t, func() bool {
		return p.f.env.Pool.QueryRow(ctx, `SELECT id FROM river_job WHERE kind='access_operation' AND args->>'operation_id'=$1 AND state='retryable' ORDER BY id LIMIT 1`, p.access.String()).Scan(&job) == nil
	})
	if _, err := p.f.workers.JobRetry(ctx, job); err != nil {
		t.Fatal("durable native access retry unavailable", err)
	}
	wait(t, func() bool {
		return p.f.env.Pool.QueryRow(ctx, `SELECT a.status='applied' AND r.rewarded_at IS NOT NULL FROM access_operations a JOIN referrer_rewards r ON r.access_operation_id=a.id WHERE a.id=$1`, p.access).Scan(&granted) == nil && granted
	})
	var accesses, grants int
	if p.f.env.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM access_operations WHERE account_id=$1 AND kind='compensate'),(SELECT count(*) FROM audit_events WHERE action='referral_reward_granted' AND access_operation_id=$2)`, p.account, p.access).Scan(&accesses, &grants) != nil || accesses != 1 || grants != 1 || !reflect.DeepEqual(nativeRewardTarget(t, p.f, p.access), frozen) {
		t.Fatal("automatic profile recovery replaced intent or repeated a grant")
	}
}

func TestNativeReferralRewardFinalAuditRollback(t *testing.T) {
	p := nativePrepareReward(t, nil)
	ctx := context.Background()
	frozen := nativeRewardTarget(t, p.f, p.access)
	if _, err := p.f.env.Pool.Exec(ctx, `CREATE FUNCTION reject_owned_referral_final_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='referral_reward_granted' THEN RAISE EXCEPTION 'owned referral final audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_owned_referral_final_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_owned_referral_final_audit()`); err != nil {
		t.Fatal("owned audit failure fixture unavailable", err)
	}
	p.stop()
	nativeRewardModules(t, p.f, p.key)
	_, stop := launchNative(t, p.f, p.bot, true, true, true)
	defer stop()
	var held bool
	wait(t, func() bool {
		return p.f.env.Pool.QueryRow(ctx, `SELECT status='needs_review' AND review_reason='bonus_status_failed' AND write_started FROM access_operations WHERE id=$1`, p.access).Scan(&held) == nil && held
	})
	var accessApplied, rewarded, assigned bool
	var grantAudit, accessAudit int
	if p.f.env.Pool.QueryRow(ctx, `SELECT a.status='applied',r.rewarded_at IS NOT NULL,c.assigned_panel_id IS NOT NULL,(SELECT count(*) FROM audit_events WHERE access_operation_id=a.id AND action='referral_reward_granted'),(SELECT count(*) FROM audit_events WHERE access_operation_id=a.id AND action='access_applied') FROM access_operations a JOIN referrer_rewards r ON r.access_operation_id=a.id JOIN accounts c ON c.id=r.account_id WHERE a.id=$1`, p.access).Scan(&accessApplied, &rewarded, &assigned, &grantAudit, &accessAudit) != nil || accessApplied || rewarded || assigned || grantAudit != 0 || accessAudit != 0 {
		t.Fatal("final referral audit failure partially committed grant/access/assignment", accessApplied, rewarded, assigned, grantAudit, accessAudit)
	}
	panelClient := vpn.NewPanelClient(p.f.cfg.VPN.Panel)
	defer panelClient.Close()
	expected := vpn.ProvisionTarget{PanelKey: frozen.PanelKey, VPNID: frozen.VPNID, SubID: frozen.SubID, ExpiryTimeMS: frozen.ExpiryTimeMS, DeviceCount: frozen.DeviceCount, TrafficLimitBytes: frozen.TrafficLimitBytes, Banned: frozen.Banned}
	view, err := panelClient.GetClient(ctx, frozen.PanelKey)
	if err != nil || !vpn.Matches(view, expected, time.Now()) {
		t.Fatal("audit failure fixture did not first confirm the external absolute target")
	}
	var buyerApplied bool
	wait(t, func() bool {
		return p.f.env.Pool.QueryRow(ctx, `SELECT fulfillment_status='applied' FROM purchase_orders WHERE id=$1`, p.order).Scan(&buyerApplied) == nil && buyerApplied
	})
	p.f.panel.mu.Lock()
	adds := p.f.panel.adds
	p.f.panel.mu.Unlock()
	if _, err = p.f.env.Pool.Exec(ctx, `DROP TRIGGER reject_owned_referral_final_audit ON audit_events; DROP FUNCTION reject_owned_referral_final_audit()`); err != nil {
		t.Fatal("owned audit failure fixture removal failed", err)
	}
	path := "/api/v1/operator/clients/" + p.account.String() + "/access-operations/" + p.access.String() + "/reconcile"
	status, _, _ := p.f.send(t, p.operator, "POST", path, map[string]any{"reason": "Owned final audit recovery", "acknowledge_reset_cost": false}, p.csrf, uuid.NewString(), false)
	if status != 202 {
		t.Fatal("native protected audit recovery failed", status)
	}
	wait(t, func() bool {
		return p.f.env.Pool.QueryRow(ctx, `SELECT a.status='applied' AND r.rewarded_at IS NOT NULL FROM access_operations a JOIN referrer_rewards r ON r.access_operation_id=a.id WHERE a.id=$1`, p.access).Scan(&held) == nil && held
	})
	view, err = panelClient.GetClient(ctx, frozen.PanelKey)
	if err != nil || !vpn.Matches(view, expected, time.Now()) || !reflect.DeepEqual(nativeRewardTarget(t, p.f, p.access), frozen) {
		t.Fatal("audit recovery recomputed expiry or changed source access identity")
	}
	var accesses int
	if p.f.env.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE access_operation_id=$1 AND action='referral_reward_granted'),(SELECT count(*) FROM audit_events WHERE access_operation_id=$1 AND action='access_applied'),(SELECT count(*) FROM access_operations WHERE account_id=$2 AND kind='compensate')`, p.access, p.account).Scan(&grantAudit, &accessAudit, &accesses) != nil || grantAudit != 1 || accessAudit != 1 || accesses != 1 {
		t.Fatal("final recovery repeated access intent or grant audit")
	}
	p.f.panel.mu.Lock()
	afterAdds := p.f.panel.adds
	p.f.panel.mu.Unlock()
	if afterAdds != adds {
		t.Fatal("audit recovery repeated an external client creation")
	}
}
