package platform

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net/url"
	"strings"
	"time"
)

func (s *Service) latestAccess(ctx context.Context, account uuid.UUID) (store.AccessOperation, bool, error) {
	op, err := store.New(s.pool).LatestAccessOperation(ctx, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return op, false, nil
	}
	if err != nil {
		return op, false, unavailable()
	}
	return op, true, nil
}

func accessSubscriptionBase(op store.AccessOperation, banned bool, now time.Time) (wire.Subscription, error) {
	out := wire.Subscription{Status: "provisioning", DataStale: true, AccessOperationId: &op.ID}
	state := wire.SubscriptionAccessOperationStatus(op.Status)
	out.AccessOperationStatus = &state
	var desired wire.AccessDesired
	if json.Unmarshal(op.Desired, &desired) != nil {
		return out, unavailable()
	}
	out.Devices = desired.Devices
	out.TrafficLimitBytes = desired.TrafficLimitBytes
	out.ExpiresAt = desired.ExpiresAt
	profile := wire.SubscriptionAccessProfile(desired.Profile)
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

func (s *Service) readAccess(ctx context.Context, op store.AccessOperation) (accessTarget, *PanelClientView, int64, int64, error) {
	var target accessTarget
	if json.Unmarshal(op.Target, &target) != nil || target.OperationID != op.ID || target.PanelID != s.cfg.PanelID {
		return target, nil, 0, 0, errPanelIdentity
	}
	a, err := store.New(s.pool).AccountByID(ctx, op.AccountID)
	if err != nil || !a.AssignedPanelID.Valid || a.AssignedPanelID.String != target.PanelID || a.PanelKey != target.PanelKey || a.VpnID != target.VPNID || a.SubID != target.SubID {
		return target, nil, 0, 0, errPanelIdentity
	}
	p := NewPanelClient(s.cfg)
	defer p.Close()
	v, err := p.GetClient(ctx, target.PanelKey)
	if err != nil {
		return target, nil, 0, 0, err
	}
	limit := target.DeviceCount
	if limit > 0 {
		limit++
	}
	if v == nil || v.VPNID != target.VPNID || v.SubID != target.SubID || v.ExpiryTimeMS != target.ExpiryTimeMS || v.LimitIP != limit || v.TrafficLimitBytes != target.TrafficLimitBytes || target.Banned && v.Enabled {
		return target, nil, 0, 0, errPanelIdentity
	}
	attach, detach, err := p.MembershipDiff(ctx, v.InboundIDs, target.InboundIDs)
	if err != nil || len(attach) > 0 || len(detach) > 0 {
		return target, nil, 0, 0, errPanelMembership
	}
	up, down, err := p.Traffic(ctx, target.PanelKey, target.VPNID, target.SubID)
	if err != nil {
		return target, nil, 0, 0, err
	}
	if target.RestoreEnabled && !v.Enabled && s.now().UnixMilli() < target.ExpiryTimeMS && (target.TrafficLimitBytes == 0 || up+down < target.TrafficLimitBytes) {
		return target, nil, 0, 0, errPanelIdentity
	}
	return target, v, up, down, nil
}

func (s *Service) accessSubscription(ctx context.Context, op store.AccessOperation, banned bool) (wire.Subscription, error) {
	out, err := accessSubscriptionBase(op, banned, s.now())
	if err != nil {
		return out, err
	}
	_, view, up, down, err := s.readAccess(ctx, op)
	if err != nil {
		out.PanelError = panelError(err)
		if errors.Is(err, errPanelIdentity) || errors.Is(err, errPanelMembership) {
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

func (s *Service) accessKey(ctx context.Context, op store.AccessOperation) (wire.SubscriptionKey, error) {
	var out wire.SubscriptionKey
	a, err := store.New(s.pool).AccountByID(ctx, op.AccountID)
	if err != nil || a.Restricted || a.VpnBanned {
		return out, failure(403, "OPERATION_NOT_READY")
	}
	t, v, up, down, err := s.readAccess(ctx, op)
	if err != nil {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	used := up + down
	view := wire.Subscription{TrafficLimitBytes: v.TrafficLimitBytes, TrafficUsedBytes: &used}
	profileLimits(&view, v.ExpiryTimeMS, v.LimitIP, v.TrafficLimitBytes)
	profileStatus(&view, false, v.Enabled, s.now())
	if view.Status != "active" && view.Status != "expired" {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	base, err := url.Parse(s.cfg.SubscriptionBaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return out, unavailable()
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + t.SubID
	base.RawPath = ""
	out.SubscriptionUrl = base.String()
	return out, nil
}
