package httpapi

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestRegressionAccessOperationInputAndForeignGuard(t *testing.T) {
	s, e := fixture(t)
	panelFixture(t, s)
	ctx := context.Background()
	actor := verified(t, s, e, "access-unready-operator@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	target, _ := approved(t, s, e, "access-unready-target@example.test")
	in := wire.AccessOperationInput{Kind: "compensate", Reason: "support correction", Days: ptrInt(7)}
	if _, err := s.createAccessOperation(ctx, actor, target, uuid.New(), in); !catalogueCode(err, "ACCESS_OPERATION_CONFLICT") {
		t.Fatalf("unresolved trial ignored: %v", err)
	}
}

func ptrInt(v int) *int { return &v }

func accessActors(t *testing.T) (*regressionFixture, *testkit.Env, *fakePanel, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	s, e := fixture(t)
	panel := panelFixture(t, s)
	ctx := context.Background()
	actor := verified(t, s, e, "access-operator@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	target, trial := approved(t, s, e, "access-target@example.test")
	if err := s.provision(ctx, trial); err != nil {
		t.Fatal(err)
	}
	return s, e, panel, actor, target, trial
}

func TestRegressionAccessCompensationPreservesConditions(t *testing.T) {
	s, e, p, actor, target, trial := accessActors(t)
	ctx := context.Background()
	beforeExpiry := integer(t, p.client["expiryTime"])
	beforeID, beforeSub, beforeKey := p.client["id"], p.client["subId"], p.client["email"]
	beforeLimit, beforeTraffic := integer(t, p.client["limitIp"]), integer(t, p.client["totalGB"])
	key := uuid.New()
	input := wire.AccessOperationInput{Kind: "compensate", Reason: "support correction", Days: ptrInt(7)}
	op, err := s.createAccessOperation(ctx, actor, target, key, input)
	if err != nil || op.Status != "pending" || op.Desired.ExpiresAt == nil || op.Desired.ExpiresAt.UnixMilli() != beforeExpiry+7*24*60*60*1000 {
		t.Fatalf("persisted absolute expiry: %v", err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	if p.updates != 1 || p.resets != 0 || p.adds != 1 || integer(t, p.client["expiryTime"]) != beforeExpiry+7*24*60*60*1000 || integer(t, p.client["limitIp"]) != beforeLimit || integer(t, p.client["totalGB"]) != beforeTraffic || p.up != 1234 || p.client["uuid"] != beforeID || p.client["subId"] != beforeSub || p.client["email"] != beforeKey {
		t.Fatal("compensation changed non-expiry state")
	}
	replay, err := s.createAccessOperation(ctx, actor, target, key, input)
	if err != nil || replay.OperationId != op.OperationId {
		t.Fatalf("replay: %v", err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil || p.updates != 1 {
		t.Fatalf("duplicate effect: %v", err)
	}
	view, err := s.subscription(ctx, target)
	if err != nil || view.AccessOperationId == nil || *view.AccessOperationId != op.OperationId || view.ExpiresAt == nil || view.ExpiresAt.UnixMilli() != op.Desired.ExpiresAt.UnixMilli() {
		t.Fatalf("current access source: %v", err)
	}
	var grants int
	if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM trial_grants WHERE operation_id=$1", trial).Scan(&grants); err != nil || grants != 1 {
		t.Fatalf("trial grant mutated: %v", err)
	}
	if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", target); err != nil {
		t.Fatal(err)
	}
	if _, err = s.subscriptionKey(ctx, target); !catalogueCode(err, "ACCOUNT_RESTRICTED") {
		t.Fatalf("restricted key after access: %v", err)
	}
	card, err := s.operatorClient(ctx, actor, target)
	if err != nil || card.Subscription.AccessOperationId == nil || *card.Subscription.AccessOperationId != op.OperationId {
		t.Fatalf("restricted operator card lost access: %v", err)
	}
}

func TestRegressionAccessExpiredCompensationRestoresNativeEnable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		refuse     bool
		exhausted  bool
		wantOp     string
		wantStatus string
		wantEnable bool
	}{
		{name: "restored", wantOp: "applied", wantStatus: "active", wantEnable: true},
		{name: "activation_refused", refuse: true, wantOp: "needs_review", wantStatus: "needs_review"},
		{name: "exhausted", exhausted: true, wantOp: "applied", wantStatus: "exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, p, actor, target, _ := accessActors(t)
			ctx := context.Background()
			p.client["expiryTime"] = json.Number("1")
			p.client["enable"] = false
			p.refuseActivation = tc.refuse
			beforeID, beforeSub, beforeKey := p.client["id"], p.client["subId"], p.client["email"]
			beforeLimit, beforeTraffic := integer(t, p.client["limitIp"]), integer(t, p.client["totalGB"])
			if tc.exhausted {
				p.up = beforeTraffic
			}
			beforeUsed := p.up
			op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "expired access", Days: ptrInt(1)})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.applyAccess(ctx, op.OperationId); err != nil {
				t.Fatal(err)
			}
			current, err := s.getAccessOperation(ctx, actor, target, op.OperationId)
			if err != nil || current.Status != wire.AccessOperationStatus(tc.wantOp) {
				t.Fatalf("operation claimed wrong result: %v status=%s", err, current.Status)
			}
			if p.client["enable"] != tc.wantEnable || p.up != beforeUsed || p.resets != 0 || integer(t, p.client["limitIp"]) != beforeLimit || integer(t, p.client["totalGB"]) != beforeTraffic || p.client["id"] != beforeID || p.client["subId"] != beforeSub || p.client["email"] != beforeKey || len(p.ids) != 2 {
				t.Fatal("compensation changed conditions or native enable state")
			}
			if !tc.refuse {
				view, err := s.subscription(ctx, target)
				if err != nil || view.Status != wire.SubscriptionStatus(tc.wantStatus) || view.ExpiresAt == nil || !view.ExpiresAt.After(s.now()) {
					t.Fatalf("subscription did not reflect extension: %v status=%s", err, view.Status)
				}
			}
		})
	}
}

func TestRegressionAccessRestoredSubscriptionLifecycle(t *testing.T) {
	for _, state := range []string{"expired", "exhausted"} {
		t.Run(state, func(t *testing.T) {
			s, _, p, actor, target, _ := accessActors(t)
			ctx := context.Background()
			p.client["expiryTime"] = json.Number("1")
			p.client["enable"] = false
			op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "restore access", Days: ptrInt(1)})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.applyAccess(ctx, op.OperationId); err != nil || p.client["enable"] != true {
				t.Fatalf("restoration not confirmed: %v", err)
			}
			p.client["enable"] = false // Native later disables an expired or depleted client.
			if state == "expired" {
				after := op.Desired.ExpiresAt.Add(time.Second)
				s.now = func() time.Time { return after }
			} else {
				p.up = integer(t, p.client["totalGB"])
			}
			view, err := s.subscription(ctx, target)
			if err != nil || view.Status != wire.SubscriptionStatus(state) || view.PanelError != nil || view.DataStale {
				t.Fatalf("legitimate native disable was treated as identity drift: %v status=%s", err, view.Status)
			}
		})
	}
}

func TestRegressionAccessUpdateConvertsNativeClientRecord(t *testing.T) {
	s, _, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	p.client["allowedIPs"] = "10.0.0.2/32, 10.0.0.3/32"
	p.client["comment"] = "manual note"
	p.client["limitHwid"] = 4
	p.strictNativeUpdate = true
	id := p.client["id"]
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "native record update", Days: ptrInt(1)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	current, err := s.getAccessOperation(ctx, actor, target, op.OperationId)
	if err != nil || current.Status != "applied" || p.updates != 1 || p.client["id"] != id || p.client["comment"] != "manual note" || p.client["limitHwid"] != json.Number("4") {
		t.Fatalf("native ClientRecord update rejected or lost raw fields: %v", err)
	}
}

func TestRegressionAccessRejectsInvalidAndBonusOnce(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	ctx := context.Background()
	actor := verified(t, s, e, "access-bonus-operator@example.test")
	target := verified(t, s, e, "access-bonus-target@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	for _, days := range []int{0, 366} {
		if _, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "bad", Days: ptrInt(days)}); err == nil {
			t.Fatal("invalid days accepted")
		}
	}
	in := wire.AccessOperationInput{Kind: "compensate", Reason: "bonus", Days: ptrInt(5)}
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), in)
	if err != nil {
		t.Fatal(err)
	}
	if op.Desired.TrafficLimitBytes != 0 || op.Desired.Devices != s.cfg.Subscriptions.TrialDevices {
		t.Fatal("bonus invented traffic cap")
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	if p.adds != 1 || p.resets != 0 {
		t.Fatal("bonus not once")
	}
	var grants int
	if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM trial_grants WHERE account_id=$1", target).Scan(&grants); err != nil || grants != 0 {
		t.Fatal("bonus invented first grant")
	}
	second, err := s.createAccessOperation(ctx, actor, target, uuid.New(), in)
	if err != nil {
		t.Fatalf("existing client compensation: %v", err)
	}
	if err = s.applyAccess(ctx, second.OperationId); err != nil || p.adds != 1 {
		t.Fatalf("bonus identity duplicated: %v", err)
	}
}

func TestRegressionAccessAssignmentAndResetPreserveIdentityAndBan(t *testing.T) {
	s, e, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	p.ids = append(p.ids, 99) // Unmanaged manual membership must survive a managed profile change.
	terms := catalogueTerms(4)
	terms.Profile = "euru"
	terms.TrafficGb = 20
	plan, err := s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: terms, Reason: "offer"})
	if err != nil {
		t.Fatal(err)
	}
	period := int64(30)
	rev := int64(1)
	in := wire.AccessOperationInput{Kind: "assign_plan", Reason: "manual", PlanId: &plan.PlanId, Revision: &rev, PeriodDays: &period}
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), in)
	if err != nil {
		t.Fatal(err)
	}
	if op.Desired.Revision == nil || *op.Desired.Revision != 1 || op.Desired.Profile != "euru" {
		t.Fatal("selected revision not frozen")
	}
	if _, err = s.reviseCataloguePlan(ctx, actor, plan.PlanId, uuid.New(), wire.CataloguePlanRevisionInput{ExpectedRevision: 1, Terms: catalogueTerms(5), Reason: "changed"}); err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	if integer(t, p.client["limitIp"]) != 5 || integer(t, p.client["totalGB"]) != 20*1024*1024*1024 || p.resets != 1 || p.up != 0 {
		t.Fatal("plan application mismatch")
	}
	if len(p.ids) != 2 || p.ids[0] != 99 && p.ids[1] != 99 {
		t.Fatal("unmanaged membership removed")
	}
	if _, err = s.createAccessOperation(ctx, actor, target, uuid.New(), in); !catalogueCode(err, "ACCESS_PLAN_CONFLICT") {
		t.Fatalf("stale plan accepted: %v", err)
	}
	if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET vpn_banned=true WHERE id=$1", target); err != nil {
		t.Fatal(err)
	}
	p.client["enable"] = false
	reset, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "reset_traffic", Reason: "reset"})
	if err != nil {
		t.Fatal(err)
	}
	p.up = 456
	if err = s.applyAccess(ctx, reset.OperationId); err != nil {
		t.Fatal(err)
	}
	if p.resets != 2 || p.disables < 1 || p.client["enable"] != false || p.up != 0 {
		t.Fatal("reset lost ban")
	}
	view, err := s.subscription(ctx, target)
	if err != nil || view.Status != "banned" || view.AccessProfile != "euru" {
		t.Fatalf("subscription after assign/reset: %v", err)
	}
}

func TestRegressionAccessReplayLostResponseAndTrialRace(t *testing.T) {
	s, e, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	p.loseReset = true
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "reset_traffic", Reason: "lost reply"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "reset_traffic", Reason: "collision"}); !catalogueCode(err, "ACCESS_OPERATION_CONFLICT") {
		t.Fatalf("active op guard: %v", err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	if p.resets != 1 {
		t.Fatal("lost response repeated reset")
	}
	got, err := s.getAccessOperation(ctx, actor, target, op.OperationId)
	if err != nil || got.Status != "needs_review" {
		t.Fatalf("lost reset reply was treated as confirmed: %v", err)
	}
	other := verified(t, s, e, "access-foreign@example.test")
	if _, err = s.getAccessOperation(ctx, actor, other, op.OperationId); err == nil {
		t.Fatal("foreign operation exposed")
	}
	if err = s.changeOperatorRole(ctx, actor, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "reset_traffic", Reason: "revoked"}); err == nil {
		t.Fatal("revoked role wrote")
	}
}

func TestRegressionAccessLostResetReplyWithZeroReadbackNeedsReview(t *testing.T) {
	s, e, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET vpn_banned=true WHERE id=$1", target); err != nil {
		t.Fatal(err)
	}
	p.client["enable"] = false
	p.loseReset = true
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "reset_traffic", Reason: "lost reset reply"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	got, err := s.getAccessOperation(ctx, actor, target, op.OperationId)
	if err != nil || got.Status != "needs_review" || p.up != 0 || p.resets != 1 || p.disables != 1 || p.client["enable"] != false {
		t.Fatalf("ambiguous reset with zero readback: %v", err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil || p.resets != 1 {
		t.Fatalf("ambiguous reset repeated: %v", err)
	}
	var resetStarted bool
	if err = e.Pool.QueryRow(ctx, "SELECT reset_started FROM access_operations WHERE id=$1", op.OperationId).Scan(&resetStarted); err != nil || !resetStarted {
		t.Fatalf("reset cost marker missing: %v", err)
	}
}

func TestRegressionAccessResetConsentConsumedBeforeNativeRetry(t *testing.T) {
	s, e, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	p.loseReset = true
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "reset_traffic", Reason: "ambiguous first reply"})
	if err != nil || s.applyAccess(ctx, op.OperationId) != nil || p.resets != 1 {
		t.Fatalf("initial ambiguous reset: %v resets=%d", err, p.resets)
	}
	if _, err = s.reconcileAccessOperation(ctx, actor, target, op.OperationId, uuid.New(), wire.AccessReconcileInput{Reason: "one more reset approved", AcknowledgeResetCost: true}); err != nil {
		t.Fatal(err)
	}
	p.loseReset = false
	if _, err = e.Pool.Exec(ctx, `CREATE FUNCTION fail_access_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		IF NEW.status IN ('applied','needs_review') THEN RAISE EXCEPTION 'controlled final-write outage'; END IF;
		RETURN NEW; END $$;
		CREATE TRIGGER fail_access_finish BEFORE UPDATE ON access_operations FOR EACH ROW EXECUTE FUNCTION fail_access_finish()`); err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err == nil || p.resets != 2 {
		t.Fatalf("second reset did not reach failed final write: %v resets=%d", err, p.resets)
	}
	if _, err = e.Pool.Exec(ctx, `DROP TRIGGER fail_access_finish ON access_operations; DROP FUNCTION fail_access_finish()`); err != nil {
		t.Fatal(err)
	}
	var acknowledged bool
	if err = e.Pool.QueryRow(ctx, `SELECT reset_acknowledged FROM access_operations WHERE id=$1`, op.OperationId).Scan(&acknowledged); err != nil || acknowledged {
		t.Fatalf("one reset consent survived external reset: %v acknowledged=%t", err, acknowledged)
	}
	p.up = 77 // Traffic accumulated after the acknowledged reset and before recovery.
	if err = s.applyAccess(ctx, op.OperationId); err != nil || p.resets != 2 || p.up != 77 {
		t.Fatalf("recovery erased new traffic: %v resets=%d traffic=%d", err, p.resets, p.up)
	}
	current, err := s.getAccessOperation(ctx, actor, target, op.OperationId)
	if err != nil || current.Status != "needs_review" {
		t.Fatalf("positive readback was not left for review: %v status=%s", err, current.Status)
	}
	if _, err = s.reconcileAccessOperation(ctx, actor, target, op.OperationId, uuid.New(), wire.AccessReconcileInput{Reason: "renew one reset", AcknowledgeResetCost: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil || p.resets != 3 || p.up != 0 {
		t.Fatalf("renewed consent did not permit one reset: %v resets=%d traffic=%d", err, p.resets, p.up)
	}
}

func TestRegressionAccessPartialResetRequiresExplicitCost(t *testing.T) {
	s, e, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET vpn_banned=true WHERE id=$1", target); err != nil {
		t.Fatal(err)
	}
	p.client["enable"] = false
	p.up = 999
	p.resetLeavesTraffic = true
	p.loseReset = true
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "reset_traffic", Reason: "controlled reset"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	current, err := s.getAccessOperation(ctx, actor, target, op.OperationId)
	if err != nil || current.Status != "needs_review" || p.resets != 1 || p.disables != 1 || p.client["enable"] != false {
		t.Fatalf("ambiguous reset/ban: %v", err)
	}
	if _, err = s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "blocked", Days: ptrInt(1)}); !catalogueCode(err, "ACCESS_OPERATION_CONFLICT") {
		t.Fatalf("review did not block: %v", err)
	}
	first, err := s.reconcileAccessOperation(ctx, actor, target, op.OperationId, uuid.New(), wire.AccessReconcileInput{Reason: "read only check", AcknowledgeResetCost: false})
	if err != nil || first.Status != "pending" {
		t.Fatalf("safe reconcile: %v", err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	current, err = s.getAccessOperation(ctx, actor, target, op.OperationId)
	if err != nil || current.Status != "needs_review" || p.resets != 1 || p.disables < 2 {
		t.Fatalf("blind reset repeated: %v", err)
	}
	p.resetLeavesTraffic = false
	second, err := s.reconcileAccessOperation(ctx, actor, target, op.OperationId, uuid.New(), wire.AccessReconcileInput{Reason: "operator accepts counter loss", AcknowledgeResetCost: true})
	if err != nil || second.Status != "pending" {
		t.Fatalf("explicit reconcile: %v", err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	current, err = s.getAccessOperation(ctx, actor, target, op.OperationId)
	if err != nil || current.Status != "applied" || p.resets != 2 || p.client["enable"] != false {
		t.Fatalf("explicit reset/ban failed: %v", err)
	}
}

func TestRegressionAccessStarterTrialAndNoPriceOverwrite(t *testing.T) {
	s, e, p, actor, target, trial := accessActors(t)
	ctx := context.Background()
	p.up = 4567
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "starter_trial", Reason: "support trial"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	if p.resets != 1 || p.up != 0 || integer(t, p.client["limitIp"]) != s.cfg.Subscriptions.TrialDevices+1 || integer(t, p.client["totalGB"]) != s.cfg.Subscriptions.TrialTrafficGB*1024*1024*1024 {
		t.Fatal("starter snapshot/reset")
	}
	var grants int
	if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM trial_grants WHERE operation_id=$1", trial).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("first trial grant rewritten")
	}
	var accessAudit int
	if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE access_operation_id=$1 AND operation_id IS NULL", op.OperationId).Scan(&accessAudit); err != nil || accessAudit < 2 {
		t.Fatal("access audit faked trial FK")
	}
}

func TestRegressionAccessPendingBonusBlocksFirstTrialAndOwner(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	ctx := context.Background()
	actor := verified(t, s, e, "access-race-operator@example.test")
	target := verified(t, s, e, "access-race-target@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended('account-access:'||$1::text,0))", target); err != nil {
		t.Fatal(err)
	}
	if _, err = s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "owner", Days: ptrInt(1)}); !catalogueCode(err, "ACCESS_OPERATION_CONFLICT") {
		t.Fatalf("owner lock ignored: %v", err)
	}
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended('account-access:'||$1::text,0))", target); err != nil {
		t.Fatal(err)
	}
	conn.Release()
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "bonus", Days: ptrInt(1)})
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.createTrialRequest(ctx, target, uuid.New(), wire.TrialRequestInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.decideTrialRequest(ctx, r.RequestId, decision(101, "approve")); !catalogueCode(err, "ACCESS_OPERATION_CONFLICT") {
		t.Fatalf("trial raced pending access: %v", err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil || p.adds != 1 {
		t.Fatalf("bonus not applied once: %v", err)
	}
}

func TestRegressionAccessCompensationRejectsChangedProfileAndUnsafeState(t *testing.T) {
	s, e, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	in := wire.AccessOperationInput{Kind: "compensate", Reason: "safe only", Days: ptrInt(2)}
	originalIDs := append([]int64{}, p.ids...)
	p.ids = []int64{9}
	if _, err := s.createAccessOperation(ctx, actor, target, uuid.New(), in); !catalogueCode(err, "ACCESS_NOT_ELIGIBLE") {
		t.Fatalf("unexpected managed membership: %v", err)
	}
	p.ids = originalIDs
	originalExpiry := p.client["expiryTime"]
	p.client["expiryTime"] = int64(0)
	if _, err := s.createAccessOperation(ctx, actor, target, uuid.New(), in); !catalogueCode(err, "ACCESS_NOT_ELIGIBLE") {
		t.Fatalf("perpetual compensation: %v", err)
	}
	p.client["expiryTime"] = originalExpiry
	if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET vpn_banned=true WHERE id=$1", target); err != nil {
		t.Fatal(err)
	}
	if _, err := s.createAccessOperation(ctx, actor, target, uuid.New(), in); !catalogueCode(err, "ACCESS_NOT_ELIGIBLE") {
		t.Fatalf("banned compensation: %v", err)
	}
	if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET vpn_banned=false WHERE id=$1", target); err != nil {
		t.Fatal(err)
	}
	p.failRead = true
	if _, err := s.createAccessOperation(ctx, actor, target, uuid.New(), in); !catalogueCode(err, "ACCESS_NOT_ELIGIBLE") {
		t.Fatalf("unreachable compensation: %v", err)
	}
	if p.updates != 0 || p.resets != 0 || p.attaches != 0 {
		t.Fatal("refusal wrote panel")
	}
}

func TestRegressionAccessCompensationRejectsFiniteUnlimitedMembership(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	p.sharedRegularUnlimited = true
	ctx := context.Background()
	actor := verified(t, s, e, "access-unlimited-operator@example.test")
	target := verified(t, s, e, "access-unlimited-target@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	var panelKey, subID string
	var vpnID uuid.UUID
	if err := e.Pool.QueryRow(ctx, "UPDATE accounts SET assigned_panel_id=$2,had_subscription=true WHERE id=$1 RETURNING panel_key,vpn_id,sub_id", target, s.cfg.Subscriptions.PanelID).Scan(&panelKey, &vpnID, &subID); err != nil {
		t.Fatal(err)
	}
	p.client = map[string]any{"email": panelKey, "id": vpnID, "subId": subID, "expiryTime": json.Number("1790000000000"), "limitIp": json.Number("4"), "totalGB": json.Number("0"), "enable": true}
	p.ids = []int64{1, 2}
	if _, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "finite unlimited", Days: ptrInt(3)}); !catalogueCode(err, "ACCESS_NOT_ELIGIBLE") {
		t.Fatalf("finite unlimited membership accepted: %v", err)
	}
	if p.updates != 0 || p.resets != 0 {
		t.Fatal("refused compensation changed panel")
	}
}

func TestRegressionAccessCompensationAcceptsConfirmedTrialSharedTag(t *testing.T) {
	s, _, p, actor, target, _ := accessActors(t)
	p.sharedRegularUnlimited = true
	if _, err := s.createAccessOperation(context.Background(), actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "confirmed regular", Days: ptrInt(3)}); err != nil {
		t.Fatalf("confirmed trial misclassified by shared tag: %v", err)
	}
}

func TestRegressionAccessWorkerRejectsChangedPanelBaseline(t *testing.T) {
	s, _, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "seven days", Days: ptrInt(7)})
	if err != nil {
		t.Fatal(err)
	}
	p.client["totalGB"] = int64(77 * 1024 * 1024 * 1024)
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	current, err := s.getAccessOperation(ctx, actor, target, op.OperationId)
	if err != nil || current.Status != "needs_review" || p.updates != 0 || p.client["totalGB"] != int64(77*1024*1024*1024) {
		t.Fatalf("external limit overwritten: %v", err)
	}
}

func TestRegressionAccessWorkerRejectsRetaggedSelectedProfile(t *testing.T) {
	s, _, p, actor, target, _ := accessActors(t)
	ctx := context.Background()
	terms := catalogueTerms(4)
	terms.Profile = "euru"
	plan, err := s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: terms, Reason: "offer"})
	if err != nil {
		t.Fatal(err)
	}
	rev, days := int64(1), int64(30)
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "assign_plan", Reason: "selected profile", PlanId: &plan.PlanId, Revision: &rev, PeriodDays: &days})
	if err != nil {
		t.Fatal(err)
	}
	p.noEuru = true
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	current, err := s.getAccessOperation(ctx, actor, target, op.OperationId)
	if err != nil || current.Status != "needs_review" || p.updates != 0 || p.resets != 0 || p.attaches != 0 {
		t.Fatalf("retagged selected profile was applied: %v", err)
	}
}

func TestRegressionAccessAssignmentCreatesOneClientWhenAbsent(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	ctx := context.Background()
	actor := verified(t, s, e, "access-assign-operator@example.test")
	target := verified(t, s, e, "access-assign-fresh@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	plan, err := s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: catalogueTerms(3), Reason: "available"})
	if err != nil {
		t.Fatal(err)
	}
	rev, days := int64(1), int64(30)
	in := wire.AccessOperationInput{Kind: "assign_plan", Reason: "operator assignment", PlanId: &plan.PlanId, Revision: &rev, PeriodDays: &days}
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), in)
	if err != nil {
		t.Fatalf("fresh assignment: %v", err)
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil {
		t.Fatal(err)
	}
	if p.adds != 1 || integer(t, p.client["limitIp"]) != 4 || p.resets != 1 {
		t.Fatal("missing assigned client created with wrong limits")
	}
	var grants int
	if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM trial_grants WHERE account_id=$1", target).Scan(&grants); err != nil || grants != 0 {
		t.Fatal("assignment fabricated free grant")
	}
	if err = s.applyAccess(ctx, op.OperationId); err != nil || p.adds != 1 {
		t.Fatalf("assignment duplicate: %v", err)
	}
}

func TestRegressionAccessWorkerRechecksActorBeforeReset(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	ctx := context.Background()
	actor := verified(t, s, e, "access-revoke-operator@example.test")
	target := verified(t, s, e, "access-revoke-fresh@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	plan, err := s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: catalogueTerms(3), Reason: "available"})
	if err != nil {
		t.Fatal(err)
	}
	rev, days := int64(1), int64(30)
	op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "assign_plan", Reason: "revoke between effects", PlanId: &plan.PlanId, Revision: &rev, PeriodDays: &days})
	if err != nil {
		t.Fatal(err)
	}
	var revokeErr error
	p.afterAdd = func() { revokeErr = s.changeOperatorRole(ctx, actor, false) }
	if err = s.applyAccess(ctx, op.OperationId); err != nil || revokeErr != nil {
		t.Fatalf("revoke interleave: apply=%v revoke=%v", err, revokeErr)
	}
	var status string
	if err = e.Pool.QueryRow(ctx, "SELECT status FROM access_operations WHERE id=$1", op.OperationId).Scan(&status); err != nil || status != "needs_review" || p.adds != 1 || p.resets != 0 {
		t.Fatalf("effect continued after role revocation: %v status=%s", err, status)
	}
}
