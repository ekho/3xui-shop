package s01

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"math"
	"sync"
	"sync/atomic"
	"time"
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
func releaseOwner(c *pgxpool.Conn, id uuid.UUID) {
	ctx, end := context.WithTimeout(context.Background(), time.Second)
	defer end()
	if _, e := c.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended('provision:'||$1::text,0))`, id.String()); e != nil {
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
	a, e := q.LockAccount(ctx, op.AccountID)
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
	r, e := q.TrialByID(ctx, op.RequestID)
	if e != nil {
		return unavailable()
	}
	if s.audit(ctx, q, "provision_needs_review", a.ID, &r.ID, &op.ID, 0, "") != nil || s.notify(ctx, q, a, r, "provision_review", "needs_review") != nil || tx.Commit(ctx) != nil {
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
	c, e := s.pool.Acquire(ctx)
	if e != nil {
		return unavailable()
	}
	var locked bool
	e = c.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended('provision:'||$1::text,0))`, id.String()).Scan(&locked)
	if e != nil || !locked {
		c.Release()
		if e == nil {
			return river.JobSnooze(10 * time.Second)
		}
		return unavailable()
	}
	defer releaseOwner(c, id)
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
	a, e := store.New(s.pool).AccountByID(ctx, op.AccountID)
	if e != nil {
		return fail(false)
	}
	if a.Restricted || !sourceEligible(a) || !validSnapshot(op) || op.PanelID != s.cfg.PanelID {
		return fail(true)
	}
	p := NewPanelClient(s.cfg)
	defer p.Close()
	var target ProvisionTarget
	hadTarget := len(op.Target) > 0
	if hadTarget {
		if json.Unmarshal(op.Target, &target) != nil {
			return fail(true)
		}
	} else {
		ids, err := p.RegularInboundIDs(ctx)
		if err != nil {
			return fail(false)
		}
		if len(ids) == 0 {
			return fail(true)
		}
		expiry := op.FirstStartedAt.Time.Add(time.Duration(op.PeriodDays) * 24 * time.Hour).UnixMilli()
		if expiry <= op.FirstStartedAt.Time.UnixMilli() {
			return fail(true)
		}
		target = ProvisionTarget{OperationID: op.ID, PanelID: op.PanelID, PanelKey: a.PanelKey, VPNID: a.VpnID, SubID: a.SubID, InboundIDs: ids, ExpiryTimeMS: expiry, DeviceCount: op.Devices, TrafficLimitBytes: op.TrafficGb * 1024 * 1024 * 1024}
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
		current, err := store.New(s.pool).AccountByID(ctx, a.ID)
		if err != nil || current.Restricted {
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
		if s.now().UnixMilli() >= target.ExpiryTimeMS || op.WriteStarted && !s.cfg.PanelDuplicateGuardVerified {
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
	if !panelMatches(v, target, s.now()) {
		return fail(true)
	}
	if missing := missingInbounds(v, target); len(missing) > 0 {
		// Existing target IDs must still be enabled regular inbounds before attaching.
		regular, err := p.RegularInboundIDs(ctx)
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
		if e != nil || !panelMatches(v, target, s.now()) || len(missingInbounds(v, target)) > 0 {
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
	a, e = q.LockAccount(ctx, a.ID)
	if e != nil || a.Restricted {
		return finalFailure()
	}
	n, e := q.ApplyOperation(ctx, store.ApplyOperationParams{ID: id, LeaseHash: lease})
	if e != nil || n != 1 {
		return finalFailure()
	}
	if q.GrantApplied(ctx, store.GrantAppliedParams{OperationID: id, GrantedAt: stamp(s.now())}) != nil || q.AssignPanel(ctx, store.AssignPanelParams{ID: a.ID, AssignedPanelID: pgtype.Text{String: op.PanelID, Valid: true}}) != nil {
		return finalFailure()
	}
	if v.UsedTraffic != nil && q.ObserveTraffic(ctx, store.ObserveTrafficParams{ID: id, TrafficUsedBytes: pgtype.Int8{Int64: *v.UsedTraffic, Valid: true}, ObservedAt: stamp(s.now())}) != nil {
		return finalFailure()
	}
	r, e := q.TrialByID(ctx, op.RequestID)
	if e != nil {
		return finalFailure()
	}
	if s.audit(ctx, q, "provision_applied", a.ID, &r.ID, &id, 0, "") != nil || s.notify(ctx, q, a, r, "provision_applied", "active") != nil {
		return finalFailure()
	}
	if tx.Commit(ctx) != nil {
		return finalFailure()
	}
	return nil
}
func (s *Service) ReconcileTrialOperation(ctx context.Context, id, key uuid.UUID, in wire.ReconcileInput) (wire.ReconcileResult, error) {
	out := wire.ReconcileResult{}
	if !s.operatorAllowed(in.OperatorTgId) {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	if id == uuid.Nil || key == uuid.Nil || !validText(in.Reason, 1, 1000) {
		return out, failure(400, "INVALID_INPUT")
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	principal := fmt.Sprintf("operator:%d", in.OperatorTgId)
	hash := bodyHash(struct {
		ID    uuid.UUID
		Input wire.ReconcileInput
	}{id, in})
	if q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "reconcileTrialOperation", Key: key}) != nil {
		return out, unavailable()
	}
	op, e := q.OperationByID(ctx, id)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if e != nil {
		return out, unavailable()
	}
	a, e := q.LockAccount(ctx, op.AccountID)
	if e != nil {
		return out, unavailable()
	}
	if prior, found, err := replay[wire.ReconcileResult](ctx, q, principal, "reconcileTrialOperation", key, hash); found || err != nil {
		return prior, err
	}
	out, e = s.reconcileOperationLocked(ctx, tx, q, a, op, trialActor{telegramID: in.OperatorTgId}, in.Reason)
	if e != nil {
		return out, e
	}
	if s.saveIdempotency(ctx, q, principal, "reconcileTrialOperation", key, hash, out) != nil || tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	return out, nil
}

func (s *Service) reconcileOperationLocked(ctx context.Context, tx pgx.Tx, q *store.Queries, a store.Account, op store.TrialOperation, actor trialActor, reason string) (wire.ReconcileResult, error) {
	var out wire.ReconcileResult
	id := op.ID
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('provision:'||$1::text,0))`, id.String()).Scan(&locked); err != nil {
		return out, unavailable()
	}
	current, err := q.OperationByID(ctx, id)
	if err != nil {
		return out, unavailable()
	}
	if !locked || current.Status != "needs_review" {
		return out, failure(409, "REQUEST_STATE_CONFLICT")
	}
	if a.Restricted || !sourceEligible(a) {
		return out, failure(403, "ACCOUNT_RESTRICTED")
	}
	if err = q.RequeueOperation(ctx, id); err != nil {
		return out, unavailable()
	}
	if _, err = s.queue.InsertTx(ctx, tx, ProvisionArgs{OperationID: id}, &river.InsertOpts{Queue: "provision", MaxAttempts: 5}); err != nil {
		return out, unavailable()
	}
	if err = s.trialActorAudit(ctx, q, "provision_reconcile_requested", a.ID, op.RequestID, &id, actor, reason); err != nil {
		return out, err
	}
	return wire.ReconcileResult{OperationId: id, Status: "provisioning"}, nil
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
