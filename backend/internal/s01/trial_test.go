package s01

import (
	"context"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"math"
	"strings"
	"sync"
	"testing"
)

func decision(actor int64, mode string) wire.DecisionInput {
	return wire.DecisionInput{OperatorTgId: actor, Decision: wire.DecisionInputDecision(mode), CallbackQueryId: uuid.NewString()}
}
func TestTrialDecisionAtomicity(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	account := verified(t, s, e, "trial@example.test")
	key := uuid.New()
	r, created, err := s.CreateTrialRequest(ctx, account, key, wire.TrialRequestInput{})
	if err != nil || !created {
		t.Fatalf("first request should persist: %v", err)
	}
	repeat, created, err := s.CreateTrialRequest(ctx, account, key, wire.TrialRequestInput{})
	if err != nil || created || repeat.RequestId != r.RequestId {
		t.Fatal("idempotent request")
	}
	if _, _, err = s.CreateTrialRequest(ctx, account, key, wire.TrialRequestInput{Comment: ptr("different")}); status(err) != 409 {
		t.Fatal("same key accepts changed body")
	}
	var wg sync.WaitGroup
	results := make(chan wire.DecisionResult, 2)
	errors := make(chan error, 2)
	for _, actor := range []int64{101, 202} {
		wg.Add(1)
		go func(actor int64) {
			defer wg.Done()
			out, err := s.DecideTrialRequest(ctx, r.RequestId, decision(actor, "approve"))
			results <- out
			errors <- err
		}(actor)
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal("concurrent approve", err)
		}
	}
	var operation uuid.UUID
	for out := range results {
		if out.OperationId == nil {
			t.Fatal("missing operation")
		}
		if operation != uuid.Nil && operation != *out.OperationId {
			t.Fatal("duplicate operation")
		}
		operation = *out.OperationId
	}
	if count(t, e, "trial_requests") != 1 || count(t, e, "trial_grants") != 1 || count(t, e, "trial_operations") != 1 {
		t.Fatal("duplicate trial")
	}
	var jobs int
	e.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='s01_provision'`).Scan(&jobs)
	var queueName string;e.Pool.QueryRow(ctx,`SELECT queue FROM river_job WHERE kind='s01_provision' LIMIT 1`).Scan(&queueName);if queueName!="provision"{t.Fatal("mail worker could consume unimplemented provision job")}
 if jobs != 1 {
		t.Fatal("duplicate provision job")
	}
	cb := decision(101, "approve")
	out, err := s.DecideTrialRequest(ctx, r.RequestId, cb)
	if err != nil || out.OperationId == nil || *out.OperationId != operation {
		t.Fatal("lost-reply retry")
	}
	out, err = s.DecideTrialRequest(ctx, r.RequestId, cb)
	if err != nil || *out.OperationId != operation {
		t.Fatal("callback replay")
	}
	_, err = s.DecideTrialRequest(ctx, r.RequestId, decision(202, "reject"))
	if status(err) != 409 {
		t.Fatal("opposite decision allowed")
	}
	domain := err.(*Error)
	if domain.Details["current_request_status"] != "approved" || domain.Details["operation_id"] != operation {
		t.Fatal("winning state absent")
	}
	other := verified(t, s, e, "rollback@example.test")
	r, _, err = s.CreateTrialRequest(ctx, other, uuid.New(), wire.TrialRequestInput{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Pool.Exec(ctx, `CREATE FUNCTION fail_provision_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='s01_provision' THEN RAISE EXCEPTION 'controlled fixture failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_provision BEFORE INSERT ON river_job FOR EACH ROW EXECUTE FUNCTION fail_provision_insert();`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DecideTrialRequest(ctx, r.RequestId, decision(101, "approve")); status(err) != 503 {
		t.Fatal("queue failure must rollback")
	}
	current, err := s.CurrentTrialRequest(ctx, other)
	if err != nil || current.Request.Status != "pending" || count(t, e, "trial_grants") != 1 || count(t, e, "trial_operations") != 1 {
		t.Fatal("partial decision commit")
	}
}
func TestTrialReconsideration(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	account := verified(t, s, e, "support@example.test")
	r, _, err := s.CreateTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err = s.DecideTrialRequest(ctx, r.RequestId, decision(101, "reject")); err != nil {
		t.Fatal(err)
	}
	var restricted bool
	e.Pool.QueryRow(ctx, `SELECT restricted FROM accounts WHERE id=$1`, account).Scan(&restricted)
	if restricted || count(t, e, "trial_operations") != 0 || count(t, e, "trial_grants") != 0 {
		t.Fatal("rejection affected account or provision")
	}
	if _, _, err = s.CreateTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{}); status(err) != 409 {
		t.Fatal("client reapplied after rejection")
	}
	key := uuid.New()
	for _, reason := range []string{"", strings.Repeat("я", 1001)} {
		if _, err = s.ReconsiderTrialRequest(ctx, r.RequestId, key, wire.ReconsiderInput{OperatorTgId: 101, Reason: reason}); status(err) != 400 {
			t.Fatal("invalid support reason")
		}
	}
	in := wire.ReconsiderInput{OperatorTgId: 101, Reason: "Support reviewed the request"}
	newRequest, err := s.ReconsiderTrialRequest(ctx, r.RequestId, key, in)
	if err != nil || newRequest.Status != "pending" || newRequest.PreviousRequestId == nil || *newRequest.PreviousRequestId != r.RequestId {
		t.Fatal("support reconsider", err)
	}
	again, err := s.ReconsiderTrialRequest(ctx, r.RequestId, key, in)
	if err != nil || again.RequestId != newRequest.RequestId {
		t.Fatal("support idempotency")
	}
	if _, err = s.DecideTrialRequest(ctx, r.RequestId, decision(101, "approve")); status(err) != 409 {
		t.Fatal("stale card reopened rejection")
	}
	current, err := s.CurrentTrialRequest(ctx, account)
	if err != nil || current.Request.RequestId != newRequest.RequestId {
		t.Fatal("current request ordering")
	}
	if _, err = s.DecideTrialRequest(ctx, newRequest.RequestId, decision(101, "approve")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReconsiderTrialRequest(ctx, r.RequestId, uuid.New(), in); status(err) != 409 {
		t.Fatal("reserved trial reconsidered")
	}
}
func TestInternalOperatorBoundary(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	account := verified(t, s, e, "actor@example.test")
	r, _, err := s.CreateTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []int64{0, 999} {
		if _, err = s.DecideTrialRequest(ctx, r.RequestId, decision(actor, "approve")); status(err) != 403 {
			t.Fatal("forged actor approved")
		}
	}
	if count(t, e, "trial_operations") != 0 {
		t.Fatal("operator boundary writes")
	}
}

func TestTrialDecisionAtomicityRace(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	account := verified(t, s, e, "race@example.test")
	ids := make(chan uuid.UUID, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _, err := s.CreateTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{})
			ids <- r.RequestId
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id uuid.UUID
	for got := range ids {
		if id != uuid.Nil && id != got {
			t.Fatal("different active requests")
		}
		id = got
	}
	if count(t, e, "trial_requests") != 1 {
		t.Fatal("duplicate pending requests")
	}
	outcomes := make(chan error, 2)
	for _, mode := range []string{"approve", "reject"} {
		wg.Add(1)
		go func(mode string) {
			defer wg.Done()
			_, err := s.DecideTrialRequest(ctx, id, decision(101, mode))
			outcomes <- err
		}(mode)
	}
	wg.Wait()
	close(outcomes)
	success, conflict := 0, 0
	for err := range outcomes {
		if err == nil {
			success++
		} else if status(err) == 409 {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("decision race has no unique winner")
	}
	current, err := s.CurrentTrialRequest(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	expected := 0
	if current.Request.Status == "approved" {
		expected = 1
	}
	if count(t, e, "trial_operations") != expected || count(t, e, "trial_grants") != expected {
		t.Fatal("winner/reservation mismatch")
	}
}
func TestTrialReconsiderationRace(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	account := verified(t, s, e, "reconsider-race@example.test")
	r, _, err := s.CreateTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DecideTrialRequest(ctx, r.RequestId, decision(101, "reject")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, actor := range []int64{101, 202} {
		wg.Add(1)
		go func(actor int64) {
			defer wg.Done()
			_, err := s.ReconsiderTrialRequest(ctx, r.RequestId, uuid.New(), wire.ReconsiderInput{OperatorTgId: actor, Reason: "Support review"})
			errs <- err
		}(actor)
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if status(err) == 409 {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 || count(t, e, "trial_requests") != 2 {
		t.Fatal("duplicate reconsideration")
	}
}
func TestTrialDecisionAtomicityEligibility(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	account := verified(t, s, e, "eligibility@example.test")
	s.cfg.TrialEnabled = false
	if _, _, err := s.CreateTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{}); status(err) != 403 {
		t.Fatal("disabled trial requested")
	}
	s.cfg.TrialEnabled = true
	for _, column := range []string{"restricted", "had_subscription"} {
		e.Pool.Exec(ctx, "UPDATE accounts SET "+column+"=true WHERE id=$1", account)
		_, _, err := s.CreateTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{})
		want := 409
		if column == "restricted" {
			want = 403
		}
		if status(err) != want {
			t.Fatal("eligibility", column, err)
		}
		e.Pool.Exec(ctx, "UPDATE accounts SET "+column+"=false WHERE id=$1", account)
	}
	e.Pool.Exec(ctx, `UPDATE accounts SET assigned_panel_id='existing' WHERE id=$1`, account)
	if _, _, err := s.CreateTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{}); status(err) != 409 {
		t.Fatal("assigned account received trial")
	}
	e.Pool.Exec(ctx, `UPDATE accounts SET assigned_panel_id=NULL WHERE id=$1`, account)
	r, _, err := s.CreateTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{})
	if err != nil {
		t.Fatal(err)
	}
	base := s.cfg
	for _, tc := range []struct{ days, traffic, devices int64 }{{0, 15, 1}, {-1, 15, 1}, {3, -1, 1}, {3, 15, -1}, {math.MaxInt64, 15, 1}, {3, math.MaxInt64, 1}, {3, 15, math.MaxInt64}} {
		s.cfg.TrialPeriodDays = tc.days
		s.cfg.TrialTrafficGB = tc.traffic
		s.cfg.TrialDevices = tc.devices
		if _, err = s.DecideTrialRequest(ctx, r.RequestId, decision(101, "approve")); status(err) != 503 {
			t.Fatal("invalid snapshot reserved")
		}
		if count(t, e, "trial_grants") != 0 {
			t.Fatal("invalid settings side effect")
		}
	}
	s.cfg = base
	e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, account)
	if _, err = s.DecideTrialRequest(ctx, r.RequestId, decision(101, "approve")); status(err) != 403 {
		t.Fatal("restricted approve")
	}
	e.Pool.Exec(ctx, `UPDATE accounts SET restricted=false WHERE id=$1`, account)
	out, err := s.DecideTrialRequest(ctx, r.RequestId, decision(101, "approve"))
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.TrialPeriodDays = 9
	var days int64
	if err = e.Pool.QueryRow(ctx, `SELECT period_days FROM trial_operations WHERE id=$1`, *out.OperationId).Scan(&days); err != nil || days != 3 {
		t.Fatal("snapshot overwritten")
	}
}
