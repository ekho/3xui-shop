package vpn

import (
	"context"
	"encoding/json"
	"errors"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/vpn/internal/store"

	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// Keep rescue beyond the worker's 125-second budget, aligned with the operation lease.
const ProvisionRescueAfter = 3 * time.Minute

// The dedicated connection holds a session lock across bounded panel calls.
// The watchdog cancels HTTP on ownership loss; apply uses that same connection.
func watchOwner(ctx context.Context, c *pgxpool.Conn, cancel context.CancelFunc) (func(), *atomic.Bool) {
	watch, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	lost := &atomic.Bool{}
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watch.Done():
				return
			case <-ticker.C:
				ping, end := context.WithTimeout(watch, time.Second)
				e := c.Ping(ping)
				end()
				if e != nil && watch.Err() == nil {
					lost.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { stop(); <-done }) }, lost
}
func releaseOwner(c *pgxpool.Conn, account uuid.UUID) {
	ctx, end := context.WithTimeout(context.Background(), time.Second)
	defer end()
	if _, e := c.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended('account-access:'||$1::text,0))`, account.String()); e != nil {
		c.Conn().Close(ctx)
	}
	c.Release()
}
func (s *Service) reviewOperation(ctx context.Context, op store.TrialOperation, lease []byte, includeApplied bool) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	_, e = s.lockAccount(ctx, tx, op.AccountID)
	if e != nil {
		return unavailable()
	}
	n, e := q.ReviewOperation(ctx, store.ReviewOperationParams{ID: op.ID, IncludeApplied: includeApplied, ExpectedLease: lease})
	if e != nil {
		return unavailable()
	}
	if n == 0 {
		return nil
	}
	if s.recordOutcome(ctx, tx, op.RequestID, op.ID, "needs_review") != nil || tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}
func validSnapshot(op store.TrialOperation) bool {
	return op.TrialEnabled && op.PeriodDays > 0 && op.PeriodDays <= math.MaxInt64/int64(24*time.Hour) && op.TrafficGb >= 0 && op.TrafficGb <= math.MaxInt64/(1024*1024*1024) && op.Devices >= 0 && op.Devices < math.MaxInt64 && op.PanelID != ""
}
func (s *Service) Provision(parent context.Context, id uuid.UUID) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	initial, e := store.New(s.pool).OperationByID(ctx, id)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return unavailable()
	}
	c, e := s.pool.Acquire(ctx)
	if e != nil {
		return unavailable()
	}
	var locked bool
	e = c.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended('account-access:'||$1::text,0))`, initial.AccountID.String()).Scan(&locked)
	if e != nil || !locked {
		c.Release()
		if e == nil {
			return river.JobSnooze(10 * time.Second)
		}
		return unavailable()
	}
	defer releaseOwner(c, initial.AccountID)
	q := store.New(c)
	op, e := q.OperationByID(ctx, id)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return unavailable()
	}
	if op.Status == "applied" || op.Status == "needs_review" {
		return nil
	}
	if op.PanelID == "" {
		if e = s.bindTrialServer(ctx, c, op); e != nil {
			if errors.Is(e, ErrPanel) || errors.Is(e, ErrBusy) {
				return river.JobSnooze(10 * time.Second)
			}
			return e
		}
	}
	lease := digest(uuid.NewString())
	op, e = q.LeaseOperation(ctx, store.LeaseOperationParams{ID: id, FirstStartedAt: stamp(s.now()), LeaseHash: lease})
	if e != nil {
		return unavailable()
	}
	stop, lost := watchOwner(ctx, c, cancel)
	defer stop()
	fail := func(ambiguous bool) error {
		stop()
		cleanup, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		if ambiguous || op.WriteStarted || op.Attempts >= 5 || lost.Load() {
			if s.reviewOperation(cleanup, op, lease, false) != nil {
				return unavailable()
			}
			return nil
		}
		if store.New(s.pool).RetryOperation(cleanup, store.RetryOperationParams{ID: id, LeaseHash: lease}) != nil {
			return unavailable()
		}
		return unavailable()
	}
	a, e := s.accountByID(ctx, op.AccountID)
	if e != nil {
		return fail(false)
	}
	if a.Restricted || !accounts.SourceEligible(a) || !validSnapshot(op) || a.AssignedPanelID != nil && *a.AssignedPanelID != op.PanelID {
		return fail(true)
	}
	p, e := s.PanelFor(ctx, op.PanelID)
	if e != nil {
		return fail(true)
	}
	defer p.Close()
	var target ProvisionTarget
	hadTarget := len(op.Target) > 0
	if hadTarget {
		if json.Unmarshal(op.Target, &target) != nil {
			return fail(true)
		}
	} else {
		profile := stringValue(a.AccessProfile)
		if profile == "" && a.Kind == "web" {
			profile = "regular"
		}
		if profile != "regular" && profile != "euru" {
			return fail(true)
		}
		ids, err := p.ProfileInboundIDs(ctx, profile)
		if err != nil {
			if errors.Is(err, ErrMembership) {
				return fail(true)
			}
			return fail(false)
		}
		if len(ids) == 0 {
			return fail(true)
		}
		expiry := op.FirstStartedAt.Time.Add(time.Duration(op.PeriodDays) * 24 * time.Hour).UnixMilli()
		if expiry <= op.FirstStartedAt.Time.UnixMilli() {
			return fail(true)
		}
		target = ProvisionTarget{OperationID: op.ID, PanelID: op.PanelID, PanelKey: a.PanelKey, VPNID: a.VpnID, SubID: a.SubID, InboundIDs: ids, ExpiryTimeMS: expiry, DeviceCount: op.Devices, TrafficLimitBytes: op.TrafficGb * 1024 * 1024 * 1024, Profile: profile, Banned: a.VpnBanned}
		data, err := json.Marshal(target)
		if err != nil || store.New(s.pool).SaveProvisionTarget(ctx, store.SaveProvisionTargetParams{ID: id, Target: data}) != nil {
			return fail(false)
		}
	}
	if target.OperationID != id || target.PanelID != op.PanelID || target.PanelKey != a.PanelKey || target.VPNID != a.VpnID || target.SubID != a.SubID || target.DeviceCount != op.Devices || target.TrafficLimitBytes != op.TrafficGb*1024*1024*1024 || len(target.InboundIDs) == 0 || target.ExpiryTimeMS != op.FirstStartedAt.Time.Add(time.Duration(op.PeriodDays)*24*time.Hour).UnixMilli() {
		return fail(true)
	}
	v, e := p.GetClient(ctx, target.PanelKey)
	if e != nil {
		return fail(false)
	}
	if v != nil && !hadTarget {
		return fail(true)
	} // No trustworthy persisted intent for an existing client.
	write := func() bool {
		if ctx.Err() != nil || lost.Load() {
			return false
		}
		// Recheck account restrictions immediately before each external write.
		current, err := s.accountByID(ctx, a.ID)
		if err != nil || current.Restricted || !accounts.SourceEligible(current) || current.PanelKey != target.PanelKey || current.VpnID != target.VPNID || current.SubID != target.SubID || current.AssignedPanelID != nil && *current.AssignedPanelID != target.PanelID || current.VpnBanned != target.Banned || target.Profile != "" && (!(current.AccessProfile != nil) || stringValue(current.AccessProfile) != target.Profile) {
			return false
		}
		n, err := store.New(s.pool).MarkPanelWrite(ctx, store.MarkPanelWriteParams{ID: id, LeaseHash: lease})
		if err != nil || n != 1 {
			return false
		}
		op.WriteStarted = true
		return true
	}
	if v == nil {
		if s.now().UnixMilli() >= target.ExpiryTimeMS || op.WriteStarted && !s.config().PanelDuplicateGuardVerified {
			return fail(true)
		}
		if !write() {
			return fail(true)
		}
		_ = p.AddClient(ctx, target)
		v, e = p.GetClient(ctx, target.PanelKey)
		if e != nil || v == nil {
			return fail(true)
		}
	}
	if target.Banned && v.Enabled {
		if !write() {
			return fail(true)
		}
		_ = p.DisableAccess(ctx, target.PanelKey)
		v, e = p.GetClient(ctx, target.PanelKey)
		if e != nil || v == nil || v.Enabled {
			return fail(true)
		}
	}
	if !Matches(v, target, s.now()) {
		return fail(true)
	}
	if missing := MissingInbounds(v, target); len(missing) > 0 {
		// Existing target IDs must still be enabled regular inbounds before attaching.
		profile := target.Profile
		if profile == "" {
			profile = "regular"
		}
		regular, err := p.ProfileInboundIDs(ctx, profile)
		if err != nil {
			return fail(true)
		}
		allowed := map[int64]bool{}
		for _, id := range regular {
			allowed[id] = true
		}
		for _, id := range missing {
			if !allowed[id] {
				return fail(true)
			}
		}
		if s.now().UnixMilli() >= target.ExpiryTimeMS || !write() {
			return fail(true)
		}
		_ = p.Attach(ctx, target.PanelKey, missing)
		v, e = p.GetClient(ctx, target.PanelKey)
		if e != nil || !Matches(v, target, s.now()) || len(MissingInbounds(v, target)) > 0 {
			return fail(true)
		}
	}
	stop()
	if lost.Load() || ctx.Err() != nil {
		return fail(true)
	}
	// This ping and transaction use the lock-owning physical session, never a replacement.
	if c.Ping(ctx) != nil {
		return fail(true)
	}
	tx, e := c.Begin(ctx)
	if e != nil {
		return fail(true)
	}
	defer tx.Rollback(ctx)
	finalFailure := func() error { tx.Rollback(context.Background()); return fail(true) }
	q = store.New(tx)
	a, e = s.lockAccount(ctx, tx, a.ID)
	if e != nil || a.Restricted || !accounts.SourceEligible(a) || a.PanelKey != target.PanelKey || a.VpnID != target.VPNID || a.SubID != target.SubID || a.AssignedPanelID != nil && *a.AssignedPanelID != target.PanelID {
		return finalFailure()
	}
	n, e := q.ApplyOperation(ctx, store.ApplyOperationParams{ID: id, LeaseHash: lease})
	if e != nil || n != 1 {
		return finalFailure()
	}
	if s.accounts.AssignPanel(ctx, tx, a.ID, op.PanelID) != nil {
		return finalFailure()
	}
	if s.ReleaseServerReservationTx(ctx, tx, a.ID, op.PanelID) != nil {
		return finalFailure()
	}
	if v.UsedTraffic != nil && q.ObserveTraffic(ctx, store.ObserveTrafficParams{ID: id, TrafficUsedBytes: pgtype.Int8{Int64: *v.UsedTraffic, Valid: true}, ObservedAt: stamp(s.now())}) != nil {
		return finalFailure()
	}
	if s.recordOutcome(ctx, tx, op.RequestID, id, "applied") != nil {
		return finalFailure()
	}
	if tx.Commit(ctx) != nil {
		return finalFailure()
	}
	return nil
}

type ProvisionWorker struct {
	river.WorkerDefaults[ProvisionArgs]
	Service *Service
}

func (w *ProvisionWorker) Work(ctx context.Context, j *river.Job[ProvisionArgs]) error {
	return w.Service.Provision(ctx, j.Args.OperationID)
}
func (w *ProvisionWorker) Timeout(*river.Job[ProvisionArgs]) time.Duration {
	return 2*time.Minute + 5*time.Second
}
func (w *ProvisionWorker) NextRetry(j *river.Job[ProvisionArgs]) time.Time {
	delays := []time.Duration{10, 30, 120, 300}
	i := j.Attempt - 1
	if i < 0 {
		i = 0
	}
	if i >= len(delays) {
		i = len(delays) - 1
	}
	return time.Now().Add(delays[i] * time.Second)
}
