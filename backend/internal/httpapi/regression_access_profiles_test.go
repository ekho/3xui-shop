package httpapi

import (
	"context"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"testing"
	"time"
)

func profileInput(value string) *wire.AccessOperationInputProfile {
	p := wire.AccessOperationInputProfile(value)
	return &p
}

// A mistaken implementation that replaces limits or unmanaged memberships on
// a regular-to-euru switch fails this test at the panel and subscription edges.
func TestRegressionAccessSetProfilePreservesConditions(t *testing.T) {
	s, _, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	p.ids = append(p.ids, 99)
	beforeExpiry, beforeLimit, beforeTraffic := integer(t, p.client["expiryTime"]), integer(t, p.client["limitIp"]), integer(t, p.client["totalGB"])
	beforeID, beforeSub, beforeKey := p.client["id"], p.client["subId"], p.client["email"]
	beforeUsed := p.up
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("euru"), Reason: "switch profile"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	view, err := s.subscription(ctx, target)
	if err != nil || op.Desired.Profile != "euru" || view.AccessProfile != "euru" || view.VpnBanned || p.resets != 0 || p.up != beforeUsed || integer(t, p.client["expiryTime"]) != beforeExpiry || integer(t, p.client["limitIp"]) != beforeLimit || integer(t, p.client["totalGB"]) != beforeTraffic || p.client["id"] != beforeID || p.client["subId"] != beforeSub || p.client["email"] != beforeKey || len(p.ids) != 2 || !(p.ids[0] == 3 && p.ids[1] == 99 || p.ids[0] == 99 && p.ids[1] == 3) {
		t.Fatalf("profile change altered unrelated access: %v, %+v; expiry=%d/%d limit=%d/%d traffic=%d/%d used=%d/%d ids=%v identity=%v/%v/%v", err, view, integer(t, p.client["expiryTime"]), beforeExpiry, integer(t, p.client["limitIp"]), beforeLimit, integer(t, p.client["totalGB"]), beforeTraffic, p.up, beforeUsed, p.ids, p.client["id"], p.client["subId"], p.client["email"])
	}
}

// An accidental account restriction or an implicit unban by reset fails here.
func TestRegressionAccessSetVPNBanIndependentAndRetained(t *testing.T) {
	s, _, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	banned := true
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_vpn_ban", VpnBanned: &banned, Reason: "support ban"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	view, err := s.subscription(ctx, target)
	if err != nil || !view.VpnBanned || view.Status != "banned" || view.AccessProfile != "regular" || p.client["enable"] != false {
		t.Fatalf("VPN ban absent: %v, %+v", err, view)
	}
	reset, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "reset_traffic", Reason: "monthly-like reset"})
	if err != nil || s.applyAccess(ctx, reset.OperationId) != nil || p.client["enable"] != false {
		t.Fatalf("reset lost ban: %v", err)
	}
}

// A saved intent must not fabricate a grant, and first provisioning must use
// the saved profile and apply the ban after the native client is created.
func TestRegressionAccessNoClientIntentUsedByFirstTrial(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	ctx := context.Background()
	actor := verified(t, s, e, "intent-operator@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	target := verified(t, s, e, "intent-target@example.test")
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("euru"), Reason: "future profile"})
	if err != nil || op.Status != "applied" || len(op.CompletedSteps) != 1 || op.CompletedSteps[0] != "intent_saved" {
		t.Fatalf("profile intent: %v, %+v", err, op)
	}
	banned := true
	ban, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_vpn_ban", VpnBanned: &banned, Reason: "future ban"})
	if err != nil || ban.Status != "applied" || count(t, e, "trial_grants") != 0 || p.adds != 0 {
		t.Fatalf("ban intent fabricated access: %v", err)
	}
	view, err := s.subscription(ctx, target)
	if err != nil || view.Status != "none" || view.AccessProfile != "euru" || !view.VpnBanned {
		t.Fatalf("saved intent not visible: %v, %+v", err, view)
	}
	request, _, err := s.createTrialRequest(ctx, target, uuid.New(), wire.TrialRequestInput{})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := s.decideTrialRequest(ctx, request.RequestId, decision(101, "approve"))
	if err != nil || approved.OperationId == nil {
		t.Fatalf("approve: %v", err)
	}
	if err = s.provision(ctx, *approved.OperationId); err != nil {
		t.Fatal(err)
	}
	view, err = s.subscription(ctx, target)
	if err != nil || view.Status != "banned" || view.AccessProfile != "euru" || !view.VpnBanned || p.adds != 1 || p.client["enable"] != false || len(p.ids) != 1 || p.ids[0] != 3 || count(t, e, "trial_grants") != 1 {
		t.Fatalf("first grant lost intent: %v, %+v", err, view)
	}
}

func TestRegressionAccessSavedProfileFirstTrialKeysFollowGrantedClient(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	ctx := context.Background()
	actor := verified(t, s, e, "first-key-operator@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	target := verified(t, s, e, "first-key-target@example.test")
	var vpnID uuid.UUID
	var subID string
	if err := e.Pool.QueryRow(ctx, `SELECT vpn_id,sub_id FROM accounts WHERE id=$1`, target).Scan(&vpnID, &subID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("euru"), Reason: "saved first profile"}); err != nil {
		t.Fatal(err)
	}
	request, _, err := s.createTrialRequest(ctx, target, uuid.New(), wire.TrialRequestInput{})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := s.decideTrialRequest(ctx, request.RequestId, decision(101, "approve"))
	if err != nil || decision.OperationId == nil {
		t.Fatalf("trial approval: %v", err)
	}
	if err := s.provision(ctx, *decision.OperationId); err != nil {
		t.Fatal(err)
	}
	view, err := s.subscription(ctx, target)
	if err != nil || view.Status != "active" || view.AccessProfile != "euru" || p.client["id"] != vpnID.String() || p.client["subId"] != subID {
		t.Fatalf("saved profile changed first grant identity or status: %v %+v", err, view)
	}
	for _, read := range []func() (wire.SubscriptionKey, error){
		func() (wire.SubscriptionKey, error) { return s.subscriptionKey(ctx, target) },
		func() (wire.SubscriptionKey, error) { return s.operatorClientKey(ctx, actor, target) },
	} {
		key, err := read()
		if err != nil || key.SubscriptionUrl != "https://subscriptions.example.test/sub/"+subID {
			t.Fatalf("confirmed first grant key unavailable: %v %+v", err, key)
		}
	}
	banned := true
	ban, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_vpn_ban", VpnBanned: &banned, Reason: "suspend access"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.subscriptionKey(ctx, target); status(err) != 409 {
		t.Fatalf("pending ban allowed key: %v", err)
	}
	if err := s.applyAccess(ctx, ban.OperationId); err != nil {
		t.Fatal(err)
	}
	for _, read := range []func() (wire.SubscriptionKey, error){
		func() (wire.SubscriptionKey, error) { return s.subscriptionKey(ctx, target) },
		func() (wire.SubscriptionKey, error) { return s.operatorClientKey(ctx, actor, target) },
	} {
		if _, err := read(); status(err) != 403 {
			t.Fatalf("banned key escaped: %v", err)
		}
	}
}

// Entering unlimited must bind one hidden current revision and revoking it
// returns to the configured starter period, without changing native identity.
func TestRegressionAccessUnlimitedCurrentRevisionAndRevoke(t *testing.T) {
	s, _, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	seed, _, err := s.seedUnlimitedCatalogue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeID, beforeSub := p.client["id"], p.client["subId"]
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("unlimited"), Reason: "upgrade"})
	if err != nil {
		t.Fatal(err)
	}
	if op.Desired.Profile != "unlimited" || op.Desired.PlanId == nil || *op.Desired.PlanId != seed.PlanId || op.Desired.Revision == nil || *op.Desired.Revision != seed.Revision || op.Desired.ExpiresAt != nil || op.Desired.Devices != 7 || op.Desired.TrafficLimitBytes != 100*1024*1024*1024 {
		t.Fatalf("wrong unlimited snapshot: %+v", op.Desired)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	view, err := s.subscription(ctx, target)
	if err != nil || view.AccessProfile != "unlimited" || view.ExpiresAt != nil || p.resets != 0 || integer(t, p.client["limitIp"]) != 8 || p.client["id"] != beforeID || p.client["subId"] != beforeSub || len(p.ids) != 3 {
		t.Fatalf("unlimited not applied: %v, %+v", err, view)
	}
	revoke, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("regular"), Reason: "revoke"})
	if err != nil || revoke.Desired.ExpiresAt == nil || revoke.Desired.Devices != s.cfg.Subscriptions.TrialDevices || revoke.Desired.TrafficLimitBytes != s.cfg.Subscriptions.TrialTrafficGB*1024*1024*1024 {
		t.Fatalf("starter snapshot: %v, %+v", err, revoke.Desired)
	}
	if err = s.applyAccess(ctx, revoke.OperationId); err != nil {
		t.Fatal(err)
	}
	view, err = s.subscription(ctx, target)
	if err != nil || view.AccessProfile != "regular" || view.ExpiresAt == nil || p.resets != 1 || integer(t, p.client["limitIp"]) != s.cfg.Subscriptions.TrialDevices+1 || p.client["id"] != beforeID || p.client["subId"] != beforeSub || len(p.ids) != 2 {
		current, _ := s.getAccessOperation(ctx, actor, target, revoke.OperationId)
		t.Fatalf("unlimited revoke failed: %v, %+v; operation=%+v; ids=%v resets=%d", err, view, current, p.ids, p.resets)
	}
}

func TestRegressionAccessUnlimitedRevisionChangeQueuesCurrentTerms(t *testing.T) {
	s, e, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	seed, _, err := s.seedUnlimitedCatalogue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("unlimited"), Reason: "current unlimited terms"}
	first, err := s.createAccessOperation(ctx, actor, target, uuid.New(), input)
	if err != nil || s.applyAccess(ctx, first.OperationId) != nil {
		t.Fatalf("first unlimited revision: %v", err)
	}
	beforeID, beforeSub, beforeTraffic := p.client["id"], p.client["subId"], p.up
	terms := wire.CatalogueTerms{Devices: 8, TrafficGb: 200, Profile: "unlimited", Hidden: true, Periods: []int64{}, Prices: []wire.CataloguePrice{}}
	revised, err := s.reviseCataloguePlan(ctx, actor, seed.PlanId, uuid.New(), wire.CataloguePlanRevisionInput{ExpectedRevision: seed.Revision, Terms: terms, Reason: "new unlimited limits"})
	if err != nil {
		t.Fatal(err)
	}
	beforeJobs := count(t, e, "river_job")
	key := uuid.New()
	second, err := s.createAccessOperation(ctx, actor, target, key, input)
	if err != nil || second.Status != "pending" || second.Desired.Revision == nil || *second.Desired.Revision != revised.Revision || second.Desired.Devices != 8 || second.Desired.TrafficLimitBytes != 200*1024*1024*1024 || count(t, e, "river_job") != beforeJobs+1 {
		t.Fatalf("changed hidden terms were treated as unchanged: %v %+v", err, second)
	}
	replay, err := s.createAccessOperation(ctx, actor, target, key, input)
	if err != nil || replay.OperationId != second.OperationId {
		t.Fatalf("changed revision replay: %v", err)
	}
	if err = s.applyAccess(ctx, second.OperationId); err != nil || integer(t, p.client["totalGB"]) != 200*1024*1024*1024 || integer(t, p.client["limitIp"]) != 9 || p.client["id"] != beforeID || p.client["subId"] != beforeSub || p.up != beforeTraffic || p.resets != 0 {
		t.Fatalf("new unlimited revision not applied without resetting usage: %v", err)
	}
	beforeJobs = count(t, e, "river_job")
	noOp, err := s.createAccessOperation(ctx, actor, target, uuid.New(), input)
	if err != nil || noOp.Status != "applied" || len(noOp.CompletedSteps) != 1 || noOp.CompletedSteps[0] != "state_unchanged" || count(t, e, "river_job") != beforeJobs {
		t.Fatalf("truly equal hidden terms queued again: %v %+v", err, noOp)
	}
}

// Same-state new keys must be observable but cannot enqueue a new panel write.
func TestRegressionAccessNewKeySameStateHasNoPanelEffect(t *testing.T) {
	s, e, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	beforeJobs := count(t, e, "river_job")
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("regular"), Reason: "confirm unchanged"})
	if err != nil || op.Status != "applied" || len(op.CompletedSteps) != 1 || op.CompletedSteps[0] != "state_unchanged" || count(t, e, "river_job") != beforeJobs || p.updates != 0 || p.resets != 0 {
		t.Fatalf("same-state produced effect: %v, %+v", err, op)
	}
}

func TestRegressionAccessSameStateRejectsNativeLimitDrift(t *testing.T) {
	s, _, p, actor, target, _ := accessActors(t)
	p.client["limitIp"] = 9
	_, err := s.createAccessOperation(context.Background(), actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("regular"), Reason: "native drift"})
	if !catalogueCode(err, "ACCESS_NOT_ELIGIBLE") {
		t.Fatalf("same-state request hid native drift: %v", err)
	}
}

// A rejected unlimited request must not reserve an access slot, queue a job,
// or change the native client. The catalogue and scheduler are both required.
func TestRegressionAccessUnlimitedRejectsUnreadySources(t *testing.T) {
	ctx := context.Background()
	seed := func(t *testing.T, s *regressionFixture) {
		t.Helper()
		if _, _, err := s.seedUnlimitedCatalogue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	insertCorruptPlan := func(t *testing.T, s *regressionFixture, terms string) {
		t.Helper()
		id := uuid.New()
		if _, err := s.pool.Exec(ctx, `INSERT INTO catalogue_plans(id,current_revision,current_devices,current_profile,current_hidden) VALUES($1,1,7,'unlimited',true)`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,source,changed_at) VALUES($1,1,$2::jsonb,false,'unlimited_seed',now())`, id, terms); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, code string
		noQueue    bool
		prepare    func(*testing.T, *regressionFixture, *fakePanel, uuid.UUID)
	}{
		{name: "no_hidden_plan", code: "ACCESS_PLAN_CONFLICT"},
		{name: "ambiguous_hidden_plans", code: "ACCESS_PLAN_CONFLICT", prepare: func(t *testing.T, s *regressionFixture, _ *fakePanel, actor uuid.UUID) {
			seed(t, s)
			terms := wire.CatalogueTerms{Devices: 8, TrafficGb: 100, Profile: "unlimited", Hidden: true, Periods: []int64{}, Prices: []wire.CataloguePrice{}}
			if _, err := s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: terms, Reason: "another hidden plan"}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "invalid_devices", code: "ACCESS_PLAN_CONFLICT", prepare: func(t *testing.T, s *regressionFixture, _ *fakePanel, _ uuid.UUID) {
			insertCorruptPlan(t, s, `{"devices":10001,"traffic_gb":100,"profile":"unlimited","hidden":true,"periods":[],"prices":[]}`)
		}},
		{name: "invalid_traffic", code: "ACCESS_PLAN_CONFLICT", prepare: func(t *testing.T, s *regressionFixture, _ *fakePanel, _ uuid.UUID) {
			insertCorruptPlan(t, s, `{"devices":7,"traffic_gb":100001,"profile":"unlimited","hidden":true,"periods":[],"prices":[]}`)
		}},
		{name: "missing_scheduler_queue", code: "SERVICE_UNAVAILABLE", noQueue: true, prepare: func(t *testing.T, s *regressionFixture, _ *fakePanel, _ uuid.UUID) {
			seed(t, s)
		}},
		{name: "invalid_scheduler_timezone", code: "SERVICE_UNAVAILABLE", prepare: func(t *testing.T, s *regressionFixture, _ *fakePanel, _ uuid.UUID) {
			seed(t, s)
			s.cfg.VPN.AccessResetTimezone = "Missing/Timezone"
		}},
		{name: "missing_period_schema", code: "SERVICE_UNAVAILABLE", prepare: func(t *testing.T, s *regressionFixture, _ *fakePanel, _ uuid.UUID) {
			seed(t, s)
			if _, err := s.pool.Exec(ctx, "DROP TABLE monthly_reset_periods"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unknown_membership", code: "ACCESS_NOT_ELIGIBLE", prepare: func(t *testing.T, s *regressionFixture, p *fakePanel, _ uuid.UUID) {
			seed(t, s)
			p.ids = []int64{777}
		}},
		{name: "empty_membership", code: "ACCESS_NOT_ELIGIBLE", prepare: func(t *testing.T, s *regressionFixture, p *fakePanel, _ uuid.UUID) {
			seed(t, s)
			p.ids = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e, p, actor, target, _ := accessActors(t)
			if tc.noQueue {
				clock := s.now
				s = newRegressionFixture(s.pool, s.limiter, nil, *s.cfg)
				s.now = clock
			}
			if tc.prepare != nil {
				tc.prepare(t, s, p, actor)
			}
			beforeOps, beforeJobs := count(t, e, "access_operations"), count(t, e, "river_job")
			beforeNative := [5]int{p.adds, p.updates, p.attaches, p.disables, p.resets}
			_, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("unlimited"), Reason: "guard unready source"})
			if !catalogueCode(err, tc.code) || count(t, e, "access_operations") != beforeOps || count(t, e, "river_job") != beforeJobs || [5]int{p.adds, p.updates, p.attaches, p.disables, p.resets} != beforeNative {
				t.Fatalf("unready unlimited source created an effect: error=%v", err)
			}
		})
	}
}

// Fresh unlimited issuance creates one native client with the account's
// persistent identity and never consumes the one-time trial grant.
func TestRegressionAccessUnlimitedFreshAccountWithoutTrialGrant(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	ctx := context.Background()
	actor := verified(t, s, e, "fresh-unlimited-operator@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	target := verified(t, s, e, "fresh-unlimited-target@example.test")
	if _, _, err := s.seedUnlimitedCatalogue(ctx); err != nil {
		t.Fatal(err)
	}
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("unlimited"), Reason: "grant unlimited"})
	if err != nil || op.Status != "pending" || op.Desired.ExpiresAt != nil {
		t.Fatalf("fresh unlimited request: %v, %+v", err, op)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	view, err := s.subscription(ctx, target)
	if err != nil || view.Status != "active" || view.AccessProfile != "unlimited" || p.adds != 1 || p.resets != 0 || count(t, e, "trial_grants") != 0 || len(p.ids) != 3 {
		t.Fatalf("fresh unlimited issuance: %v, %+v", err, view)
	}
}

// A banned bonus remains forbidden; once explicitly unbanned, a fresh bonus
// uses the stored EURU intent rather than silently provisioning regular.
func TestRegressionAccessBonusUsesSavedProfileAfterUnban(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	ctx := context.Background()
	actor := verified(t, s, e, "bonus-intent-operator@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	target := verified(t, s, e, "bonus-intent-target@example.test")
	if _, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("euru"), Reason: "save EURU"}); err != nil {
		t.Fatal(err)
	}
	banned := true
	if _, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_vpn_ban", VpnBanned: &banned, Reason: "hold"}); err != nil {
		t.Fatal(err)
	}
	bonus := wire.AccessOperationInput{Kind: "compensate", Days: ptrInt(1), Reason: "bonus"}
	if _, err := s.createAccessOperation(ctx, actor, target, uuid.New(), bonus); !catalogueCode(err, "ACCESS_NOT_ELIGIBLE") {
		t.Fatalf("banned bonus allowed: %v", err)
	}
	banned = false
	if _, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_vpn_ban", VpnBanned: &banned, Reason: "release"}); err != nil {
		t.Fatal(err)
	}
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), bonus)
	if err != nil || op.Desired.Profile != "euru" {
		t.Fatalf("bonus profile not saved: %v, %+v", err, op.Desired)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil || len(p.ids) != 1 || p.ids[0] != 3 {
		t.Fatalf("bonus provisioned wrong profile: %v ids=%v", err, p.ids)
	}
}

// Recovery belongs to the current reconciler, while the first operator stays
// recorded as author. Revoking that author must not block a different live one.
func TestRegressionAccessReconcilerRoleIndependentOfOriginalAuthor(t *testing.T) {
	s, e, p, author, target, _ := accessActors(t)
	ctx := context.Background()
	p.loseReset = true
	op, err := s.createAccessOperation(ctx, author, target, uuid.New(), wire.AccessOperationInput{Kind: "reset_traffic", Reason: "lost reply"})
	if err != nil || s.applyAccess(ctx, op.OperationId) != nil {
		t.Fatalf("ambiguous reset: %v", err)
	}
	if err = s.changeOperatorRole(ctx, author, false); err != nil {
		t.Fatal(err)
	}
	reconciler := verified(t, s, e, "access-reconciler@example.test")
	if err = s.changeOperatorRole(ctx, reconciler, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.reconcileAccessOperation(ctx, reconciler, target, op.OperationId, uuid.New(), wire.AccessReconcileInput{Reason: "verified cost", AcknowledgeResetCost: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	got, err := s.getAccessOperation(ctx, reconciler, target, op.OperationId)
	if err != nil || got.Status != "applied" || got.OperatorAccountId == nil || *got.OperatorAccountId != author || p.resets != 2 {
		t.Fatalf("B recovery blocked by A revoke: %v, %+v resets=%d", err, got, p.resets)
	}
}

func TestRegressionAccessRevokedReconcilerCannotRepeatAmbiguousReset(t *testing.T) {
	s, e, p, author, target, _ := accessActors(t)
	ctx := context.Background()
	p.loseReset = true
	op, err := s.createAccessOperation(ctx, author, target, uuid.New(), wire.AccessOperationInput{Kind: "reset_traffic", Reason: "lost reply"})
	if err != nil || s.applyAccess(ctx, op.OperationId) != nil {
		t.Fatalf("ambiguous reset: %v", err)
	}
	if err = s.changeOperatorRole(ctx, author, false); err != nil {
		t.Fatal(err)
	}
	reconciler := verified(t, s, e, "revoked-reconciler@example.test")
	if err = s.changeOperatorRole(ctx, reconciler, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.reconcileAccessOperation(ctx, reconciler, target, op.OperationId, uuid.New(), wire.AccessReconcileInput{Reason: "acknowledge cost", AcknowledgeResetCost: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.changeOperatorRole(ctx, reconciler, false); err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = e.Pool.QueryRow(ctx, "SELECT status FROM access_operations WHERE id=$1", op.OperationId).Scan(&status); err != nil || status != "needs_review" || p.resets != 1 {
		t.Fatalf("revoked reconciler wrote panel: %v status=%s resets=%d", err, status, p.resets)
	}
}

func TestRegressionAccessUnbanEnablesNativeButKeepsExpiredOrExhaustedStatus(t *testing.T) {
	for _, tc := range []struct {
		name, status string
	}{
		{"expired", "expired"},
		{"exhausted", "exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, p, actor, target, _ := accessActors(t)
			ctx := context.Background()
			if tc.name == "expired" {
				s.now = func() time.Time { return time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC) }
			} else {
				p.up = integer(t, p.client["totalGB"])
			}
			p.client["enable"] = false
			banned := true
			op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_vpn_ban", VpnBanned: &banned, Reason: "hold"})
			if err != nil || s.applyAccess(ctx, op.OperationId) != nil {
				t.Fatalf("set ban: %v", err)
			}
			banned = false
			op, err = s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_vpn_ban", VpnBanned: &banned, Reason: "unban"})
			if err != nil || s.applyAccess(ctx, op.OperationId) != nil {
				t.Fatalf("unban: %v", err)
			}
			view, err := s.subscription(ctx, target)
			if err != nil || string(view.Status) != tc.status || view.VpnBanned || p.client["enable"] != true {
				t.Fatalf("unban misreported eligibility: %v, %+v native=%v", err, view, p.client["enable"])
			}
		})
	}
}

func TestRegressionAccessExpiredUnlimitedGrantRestoresNativeAccess(t *testing.T) {
	s, _, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	if _, _, err := s.seedUnlimitedCatalogue(ctx); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC) }
	p.client["enable"] = false
	beforeID, beforeSub, beforeUsed := p.client["id"], p.client["subId"], p.up
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("unlimited"), Reason: "expired upgrade"})
	if err != nil || s.applyAccess(ctx, op.OperationId) != nil {
		t.Fatalf("expired unlimited: %v", err)
	}
	view, err := s.subscription(ctx, target)
	if err != nil || view.Status != "active" || view.ExpiresAt != nil || p.client["enable"] != true || p.client["id"] != beforeID || p.client["subId"] != beforeSub || p.up != beforeUsed || p.resets != 0 {
		t.Fatalf("expired upgrade failed to restore native access: %v, %+v", err, view)
	}
}
