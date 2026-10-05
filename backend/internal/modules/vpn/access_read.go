package vpn

import (
	"context"
	"encoding/json"
)

func (s *Service) ReadAccess(ctx context.Context, op AccessState) (AccessTarget, *PanelClientView, int64, int64, error) {
	var target AccessTarget
	if json.Unmarshal(op.Target, &target) != nil || target.OperationID != op.ID || target.PanelID != s.config().PanelID {
		return target, nil, 0, 0, ErrIdentity
	}
	a, err := s.accountByID(ctx, op.AccountID)
	if err != nil || !(a.AssignedPanelID != nil) || stringValue(a.AssignedPanelID) != target.PanelID || a.PanelKey != target.PanelKey || a.VpnID != target.VPNID || a.SubID != target.SubID {
		return target, nil, 0, 0, ErrIdentity
	}
	p := s.PanelClient()
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
		return target, nil, 0, 0, ErrIdentity
	}
	attach, detach, err := p.MembershipDiff(ctx, v.InboundIDs, target.InboundIDs)
	if err != nil || len(attach) > 0 || len(detach) > 0 {
		return target, nil, 0, 0, ErrMembership
	}
	up, down, err := p.Traffic(ctx, target.PanelKey, target.VPNID, target.SubID)
	if err != nil {
		return target, nil, 0, 0, err
	}
	if target.RestoreEnabled && !v.Enabled && s.now().UnixMilli() < target.ExpiryTimeMS && (target.TrafficLimitBytes == 0 || up+down < target.TrafficLimitBytes) {
		return target, nil, 0, 0, ErrIdentity
	}
	return target, v, up, down, nil
}
