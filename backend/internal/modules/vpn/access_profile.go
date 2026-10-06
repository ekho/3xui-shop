package vpn

import (
	"context"
	"encoding/json"
	"errors"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/vpn/internal/store"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) ConfirmedAccessProfile(ctx context.Context, baseline AccessBaseline, a accounts.Snapshot, v *PanelClientView, p *PanelClient, strict bool) (string, error) {
	if baseline.AccessID != nil {
		var target AccessTarget
		if json.Unmarshal(baseline.AccessTarget, &target) != nil || target.PanelKey != a.PanelKey || target.VPNID != a.VpnID || target.SubID != a.SubID {
			return "", ErrIdentity
		}
		if !target.NoClientIntent {
			limit := target.DeviceCount
			if limit > 0 {
				limit++
			}
			if target.Profile == "" || strict && (v.ExpiryTimeMS != target.ExpiryTimeMS || v.LimitIP != limit || v.TrafficLimitBytes != target.TrafficLimitBytes) {
				return "", ErrIdentity
			}
			attach, detach, e := p.MembershipDiff(ctx, v.InboundIDs, target.InboundIDs)
			if e != nil || len(attach) > 0 || len(detach) > 0 {
				return "", ErrMembership
			}
			return target.Profile, nil
		}
	}
	if baseline.TrialID != nil && baseline.TrialStatus == "applied" {
		var target ProvisionTarget
		if json.Unmarshal(baseline.TrialTarget, &target) != nil {
			return "", ErrIdentity
		}
		limit := target.DeviceCount
		if limit > 0 {
			limit++
		}
		if strict && (v.ExpiryTimeMS != target.ExpiryTimeMS || v.LimitIP != limit || v.TrafficLimitBytes != target.TrafficLimitBytes) {
			return "", ErrIdentity
		}
		attach, detach, e := p.MembershipDiff(ctx, v.InboundIDs, target.InboundIDs)
		if e != nil || len(attach) > 0 || len(detach) > 0 {
			return "", ErrMembership
		}
		if target.Profile != "" {
			return target.Profile, nil
		}
		return "regular", nil
	}
	profile, err := p.AccessProfile(ctx, v.InboundIDs)
	if err != nil {
		return "", err
	}
	return profile, nil
}

type AccessBaseline struct {
	AccessID, TrialID         *uuid.UUID
	AccessTarget, TrialTarget json.RawMessage
	TrialStatus               string
}

func (s *Service) AccessBaselineTx(ctx context.Context, tx pgx.Tx, account uuid.UUID) (AccessBaseline, error) {
	q := store.New(tx)
	var out AccessBaseline
	access, e := q.LatestAppliedAccess(ctx, account)
	if e == nil {
		out.AccessID, out.AccessTarget = &access.ID, access.Target
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return out, e
	}
	trial, e := q.AccountOperation(ctx, account)
	if e == nil {
		out.TrialID, out.TrialTarget, out.TrialStatus = &trial.ID, trial.Target, trial.Status
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return out, e
	}
	return out, nil
}
