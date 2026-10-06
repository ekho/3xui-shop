package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"example.com/cabinet/backend/internal/modules/vpn/internal/store"

	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

type MonthlyResetArgs struct {
	AccountID   uuid.UUID `json:"account_id"`
	LocalPeriod string    `json:"local_period"`
}

func (MonthlyResetArgs) Kind() string { return "monthly_traffic_reset" }

type MonthlyResetWorker struct {
	river.WorkerDefaults[MonthlyResetArgs]
	Service *Service
}

func (w *MonthlyResetWorker) Work(ctx context.Context, job *river.Job[MonthlyResetArgs]) error {
	return w.Service.ApplyMonthlyReset(ctx, job.Args.AccountID, job.Args.LocalPeriod)
}
func (w *MonthlyResetWorker) Timeout(*river.Job[MonthlyResetArgs]) time.Duration {
	return 2*time.Minute + 5*time.Second
}

func (s *Service) monthlyZone() (*time.Location, error) {
	zone := s.config().AccessResetTimezone
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, unavailable()
	}
	return loc, nil
}

func monthlyPeriod(at time.Time, loc *time.Location) (string, time.Time) {
	local := at.In(loc)
	start := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	return start.Format("2006-01"), start
}

// EnqueueMonthlyResets is called at startup in the one-hour catch-up window
// and at the next local month boundary. Each claim and its River job commit
// together, so a busy account retains its period without a second panel writer.
func (s *Service) EnqueueMonthlyResets(ctx context.Context, at time.Time) (int, error) {
	loc, err := s.monthlyZone()
	if err != nil || (s.queue == nil || s.queue() == nil) {
		return 0, unavailable()
	}
	period, boundary := monthlyPeriod(at, loc)
	if at.Before(boundary) || at.Sub(boundary) > time.Hour {
		return 0, nil
	}
	ids, err := s.accounts.UnlimitedAccounts(ctx)
	if err != nil {
		return 0, unavailable()
	}
	created := 0
	for _, id := range ids {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return created, unavailable()
		}
		a, err := s.lockAccount(ctx, tx, id)
		if err != nil || !(a.AccessProfile != nil) || stringValue(a.AccessProfile) != "unlimited" {
			tx.Rollback(ctx)
			if err != nil {
				return created, unavailable()
			}
			continue
		}
		var inserted bool
		err = tx.QueryRow(ctx, "INSERT INTO monthly_reset_periods(account_id,local_period,timezone,status,created_at,updated_at) VALUES($1,$2,$3,'waiting',$4,$4) ON CONFLICT DO NOTHING RETURNING true", id, period, loc.String(), at.UTC()).Scan(&inserted)
		if errors.Is(err, pgx.ErrNoRows) {
			tx.Rollback(ctx)
			continue
		}
		if err != nil {
			tx.Rollback(ctx)
			return created, unavailable()
		}
		if _, err = s.queue().InsertTx(ctx, tx, MonthlyResetArgs{AccountID: id, LocalPeriod: period}, &river.InsertOpts{Queue: "provision", MaxAttempts: 1000000}); err != nil {
			tx.Rollback(ctx)
			return created, unavailable()
		}
		if err = tx.Commit(ctx); err != nil {
			return created, unavailable()
		}
		created++
	}
	return created, nil
}

func monthlyAudit(ctx context.Context, tx pgx.Tx, account uuid.UUID, period, action string, op *uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, "INSERT INTO audit_events(id,created_at,action,account_id,access_operation_id,system_actor,monthly_period) VALUES($1,$2,$3,$4,$5,true,$6)", uuid.New(), now.UTC(), action, account, op, period)
	return err
}

func (s *Service) monthlyOperationExpired(ctx context.Context, op store.AccessOperation) (bool, error) {
	if !op.MonthlyPeriod.Valid {
		return false, unavailable()
	}
	var zone string
	if err := s.pool.QueryRow(ctx, "SELECT timezone FROM monthly_reset_periods WHERE account_id=$1 AND local_period=$2 AND operation_id=$3", op.AccountID, op.MonthlyPeriod.String, op.ID).Scan(&zone); err != nil {
		return false, unavailable()
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return false, unavailable()
	}
	current, _ := monthlyPeriod(s.now(), loc)
	return current != op.MonthlyPeriod.String, nil
}

func (s *Service) finishMonthlyWithoutWrite(ctx context.Context, op store.AccessOperation, lease []byte, step string) error {
	claimStatus, action := "skipped", "monthly_reset_skipped"
	if step == "period_elapsed_unserved" {
		claimStatus, action = "period_elapsed_unserved", "monthly_reset_period_elapsed_unserved"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	now := s.now().UTC().Truncate(time.Microsecond)
	result, err := tx.Exec(ctx, "UPDATE access_operations SET status='skipped',lease_hash=NULL,lease_expires_at=NULL,completed_steps=completed_steps||jsonb_build_array($4::text),updated_at=$3 WHERE id=$1 AND lease_hash=$2 AND status='provisioning' AND NOT write_started AND NOT reset_started", op.ID, lease, now, step)
	if err != nil || result.RowsAffected() != 1 {
		return unavailable()
	}
	if _, err = tx.Exec(ctx, "UPDATE monthly_reset_periods SET status=$5,updated_at=$3 WHERE account_id=$1 AND local_period=$2 AND operation_id=$4", op.AccountID, op.MonthlyPeriod.String, now, op.ID, claimStatus); err != nil || monthlyAudit(ctx, tx, op.AccountID, op.MonthlyPeriod.String, action, &op.ID, now) != nil || tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}

func (s *Service) ApplyMonthlyReset(ctx context.Context, account uuid.UUID, period string) error {
	if account == uuid.Nil || len(period) != 7 {
		return unavailable()
	}
	owner, err := s.OpenAccessOwner(ctx, account)
	if err != nil {
		return unavailable()
	}
	defer owner.Release()
	tx, err := owner.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	now := s.now().UTC().Truncate(time.Microsecond)
	a, err := s.lockAccount(ctx, tx, account)
	if err != nil {
		return unavailable()
	}
	var status, timezone string
	var deferredAt pgtype.Timestamptz
	readClaim := func() error {
		return tx.QueryRow(ctx, "SELECT status,deferred_at,timezone FROM monthly_reset_periods WHERE account_id=$1 AND local_period=$2 FOR UPDATE", account, period).Scan(&status, &deferredAt, &timezone)
	}
	finishClaim := func(state, action string) error {
		if _, err := tx.Exec(ctx, "UPDATE monthly_reset_periods SET status=$4,updated_at=$3 WHERE account_id=$1 AND local_period=$2", account, period, now, state); err != nil || monthlyAudit(ctx, tx, account, period, action, nil, now) != nil || tx.Commit(ctx) != nil {
			return unavailable()
		}
		return nil
	}
	deferClaim := func() error {
		if _, err := tx.Exec(ctx, "UPDATE monthly_reset_periods SET deferred_at=COALESCE(deferred_at,$3),updated_at=$3 WHERE account_id=$1 AND local_period=$2", account, period, now); err != nil {
			return unavailable()
		}
		if !deferredAt.Valid && monthlyAudit(ctx, tx, account, period, "monthly_reset_waiting", nil, now) != nil {
			return unavailable()
		}
		if tx.Commit(ctx) != nil {
			return unavailable()
		}
		return river.JobSnooze(30 * time.Second)
	}
	if err = readClaim(); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return unavailable()
	}
	if status != "waiting" {
		return nil
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return unavailable()
	}
	currentPeriod, _ := monthlyPeriod(now, loc)
	if currentPeriod != period {
		return finishClaim("period_elapsed_unserved", "monthly_reset_period_elapsed_unserved")
	}
	lockErr := owner.TryLock(ctx)
	if lockErr != nil && !errors.Is(lockErr, ErrBusy) {
		return unavailable()
	}
	active, err := s.UnresolvedTx(ctx, tx, account)
	if err != nil {
		return unavailable()
	}
	if errors.Is(lockErr, ErrBusy) || active {
		return deferClaim()
	}
	if a.AccessProfile == nil || *a.AccessProfile != "unlimited" || a.VpnBanned {
		return finishClaim("skipped", "monthly_reset_skipped")
	}
	if a.AssignedPanelID == nil || *a.AssignedPanelID != s.config().PanelID {
		return deferClaim()
	}
	savedZone := timezone
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	p := s.PanelClient()
	defer p.Close()
	v, readErr := p.GetClient(ctx, a.PanelKey)
	valid := readErr == nil && v != nil && v.VPNID == a.VpnID && v.SubID == a.SubID
	var ids []int64
	if valid {
		ids, readErr = p.ProfileInboundIDs(ctx, "unlimited")
		valid = readErr == nil
	}
	if valid {
		attach, detach, e := p.MembershipDiff(ctx, v.InboundIDs, ids)
		valid = e == nil && len(attach) == 0 && len(detach) == 0 && v.ExpiryTimeMS == 0
	}
	// No network call follows this Begin; the original session remains the owner.
	tx, err = owner.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	now = s.now().UTC().Truncate(time.Microsecond)
	current, err := s.lockAccount(ctx, tx, account)
	if err != nil {
		return unavailable()
	}
	if err = readClaim(); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return unavailable()
	}
	if status != "waiting" {
		return nil
	}
	if timezone != savedZone {
		return unavailable()
	}
	currentPeriod, _ = monthlyPeriod(now, loc)
	if currentPeriod != period {
		return finishClaim("period_elapsed_unserved", "monthly_reset_period_elapsed_unserved")
	}
	active, err = s.UnresolvedTx(ctx, tx, account)
	if err != nil {
		return unavailable()
	}
	if active {
		return deferClaim()
	}
	if current.AccessProfile == nil || *current.AccessProfile != "unlimited" || current.VpnBanned {
		return finishClaim("skipped", "monthly_reset_skipped")
	}
	if !valid || !reflect.DeepEqual(a, current) {
		return deferClaim()
	}
	id := uuid.New()
	target := AccessTarget{OperationID: id, PanelID: s.config().PanelID, PanelKey: a.PanelKey, VPNID: a.VpnID, SubID: a.SubID, ExpiryTimeMS: 0, DeviceCount: max(0, v.LimitIP-1), TrafficLimitBytes: v.TrafficLimitBytes, Profile: "unlimited", InboundIDs: ids, Reset: true, PreviousExpiryMS: v.ExpiryTimeMS, PreviousLimitIP: v.LimitIP, PreviousTrafficLimitBytes: v.TrafficLimitBytes, PreviousInboundIDs: v.InboundIDs}
	desired := AccessDesired{Devices: target.DeviceCount, TrafficLimitBytes: target.TrafficLimitBytes, Profile: "unlimited", ResetTraffic: true, VpnBanned: false}
	desiredRaw, _ := json.Marshal(desired)
	targetRaw, _ := json.Marshal(target)
	if err = store.New(tx).InsertAccessOperation(ctx, store.InsertAccessOperationParams{ID: id, AccountID: account, Kind: "monthly_reset", Reason: "monthly reset", Desired: desiredRaw, Target: targetRaw, MonthlyPeriod: pgtype.Text{String: period, Valid: true}, CreatedAt: stamp(now)}); err != nil {
		return accessConflict(err)
	}
	if s.queue == nil || s.queue() == nil {
		return unavailable()
	}
	if _, err = s.queue().InsertTx(ctx, tx, AccessArgs{OperationID: id}, &river.InsertOpts{Queue: "provision", MaxAttempts: 5}); err != nil {
		return unavailable()
	}
	if _, err = tx.Exec(ctx, "UPDATE monthly_reset_periods SET status='enqueued',operation_id=$3,updated_at=$4 WHERE account_id=$1 AND local_period=$2", account, period, id, now); err != nil || monthlyAudit(ctx, tx, account, period, "monthly_reset_requested", &id, now) != nil || tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}

// RunMonthlyResetScheduler has one bounded timer per process. Concurrent
// processes are harmless because the database owns account/period uniqueness.
func (s *Service) RunMonthlyResetScheduler(ctx context.Context) error {
	loc, err := s.monthlyZone()
	if err != nil {
		return err
	}
	if _, err = s.EnqueueMonthlyResets(ctx, s.now()); err != nil {
		return err
	}
	for {
		local := s.now().In(loc)
		next := time.Date(local.Year(), local.Month()+1, 1, 0, 0, 0, 0, loc)
		delay := next.Sub(s.now())
		if delay <= 0 {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if _, err = s.EnqueueMonthlyResets(ctx, s.now()); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}
