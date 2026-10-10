package httpapi

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"testing"
	"time"
)

func TestRegressionAccessConcurrentOwnership(t *testing.T) {
	t.Run("same key enqueues once", func(t *testing.T) {
		s, e, _, actor, target, _ := accessActors(t)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		key := uuid.New()
		input := wire.AccessOperationInput{Kind: "compensate", Reason: "concurrent replay", Days: ptrInt(1)}
		start := make(chan struct{})
		results := make(chan struct {
			op  wire.AccessOperation
			err error
		}, 2)
		for range 2 {
			go func() {
				<-start
				op, err := s.createAccessOperation(ctx, actor, target, key, input)
				results <- struct {
					op  wire.AccessOperation
					err error
				}{op, err}
			}()
		}
		close(start)
		first, second := <-results, <-results
		if first.err != nil || second.err != nil || first.op.OperationId == uuid.Nil || first.op.OperationId != second.op.OperationId {
			t.Fatalf("same-key concurrent calls diverged: first=%v second=%v", first.err, second.err)
		}
		var jobs int
		if err := e.Pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind='access_operation'").Scan(&jobs); err != nil || jobs != 1 || count(t, e, "access_operations") != 1 {
			t.Fatalf("concurrent replay queued duplicate: %v jobs=%d", err, jobs)
		}
	})

	t.Run("two workers one native effect", func(t *testing.T) {
		s, e := fixture(t)
		p := panelFixture(t, s)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		actor := verified(t, s, e, "worker-race-operator@example.test")
		target := verified(t, s, e, "worker-race-target@example.test")
		if err := s.changeOperatorRole(ctx, actor, true); err != nil {
			t.Fatal(err)
		}
		op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "one bonus", Days: ptrInt(1)})
		if err != nil {
			t.Fatal(err)
		}
		started, release := make(chan struct{}), make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(release)
			}
		}()
		p.afterAdd = func() { close(started); <-release }
		first := make(chan error, 1)
		go func() { first <- s.applyAccess(ctx, op.OperationId) }()
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("first worker never reached native add")
		}
		second := make(chan error, 1)
		go func() { second <- s.applyAccess(ctx, op.OperationId) }()
		var snooze *river.JobSnoozeError
		select {
		case err := <-second:
			if !errors.As(err, &snooze) {
				t.Fatalf("competing worker did not yield account owner: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("competing worker did not yield")
		}
		close(release)
		released = true
		if err := <-first; err != nil {
			t.Fatalf("owning worker failed: %v", err)
		}
		current, err := s.getAccessOperation(ctx, actor, target, op.OperationId)
		if err != nil || current.Status != "applied" || p.adds != 1 {
			t.Fatalf("duplicate native effect or incomplete operation: %v adds=%d", err, p.adds)
		}
	})

	t.Run("trial approval versus bonus", func(t *testing.T) {
		s, e := fixture(t)
		p := panelFixture(t, s)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		actor := verified(t, s, e, "trial-race-operator@example.test")
		target := verified(t, s, e, "trial-race-target@example.test")
		if err := s.changeOperatorRole(ctx, actor, true); err != nil {
			t.Fatal(err)
		}
		request, _, err := s.createTrialRequest(ctx, target, uuid.New(), wire.TrialRequestInput{})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		access := make(chan struct {
			op  wire.AccessOperation
			err error
		}, 1)
		trial := make(chan struct {
			result subscriptions.DecisionResult
			err    error
		}, 1)
		go func() {
			<-start
			op, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "bonus race", Days: ptrInt(1)})
			access <- struct {
				op  wire.AccessOperation
				err error
			}{op, err}
		}()
		go func() {
			<-start
			result, err := s.decideTrialRequest(ctx, request.RequestId, decision(101, "approve"))
			trial <- struct {
				result subscriptions.DecisionResult
				err    error
			}{result, err}
		}()
		close(start)
		a, d := <-access, <-trial
		switch {
		case a.err == nil && catalogueCode(d.err, "ACCESS_OPERATION_CONFLICT"):
			if err := s.applyAccess(ctx, a.op.OperationId); err != nil || count(t, e, "trial_grants") != 0 || count(t, e, "access_operations") != 1 {
				t.Fatalf("winning bonus conflicted with trial: %v", err)
			}
		case d.err == nil && catalogueCode(a.err, "ACCESS_OPERATION_CONFLICT"):
			if d.result.OperationId == nil {
				t.Fatal("winning trial has no operation")
			}
			if err := s.provision(ctx, *d.result.OperationId); err != nil || count(t, e, "trial_grants") != 1 || count(t, e, "access_operations") != 0 {
				t.Fatalf("winning trial conflicted with bonus: %v", err)
			}
		default:
			t.Fatalf("trial and bonus were both accepted or both rejected: access=%v trial=%v", a.err, d.err)
		}
		if p.adds != 1 {
			t.Fatalf("common account ownership allowed %d native adds", p.adds)
		}
	})
}
