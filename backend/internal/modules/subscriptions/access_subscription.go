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

func (s *Service) latestAccess(ctx context.Context, account uuid.UUID) (vpn.AccessState, bool, error) {
	op, err := s.vpn.LatestAccessState(ctx, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return op, false, nil
	}
	if err != nil {
		return op, false, unavailable()
	}
	return op, true, nil
}

func accessSubscriptionBase(op vpn.AccessState, banned bool, now time.Time) (Subscription, error) {
	out := Subscription{Status: "provisioning", DataStale: true, AccessOperationId: &op.ID}
	state := SubscriptionAccessOperationStatus(op.Status)
	out.AccessOperationStatus = &state
	var desired AccessDesired
	if json.Unmarshal(op.Desired, &desired) != nil {
		return out, unavailable()
	}
	out.Devices = desired.Devices
	out.TrafficLimitBytes = desired.TrafficLimitBytes
	out.ExpiresAt = desired.ExpiresAt
	profile := SubscriptionAccessProfile(desired.Profile)
	out.AccessProfile = profile
	unlimitedTraffic := desired.TrafficLimitBytes == 0
	unlimitedDevices := desired.Devices == 0
	out.UnlimitedTraffic = &unlimitedTraffic
	out.UnlimitedDevices = &unlimitedDevices
	available := false
	out.ConnectionAvailable = &available
	if op.Status == "needs_review" {
		out.Status = "needs_review"
	} else if op.Status == "applied" {
		out.Status = "active"
		if banned {
			out.Status = "banned"
		} else if desired.ExpiresAt != nil && !now.Before(*desired.ExpiresAt) {
			out.Status = "expired"
		}
	}
	return out, nil
}

func (s *Service) accessSubscription(ctx context.Context, op vpn.AccessState, banned bool) (Subscription, error) {
	out, err := accessSubscriptionBase(op, banned, s.now())
	if err != nil {
		return out, err
	}
	_, view, up, down, err := s.vpn.ReadAccess(ctx, op)
	if err != nil {
		out.PanelError = panelError(err)
		if errors.Is(err, vpn.ErrIdentity) || errors.Is(err, vpn.ErrMembership) {
			out.Status = "needs_review"
		}
		return out, nil
	}
	used := up + down
	out.TrafficUploadBytes = &up
	out.TrafficDownloadBytes = &down
	out.TrafficUsedBytes = &used
	now := s.now()
	out.ObservedAt = &now
	profileLimits(&out, view.ExpiryTimeMS, view.LimitIP, view.TrafficLimitBytes)
	profileStatus(&out, banned, view.Enabled, now)
	out.DataStale = false
	return out, nil
}

func (s *Service) accessKey(ctx context.Context, op vpn.AccessState) (SubscriptionKey, error) {
	var out SubscriptionKey
	a, err := s.accountByID(ctx, op.AccountID)
	if err != nil || a.Restricted || a.VpnBanned {
		return out, failure(403, "OPERATION_NOT_READY")
	}
	t, v, up, down, err := s.vpn.ReadAccess(ctx, op)
	if err != nil {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	used := up + down
	view := Subscription{TrafficLimitBytes: v.TrafficLimitBytes, TrafficUsedBytes: &used}
	profileLimits(&view, v.ExpiryTimeMS, v.LimitIP, v.TrafficLimitBytes)
	profileStatus(&view, false, v.Enabled, s.now())
	if view.Status != "active" && view.Status != "expired" {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	baseURL, err := s.vpn.SubscriptionBase(ctx, t.PanelID)
	if err != nil {
		return out, unavailable()
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return out, unavailable()
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + t.SubID
	base.RawPath = ""
	out.SubscriptionUrl = base.String()
	return out, nil
}
