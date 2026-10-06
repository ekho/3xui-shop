package subscriptions

import (
	"context"
	"errors"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/jackc/pgx/v5"
)

// Restricted customers cannot call the cabinet Subscription endpoint. The
// operator card still shows the last stored observation, explicitly stale.
func (s *Service) restrictedOperatorSubscription(ctx context.Context, a accounts.Snapshot) (Subscription, error) {
	if latest, found, err := s.latestAccess(ctx, a.ID); err != nil {
		return Subscription{}, err
	} else if found {
		base := latest
		if latest.Status != "applied" {
			if prior, e := s.vpn.LatestAppliedAccessState(ctx, a.ID); e == nil {
				base = prior
			}
		}
		out, e := accessSubscriptionBase(base, a.VpnBanned, s.now())
		if e != nil {
			return out, e
		}
		out.AccessOperationId = &latest.ID
		state := SubscriptionAccessOperationStatus(latest.Status)
		out.AccessOperationStatus = &state
		if latest.Status == "needs_review" {
			out.Status = "needs_review"
		} else if latest.Status != "applied" {
			out.Status = "provisioning"
		}
		return out, nil
	}
	out := Subscription{Status: "none", DataStale: true}
	available := false
	out.ConnectionAvailable = &available
	op, err := s.vpn.AccountTrialState(ctx, a.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, unavailable()
	}
	out.Devices = op.Devices
	out.TrafficLimitBytes = op.TrafficGb * 1024 * 1024 * 1024
	if op.FirstStartedAt != nil {
		expiry := (*op.FirstStartedAt).Add(time.Duration(op.PeriodDays) * 24 * time.Hour)
		out.ExpiresAt = &expiry
	}
	cachedTraffic(&out, op)
	switch op.Status {
	case "needs_review":
		out.Status = "needs_review"
	case "applied":
		if !cachedProfile(&out, op, a.VpnBanned, s.now()) {
			out.Status = "active"
			if a.VpnBanned {
				out.Status = "banned"
			} else if out.ExpiresAt != nil && !s.now().Before(*out.ExpiresAt) {
				out.Status = "expired"
			}
		}
	default:
		out.Status = "provisioning"
	}
	return out, nil
}
