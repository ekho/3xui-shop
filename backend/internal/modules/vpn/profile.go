package vpn

import (
	"context"
	"encoding/json"
	"errors"

	"example.com/cabinet/backend/internal/modules/vpn/internal/store"

	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ProfileRead struct {
	Target     ProvisionTarget
	Client     *PanelClientView
	Profile    string
	Up, Down   int64
	TrafficErr error
}

func (s *Service) ReadTrialProfile(ctx context.Context, op TrialState) (ProfileRead, error) {
	var out ProfileRead
	if json.Unmarshal(op.Target, &out.Target) != nil || out.Target.OperationID != op.ID || out.Target.PanelID != op.PanelID {
		return out, ErrIdentity
	}
	a, e := s.accountByID(ctx, op.AccountID)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, ErrIdentity
	}
	if e != nil {
		return out, ErrPanel
	}
	if a.Restricted || !(a.AssignedPanelID != nil) || stringValue(a.AssignedPanelID) != op.PanelID || a.PanelKey != out.Target.PanelKey || a.VpnID != out.Target.VPNID || a.SubID != out.Target.SubID {
		return out, ErrIdentity
	}
	p, e := s.PanelFor(ctx, op.PanelID)
	if e != nil {
		return out, e
	}
	defer p.Close()
	out.Client, e = p.GetClient(ctx, out.Target.PanelKey)
	if e != nil {
		return out, e
	}
	if out.Client == nil || out.Client.PanelKey != out.Target.PanelKey || out.Client.VPNID != out.Target.VPNID || out.Client.SubID != out.Target.SubID {
		return out, ErrIdentity
	}
	if out.Target.Profile != "" {
		expected, err := p.ProfileInboundIDs(ctx, out.Target.Profile)
		if err != nil {
			return out, err
		}
		attach, detach, err := p.MembershipDiff(ctx, out.Client.InboundIDs, expected)
		if err != nil {
			return out, err
		}
		if len(attach) == 0 && len(detach) == 0 {
			out.Profile = out.Target.Profile
		} else {
			out.Profile, e = p.AccessProfile(ctx, out.Client.InboundIDs)
		}
	} else {
		out.Profile, e = p.AccessProfile(ctx, out.Client.InboundIDs)
	}
	if e != nil {
		return out, e
	}
	out.Up, out.Down, out.TrafficErr = p.Traffic(ctx, out.Target.PanelKey, out.Target.VPNID, out.Target.SubID)
	return out, nil
}

type ProfileSnapshot struct {
	ExpiryTimeMS      *int64 `json:"expiry_time_ms"`
	LimitIP           *int64 `json:"limit_ip"`
	TrafficLimitBytes *int64 `json:"traffic_limit_bytes"`
	Enabled           *bool  `json:"enabled"`
	Profile           string `json:"profile"`
}

func ObserveProfileTraffic(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, up, down int64, at time.Time, snapshot ProfileSnapshot) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return store.New(pool).ObserveProfileTraffic(ctx, store.ObserveProfileTrafficParams{ID: id, TrafficUpBytes: pgtype.Int8{Int64: up, Valid: true}, TrafficDownBytes: pgtype.Int8{Int64: down, Valid: true}, ObservedAt: stamp(at), Snapshot: raw})
}
