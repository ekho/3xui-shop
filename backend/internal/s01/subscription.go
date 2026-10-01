package s01

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
func (s *Service) confirmedView(ctx context.Context, op store.TrialOperation) (ProvisionTarget, *PanelClientView, error) {
	var target ProvisionTarget
	if json.Unmarshal(op.Target, &target) != nil || target.OperationID != op.ID || target.PanelID != op.PanelID || op.PanelID != s.cfg.PanelID {
		return target, nil, errPanel
	}
	p := NewPanelClient(s.cfg)
	defer p.Close()
	v, e := p.GetClient(ctx, target.PanelKey)
	if e != nil {
		return target, nil, e
	}
	if !panelMatches(v, target, s.now()) || len(missingInbounds(v, target)) > 0 {
		if s.reviewOperation(ctx, op, nil, true) != nil {
			return target, nil, unavailable()
		}
		return target, nil, failure(409, "OPERATION_NOT_READY")
	}
	return target, v, nil
}
func (s *Service) Subscription(ctx context.Context, account uuid.UUID) (wire.Subscription, error) {
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
		expires := op.FirstStartedAt.Time.Add(time.Duration(op.PeriodDays) * 24 * time.Hour)
		out.ExpiresAt = &expires
	}
	if op.ObservedAt.Valid {
		out.ObservedAt = &op.ObservedAt.Time
	}
	if op.TrafficUsedBytes.Valid {
		out.TrafficUsedBytes = &op.TrafficUsedBytes.Int64
	}
	if op.Status == "needs_review" {
		out.Status = "needs_review"
		return out, nil
	}
	if op.Status != "applied" {
		out.Status = "provisioning"
		return out, nil
	}
	out.Status = "active"
	if out.ExpiresAt != nil && !s.now().Before(*out.ExpiresAt) {
		out.Status = "expired"
	}
	_, v, e := s.confirmedView(ctx, op)
	if e != nil {
		var domain *Error
		if errors.As(e, &domain) && domain.Status == 409 {
			out.Status = "needs_review"
		}
		return out, nil
	}
	if v.UsedTraffic != nil {
		now := s.now()
		if store.New(s.pool).ObserveTraffic(ctx, store.ObserveTrafficParams{ID: op.ID, TrafficUsedBytes: pgtype.Int8{Int64: *v.UsedTraffic, Valid: true}, ObservedAt: stamp(now)}) != nil {
			return out, unavailable()
		}
		out.TrafficUsedBytes = v.UsedTraffic
		out.ObservedAt = &now
		out.DataStale = false
	}
	return out, nil
}
func (s *Service) SubscriptionKey(ctx context.Context, account uuid.UUID) (wire.SubscriptionKey, error) {
	out := wire.SubscriptionKey{}
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
	target, _, e := s.confirmedView(ctx, op)
	if e != nil {
		return out, failure(409, "OPERATION_NOT_READY")
	}
	base, e := url.Parse(s.cfg.SubscriptionBaseURL)
	if e != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return out, unavailable()
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + target.SubID
	base.RawPath = ""
	out.SubscriptionUrl = base.String()
	return out, nil
}
