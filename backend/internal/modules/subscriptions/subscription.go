package subscriptions

import (
	"context"
	"encoding/json"
	"errors"

	"net/url"
	"strings"
	"time"

	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) accountOperation(ctx context.Context, account uuid.UUID) (vpn.TrialState, error) {
	a, e := s.accountByID(ctx, account)
	if errors.Is(e, pgx.ErrNoRows) {
		return vpn.TrialState{}, failure(404, "INVALID_INPUT")
	}
	if e != nil {
		return vpn.TrialState{}, unavailable()
	}
	if a.Restricted {
		return vpn.TrialState{}, failure(403, "ACCOUNT_RESTRICTED")
	}
	return s.vpn.AccountTrialState(ctx, account)
}

func savedNoClientIntent(op vpn.AccessState) bool {
	if op.Kind != "set_profile" && op.Kind != "set_vpn_ban" {
		return false
	}
	var target vpn.AccessTarget
	return json.Unmarshal(op.Target, &target) == nil && target.NoClientIntent
}

func cachedTraffic(out *Subscription, op vpn.TrialState) {
	if !(op.TrafficUpBytes != nil) || !(op.TrafficDownBytes != nil) || !(op.TrafficUsedBytes != nil) || !(op.ObservedAt != nil) {
		return
	}
	out.TrafficUploadBytes = op.TrafficUpBytes
	out.TrafficDownloadBytes = op.TrafficDownBytes
	out.TrafficUsedBytes = op.TrafficUsedBytes
	out.ObservedAt = op.ObservedAt
}
func cachedProfile(out *Subscription, op vpn.TrialState, banned bool, now time.Time) bool {
	if out.TrafficUsedBytes == nil || len(op.ProfileSnapshot) == 0 {
		return false
	}
	var snapshot vpn.ProfileSnapshot
	if json.Unmarshal(op.ProfileSnapshot, &snapshot) != nil || snapshot.ExpiryTimeMS == nil || snapshot.LimitIP == nil || snapshot.TrafficLimitBytes == nil || snapshot.Enabled == nil || *snapshot.ExpiryTimeMS < 0 || *snapshot.LimitIP < 0 || *snapshot.TrafficLimitBytes < 0 {
		return false
	}
	switch snapshot.Profile {
	case "regular", "euru", "unlimited":
	default:
		return false
	}
	profileLimits(out, *snapshot.ExpiryTimeMS, *snapshot.LimitIP, *snapshot.TrafficLimitBytes)
	profile := SubscriptionAccessProfile(snapshot.Profile)
	out.AccessProfile = profile
	profileStatus(out, banned, *snapshot.Enabled, now)
	return true
}
func profileLimits(out *Subscription, expiryMS, limitIP, trafficLimit int64) {
	out.Devices = max(0, limitIP-1)
	devicesUnlimited, trafficUnlimited := limitIP == 0, trafficLimit == 0
	out.UnlimitedDevices, out.UnlimitedTraffic = &devicesUnlimited, &trafficUnlimited
	out.TrafficLimitBytes = trafficLimit
	if expiryMS == 0 {
		out.ExpiresAt = nil
	} else {
		expiry := time.UnixMilli(expiryMS)
		out.ExpiresAt = &expiry
	}
}
func profileStatus(out *Subscription, banned, enabled bool, now time.Time) {
	out.TrafficRemainingBytes = nil
	if out.TrafficLimitBytes > 0 && out.TrafficUsedBytes != nil {
		remaining := max(0, out.TrafficLimitBytes-*out.TrafficUsedBytes)
		out.TrafficRemainingBytes = &remaining
	}
	switch {
	case banned:
		out.Status = "banned"
	case out.ExpiresAt != nil && !now.Before(*out.ExpiresAt):
		out.Status = "expired"
	case out.TrafficLimitBytes > 0 && out.TrafficUsedBytes != nil && *out.TrafficUsedBytes >= out.TrafficLimitBytes:
		out.Status = "exhausted"
	case !enabled:
		out.Status = "disabled"
	default:
		out.Status = "active"
	}
	available := out.Status == "active" || out.Status == "expired"
	out.ConnectionAvailable = &available
}
func panelError(err error) *SubscriptionPanelError {
	var value SubscriptionPanelError
	switch {
	case errors.Is(err, vpn.ErrIdentity):
		value = SubscriptionPanelErrorIdentityMismatch
	case errors.Is(err, vpn.ErrMembership):
		value = SubscriptionPanelErrorUnknownMembership
	case errors.Is(err, vpn.ErrTraffic):
		value = SubscriptionPanelErrorInvalidTraffic
	default:
		value = SubscriptionPanelErrorUnavailable
	}
	return &value
}

func (s *Service) Subscription(ctx context.Context, account uuid.UUID) (out Subscription, retErr error) {
	a, err := s.accountByID(ctx, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return Subscription{}, unavailable()
	}
	if a.Restricted {
		return Subscription{}, failure(403, "ACCOUNT_RESTRICTED")
	}
	defer func() {
		if retErr != nil {
			return
		}
		out.VpnBanned = a.VpnBanned
		if out.AccessProfile == "" {
			if a.AccessProfile != nil {
				out.AccessProfile = SubscriptionAccessProfile(stringValue(a.AccessProfile))
			} else {
				out.AccessProfile = "unknown"
			}
		}
	}()
	latest, found, err := s.latestAccess(ctx, account)
	if err != nil {
		return Subscription{}, err
	}
	if found && latest.Status == "applied" {
		if !(a.AssignedPanelID != nil) && (latest.Kind == "set_profile" || latest.Kind == "set_vpn_ban") {
			state := SubscriptionAccessOperationStatus("applied")
			return Subscription{Status: "none", DataStale: true, AccessOperationId: &latest.ID, AccessOperationStatus: &state}, nil
		}
		if (a.AssignedPanelID != nil) && savedNoClientIntent(latest) {
			return s.trialSubscription(ctx, account)
		}
		return s.accessSubscription(ctx, latest, a.VpnBanned)
	}
	if found {
		prior, e := s.vpn.LatestAppliedAccessState(ctx, account)
		if e == nil {
			out, e := s.accessSubscription(ctx, prior, a.VpnBanned)
			if e != nil {
				return out, e
			}
			out.AccessOperationId = &latest.ID
			state := SubscriptionAccessOperationStatus(latest.Status)
			out.AccessOperationStatus = &state
			if latest.Status == "needs_review" {
				out.Status = "needs_review"
			} else {
				out.Status = "provisioning"
			}
			out.DataStale = true
			return out, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return Subscription{}, unavailable()
		}
	}
	out, e := s.trialSubscription(ctx, account)
	if found && e == nil {
		out.AccessOperationId = &latest.ID
		state := SubscriptionAccessOperationStatus(latest.Status)
		out.AccessOperationStatus = &state
		if latest.Status == "needs_review" {
			out.Status = "needs_review"
		} else {
			out.Status = "provisioning"
		}
		out.DataStale = true
	}
	return out, e
}

func (s *Service) trialSubscription(ctx context.Context, account uuid.UUID) (Subscription, error) {
	out := Subscription{Status: "none", DataStale: true}
	op, e := s.accountOperation(ctx, account)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	out.Devices = op.Devices
	out.TrafficLimitBytes = op.TrafficGb * 1024 * 1024 * 1024
	if op.FirstStartedAt != nil {
		expiry := (*op.FirstStartedAt).Add(time.Duration(op.PeriodDays) * 24 * time.Hour)
		out.ExpiresAt = &expiry
	}
	cachedTraffic(&out, op)
	if op.Status == "needs_review" {
		out.Status = "needs_review"
		return out, nil
	}
	if op.Status != "applied" {
		out.Status = "provisioning"
		return out, nil
	}
	banned, e := s.accountVPNBan(ctx, account)
	if e != nil {
		return out, unavailable()
	}
	if !cachedProfile(&out, op, banned, s.now()) {
		out.Status = "active"
		if banned {
			out.Status = "banned"
		} else if out.ExpiresAt != nil && !s.now().Before(*out.ExpiresAt) {
			out.Status = "expired"
		}
	}
	observedAt := s.now()
	read, e := s.readTrialProfile(ctx, op)
	if e != nil {
		out.PanelError = panelError(e)
		if errors.Is(e, vpn.ErrIdentity) || errors.Is(e, vpn.ErrMembership) {
			out.Status = "needs_review"
			if errors.Is(e, vpn.ErrMembership) {
				profile := SubscriptionAccessProfileUnknown
				out.AccessProfile = profile
			}
		}
		available := false
		out.ConnectionAvailable = &available
		return out, nil
	}
	v := read.Client
	if read.TrafficErr != nil {
		out.PanelError = panelError(read.TrafficErr)
		if errors.Is(read.TrafficErr, vpn.ErrIdentity) {
			out.Status = "needs_review"
		}
		available := false
		out.ConnectionAvailable = &available
		return out, nil
	}
	snapshot := vpn.ProfileSnapshot{ExpiryTimeMS: &v.ExpiryTimeMS, LimitIP: &v.LimitIP, TrafficLimitBytes: &v.TrafficLimitBytes, Enabled: &v.Enabled, Profile: read.Profile}
	if vpn.ObserveProfileTraffic(ctx, s.pool, op.ID, read.Up, read.Down, observedAt, snapshot) != nil {
		return out, unavailable()
	}
	current, e := s.vpn.TrialStateTx(ctx, nil, op.ID)
	if e != nil {
		return out, unavailable()
	}
	cachedTraffic(&out, current)
	if !cachedProfile(&out, current, banned, s.now()) {
		return out, unavailable()
	}
	out.DataStale = false
	return out, nil
}

func (s *Service) SubscriptionKey(ctx context.Context, account uuid.UUID) (SubscriptionKey, error) {
	out := SubscriptionKey{}
	a, authErr := s.accountByID(ctx, account)
	if errors.Is(authErr, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if authErr != nil {
		return out, unavailable()
	}
	if a.Restricted {
		return out, failure(403, "ACCOUNT_RESTRICTED")
	}
	latest, found, e := s.latestAccess(ctx, account)
	if e != nil {
		return out, e
	}
	if found {
		if latest.Status != "applied" {
			return out, failure(409, "OPERATION_NOT_READY")
		}
		if !(a.AssignedPanelID != nil) || !savedNoClientIntent(latest) {
			return s.accessKey(ctx, latest)
		}
	}
	op, e := s.accountOperation(ctx, account)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	if e != nil {
		return out, e
	}
	if op.Status != "applied" {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	banned, e := s.accountVPNBan(ctx, account)
	if e != nil {
		return out, unavailable()
	}
	if banned {
		return out, failure(403, "OPERATION_NOT_READY")
	}
	read, e := s.readTrialProfile(ctx, op)
	if e != nil {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	if read.TrafficErr != nil {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	v := read.Client
	used := read.Up + read.Down
	view := Subscription{TrafficLimitBytes: v.TrafficLimitBytes, TrafficUsedBytes: &used}
	profileLimits(&view, v.ExpiryTimeMS, v.LimitIP, v.TrafficLimitBytes)
	profileStatus(&view, false, v.Enabled, s.now())
	if view.Status != "active" && view.Status != "expired" {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	baseURL, e := s.vpn.SubscriptionBase(ctx, op.PanelID)
	if e != nil {
		return out, unavailable()
	}
	base, e := url.Parse(baseURL)
	if e != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return out, unavailable()
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + read.Target.SubID
	base.RawPath = ""
	out.SubscriptionUrl = base.String()
	return out, nil
}
