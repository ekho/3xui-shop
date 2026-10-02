package s01

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"testing"
	"time"
)

func unlimitedAccount(t *testing.T) (*Service, *fakePanel, uuid.UUID, uuid.UUID) {
	t.Helper()
	s, _, p, actor, target, _ := accessActors(t)
	if _, _, err := s.SeedUnlimitedCatalogue(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateAccessOperation(context.Background(), actor, target, uuid.New(), wire.AccessOperationInput{Kind: "set_profile", Profile: profileInput("unlimited"), Reason: "monthly fixture"})
	if err != nil || s.ApplyAccess(context.Background(), op.OperationId) != nil {
		t.Fatalf("unlimited fixture: %v", err)
	}
	return s, p, actor, target
}

// A restart or second process must not create a second account-period claim.
func TestMonthlyResetUniquePeriodAndGrace(t *testing.T) {
	s, _, _, target := unlimitedAccount(t)
	ctx := context.Background()
	boundary := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s.cfg.AccessResetTimezone = "UTC"
	if n, err := s.EnqueueMonthlyResets(ctx, boundary.Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("exact 3600-second grace: %v count=%d", err, n)
	}
	other := NewService(s.pool, s.limiter, s.queue, s.cfg)
	if n, err := other.EnqueueMonthlyResets(ctx, boundary.Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("duplicate period: %v count=%d", err, n)
	}
	var claims, jobs int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM monthly_reset_periods WHERE account_id=$1", target).Scan(&claims); err != nil || claims != 1 {
		t.Fatalf("claim count: %v %d", err, claims)
	}
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind='s41_monthly_reset'").Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("waiting job count: %v %d", err, jobs)
	}
	if n, err := s.EnqueueMonthlyResets(ctx, boundary.Add(time.Hour+time.Second)); err != nil || n != 0 {
		t.Fatalf("3601-second stale catch-up: %v count=%d", err, n)
	}
}

// A pending manual write occupies the single account slot; the monthly claim
// waits and later promotes through the existing executor, never writes panel.
func TestMonthlyBusyClaimWaitsThenPromotes(t *testing.T) {
	s, _, actor, target := unlimitedAccount(t)
	ctx := context.Background()
	s.cfg.AccessResetTimezone = "UTC"
	boundary := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	manual, err := s.CreateAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "reset_traffic", Reason: "manual busy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnqueueMonthlyResets(ctx, boundary); err != nil {
		t.Fatal(err)
	}
	var snooze *river.JobSnoozeError
	if err = s.ApplyMonthlyReset(ctx, target, "2026-10"); !errors.As(err, &snooze) {
		t.Fatalf("busy account did not wait: %v", err)
	}
	if err = s.ApplyMonthlyReset(ctx, target, "2026-10"); !errors.As(err, &snooze) {
		t.Fatalf("second busy pass did not wait: %v", err)
	}
	var waitingEvents int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE account_id=$1 AND action='monthly_reset_waiting'", target).Scan(&waitingEvents); err != nil || waitingEvents != 1 {
		t.Fatalf("deferral audit repeated: %v %d", err, waitingEvents)
	}
	if err = s.ApplyAccess(ctx, manual.OperationId); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyMonthlyReset(ctx, target, "2026-10"); err != nil {
		t.Fatal(err)
	}
	var status string
	var opID uuid.UUID
	if err = s.pool.QueryRow(ctx, "SELECT status,operation_id FROM monthly_reset_periods WHERE account_id=$1 AND local_period='2026-10'", target).Scan(&status, &opID); err != nil || status != "enqueued" || opID == uuid.Nil {
		t.Fatalf("claim not promoted: %v %s", err, status)
	}
	if err = s.ApplyAccess(ctx, opID); err != nil {
		t.Fatal(err)
	}
	op, err := s.GetAccessOperation(ctx, actor, target, opID)
	if err != nil || op.Kind != "monthly_reset" || op.Status != "applied" || op.Desired.ExpiresAt != nil {
		t.Fatalf("monthly executor result: %v, %+v", err, op)
	}
}

func TestMonthlyMoscowBoundaryAndOldPeriod(t *testing.T) {
	s, _, _, target := unlimitedAccount(t)
	ctx := context.Background()
	s.cfg.AccessResetTimezone = "Europe/Moscow"
	boundary := time.Date(2026, 9, 30, 21, 0, 0, 0, time.UTC)
	if n, err := s.EnqueueMonthlyResets(ctx, boundary); err != nil || n != 1 {
		t.Fatalf("Moscow month boundary: %v count=%d", err, n)
	}
	var period string
	if err := s.pool.QueryRow(ctx, "SELECT local_period FROM monthly_reset_periods WHERE account_id=$1", target).Scan(&period); err != nil || period != "2026-10" {
		t.Fatalf("wrong local period: %v %s", err, period)
	}
	s.cfg.AccessResetTimezone = "UTC" // Restart with changed config cannot reinterpret the saved period.
	s.now = func() time.Time { return time.Date(2026, 10, 31, 21, 30, 0, 0, time.UTC) }
	if err := s.ApplyMonthlyReset(ctx, target, "2026-10"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.pool.QueryRow(ctx, "SELECT status FROM monthly_reset_periods WHERE account_id=$1 AND local_period='2026-10'", target).Scan(&status); err != nil || status != "period_elapsed_unserved" {
		t.Fatalf("old month not retained as unserved: %v %s", err, status)
	}
	var operations int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM access_operations WHERE account_id=$1", target).Scan(&operations); err != nil || operations != 1 {
		t.Fatalf("previous month reset created access operation: %v count=%d", err, operations)
	}
}

func TestMonthlyResetSkipsChangedEligibilityBeforeNativeWrite(t *testing.T) {
	for _, tc := range []struct {
		name, statement string
	}{
		{"ban", "UPDATE accounts SET vpn_banned=true WHERE id=$1"},
		{"profile", "UPDATE accounts SET access_profile='regular' WHERE id=$1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p, actor, target := unlimitedAccount(t)
			ctx := context.Background()
			s.cfg.AccessResetTimezone = "UTC"
			if _, err := s.EnqueueMonthlyResets(ctx, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)); err != nil || s.ApplyMonthlyReset(ctx, target, "2026-10") != nil {
				t.Fatalf("enqueue monthly: %v", err)
			}
			var opID uuid.UUID
			if err := s.pool.QueryRow(ctx, "SELECT operation_id FROM monthly_reset_periods WHERE account_id=$1 AND local_period='2026-10'", target).Scan(&opID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(ctx, tc.statement, target); err != nil {
				t.Fatal(err)
			}
			beforeResets := p.resets
			if err := s.ApplyAccess(ctx, opID); err != nil {
				t.Fatal(err)
			}
			if p.resets != beforeResets {
				t.Fatal("changed eligibility wrote panel reset")
			}
			op, err := s.GetAccessOperation(ctx, actor, target, opID)
			if err != nil || op.Status != "skipped" || len(op.CompletedSteps) == 0 || op.CompletedSteps[len(op.CompletedSteps)-1] != "eligibility_changed" {
				t.Fatalf("changed eligibility attempted monthly reset: %v, %+v", err, op)
			}
			var claim string
			if err := s.pool.QueryRow(ctx, "SELECT status FROM monthly_reset_periods WHERE account_id=$1 AND local_period='2026-10'", target).Scan(&claim); err != nil || claim != "skipped" {
				t.Fatalf("period not consumed: %v %s", err, claim)
			}
		})
	}
}

func TestMonthlyLostResetReplyNeedsReview(t *testing.T) {
	s, p, actor, target := unlimitedAccount(t)
	ctx := context.Background()
	s.cfg.AccessResetTimezone = "UTC"
	if _, err := s.EnqueueMonthlyResets(ctx, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)); err != nil || s.ApplyMonthlyReset(ctx, target, "2026-10") != nil {
		t.Fatalf("monthly enqueue: %v", err)
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, "SELECT operation_id FROM monthly_reset_periods WHERE account_id=$1 AND local_period='2026-10'", target).Scan(&id); err != nil {
		t.Fatal(err)
	}
	p.loseReset = true
	if err := s.ApplyAccess(ctx, id); err != nil {
		t.Fatal(err)
	}
	op, err := s.GetAccessOperation(ctx, actor, target, id)
	if err != nil || op.Status != "needs_review" || op.Desired.ExpiresAt != nil || p.resets != 1 {
		t.Fatalf("lost reply was treated as confirmed: %v, %+v resets=%d", err, op, p.resets)
	}
	if err = s.ApplyAccess(ctx, id); err != nil || p.resets != 1 {
		t.Fatalf("ambiguous reset repeated: %v resets=%d", err, p.resets)
	}
	if _, err = s.ReconcileAccessOperation(ctx, actor, target, id, uuid.New(), wire.AccessReconcileInput{Reason: "verified cost", AcknowledgeResetCost: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeOperatorRole(ctx, actor, false); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyAccess(ctx, id); err != nil {
		t.Fatal(err)
	}
	if p.resets != 1 {
		t.Fatal("revoked reconciler retried system reset")
	}
	var status string
	if err = s.pool.QueryRow(ctx, "SELECT status FROM access_operations WHERE id=$1", id).Scan(&status); err != nil || status != "needs_review" {
		t.Fatalf("revoked monthly reconciler escaped review: %v %s", err, status)
	}
}

func TestMonthlyPromotedOperationCannotResetPreviousMonth(t *testing.T) {
	s, p, actor, target := unlimitedAccount(t)
	ctx := context.Background()
	s.cfg.AccessResetTimezone = "Europe/Moscow"
	if _, err := s.EnqueueMonthlyResets(ctx, time.Date(2026, 9, 30, 21, 0, 0, 0, time.UTC)); err != nil || s.ApplyMonthlyReset(ctx, target, "2026-10") != nil {
		t.Fatalf("monthly promotion: %v", err)
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, "SELECT operation_id FROM monthly_reset_periods WHERE account_id=$1 AND local_period='2026-10'", target).Scan(&id); err != nil {
		t.Fatal(err)
	}
	s.cfg.AccessResetTimezone = "UTC"
	s.now = func() time.Time { return time.Date(2026, 10, 31, 21, 30, 0, 0, time.UTC) }
	if err := s.ApplyAccess(ctx, id); err != nil {
		t.Fatal(err)
	}
	op, err := s.GetAccessOperation(ctx, actor, target, id)
	if err != nil || op.Status != "skipped" || p.resets != 0 || len(op.CompletedSteps) == 0 || op.CompletedSteps[len(op.CompletedSteps)-1] != "period_elapsed_unserved" {
		t.Fatalf("old promoted reset wrote or stayed pending: %v, %+v resets=%d", err, op, p.resets)
	}
	var claim string
	if err = s.pool.QueryRow(ctx, "SELECT status FROM monthly_reset_periods WHERE account_id=$1 AND local_period='2026-10'", target).Scan(&claim); err != nil || claim != "period_elapsed_unserved" {
		t.Fatalf("old period not recorded: %v %s", err, claim)
	}
}
