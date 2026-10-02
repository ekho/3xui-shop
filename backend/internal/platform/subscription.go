package platform

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"strings"
	"time"
)

func (s *Service) accountOperation(ctx context.Context, account uuid.UUID) (store.TrialOperation, error) {
	q := store.New(s.pool)
	a, e := q.AccountByID(ctx, account)
	if errors.Is(e, pgx.ErrNoRows) {
		return store.TrialOperation{}, failure(404, "INVALID_INPUT")
	}
	if e != nil {
		return store.TrialOperation{}, unavailable()
	}
	if a.Restricted {
		return store.TrialOperation{}, failure(403, "ACCOUNT_RESTRICTED")
	}
	return q.AccountOperation(ctx, account)
}

func savedNoClientIntent(op store.AccessOperation) bool {
	if op.Kind != "set_profile" && op.Kind != "set_vpn_ban" {
		return false
	}
	var target accessTarget
	return json.Unmarshal(op.Target, &target) == nil && target.NoClientIntent
}

type profileRead struct {
	target     ProvisionTarget
	client     *PanelClientView
	profile    string
	up, down   int64
	trafficErr error
}

func (s *Service) readProfile(ctx context.Context, op store.TrialOperation) (profileRead, error) {
	var out profileRead
	if json.Unmarshal(op.Target, &out.target) != nil || out.target.OperationID != op.ID || out.target.PanelID != op.PanelID || op.PanelID != s.cfg.PanelID {
		return out, errPanelIdentity
	}
	q := store.New(s.pool)
	a, e := q.AccountByID(ctx, op.AccountID)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, errPanelIdentity
	}
	if e != nil {
		return out, errPanel
	}
	if a.Restricted || !a.AssignedPanelID.Valid || a.AssignedPanelID.String != op.PanelID || a.PanelKey != out.target.PanelKey || a.VpnID != out.target.VPNID || a.SubID != out.target.SubID {
		return out, errPanelIdentity
	}
	var grant string
	e = s.pool.QueryRow(ctx, `SELECT status FROM trial_grants WHERE operation_id=$1 AND account_id=$2`, op.ID, op.AccountID).Scan(&grant)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return out, errPanel
	}
	if e != nil || grant != "granted" {
		return out, errPanelIdentity
	}
	p := NewPanelClient(s.cfg)
	defer p.Close()
	out.client, e = p.GetClient(ctx, out.target.PanelKey)
	if e != nil {
		return out, e
	}
	if out.client == nil || out.client.PanelKey != out.target.PanelKey || out.client.VPNID != out.target.VPNID || out.client.SubID != out.target.SubID {
		return out, errPanelIdentity
	}
	if out.target.Profile != "" {
		expected, err := p.ProfileInboundIDs(ctx, out.target.Profile)
		if err != nil {
			return out, err
		}
		attach, detach, err := p.MembershipDiff(ctx, out.client.InboundIDs, expected)
		if err != nil {
			return out, err
		}
		if len(attach) == 0 && len(detach) == 0 {
			out.profile = out.target.Profile
		} else {
			out.profile, e = p.AccessProfile(ctx, out.client.InboundIDs)
		}
	} else {
		out.profile, e = p.AccessProfile(ctx, out.client.InboundIDs)
	}
	if e != nil {
		return out, e
	}
	out.up, out.down, out.trafficErr = p.Traffic(ctx, out.target.PanelKey, out.target.VPNID, out.target.SubID)
	return out, nil
}

type profileSnapshot struct {
	ExpiryTimeMS      *int64 `json:"expiry_time_ms"`
	LimitIP           *int64 `json:"limit_ip"`
	TrafficLimitBytes *int64 `json:"traffic_limit_bytes"`
	Enabled           *bool  `json:"enabled"`
	Profile           string `json:"profile"`
}

func observeProfileTraffic(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, up, down int64, at time.Time, snapshot profileSnapshot) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return store.New(pool).ObserveProfileTraffic(ctx, store.ObserveProfileTrafficParams{ID: id, TrafficUpBytes: pgtype.Int8{Int64: up, Valid: true}, TrafficDownBytes: pgtype.Int8{Int64: down, Valid: true}, ObservedAt: stamp(at), Snapshot: raw})
}

func cachedTraffic(out *wire.Subscription, op store.TrialOperation) {
	if !op.TrafficUpBytes.Valid || !op.TrafficDownBytes.Valid || !op.TrafficUsedBytes.Valid || !op.ObservedAt.Valid {
		return
	}
	out.TrafficUploadBytes = &op.TrafficUpBytes.Int64
	out.TrafficDownloadBytes = &op.TrafficDownBytes.Int64
	out.TrafficUsedBytes = &op.TrafficUsedBytes.Int64
	out.ObservedAt = &op.ObservedAt.Time
}
func cachedProfile(out *wire.Subscription, op store.TrialOperation, banned bool, now time.Time) bool {
	if out.TrafficUsedBytes == nil || len(op.ProfileSnapshot) == 0 {
		return false
	}
	var snapshot profileSnapshot
	if json.Unmarshal(op.ProfileSnapshot, &snapshot) != nil || snapshot.ExpiryTimeMS == nil || snapshot.LimitIP == nil || snapshot.TrafficLimitBytes == nil || snapshot.Enabled == nil || *snapshot.ExpiryTimeMS < 0 || *snapshot.LimitIP < 0 || *snapshot.TrafficLimitBytes < 0 {
		return false
	}
	switch snapshot.Profile {
	case "regular", "euru", "unlimited":
	default:
		return false
	}
	profileLimits(out, *snapshot.ExpiryTimeMS, *snapshot.LimitIP, *snapshot.TrafficLimitBytes)
	profile := wire.SubscriptionAccessProfile(snapshot.Profile)
	out.AccessProfile = profile
	profileStatus(out, banned, *snapshot.Enabled, now)
	return true
}
func profileLimits(out *wire.Subscription, expiryMS, limitIP, trafficLimit int64) {
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
func profileStatus(out *wire.Subscription, banned, enabled bool, now time.Time) {
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
func panelError(err error) *wire.SubscriptionPanelError {
	var value wire.SubscriptionPanelError
	switch {
	case errors.Is(err, errPanelIdentity):
		value = wire.SubscriptionPanelErrorIdentityMismatch
	case errors.Is(err, errPanelMembership):
		value = wire.SubscriptionPanelErrorUnknownMembership
	case errors.Is(err, errPanelTraffic):
		value = wire.SubscriptionPanelErrorInvalidTraffic
	default:
		value = wire.SubscriptionPanelErrorUnavailable
	}
	return &value
}

func (s *Service) Subscription(ctx context.Context, account uuid.UUID) (out wire.Subscription, retErr error) {
	a, err := store.New(s.pool).AccountByID(ctx, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return wire.Subscription{}, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return wire.Subscription{}, unavailable()
	}
	if a.Restricted {
		return wire.Subscription{}, failure(403, "ACCOUNT_RESTRICTED")
	}
	defer func() {
		if retErr != nil {
			return
		}
		out.VpnBanned = a.VpnBanned
		if out.AccessProfile == "" {
			if a.AccessProfile.Valid {
				out.AccessProfile = wire.SubscriptionAccessProfile(a.AccessProfile.String)
			} else {
				out.AccessProfile = "unknown"
			}
		}
	}()
	latest, found, err := s.latestAccess(ctx, account)
	if err != nil {
		return wire.Subscription{}, err
	}
	if found && latest.Status == "applied" {
		if !a.AssignedPanelID.Valid && (latest.Kind == "set_profile" || latest.Kind == "set_vpn_ban") {
			state := wire.SubscriptionAccessOperationStatus("applied")
			return wire.Subscription{Status: "none", DataStale: true, AccessOperationId: &latest.ID, AccessOperationStatus: &state}, nil
		}
		if a.AssignedPanelID.Valid && savedNoClientIntent(latest) {
			return s.trialSubscription(ctx, account)
		}
		return s.accessSubscription(ctx, latest, a.VpnBanned)
	}
	if found {
		prior, e := store.New(s.pool).LatestAppliedAccess(ctx, account)
		if e == nil {
			out, e := s.accessSubscription(ctx, prior, a.VpnBanned)
			if e != nil {
				return out, e
			}
			out.AccessOperationId = &latest.ID
			state := wire.SubscriptionAccessOperationStatus(latest.Status)
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
			return wire.Subscription{}, unavailable()
		}
	}
	out, e := s.trialSubscription(ctx, account)
	if found && e == nil {
		out.AccessOperationId = &latest.ID
		state := wire.SubscriptionAccessOperationStatus(latest.Status)
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

func (s *Service) trialSubscription(ctx context.Context, account uuid.UUID) (wire.Subscription, error) {
	out := wire.Subscription{Status: "none", DataStale: true}
	op, e := s.accountOperation(ctx, account)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	out.Devices = op.Devices
	out.TrafficLimitBytes = op.TrafficGb * 1024 * 1024 * 1024
	if op.FirstStartedAt.Valid {
		expiry := op.FirstStartedAt.Time.Add(time.Duration(op.PeriodDays) * 24 * time.Hour)
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
	banned, e := store.New(s.pool).AccountVPNBan(ctx, account)
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
	read, e := s.readProfile(ctx, op)
	if e != nil {
		out.PanelError = panelError(e)
		if errors.Is(e, errPanelIdentity) || errors.Is(e, errPanelMembership) {
			out.Status = "needs_review"
			if errors.Is(e, errPanelMembership) {
				profile := wire.SubscriptionAccessProfileUnknown
				out.AccessProfile = profile
			}
		}
		available := false
		out.ConnectionAvailable = &available
		return out, nil
	}
	v := read.client
	if read.trafficErr != nil {
		out.PanelError = panelError(read.trafficErr)
		if errors.Is(read.trafficErr, errPanelIdentity) {
			out.Status = "needs_review"
		}
		available := false
		out.ConnectionAvailable = &available
		return out, nil
	}
	snapshot := profileSnapshot{ExpiryTimeMS: &v.ExpiryTimeMS, LimitIP: &v.LimitIP, TrafficLimitBytes: &v.TrafficLimitBytes, Enabled: &v.Enabled, Profile: read.profile}
	if observeProfileTraffic(ctx, s.pool, op.ID, read.up, read.down, observedAt, snapshot) != nil {
		return out, unavailable()
	}
	current, e := store.New(s.pool).OperationByID(ctx, op.ID)
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

func (s *Service) SubscriptionKey(ctx context.Context, account uuid.UUID) (wire.SubscriptionKey, error) {
	out := wire.SubscriptionKey{}
	a, authErr := store.New(s.pool).AccountByID(ctx, account)
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
		if !a.AssignedPanelID.Valid || !savedNoClientIntent(latest) {
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
	banned, e := store.New(s.pool).AccountVPNBan(ctx, account)
	if e != nil {
		return out, unavailable()
	}
	if banned {
		return out, failure(403, "OPERATION_NOT_READY")
	}
	read, e := s.readProfile(ctx, op)
	if e != nil {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	if read.trafficErr != nil {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	v := read.client
	used := read.up + read.down
	view := wire.Subscription{TrafficLimitBytes: v.TrafficLimitBytes, TrafficUsedBytes: &used}
	profileLimits(&view, v.ExpiryTimeMS, v.LimitIP, v.TrafficLimitBytes)
	profileStatus(&view, false, v.Enabled, s.now())
	if view.Status != "active" && view.Status != "expired" {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	base, e := url.Parse(s.cfg.SubscriptionBaseURL)
	if e != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return out, unavailable()
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + read.target.SubID
	base.RawPath = ""
	out.SubscriptionUrl = base.String()
	return out, nil
}
