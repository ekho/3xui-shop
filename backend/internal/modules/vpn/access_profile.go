package vpn

import (
	"context"
	"encoding/json"
	"errors"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/vpn/internal/store"

	"github.com/jackc/pgx/v5"
)

func (s *Service) ConfirmedAccessProfileTx(ctx context.Context, tx pgx.Tx, a accounts.Snapshot, v *PanelClientView, p *PanelClient, strict bool) (string, error) {
	q := store.New(tx)
	if last, err := q.LatestAppliedAccess(ctx, a.ID); err == nil {
		var target AccessTarget
		if json.Unmarshal(last.Target, &target) != nil || target.PanelKey != a.PanelKey || target.VPNID != a.VpnID || target.SubID != a.SubID {
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
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if grant, err := q.AccountOperation(ctx, a.ID); err == nil && grant.Status == "applied" {
		var target ProvisionTarget
		if json.Unmarshal(grant.Target, &target) != nil {
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
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	profile, err := p.AccessProfile(ctx, v.InboundIDs)
	if err != nil {
		return "", err
	}
	return profile, nil
}
