package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/vpn/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) WithGroupFailureNotifier(notify func(context.Context, pgx.Tx, uuid.UUID, string) error) {
	s.groupFailure = notify
}

func (s *Service) groupFailureTx(ctx context.Context, tx pgx.Tx, account uuid.UUID, code string, operation *uuid.UUID) error {
	system := true
	if err := auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: "group_reconcile_failed", AccountID: account, SystemActor: &system, Reason: &code, AccessOperationID: operation}); err != nil {
		return unavailable()
	}
	if s.groupFailure != nil {
		return s.groupFailure(ctx, tx, account, code)
	}
	return nil
}

// Preparation never writes the panel; AccessWorker remains its sole executor.
func (s *Service) PrepareGroupReconciliation(ctx context.Context, account uuid.UUID) (uuid.UUID, error) {
	owner, err := s.OpenAccessOwner(ctx, account)
	if err != nil {
		return uuid.Nil, unavailable()
	}
	defer owner.Release()
	if err = owner.TryLock(ctx); errors.Is(err, ErrBusy) {
		return uuid.Nil, nil
	} else if err != nil {
		return uuid.Nil, unavailable()
	}
	tx, err := owner.Begin(ctx)
	if err != nil {
		return uuid.Nil, unavailable()
	}
	defer tx.Rollback(ctx)
	a, err := s.lockAccount(ctx, tx, account)
	if err != nil {
		return uuid.Nil, unavailable()
	}
	active, err := s.UnresolvedTrialTx(ctx, tx, account)
	if err != nil {
		return uuid.Nil, unavailable()
	}
	if active || a.AssignedPanelID == nil {
		return uuid.Nil, nil
	}
	var prior *store.AccessOperation
	if active, err = s.UnresolvedAccessTx(ctx, tx, account); err != nil {
		return uuid.Nil, unavailable()
	} else if active {
		row, err := store.New(tx).LatestAccessOperation(ctx, account)
		if err != nil {
			return uuid.Nil, unavailable()
		}
		if row.Kind != "group_reconcile" {
			return uuid.Nil, nil
		}
		if row.Status != "needs_review" {
			return row.ID, nil
		}
		prior = &row
	}
	if tx.Commit(ctx) != nil {
		return uuid.Nil, unavailable()
	}
	fail := func(code string, cause error) (uuid.UUID, error) {
		tx, err := owner.Begin(ctx)
		if err != nil {
			return uuid.Nil, unavailable()
		}
		defer tx.Rollback(ctx)
		if s.groupFailureTx(ctx, tx, account, code, nil) != nil || tx.Commit(ctx) != nil {
			return uuid.Nil, unavailable()
		}
		return uuid.Nil, cause
	}
	profile := stringValue(a.AccessProfile)
	if profile != "regular" && profile != "euru" && profile != "unlimited" {
		return fail("unknown_profile", ErrMembership)
	}
	p, err := owner.PanelFor(ctx, *a.AssignedPanelID)
	if err != nil {
		return fail("panel_unavailable", ErrPanel)
	}
	defer p.Close()
	v, err := p.GetClient(ctx, a.PanelKey)
	if err != nil {
		return fail("panel_unavailable", ErrPanel)
	}
	if v == nil {
		return fail("missing_client", ErrIdentity)
	}
	if v.PanelKey != a.PanelKey || v.VPNID != a.VpnID || v.SubID != a.SubID {
		return fail("identity_mismatch", ErrIdentity)
	}
	ids, err := p.ProfileInboundIDs(ctx, profile)
	banOnly := false
	if err != nil {
		if !errors.Is(err, ErrMembership) {
			return fail("panel_unavailable", ErrPanel)
		}
		if !a.VpnBanned || !v.Enabled {
			return fail("empty_membership", ErrMembership)
		}
		banOnly = true
		ids = []int64{}
	}
	attach, detach := []int64{}, []int64{}
	if !banOnly {
		attach, detach, err = p.MembershipDiff(ctx, v.InboundIDs, ids)
		if err != nil {
			return fail("panel_unavailable", ErrPanel)
		}
	}
	tx, err = owner.Begin(ctx)
	if err != nil {
		return uuid.Nil, unavailable()
	}
	defer tx.Rollback(ctx)
	current, err := s.lockAccount(ctx, tx, account)
	if err != nil {
		return uuid.Nil, unavailable()
	}
	if !reflect.DeepEqual(a, current) {
		return uuid.Nil, nil
	}
	if active, err = s.UnresolvedTrialTx(ctx, tx, account); err != nil {
		return uuid.Nil, unavailable()
	} else if active {
		return uuid.Nil, nil
	}
	if prior != nil {
		// A fresh owned read retires only group work; all original targets remain evidence.
		tag, err := tx.Exec(ctx, `UPDATE access_operations SET status='skipped',updated_at=$2 WHERE id=$1 AND kind='group_reconcile' AND status='needs_review'`, prior.ID, s.now())
		if err != nil || tag.RowsAffected() != 1 {
			return uuid.Nil, unavailable()
		}
		system := true
		if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: "group_reconcile_superseded", AccountID: account, AccessOperationID: &prior.ID, SystemActor: &system}) != nil {
			return uuid.Nil, unavailable()
		}
	}
	if active, err = s.UnresolvedAccessTx(ctx, tx, account); err != nil {
		return uuid.Nil, unavailable()
	} else if active {
		return uuid.Nil, nil
	}
	if len(attach) == 0 && len(detach) == 0 && (!a.VpnBanned || !v.Enabled) {
		baseline, err := s.AccessBaselineTx(ctx, tx, account)
		if err != nil {
			return uuid.Nil, unavailable()
		}
		var captured AccessTarget
		if baseline.AccessID != nil && json.Unmarshal(baseline.AccessTarget, &captured) != nil {
			return uuid.Nil, unavailable()
		}
		if (baseline.AccessID == nil || captured.NoClientIntent) && baseline.TrialStatus == "applied" {
			captured = AccessTarget{}
			if json.Unmarshal(baseline.TrialTarget, &captured) != nil {
				return uuid.Nil, unavailable()
			}
			if captured.Profile == "" {
				captured.Profile = "regular"
			}
		}
		// Confirm changed group targets even when the panel already applied their memberships.
		if prior == nil && (captured.OperationID == uuid.Nil || captured.NoClientIntent || captured.Profile == profile && slices.Equal(captured.InboundIDs, ids)) {
			return uuid.Nil, tx.Commit(ctx)
		}
	}
	if banOnly && s.groupFailureTx(ctx, tx, account, "empty_membership", nil) != nil {
		return uuid.Nil, unavailable()
	}
	id := uuid.New()
	target := AccessTarget{OperationID: id, PanelID: *a.AssignedPanelID, PanelKey: a.PanelKey, VPNID: a.VpnID, SubID: a.SubID, Profile: profile, InboundIDs: ids, Banned: a.VpnBanned, PreviousBanned: a.VpnBanned, ExpiryTimeMS: v.ExpiryTimeMS, DeviceCount: max(0, v.LimitIP-1), TrafficLimitBytes: v.TrafficLimitBytes, PreviousExpiryMS: v.ExpiryTimeMS, PreviousLimitIP: v.LimitIP, PreviousTrafficLimitBytes: v.TrafficLimitBytes, PreviousInboundIDs: v.InboundIDs}
	desired := AccessDesired{Devices: target.DeviceCount, TrafficLimitBytes: target.TrafficLimitBytes, Profile: profile, VpnBanned: a.VpnBanned}
	if v.ExpiryTimeMS > 0 {
		at := time.UnixMilli(v.ExpiryTimeMS)
		desired.ExpiresAt = &at
	}
	targetRaw, err := json.Marshal(target)
	if err != nil {
		return uuid.Nil, unavailable()
	}
	desiredRaw, err := json.Marshal(desired)
	if err != nil {
		return uuid.Nil, unavailable()
	}
	if _, err = s.QueueAccessTx(ctx, tx, AccessWrite{ID: id, AccountID: account, Kind: "group_reconcile", Reason: "scheduled group reconciliation", Desired: desiredRaw, Target: targetRaw, CreatedAt: s.now()}); err != nil {
		return uuid.Nil, err
	}
	system := true
	if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: "group_reconcile_requested", AccountID: account, AccessOperationID: &id, SystemActor: &system}) != nil || tx.Commit(ctx) != nil {
		return uuid.Nil, unavailable()
	}
	return id, nil
}

func (s *Service) ReconcileGroups(ctx context.Context) error {
	ids, err := s.accounts.AssignedVPNAccounts(ctx)
	if err != nil {
		return unavailable()
	}
	for _, id := range ids {
		if _, err = s.PrepareGroupReconciliation(ctx, id); err != nil && !errors.Is(err, ErrPanel) && !errors.Is(err, ErrMembership) && !errors.Is(err, ErrIdentity) {
			return err
		}
	}
	return nil
}

func (s *Service) RunGroupReconciliationScheduler(ctx context.Context) error {
	timer := time.NewTicker(time.Hour)
	defer timer.Stop()
	for {
		if err := s.ReconcileGroups(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}
