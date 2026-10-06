package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"math"
	"slices"
	"time"

	"example.com/cabinet/backend/internal/modules/vpn/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

type AccessWorker struct {
	river.WorkerDefaults[AccessArgs]
	Service *Service
}

func (w *AccessWorker) Work(ctx context.Context, j *river.Job[AccessArgs]) error {
	return w.Service.ApplyAccess(ctx, j.Args.OperationID)
}
func (w *AccessWorker) Timeout(*river.Job[AccessArgs]) time.Duration {
	return 2*time.Minute + 5*time.Second
}
func (w *AccessWorker) NextRetry(j *river.Job[AccessArgs]) time.Time {
	return time.Now().Add(time.Duration(min(j.Attempt, 5)) * 10 * time.Second)
}

func (s *Service) accessStep(ctx context.Context, id uuid.UUID, lease []byte, step string) error {
	n, err := store.New(s.pool).AppendAccessStep(ctx, store.AppendAccessStepParams{ID: id, LeaseHash: lease, Step: step, UpdatedAt: stamp(s.now())})
	if err != nil || n != 1 {
		return unavailable()
	}
	return nil
}

func (s *Service) ApplyAccess(parent context.Context, id uuid.UUID) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	initial, err := store.New(s.pool).AccessOperationByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	if initial.Status == "applied" || initial.Status == "needs_review" || initial.Status == "skipped" {
		return nil
	}
	c, err := s.pool.Acquire(ctx)
	if err != nil {
		return unavailable()
	}
	var locked bool
	if err = c.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended('account-access:'||$1::text,0))", initial.AccountID).Scan(&locked); err != nil || !locked {
		c.Release()
		if err == nil {
			return river.JobSnooze(10 * time.Second)
		}
		return unavailable()
	}
	defer releaseOwner(c, initial.AccountID)
	q := store.New(c)
	current, err := q.AccessOperationByID(ctx, id)
	if err != nil {
		return unavailable()
	}
	if current.Status == "applied" || current.Status == "needs_review" || current.Status == "skipped" {
		return nil
	}
	lease := digest(uuid.NewString())
	op, err := q.LeaseAccessOperation(ctx, store.LeaseAccessOperationParams{ID: id, LeaseHash: lease, UpdatedAt: stamp(s.now())})
	if err != nil {
		return unavailable()
	}
	stop, lost := watchOwner(ctx, c, cancel)
	defer stop()
	cleanup := func(code string, ambiguous bool) error {
		stop()
		other, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		if op.Kind == "monthly_reset" && !op.WriteStarted && !op.ResetStarted && (code == "write_unavailable" || code == "membership_write_unavailable" || code == "reset_write_unavailable") {
			expired, e := s.monthlyOperationExpired(other, op)
			if e == nil && expired {
				return s.finishMonthlyWithoutWrite(other, op, lease, "period_elapsed_unserved")
			}
			current, e := s.accountByID(other, op.AccountID)
			if e == nil && (current.VpnBanned || !(current.AccessProfile != nil) || stringValue(current.AccessProfile) != "unlimited") {
				return s.finishMonthlyWithoutWrite(other, op, lease, "eligibility_changed")
			}
		}
		if ambiguous || op.WriteStarted || op.ResetStarted || op.Attempts >= 5 || lost.Load() {
			tx, e := s.pool.Begin(other)
			if e != nil {
				return unavailable()
			}
			defer tx.Rollback(other)
			n, e := store.New(tx).AccessNeedsReview(other, store.AccessNeedsReviewParams{ID: id, LeaseHash: lease, ReviewReason: pgtype.Text{String: code, Valid: true}, UpdatedAt: stamp(s.now())})
			if e != nil || n != 1 {
				return unavailable()
			}
			if op.Kind == "purchase" {
				if e = s.purchaseOutcome(other, tx, id, "needs_review", code); e != nil {
					return unavailable()
				}
			}
			return tx.Commit(other)
		}
		n, e := store.New(s.pool).AccessRetry(other, store.AccessRetryParams{ID: id, LeaseHash: lease, UpdatedAt: stamp(s.now())})
		if e != nil || n != 1 {
			return unavailable()
		}
		return unavailable()
	}
	var t AccessTarget
	if json.Unmarshal(op.Target, &t) != nil || t.OperationID != id || t.PanelID != s.config().PanelID || t.PanelKey == "" || t.VPNID == uuid.Nil || t.SubID == "" || t.DeviceCount < 0 || t.DeviceCount >= math.MaxInt64 || t.TrafficLimitBytes < 0 || t.ExpiryTimeMS < 0 || len(t.InboundIDs) == 0 {
		return cleanup("invalid_target", true)
	}
	if op.Kind == "monthly_reset" && !op.WriteStarted && !op.ResetStarted {
		expired, e := s.monthlyOperationExpired(ctx, op)
		if e != nil {
			return cleanup("period_guard_unavailable", true)
		}
		if expired {
			stop()
			return s.finishMonthlyWithoutWrite(ctx, op, lease, "period_elapsed_unserved")
		}
	}
	expectedBan := t.Banned
	if op.Kind == "set_vpn_ban" {
		expectedBan = t.PreviousBanned
	}
	a, err := s.accountByID(ctx, op.AccountID)
	if op.Kind == "monthly_reset" && !op.WriteStarted && !op.ResetStarted && err == nil && (a.VpnBanned || !(a.AccessProfile != nil) || stringValue(a.AccessProfile) != "unlimited") {
		stop()
		return s.finishMonthlyWithoutWrite(ctx, op, lease, "eligibility_changed")
	}
	if err != nil || a.PanelKey != t.PanelKey || a.VpnID != t.VPNID || a.SubID != t.SubID || a.VpnBanned != expectedBan || ((a.AssignedPanelID != nil) && stringValue(a.AssignedPanelID) != t.PanelID) {
		return cleanup("identity_changed", true)
	}
	if op.Kind == "purchase" {
		if a.Restricted || a.VpnBanned || op.PurchaseOrderID == nil {
			return cleanup("purchase_guard_changed", true)
		}
		if reason := s.checkPurchase(ctx, nil, op.PurchaseOrderID, op.AccountID, op.ID); reason != "" {
			return cleanup(reason, true)
		}
	}
	executor := op.ExecutionActorID
	if op.Kind == "purchase" {
		executor = nil
	}
	if executor == nil && op.Kind != "monthly_reset" && op.Kind != "purchase" {
		executor = op.OperatorAccountID // Operations written before migration 13.
	}
	if executor == nil && op.Kind != "monthly_reset" && op.Kind != "purchase" {
		return cleanup("actor_missing", true)
	}
	if executor != nil && s.accounts.RequireOperator(ctx, *executor) != nil {
		return cleanup("actor_revoked", true)
	}
	p := s.PanelClient()
	defer p.Close()
	if t.Profile == "regular" || t.Profile == "euru" || t.Profile == "unlimited" {
		selected, e := p.ProfileInboundIDs(ctx, t.Profile)
		if e != nil {
			return cleanup("profile_changed", true)
		}
		attach, detach, e := p.MembershipDiff(ctx, t.InboundIDs, selected)
		if e != nil || len(attach) > 0 || len(detach) > 0 {
			return cleanup("profile_changed", true)
		}
	}
	view, err := p.GetClient(ctx, t.PanelKey)
	if err != nil {
		return cleanup("panel_unavailable", op.WriteStarted)
	}
	markWrite := func() bool {
		if ctx.Err() != nil || lost.Load() {
			return false
		}
		if op.Kind == "purchase" && view != nil && !view.Enabled && view.ExpiryTimeMS > s.now().UnixMilli() && (view.TrafficLimitBytes <= 0 || view.UsedTraffic == nil || *view.UsedTraffic < view.TrafficLimitBytes) {
			return false
		}
		if op.Kind == "monthly_reset" {
			expired, e := s.monthlyOperationExpired(ctx, op)
			if e != nil || expired {
				return false
			}
		}
		if executor != nil && s.accounts.RequireOperator(ctx, *executor) != nil {
			return false
		}
		if op.Kind == "purchase" && s.checkPurchase(ctx, nil, op.PurchaseOrderID, op.AccountID, op.ID) != "" {
			return false
		}
		current, e := s.accountByID(ctx, op.AccountID)
		if e != nil || current.PanelKey != t.PanelKey || current.VpnID != t.VPNID || current.SubID != t.SubID || current.VpnBanned != expectedBan || op.Kind == "purchase" && current.Restricted || op.Kind == "monthly_reset" && (!(current.AccessProfile != nil) || stringValue(current.AccessProfile) != "unlimited") {
			return false
		}
		n, e := store.New(s.pool).MarkAccessWrite(ctx, store.MarkAccessWriteParams{ID: id, LeaseHash: lease, UpdatedAt: stamp(s.now())})
		if e != nil || n != 1 {
			return false
		}
		op.WriteStarted = true
		return true
	}
	if view == nil {
		if !t.Missing || op.WriteStarted || !markWrite() {
			return cleanup("missing_client", true)
		}
		_ = p.AddClient(ctx, ProvisionTarget{OperationID: id, PanelID: t.PanelID, PanelKey: t.PanelKey, VPNID: t.VPNID, SubID: t.SubID, InboundIDs: t.InboundIDs, ExpiryTimeMS: t.ExpiryTimeMS, DeviceCount: t.DeviceCount, TrafficLimitBytes: t.TrafficLimitBytes})
		view, err = p.GetClient(ctx, t.PanelKey)
		if err != nil || view == nil {
			return cleanup("add_unconfirmed", true)
		}
	}
	if view.VPNID != t.VPNID || view.SubID != t.SubID {
		return cleanup("identity_mismatch", true)
	}
	if !op.WriteStarted && !t.Missing {
		previous := slices.Clone(t.PreviousInboundIDs)
		actual := slices.Clone(view.InboundIDs)
		slices.Sort(previous)
		slices.Sort(actual)
		if view.ExpiryTimeMS != t.PreviousExpiryMS || view.LimitIP != t.PreviousLimitIP || view.TrafficLimitBytes != t.PreviousTrafficLimitBytes || !slices.Equal(previous, actual) {
			return cleanup("baseline_changed", true)
		}
	}
	if t.RestoreEnabled {
		if t.Banned || op.Kind != "compensate" || s.now().UnixMilli() >= t.ExpiryTimeMS || t.TrafficLimitBytes > 0 && (view.UsedTraffic == nil || *view.UsedTraffic >= t.TrafficLimitBytes) {
			return cleanup("activation_unsafe", true)
		}
		banned, e := s.accountVPNBan(ctx, op.AccountID)
		if e != nil || banned {
			return cleanup("ban_changed", true)
		}
	}
	limit := t.DeviceCount
	if limit > 0 {
		limit++
	}
	if view.ExpiryTimeMS != t.ExpiryTimeMS || view.LimitIP != limit || view.TrafficLimitBytes != t.TrafficLimitBytes || t.Banned && view.Enabled || (t.RestoreEnabled || t.Enable) && !view.Enabled {
		if !markWrite() {
			return cleanup("write_unavailable", true)
		}
		_ = p.UpdateAccess(ctx, view, t)
		view, err = p.GetClient(ctx, t.PanelKey)
		if err != nil || view == nil || view.VPNID != t.VPNID || view.SubID != t.SubID || view.ExpiryTimeMS != t.ExpiryTimeMS || view.LimitIP != limit || view.TrafficLimitBytes != t.TrafficLimitBytes || (t.RestoreEnabled || t.Enable) && !view.Enabled {
			return cleanup("update_unconfirmed", true)
		}
		if s.accessStep(ctx, id, lease, "panel_updated") != nil {
			return cleanup("step_unrecorded", true)
		}
	}
	attach, detach, err := p.MembershipDiff(ctx, view.InboundIDs, t.InboundIDs)
	if err != nil {
		return cleanup("membership_unknown", true)
	}
	if len(attach) > 0 || len(detach) > 0 {
		if op.WriteStarted && op.Attempts > 1 {
			return cleanup("membership_partial", true)
		}
		if !markWrite() {
			return cleanup("membership_write_unavailable", true)
		}
		if len(attach) > 0 {
			_ = p.Attach(ctx, t.PanelKey, attach)
		}
		if len(detach) > 0 {
			_ = p.Detach(ctx, t.PanelKey, detach)
		}
		view, err = p.GetClient(ctx, t.PanelKey)
		if err != nil || view == nil {
			return cleanup("membership_unconfirmed", true)
		}
		attach, detach, err = p.MembershipDiff(ctx, view.InboundIDs, t.InboundIDs)
		if err != nil || len(attach) > 0 || len(detach) > 0 {
			return cleanup("membership_partial", true)
		}
		if s.accessStep(ctx, id, lease, "membership_updated") != nil {
			return cleanup("step_unrecorded", true)
		}
	}
	if t.Reset {
		shouldReset := !op.ResetStarted || op.ResetAcknowledged
		if op.ResetStarted && !op.ResetAcknowledged {
			if t.Banned {
				if e := p.DisableAccess(ctx, t.PanelKey); e != nil {
					return cleanup("ban_reapply_failed", true)
				}
				if s.accessStep(ctx, id, lease, "ban_reapplied") != nil {
					return cleanup("step_unrecorded", true)
				}
			}
			up, down, e := p.Traffic(ctx, t.PanelKey, t.VPNID, t.SubID)
			if e != nil || up != 0 || down != 0 {
				return cleanup("reset_ambiguous", true)
			}
			if s.accessStep(ctx, id, lease, "reset_confirmed") != nil {
				return cleanup("step_unrecorded", true)
			}
		}
		if shouldReset {
			if !markWrite() {
				return cleanup("reset_write_unavailable", true)
			}
			n, e := store.New(s.pool).MarkAccessReset(ctx, store.MarkAccessResetParams{ID: id, LeaseHash: lease, UpdatedAt: stamp(s.now())})
			if e != nil || n != 1 {
				return cleanup("reset_mark_failed", true)
			}
			op.ResetStarted = true
			op.ResetAcknowledged = false
			resetErr := p.ResetAccessTraffic(ctx, t.PanelKey)
			// Native resetTraffic can re-enable a banned client even when the reply is lost.
			if t.Banned {
				if e = p.DisableAccess(ctx, t.PanelKey); e != nil {
					return cleanup("ban_reapply_failed", true)
				}
				if s.accessStep(ctx, id, lease, "ban_reapplied") != nil {
					return cleanup("step_unrecorded", true)
				}
			}
			up, down, e := p.Traffic(ctx, t.PanelKey, t.VPNID, t.SubID)
			if e != nil || up != 0 || down != 0 {
				return cleanup("reset_unconfirmed", true)
			}
			if resetErr != nil {
				return cleanup("reset_reply_lost", true)
			}
			if s.accessStep(ctx, id, lease, "reset_confirmed") != nil {
				return cleanup("step_unrecorded", true)
			}
		} else if t.Banned {
			if e := p.DisableAccess(ctx, t.PanelKey); e != nil {
				return cleanup("ban_reapply_failed", true)
			}
			if s.accessStep(ctx, id, lease, "ban_reapplied") != nil {
				return cleanup("step_unrecorded", true)
			}
		}
	}
	view, err = p.GetClient(ctx, t.PanelKey)
	if err != nil || view == nil || view.VPNID != t.VPNID || view.SubID != t.SubID || view.ExpiryTimeMS != t.ExpiryTimeMS || view.LimitIP != limit || view.TrafficLimitBytes != t.TrafficLimitBytes || t.Banned && view.Enabled || (t.RestoreEnabled || t.Enable) && !view.Enabled {
		return cleanup("readback_mismatch", true)
	}
	attach, detach, err = p.MembershipDiff(ctx, view.InboundIDs, t.InboundIDs)
	if err != nil || len(attach) > 0 || len(detach) > 0 {
		return cleanup("membership_mismatch", true)
	}
	stop()
	if lost.Load() || ctx.Err() != nil || c.Ping(ctx) != nil {
		return cleanup("owner_lost", true)
	}
	tx, err := c.Begin(ctx)
	if err != nil {
		return cleanup("final_tx_failed", true)
	}
	defer tx.Rollback(ctx)
	final := store.New(tx)
	a, err = s.lockAccount(ctx, tx, op.AccountID)
	if err != nil || a.PanelKey != t.PanelKey || a.VpnID != t.VPNID || a.SubID != t.SubID || a.VpnBanned != expectedBan {
		tx.Rollback(ctx)
		return cleanup("identity_changed", true)
	}
	if op.Kind == "purchase" {
		if a.Restricted {
			tx.Rollback(ctx)
			return cleanup("purchase_guard_changed", true)
		}
		if reason := s.checkPurchase(ctx, tx, op.PurchaseOrderID, op.AccountID, op.ID); reason != "" {
			tx.Rollback(ctx)
			return cleanup(reason, true)
		}
	}
	n, err := final.AccessApplied(ctx, store.AccessAppliedParams{ID: id, LeaseHash: lease, UpdatedAt: stamp(s.now())})
	if err != nil || n != 1 {
		tx.Rollback(ctx)
		return cleanup("final_write_failed", true)
	}
	if t.Missing {
		if err = s.accounts.AssignPanel(ctx, tx, a.ID, t.PanelID); err != nil {
			tx.Rollback(ctx)
			return cleanup("assign_failed", true)
		}
	}
	if err = s.accounts.SetAccessMetadata(ctx, tx, a.ID, t.Profile, t.Banned); err != nil {
		tx.Rollback(ctx)
		return cleanup("profile_save_failed", true)
	}
	if op.Kind == "purchase" {
		if err = s.purchaseOutcome(ctx, tx, id, "applied", ""); err != nil {
			tx.Rollback(ctx)
			return cleanup("purchase_status_failed", true)
		}
	}
	if op.Kind == "monthly_reset" {
		err = monthlyAudit(ctx, tx, a.ID, op.MonthlyPeriod.String, "monthly_reset_applied", &id, s.now())
	} else {
		err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: "access_applied", AccountID: a.ID, OperatorAccountID: op.OperatorAccountID, Reason: &op.Reason, AccessOperationID: &id})
	}
	if err != nil {
		tx.Rollback(ctx)
		return cleanup("audit_failed", true)
	}
	if err = tx.Commit(ctx); err != nil {
		return cleanup("commit_failed", true)
	}
	return nil
}
