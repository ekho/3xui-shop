package subscriptions

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"reflect"

	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/subscriptions/internal/store"

	"math"
	"strings"
	"time"

	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

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

func accessPublic(r vpn.AccessState) (AccessOperation, error) {
	var desired AccessDesired
	var steps []AccessOperationCompletedSteps
	if json.Unmarshal(r.Desired, &desired) != nil || json.Unmarshal(r.CompletedSteps, &steps) != nil || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		return AccessOperation{}, unavailable()
	}
	if steps == nil {
		steps = []AccessOperationCompletedSteps{}
	}
	out := AccessOperation{OperationId: r.ID, AccountId: r.AccountID, OperatorAccountId: r.OperatorAccountID, Kind: AccessOperationKind(r.Kind), Status: AccessOperationStatus(r.Status), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Reason: r.Reason, Desired: desired, CompletedSteps: steps}
	if r.ReviewReason != nil {
		out.ReviewReason = r.ReviewReason
	}
	return out, nil
}

func accessInput(in AccessOperationInput) error {
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
func (s *Service) CreateAccessOperation(ctx context.Context, actor, target, key uuid.UUID, in AccessOperationInput) (AccessOperation, error) {
	return s.createAccessOperation(ctx, actor, target, key, in, nil)
}

// GrantBonusDays commits the caller's retained bonus fact with the same compensation intent.
func (s *Service) GrantBonusDays(ctx context.Context, account, key uuid.UUID, days int, reason string, persist func(context.Context, pgx.Tx, AccessOperation) error) (AccessOperation, error) {
	if persist == nil {
		return AccessOperation{}, failure(400, "INVALID_INPUT")
	}
	return s.createAccessOperation(ctx, account, account, key, AccessOperationInput{Kind: "compensate", Days: &days, Reason: reason}, persist)
}

func (s *Service) createAccessOperation(ctx context.Context, actor, target, key uuid.UUID, in AccessOperationInput, persist func(context.Context, pgx.Tx, AccessOperation) error) (AccessOperation, error) {
	var out AccessOperation
	if actor == uuid.Nil || target == uuid.Nil || key == uuid.Nil {
		return out, failure(400, "INVALID_INPUT")
	}
	if err := accessInput(in); err != nil {
		return out, err
	}
	principal := "operator-account:" + actor.String()
	operation := "createAccessOperation"
	operatorID := &actor
	lock := s.lockOperatorPair
	if persist != nil {
		principal, operation, operatorID = "bonus-account:"+actor.String(), "grantBonusDays", nil
		lock = func(ctx context.Context, tx pgx.Tx, actor, target uuid.UUID) (accounts.Snapshot, error) {
			return s.lockBonusAccount(ctx, tx, target)
		}
	}
	hash := bodyHash(struct {
		Target uuid.UUID
		Input  AccessOperationInput
	}{target, in})
	owner, err := s.vpn.OpenAccessOwner(ctx, target)
	if err != nil {
		return out, unavailable()
	}
	defer owner.Release()
	tx, err := owner.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: operation, Key: key}); err != nil {
		return out, unavailable()
	}
	a, err := lock(ctx, tx, actor, target)
	if err != nil {
		return out, err
	}
	if prior, found, e := replay[AccessOperation](ctx, q, principal, operation, key, hash); found || e != nil {
		return prior, e
	}
	if persist != nil {
		if err = s.allowNew(ctx, tx); err != nil {
			return out, err
		}
	}
	if q.LockIdempotencySession(ctx, store.LockIdempotencySessionParams{Principal: principal, Operation: operation, Key: key}) != nil {
		return out, unavailable()
	}
	if err = owner.TryLock(ctx); errors.Is(err, vpn.ErrBusy) {
		return out, failure(409, "ACCESS_OPERATION_CONFLICT")
	} else if err != nil {
		return out, unavailable()
	}
	active, err := s.vpn.UnresolvedAccessTx(ctx, tx, target)
	if err != nil {
		return out, unavailable()
	}
	trial, err := s.vpn.UnresolvedTrialTx(ctx, tx, target)
	if err != nil {
		return out, unavailable()
	}
	if active || trial {
		return out, failure(409, "ACCESS_OPERATION_CONFLICT")
	}
	if a.AssignedPanelID != nil {
		if _, err := s.vpn.ServerTx(ctx, tx, *a.AssignedPanelID); err != nil {
			return out, failure(409, "ACCESS_NOT_ELIGIBLE")
		}
	}
	if a.AssignedPanelID == nil && s.config().PanelID == "" {
		return out, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	baseline, err := s.vpn.AccessBaselineTx(ctx, tx, target)
	if err != nil {
		return out, unavailable()
	}
	var selected catalogue.CurrentPlan
	var selectedErr error
	if in.Kind == "assign_plan" {
		selected, selectedErr = s.catalogue.CurrentPlanTx(ctx, tx, *in.PlanId)
	}
	var unlimited unlimitedPlan
	var unlimitedErr, monthlyErr error
	if in.Kind == "set_profile" && *in.Profile == "unlimited" {
		monthlyErr = s.vpn.CheckMonthlyResetTx(ctx, tx)
		if monthlyErr == nil {
			unlimited, unlimitedErr = s.unlimitedAccessPlan(ctx, tx)
		}
	}
	if tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	panelID := stringValue(a.AssignedPanelID)
	if panelID == "" {
		server, err := owner.AvailableServer(ctx)
		if err != nil {
			return out, failure(409, "ACCESS_NOT_ELIGIBLE")
		}
		panelID = server.ID
	}
	panel, err := owner.PanelFor(ctx, panelID)
	if err != nil {
		return out, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	defer panel.Close()
	v, err := panel.GetClient(ctx, a.PanelKey)
	if err != nil {
		return out, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	if v != nil && (v.VPNID != a.VpnID || v.SubID != a.SubID || !(a.AssignedPanelID != nil)) {
		return out, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	if v == nil && in.Kind != "compensate" && in.Kind != "assign_plan" && in.Kind != "set_profile" && in.Kind != "set_vpn_ban" {
		return out, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	id := uuid.New()
	t := vpn.AccessTarget{OperationID: id, PanelID: panelID, PanelKey: a.PanelKey, VPNID: a.VpnID, SubID: a.SubID, Banned: a.VpnBanned, PreviousBanned: a.VpnBanned}
	var planID *uuid.UUID
	var planTerms *catalogue.Terms
	var planRev, period pgtype.Int8
	var profile string
	if v != nil {
		profile, err = s.vpn.ConfirmedAccessProfile(ctx, baseline, a, v, panel, in.Kind == "set_profile" || in.Kind == "set_vpn_ban")
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
		if a.HadSubscription || (a.AssignedPanelID != nil) || a.VpnBanned && in.Kind != "set_profile" && in.Kind != "set_vpn_ban" {
			return out, failure(409, "ACCESS_NOT_ELIGIBLE")
		}
		t.Missing = in.Kind != "set_profile" && in.Kind != "set_vpn_ban"
		t.NoClientIntent = !t.Missing
		t.Profile = stringValue(a.AccessProfile)
		if t.Profile == "" && a.Kind == "web" {
			t.Profile = "regular"
		}
		t.DeviceCount = s.config().TrialDevices
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
		plan, err := selected, selectedErr
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
		planTerms = &plan.Terms
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
		if s.config().TrialPeriodDays < 1 || s.config().TrialPeriodDays > 106751 || s.config().TrialDevices < 1 || s.config().TrialTrafficGB < 0 || s.config().TrialTrafficGB > math.MaxInt64/(1024*1024*1024) {
			return out, unavailable()
		}
		t.Profile = "regular"
		t.DeviceCount = s.config().TrialDevices
		t.TrafficLimitBytes = s.config().TrialTrafficGB * 1024 * 1024 * 1024
		t.Reset = true
		t.ExpiryTimeMS = now.Add(time.Duration(s.config().TrialPeriodDays) * 24 * time.Hour).UnixMilli()
		t.InboundIDs, err = panel.ProfileInboundIDs(ctx, "regular")
		if err != nil {
			return out, failure(409, "ACCESS_NOT_ELIGIBLE")
		}
	case "reset_traffic":
		t.Reset = true
	case "set_profile":
		requested := string(*in.Profile)
		if requested == "unlimited" {
			if monthlyErr != nil {
				return out, unavailable()
			}
			plan, e := unlimited, unlimitedErr
			if e != nil {
				return out, e
			}
			planTerms = &plan.Terms
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
			if s.config().TrialPeriodDays < 1 || s.config().TrialDevices < 1 || s.config().TrialTrafficGB < 0 || s.config().TrialTrafficGB > math.MaxInt64/(1024*1024*1024) {
				return out, unavailable()
			}
			t.DeviceCount, t.TrafficLimitBytes = s.config().TrialDevices, s.config().TrialTrafficGB*1024*1024*1024
			t.ExpiryTimeMS = now.Add(time.Duration(s.config().TrialPeriodDays) * 24 * time.Hour).UnixMilli()
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
	desired := AccessDesired{ExpiresAt: expires, Devices: t.DeviceCount, TrafficLimitBytes: t.TrafficLimitBytes, Profile: AccessDesiredProfile(t.Profile), PlanId: planID, ResetTraffic: t.Reset, VpnBanned: t.Banned}
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
		if in.Kind == "set_profile" && (a.AccessProfile != nil) && stringValue(a.AccessProfile) == t.Profile || in.Kind == "set_vpn_ban" && a.VpnBanned == t.Banned {
			step = "state_unchanged"
		}
	} else if v != nil && sameNativeTarget && (in.Kind == "set_profile" && profile == t.Profile || in.Kind == "set_vpn_ban" && a.VpnBanned == t.Banned) && !t.Reset && !t.Enable && (t.Banned && !v.Enabled || !t.Banned && (v.Enabled || in.Kind == "set_profile" && (v.ExpiryTimeMS > 0 && v.ExpiryTimeMS <= now.UnixMilli() || v.TrafficLimitBytes > 0 && v.UsedTraffic != nil && *v.UsedTraffic >= v.TrafficLimitBytes))) {
		immediate, step = true, "state_unchanged"
	}
	tx, err = owner.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	current, err := lock(ctx, tx, actor, target)
	if err != nil {
		return out, err
	}
	q = store.New(tx)
	if q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: operation, Key: key}) != nil {
		return out, unavailable()
	}
	if prior, found, e := replay[AccessOperation](ctx, q, principal, operation, key, hash); found || e != nil {
		return prior, e
	}
	if !reflect.DeepEqual(a, current) {
		return out, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	unresolved, err := s.vpn.UnresolvedTx(ctx, tx, target)
	if err != nil {
		return out, unavailable()
	}
	if unresolved {
		return out, failure(409, "ACCESS_OPERATION_CONFLICT")
	}
	latest, err := s.vpn.AccessBaselineTx(ctx, tx, target)
	if err != nil {
		return out, unavailable()
	}
	if !reflect.DeepEqual(baseline, latest) {
		return out, failure(409, "ACCESS_NOT_ELIGIBLE")
	}
	if planID != nil {
		if in.Kind == "set_profile" && t.Profile == "unlimited" {
			current, e := s.unlimitedAccessPlan(ctx, tx)
			if e != nil {
				return out, e
			}
			if current.ID != *planID || current.Revision != planRev.Int64 {
				return out, failure(409, "ACCESS_PLAN_CONFLICT")
			}
		}
		current, e := s.catalogue.LockCurrentPlan(ctx, tx, *planID)
		if errors.Is(e, catalogue.ErrNotFound) {
			return out, failure(409, "ACCESS_PLAN_CONFLICT")
		}
		if e != nil && !errors.Is(e, catalogue.ErrInvalidTerms) {
			return out, unavailable()
		}
		if current.Archived || current.Revision != planRev.Int64 || current.Profile != t.Profile {
			return out, failure(409, "ACCESS_PLAN_CONFLICT")
		}
		if e != nil {
			return out, unavailable()
		}
		if planTerms == nil || !reflect.DeepEqual(current.Terms, *planTerms) {
			return out, failure(409, "ACCESS_PLAN_CONFLICT")
		}
	}
	if (in.Kind == "assign_plan" || in.Kind == "starter_trial" || (in.Kind == "set_profile" || in.Kind == "set_vpn_ban" && t.Banned) && step != "state_unchanged") && s.config().RequireStarsCancellation != nil {
		if err = s.config().RequireStarsCancellation(ctx, tx, target, "Operator access intent changed"); err != nil {
			return out, unavailable()
		}
	}
	auditReason := strings.TrimSpace(in.Reason)
	if immediate {
		if t.NoClientIntent && step != "state_unchanged" {
			if err = s.accounts.SetAccessMetadata(ctx, tx, target, t.Profile, t.Banned); err != nil {
				return out, unavailable()
			}
		}
		if _, err = s.vpn.QueueAccessTx(ctx, tx, vpn.AccessWrite{ID: id, AccountID: target, OperatorAccountID: operatorID, Kind: string(in.Kind), Reason: strings.TrimSpace(in.Reason), PlanID: planID, Revision: desired.Revision, PeriodDays: desired.PeriodDays, Desired: desiredRaw, Target: targetRaw, Immediate: true, Step: step, CreatedAt: now}); err != nil {
			return out, vpnError(err)
		}
		out = AccessOperation{OperationId: id, AccountId: target, OperatorAccountId: operatorID, Kind: AccessOperationKind(in.Kind), Status: "applied", CreatedAt: now, UpdatedAt: now, Reason: strings.TrimSpace(in.Reason), Desired: desired, CompletedSteps: []AccessOperationCompletedSteps{AccessOperationCompletedSteps(step)}}
		if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: now, Action: "access_applied", AccountID: target, OperatorAccountID: operatorID, Reason: &auditReason, AccessOperationID: &id}); err != nil {
			return out, unavailable()
		}
		if err = s.saveIdempotency(ctx, q, principal, operation, key, hash, out); err != nil {
			return out, err
		}
		if err = tx.Commit(ctx); err != nil {
			return out, unavailable()
		}
		return out, nil
	}
	if _, err = s.vpn.QueueAccessTx(ctx, tx, vpn.AccessWrite{ID: id, AccountID: target, OperatorAccountID: operatorID, Kind: string(in.Kind), Reason: strings.TrimSpace(in.Reason), PlanID: planID, Revision: desired.Revision, PeriodDays: desired.PeriodDays, Desired: desiredRaw, Target: targetRaw, CreatedAt: now}); err != nil {
		if errors.Is(err, vpn.ErrBusy) || errors.Is(err, vpn.ErrPanel) {
			return out, failure(409, "ACCESS_OPERATION_CONFLICT")
		}
		return out, vpnError(err)
	}
	if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: now, Action: "access_requested", AccountID: target, OperatorAccountID: operatorID, Reason: &auditReason, AccessOperationID: &id}); err != nil {
		return out, unavailable()
	}
	out = AccessOperation{OperationId: id, AccountId: target, OperatorAccountId: operatorID, Kind: AccessOperationKind(in.Kind), Status: "pending", CreatedAt: now, UpdatedAt: now, Reason: strings.TrimSpace(in.Reason), Desired: desired, CompletedSteps: []AccessOperationCompletedSteps{"prepared"}}
	if persist != nil {
		if err = s.allowNew(ctx, tx); err != nil {
			return out, err
		}
		if err = persist(ctx, tx, out); err != nil {
			return out, err
		}
	}
	if err = s.saveIdempotency(ctx, q, principal, operation, key, hash, out); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, unavailable()
	}
	return out, nil
}

func (s *Service) GetAccessOperation(ctx context.Context, actor, target, id uuid.UUID) (AccessOperation, error) {
	var out AccessOperation
	if err := s.requireOperator(ctx, actor); err != nil {
		return out, err
	}
	r, err := s.vpn.AccessStateForAccountTx(ctx, nil, id, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, unavailable()
	}
	return accessPublic(r)
}

func (s *Service) ReconcileAccessOperation(ctx context.Context, actor, target, id, key uuid.UUID, in AccessReconcileInput) (AccessOperation, error) {
	var out AccessOperation
	if actor == uuid.Nil || target == uuid.Nil || id == uuid.Nil || key == uuid.Nil || !validText(strings.TrimSpace(in.Reason), 1, 1000) {
		return out, failure(400, "INVALID_INPUT")
	}
	principal := "operator-account:" + actor.String()
	hash := bodyHash(struct {
		Target, ID uuid.UUID
		Input      AccessReconcileInput
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
	if prior, found, e := replay[AccessOperation](ctx, q, principal, "reconcileAccessOperation", key, hash); found || e != nil {
		return prior, e
	}
	var locked bool
	if err = tx.QueryRow(ctx, accessOwnerSQL(), target).Scan(&locked); err != nil {
		return out, unavailable()
	}
	if !locked {
		return out, failure(409, "ACCESS_OPERATION_CONFLICT")
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	r, err := s.vpn.RequeueAccessTx(ctx, tx, id, target, actor, in.AcknowledgeResetCost, now)
	if err != nil {
		return out, vpnError(err)
	}

	auditReason := strings.TrimSpace(in.Reason)
	if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: now, Action: "access_reconcile_requested", AccountID: target, OperatorAccountID: &actor, Reason: &auditReason, AccessOperationID: &id}); err != nil {
		return out, unavailable()
	}
	r.Status = "pending"
	r.UpdatedAt = now
	r.ReviewReason = nil
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
