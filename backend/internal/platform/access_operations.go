package platform

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"math"
	"strings"
	"time"
)

type AccessArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (AccessArgs) Kind() string { return "access_operation" }

// The private persisted target is fixed before any panel write. It is never returned by HTTP.
type accessTarget struct {
	OperationID               uuid.UUID `json:"operation_id"`
	PanelID                   string    `json:"panel_id"`
	PanelKey                  string    `json:"panel_key"`
	VPNID                     uuid.UUID `json:"vpn_id"`
	SubID                     string    `json:"sub_id"`
	ExpiryTimeMS              int64     `json:"expiry_time_ms"`
	DeviceCount               int64     `json:"device_count"`
	TrafficLimitBytes         int64     `json:"traffic_limit_bytes"`
	Profile                   string    `json:"profile"`
	InboundIDs                []int64   `json:"inbound_ids"`
	Missing                   bool      `json:"missing"`
	Reset                     bool      `json:"reset"`
	Banned                    bool      `json:"banned"`
	PreviousBanned            bool      `json:"previous_banned"`
	Enable                    bool      `json:"enable"`
	NoClientIntent            bool      `json:"no_client_intent"`
	RestoreEnabled            bool      `json:"restore_enabled"`
	PreviousExpiryMS          int64     `json:"previous_expiry_ms"`
	PreviousLimitIP           int64     `json:"previous_limit_ip"`
	PreviousTrafficLimitBytes int64     `json:"previous_traffic_limit_bytes"`
	PreviousInboundIDs        []int64   `json:"previous_inbound_ids"`
}

type unlimitedPlan struct {
	ID       uuid.UUID
	Revision int64
	Terms    catalogue.Terms
}

func (s *Service) unlimitedAccessPlan(ctx context.Context, tx pgx.Tx) (unlimitedPlan, error) {
	rows, err := s.catalogue.UnlimitedPlansTx(ctx, tx)
	if errors.Is(err, catalogue.ErrInvalidTerms) {
		return unlimitedPlan{}, failure(409, "ACCESS_PLAN_CONFLICT")
	}
	if err != nil {
		return unlimitedPlan{}, unavailable()
	}
	var out unlimitedPlan
	count := 0
	for _, row := range rows {
		if row.Archived {
			continue
		}
		count++
		out.Terms = row.Terms
		if !row.Hidden || out.Terms.Profile != "unlimited" || !out.Terms.Hidden || out.Terms.Devices < 1 || out.Terms.Devices > 10000 || out.Terms.TrafficGb < 1 || out.Terms.TrafficGb > 100000 {
			return unlimitedPlan{}, failure(409, "ACCESS_PLAN_CONFLICT")
		}
		out.ID, out.Revision = row.ID, row.Revision
	}
	if count != 1 {
		return unlimitedPlan{}, failure(409, "ACCESS_PLAN_CONFLICT")
	}
	return out, nil
}

func accessPublic(r store.AccessOperation) (wire.AccessOperation, error) {
	var desired wire.AccessDesired
	var steps []wire.AccessOperationCompletedSteps
	if json.Unmarshal(r.Desired, &desired) != nil || json.Unmarshal(r.CompletedSteps, &steps) != nil || !r.CreatedAt.Valid || !r.UpdatedAt.Valid {
		return wire.AccessOperation{}, unavailable()
	}
	if steps == nil {
		steps = []wire.AccessOperationCompletedSteps{}
	}
	out := wire.AccessOperation{OperationId: r.ID, AccountId: r.AccountID, OperatorAccountId: r.OperatorAccountID, Kind: wire.AccessOperationKind(r.Kind), Status: wire.AccessOperationStatus(r.Status), CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time, Reason: r.Reason, Desired: desired, CompletedSteps: steps}
	if r.ReviewReason.Valid {
		out.ReviewReason = &r.ReviewReason.String
	}
	return out, nil
}

func accessInput(in wire.AccessOperationInput) error {
	if !validText(strings.TrimSpace(in.Reason), 1, 1000) {
		return failure(400, "INVALID_INPUT")
	}
	switch in.Kind {
	case "compensate":
		if in.Days == nil || *in.Days < 1 || *in.Days > 365 || in.PlanId != nil || in.Revision != nil || in.PeriodDays != nil || in.Profile != nil || in.VpnBanned != nil {
			return failure(400, "INVALID_INPUT")
		}
	case "assign_plan":
		if in.Days != nil || in.PlanId == nil || *in.PlanId == uuid.Nil || in.Revision == nil || *in.Revision < 1 || in.PeriodDays == nil || *in.PeriodDays < 1 || *in.PeriodDays > 106751 || in.Profile != nil || in.VpnBanned != nil {
			return failure(400, "INVALID_INPUT")
		}
	case "starter_trial", "reset_traffic":
		if in.Days != nil || in.PlanId != nil || in.Revision != nil || in.PeriodDays != nil || in.Profile != nil || in.VpnBanned != nil {
			return failure(400, "INVALID_INPUT")
		}
	case "set_profile":
		if in.Profile == nil || (*in.Profile != "regular" && *in.Profile != "euru" && *in.Profile != "unlimited") || in.Days != nil || in.PlanId != nil || in.Revision != nil || in.PeriodDays != nil || in.VpnBanned != nil {
			return failure(400, "INVALID_INPUT")
		}
	case "set_vpn_ban":
		if in.VpnBanned == nil || in.Days != nil || in.PlanId != nil || in.Revision != nil || in.PeriodDays != nil || in.Profile != nil {
			return failure(400, "INVALID_INPUT")
		}
	default:
		return failure(400, "INVALID_INPUT")
	}
	return nil
}

func accessOwnerSQL() string {
	return "SELECT pg_try_advisory_xact_lock(hashtextextended('account-access:'||$1::text,0))"
}
func accessConflict(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "access_one_unresolved_account" {
		return failure(409, "ACCESS_OPERATION_CONFLICT")
	}
	return unavailable()
}

func (s *Service) CreateAccessOperation(ctx context.Context, actor, target, key uuid.UUID, in wire.AccessOperationInput) (wire.AccessOperation, error) {
	var out wire.AccessOperation
	if actor == uuid.Nil || target == uuid.Nil || key == uuid.Nil {
		return out, failure(400, "INVALID_INPUT")
	}
	if err := accessInput(in); err != nil {
		return out, err
	}
	principal := "operator-account:" + actor.String()
	hash := bodyHash(struct {
		Target uuid.UUID
		Input  wire.AccessOperationInput
	}{target, in})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	a, err := s.lockOperatorPair(ctx, tx, actor, target)
	if err != nil {
		return out, err
	}
	q := store.New(tx)
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "createAccessOperation", Key: key}); err != nil {
		return out, unavailable()
	}
	if prior, found, e := replay[wire.AccessOperation](ctx, q, principal, "createAccessOperation", key, hash); found || e != nil {
		return prior, e
	}
	var locked bool
	if err = tx.QueryRow(ctx, accessOwnerSQL(), target).Scan(&locked); err != nil {
		return out, unavailable()
	}
	if !locked {
		return out, failure(409, "ACCESS_OPERATION_CONFLICT")
	}
	active, err := q.UnresolvedAccessExists(ctx, target)
	if err != nil {
		return out, unavailable()
	}
	trial, err := q.UnresolvedTrialExists(ctx, target)
	if err != nil {
		return out, unavailable()
	}
	if active || trial {
		return out, failure(409, "ACCESS_OPERATION_CONFLICT")
	}
	if s.cfg.PanelID == "" || (a.AssignedPanelID.Valid && a.AssignedPanelID.String != s.cfg.PanelID) {
		return out, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	panel := NewPanelClient(s.cfg)
	defer panel.Close()
	v, err := panel.GetClient(ctx, a.PanelKey)
	if err != nil {
		return out, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	if v != nil && (v.VPNID != a.VpnID || v.SubID != a.SubID || !a.AssignedPanelID.Valid) {
		return out, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	if v == nil && in.Kind != "compensate" && in.Kind != "assign_plan" && in.Kind != "set_profile" && in.Kind != "set_vpn_ban" {
		return out, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	id := uuid.New()
	t := accessTarget{OperationID: id, PanelID: s.cfg.PanelID, PanelKey: a.PanelKey, VPNID: a.VpnID, SubID: a.SubID, Banned: a.VpnBanned, PreviousBanned: a.VpnBanned}
	var planID *uuid.UUID
	var planRev, period pgtype.Int8
	var profile string
	if v != nil {
		profile, err = s.confirmedAccessProfile(ctx, q, a, v, panel, in.Kind == "set_profile" || in.Kind == "set_vpn_ban")
		if err != nil {
			return out, failure(409, "ACCESS_NOT_ELIGIBLE")
		}
		if profile == "regular" || profile == "euru" || profile == "unlimited" {
			expected, e := panel.ProfileInboundIDs(ctx, profile)
			if e != nil {
				return out, failure(409, "ACCESS_NOT_ELIGIBLE")
			}
			attach, detach, e := panel.MembershipDiff(ctx, v.InboundIDs, expected)
			if e != nil || len(attach) > 0 || len(detach) > 0 {
				return out, failure(409, "ACCESS_NOT_ELIGIBLE")
			}
		}
		t.ExpiryTimeMS = v.ExpiryTimeMS
		t.PreviousExpiryMS = v.ExpiryTimeMS
		t.PreviousLimitIP = v.LimitIP
		t.PreviousTrafficLimitBytes = v.TrafficLimitBytes
		t.PreviousInboundIDs = append([]int64{}, v.InboundIDs...)
		t.DeviceCount = max(0, v.LimitIP-1)
		t.TrafficLimitBytes = v.TrafficLimitBytes
		t.Profile = profile
		t.InboundIDs = append([]int64{}, v.InboundIDs...)
	} else {
		if a.HadSubscription || a.AssignedPanelID.Valid || a.VpnBanned && in.Kind != "set_profile" && in.Kind != "set_vpn_ban" {
			return out, failure(409, "ACCESS_NOT_ELIGIBLE")
		}
		t.Missing = in.Kind != "set_profile" && in.Kind != "set_vpn_ban"
		t.NoClientIntent = !t.Missing
		t.Profile = a.AccessProfile.String
		if t.Profile == "" && a.Kind == "web" {
			t.Profile = "regular"
		}
		t.DeviceCount = s.cfg.TrialDevices
		t.TrafficLimitBytes = 0
	}
	switch in.Kind {
	case "compensate":
		if a.VpnBanned || t.Profile == "unlimited" || !t.Missing && t.ExpiryTimeMS == 0 {
			return out, failure(409, "ACCESS_NOT_ELIGIBLE")
		}
		if t.Missing {
			if t.DeviceCount < 1 || t.DeviceCount >= math.MaxInt64 {
				return out, unavailable()
			}
			t.InboundIDs, err = panel.ProfileInboundIDs(ctx, t.Profile)
			if err != nil {
				return out, failure(409, "ACCESS_NOT_ELIGIBLE")
			}
		}
		if v != nil && !v.Enabled && !a.VpnBanned && v.ExpiryTimeMS > 0 && v.ExpiryTimeMS <= now.UnixMilli() {
			if v.TrafficLimitBytes > 0 && v.UsedTraffic == nil {
				return out, failure(409, "ACCESS_NOT_ELIGIBLE")
			}
			t.RestoreEnabled = v.TrafficLimitBytes == 0 || *v.UsedTraffic < v.TrafficLimitBytes
		}
		base := now.UnixMilli()
		if t.ExpiryTimeMS > base {
			base = t.ExpiryTimeMS
		}
		added := int64(*in.Days) * int64(24*time.Hour/time.Millisecond)
		if base > math.MaxInt64-added {
			return out, failure(400, "INVALID_INPUT")
		}
		t.ExpiryTimeMS = base + added
	case "assign_plan":
		plan, err := s.catalogue.CurrentPlanTx(ctx, tx, *in.PlanId)
		if errors.Is(err, catalogue.ErrNotFound) {
			return out, failure(409, "ACCESS_PLAN_CONFLICT")
		}
		if err != nil && !errors.Is(err, catalogue.ErrInvalidTerms) {
			return out, unavailable()
		}
		if plan.Archived || plan.Revision != *in.Revision || plan.Profile == "unlimited" {
			return out, failure(409, "ACCESS_PLAN_CONFLICT")
		}
		if err != nil {
			return out, unavailable()
		}
		terms := plan.Terms
		found := false
		for _, days := range terms.Periods {
			if days == *in.PeriodDays {
				found = true
			}
		}
		if !found || terms.TrafficGb > math.MaxInt64/(1024*1024*1024) {
			return out, failure(409, "ACCESS_PLAN_CONFLICT")
		}
		t.Profile = string(terms.Profile)
		t.DeviceCount = int64(terms.Devices)
		t.TrafficLimitBytes = int64(terms.TrafficGb) * 1024 * 1024 * 1024
		t.Reset = true
		t.ExpiryTimeMS = now.Add(time.Duration(*in.PeriodDays) * 24 * time.Hour).UnixMilli()
		t.InboundIDs, err = panel.ProfileInboundIDs(ctx, t.Profile)
		if err != nil {
			return out, failure(409, "ACCESS_NOT_ELIGIBLE")
		}
		planID = in.PlanId
		planRev = pgtype.Int8{Int64: *in.Revision, Valid: true}
		period = pgtype.Int8{Int64: *in.PeriodDays, Valid: true}
	case "starter_trial":
		if s.cfg.TrialPeriodDays < 1 || s.cfg.TrialPeriodDays > 106751 || s.cfg.TrialDevices < 1 || s.cfg.TrialTrafficGB < 0 || s.cfg.TrialTrafficGB > math.MaxInt64/(1024*1024*1024) {
			return out, unavailable()
		}
		t.Profile = "regular"
		t.DeviceCount = s.cfg.TrialDevices
		t.TrafficLimitBytes = s.cfg.TrialTrafficGB * 1024 * 1024 * 1024
		t.Reset = true
		t.ExpiryTimeMS = now.Add(time.Duration(s.cfg.TrialPeriodDays) * 24 * time.Hour).UnixMilli()
		t.InboundIDs, err = panel.ProfileInboundIDs(ctx, "regular")
		if err != nil {
			return out, failure(409, "ACCESS_NOT_ELIGIBLE")
		}
	case "reset_traffic":
		t.Reset = true
	case "set_profile":
		requested := string(*in.Profile)
		if requested == "unlimited" {
			if s.queue == nil {
				return out, unavailable()
			}
			if _, e := s.monthlyZone(); e != nil {
				return out, unavailable()
			}
			if _, e := tx.Exec(ctx, "SELECT 1 FROM monthly_reset_periods LIMIT 0"); e != nil {
				return out, unavailable()
			}
			plan, e := s.unlimitedAccessPlan(ctx, tx)
			if e != nil {
				return out, e
			}
			t.DeviceCount, t.TrafficLimitBytes, t.ExpiryTimeMS = int64(plan.Terms.Devices), int64(plan.Terms.TrafficGb)*1024*1024*1024, 0
			// Legacy unlimited grant preserves existing traffic; monthly reset runs by period.
			if v == nil {
				t.Missing, t.NoClientIntent = true, false
			} else if !t.Banned && !v.Enabled && v.ExpiryTimeMS > 0 && v.ExpiryTimeMS <= now.UnixMilli() {
				if v.UsedTraffic == nil {
					return out, failure(409, "ACCESS_NOT_ELIGIBLE")
				}
				t.Enable = t.TrafficLimitBytes == 0 || *v.UsedTraffic < t.TrafficLimitBytes
			}
			planID = &plan.ID
			planRev = pgtype.Int8{Int64: plan.Revision, Valid: true}
		} else if t.Profile == "unlimited" && v != nil {
			if s.cfg.TrialPeriodDays < 1 || s.cfg.TrialDevices < 1 || s.cfg.TrialTrafficGB < 0 || s.cfg.TrialTrafficGB > math.MaxInt64/(1024*1024*1024) {
				return out, unavailable()
			}
			t.DeviceCount, t.TrafficLimitBytes = s.cfg.TrialDevices, s.cfg.TrialTrafficGB*1024*1024*1024
			t.ExpiryTimeMS = now.Add(time.Duration(s.cfg.TrialPeriodDays) * 24 * time.Hour).UnixMilli()
			t.Reset = true
		}
		t.Profile = requested
		if v != nil || requested == "unlimited" {
			t.InboundIDs, err = panel.ProfileInboundIDs(ctx, requested)
			if err != nil {
				return out, failure(409, "ACCESS_NOT_ELIGIBLE")
			}
		}
	case "set_vpn_ban":
		t.Banned = *in.VpnBanned
		t.Enable = !t.Banned
	}
	if t.ExpiryTimeMS < 0 || t.DeviceCount < 0 || t.DeviceCount >= math.MaxInt64 || len(t.InboundIDs) == 0 && !t.NoClientIntent {
		return out, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	expires := (*time.Time)(nil)
	if t.ExpiryTimeMS > 0 {
		value := time.UnixMilli(t.ExpiryTimeMS)
		expires = &value
	}
	desired := wire.AccessDesired{ExpiresAt: expires, Devices: t.DeviceCount, TrafficLimitBytes: t.TrafficLimitBytes, Profile: wire.AccessDesiredProfile(t.Profile), PlanId: planID, ResetTraffic: t.Reset, VpnBanned: t.Banned}
	if planRev.Valid {
		desired.Revision = &planRev.Int64
	}
	if period.Valid {
		desired.PeriodDays = &period.Int64
	}
	desiredRaw, err := json.Marshal(desired)
	if err != nil {
		return out, unavailable()
	}
	targetRaw, err := json.Marshal(t)
	if err != nil {
		return out, unavailable()
	}
	immediate := t.NoClientIntent
	step := "intent_saved"
	sameNativeTarget := true
	if v != nil && in.Kind == "set_profile" && t.Profile == "unlimited" {
		limit := t.DeviceCount
		if limit > 0 {
			limit++
		}
		attach, detach, e := panel.MembershipDiff(ctx, v.InboundIDs, t.InboundIDs)
		if e != nil {
			return out, failure(409, "ACCESS_NOT_ELIGIBLE")
		}
		sameNativeTarget = v.ExpiryTimeMS == t.ExpiryTimeMS && v.LimitIP == limit && v.TrafficLimitBytes == t.TrafficLimitBytes && len(attach) == 0 && len(detach) == 0
	}
	if t.NoClientIntent {
		if t.Profile == "" {
			return out, failure(409, "ACCESS_NOT_ELIGIBLE")
		}
		if in.Kind == "set_profile" && a.AccessProfile.Valid && a.AccessProfile.String == t.Profile || in.Kind == "set_vpn_ban" && a.VpnBanned == t.Banned {
			step = "state_unchanged"
		}
	} else if v != nil && sameNativeTarget && (in.Kind == "set_profile" && profile == t.Profile || in.Kind == "set_vpn_ban" && a.VpnBanned == t.Banned) && !t.Reset && !t.Enable && (t.Banned && !v.Enabled || !t.Banned && (v.Enabled || in.Kind == "set_profile" && (v.ExpiryTimeMS > 0 && v.ExpiryTimeMS <= now.UnixMilli() || v.TrafficLimitBytes > 0 && v.UsedTraffic != nil && *v.UsedTraffic >= v.TrafficLimitBytes))) {
		immediate, step = true, "state_unchanged"
	}
	if immediate {
		if t.NoClientIntent && step != "state_unchanged" {
			if err = s.accounts.SetAccessMetadata(ctx, tx, target, t.Profile, t.Banned); err != nil {
				return out, unavailable()
			}
		}
		if _, err = tx.Exec(ctx, "INSERT INTO access_operations(id,account_id,operator_account_id,execution_actor_id,kind,status,reason,plan_id,plan_revision,period_days,desired,target,completed_steps,created_at,updated_at) VALUES($1,$2,$3,$3,$4,'applied',$5,$6,$7,$8,$9,$10,jsonb_build_array($11::text),$12,$12)", id, target, actor, string(in.Kind), strings.TrimSpace(in.Reason), planID, planRev, period, desiredRaw, targetRaw, step, now); err != nil {
			return out, accessConflict(err)
		}
		out = wire.AccessOperation{OperationId: id, AccountId: target, OperatorAccountId: &actor, Kind: wire.AccessOperationKind(in.Kind), Status: "applied", CreatedAt: now, UpdatedAt: now, Reason: strings.TrimSpace(in.Reason), Desired: desired, CompletedSteps: []wire.AccessOperationCompletedSteps{wire.AccessOperationCompletedSteps(step)}}
		if _, err = tx.Exec(ctx, "INSERT INTO audit_events(id,created_at,action,account_id,operator_account_id,reason,access_operation_id) VALUES($1,$2,'access_applied',$3,$4,$5,$6)", uuid.New(), now, target, actor, strings.TrimSpace(in.Reason), id); err != nil {
			return out, unavailable()
		}
		if err = s.saveIdempotency(ctx, q, principal, "createAccessOperation", key, hash, out); err != nil {
			return out, err
		}
		if err = tx.Commit(ctx); err != nil {
			return out, unavailable()
		}
		return out, nil
	}
	if err = q.InsertAccessOperation(ctx, store.InsertAccessOperationParams{ID: id, AccountID: target, OperatorAccountID: &actor, Kind: string(in.Kind), Reason: strings.TrimSpace(in.Reason), PlanID: planID, PlanRevision: planRev, PeriodDays: period, Desired: desiredRaw, Target: targetRaw, CreatedAt: stamp(now)}); err != nil {
		return out, accessConflict(err)
	}
	if _, err = s.queue.InsertTx(ctx, tx, AccessArgs{OperationID: id}, &river.InsertOpts{Queue: "provision", MaxAttempts: 5}); err != nil {
		return out, unavailable()
	}
	if _, err = tx.Exec(ctx, "INSERT INTO audit_events(id,created_at,action,account_id,operator_account_id,reason,access_operation_id) VALUES($1,$2,$3,$4,$5,$6,$7)", uuid.New(), now, "access_requested", target, actor, strings.TrimSpace(in.Reason), id); err != nil {
		return out, unavailable()
	}
	out = wire.AccessOperation{OperationId: id, AccountId: target, OperatorAccountId: &actor, Kind: wire.AccessOperationKind(in.Kind), Status: "pending", CreatedAt: now, UpdatedAt: now, Reason: strings.TrimSpace(in.Reason), Desired: desired, CompletedSteps: []wire.AccessOperationCompletedSteps{"prepared"}}
	if err = s.saveIdempotency(ctx, q, principal, "createAccessOperation", key, hash, out); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, unavailable()
	}
	return out, nil
}

func (s *Service) confirmedAccessProfile(ctx context.Context, q *store.Queries, a store.Account, v *PanelClientView, p *PanelClient, strict bool) (string, error) {
	if last, err := q.LatestAppliedAccess(ctx, a.ID); err == nil {
		var target accessTarget
		if json.Unmarshal(last.Target, &target) != nil || target.PanelKey != a.PanelKey || target.VPNID != a.VpnID || target.SubID != a.SubID {
			return "", errPanelIdentity
		}
		if !target.NoClientIntent {
			limit := target.DeviceCount
			if limit > 0 {
				limit++
			}
			if target.Profile == "" || strict && (v.ExpiryTimeMS != target.ExpiryTimeMS || v.LimitIP != limit || v.TrafficLimitBytes != target.TrafficLimitBytes) {
				return "", errPanelIdentity
			}
			attach, detach, e := p.MembershipDiff(ctx, v.InboundIDs, target.InboundIDs)
			if e != nil || len(attach) > 0 || len(detach) > 0 {
				return "", errPanelMembership
			}
			return target.Profile, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if grant, err := q.AccountOperation(ctx, a.ID); err == nil && grant.Status == "applied" {
		var target ProvisionTarget
		if json.Unmarshal(grant.Target, &target) != nil {
			return "", errPanelIdentity
		}
		limit := target.DeviceCount
		if limit > 0 {
			limit++
		}
		if strict && (v.ExpiryTimeMS != target.ExpiryTimeMS || v.LimitIP != limit || v.TrafficLimitBytes != target.TrafficLimitBytes) {
			return "", errPanelIdentity
		}
		attach, detach, e := p.MembershipDiff(ctx, v.InboundIDs, target.InboundIDs)
		if e != nil || len(attach) > 0 || len(detach) > 0 {
			return "", errPanelMembership
		}
		if target.Profile != "" {
			return target.Profile, nil
		}
		return "regular", nil
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	profile, err := p.AccessProfile(ctx, v.InboundIDs)
	if err != nil {
		return "", err
	}
	return profile, nil
}

func (s *Service) GetAccessOperation(ctx context.Context, actor, target, id uuid.UUID) (wire.AccessOperation, error) {
	var out wire.AccessOperation
	if err := s.RequireSupportOperator(ctx, actor); err != nil {
		return out, err
	}
	r, err := store.New(s.pool).AccessOperationForAccount(ctx, store.AccessOperationForAccountParams{ID: id, AccountID: target})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, unavailable()
	}
	return accessPublic(r)
}

func (s *Service) ReconcileAccessOperation(ctx context.Context, actor, target, id, key uuid.UUID, in wire.AccessReconcileInput) (wire.AccessOperation, error) {
	var out wire.AccessOperation
	if actor == uuid.Nil || target == uuid.Nil || id == uuid.Nil || key == uuid.Nil || !validText(strings.TrimSpace(in.Reason), 1, 1000) {
		return out, failure(400, "INVALID_INPUT")
	}
	principal := "operator-account:" + actor.String()
	hash := bodyHash(struct {
		Target, ID uuid.UUID
		Input      wire.AccessReconcileInput
	}{target, id, in})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.lockOperatorPair(ctx, tx, actor, target); err != nil {
		return out, err
	}
	q := store.New(tx)
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "reconcileAccessOperation", Key: key}); err != nil {
		return out, unavailable()
	}
	if prior, found, e := replay[wire.AccessOperation](ctx, q, principal, "reconcileAccessOperation", key, hash); found || e != nil {
		return prior, e
	}
	var locked bool
	if err = tx.QueryRow(ctx, accessOwnerSQL(), target).Scan(&locked); err != nil {
		return out, unavailable()
	}
	if !locked {
		return out, failure(409, "ACCESS_OPERATION_CONFLICT")
	}
	r, err := q.AccessOperationForAccount(ctx, store.AccessOperationForAccountParams{ID: id, AccountID: target})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, unavailable()
	}
	if r.Status != "needs_review" {
		return out, failure(409, "ACCESS_OPERATION_CONFLICT")
	}
	if !r.ResetStarted && in.AcknowledgeResetCost {
		return out, failure(400, "INVALID_INPUT")
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	n, err := q.RequeueAccess(ctx, store.RequeueAccessParams{ID: id, ResetAcknowledged: in.AcknowledgeResetCost, ExecutionActorID: actor, UpdatedAt: stamp(now)})
	if err != nil || n != 1 {
		return out, unavailable()
	}
	if _, err = s.queue.InsertTx(ctx, tx, AccessArgs{OperationID: id}, &river.InsertOpts{Queue: "provision", MaxAttempts: 5}); err != nil {
		return out, unavailable()
	}
	if _, err = tx.Exec(ctx, "INSERT INTO audit_events(id,created_at,action,account_id,operator_account_id,reason,access_operation_id) VALUES($1,$2,$3,$4,$5,$6,$7)", uuid.New(), now, "access_reconcile_requested", target, actor, strings.TrimSpace(in.Reason), id); err != nil {
		return out, unavailable()
	}
	r.Status = "pending"
	r.UpdatedAt = stamp(now)
	r.ResetAcknowledged = in.AcknowledgeResetCost
	r.ReviewReason = pgtype.Text{}
	out, err = accessPublic(r)
	if err != nil {
		return out, err
	}
	if err = s.saveIdempotency(ctx, q, principal, "reconcileAccessOperation", key, hash, out); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, unavailable()
	}
	return out, nil
}
