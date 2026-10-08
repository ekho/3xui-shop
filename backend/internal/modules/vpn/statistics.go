package vpn

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type statisticsBaseline struct {
	AccessBaseline
	Unresolved bool
}

// StatisticsTx owns both VPN operation facts and the bounded provider read.
// It never calls Subscription(), which persists observed traffic.
func (s *Service) StatisticsTx(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (auditreports.VPNStatistics, error) {
	out := auditreports.VPNStatistics{}
	accounts, err := s.accounts.LookupManyTx(ctx, tx, ids)
	if err != nil {
		return out, unavailable()
	}
	rows, err := tx.Query(ctx, `SELECT selected.account_id,access.id,access.target,trial.id,COALESCE(trial.status,''),trial.target,
 EXISTS(SELECT 1 FROM access_operations WHERE account_id=selected.account_id AND status IN ('pending','provisioning','needs_review'))
 OR EXISTS(SELECT 1 FROM trial_operations WHERE account_id=selected.account_id AND status IN ('pending','provisioning','needs_review'))
 FROM unnest($1::uuid[]) selected(account_id)
 LEFT JOIN LATERAL (SELECT id,target FROM access_operations WHERE account_id=selected.account_id AND status='applied' ORDER BY updated_at DESC,sequence DESC LIMIT 1) access ON true
 LEFT JOIN LATERAL (SELECT id,status,target FROM trial_operations WHERE account_id=selected.account_id ORDER BY created_at DESC,id DESC LIMIT 1) trial ON true`, ids)
	if err != nil {
		return out, unavailable()
	}
	baselines := map[uuid.UUID]statisticsBaseline{}
	for rows.Next() {
		var id uuid.UUID
		var b statisticsBaseline
		if rows.Scan(&id, &b.AccessID, &b.AccessTarget, &b.TrialID, &b.TrialStatus, &b.TrialTarget, &b.Unresolved) != nil {
			rows.Close()
			return out, unavailable()
		}
		baselines[id] = b
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(accounts) != len(ids) {
		return out, unavailable()
	}
	config := s.config()
	p := NewPanelClient(config.Panel)
	defer p.Close()
	snapshot, panelErr := p.statisticsSnapshot(ctx)
	observed := s.now().UTC()
	server := auditreports.StatisticsServer{PanelID: config.PanelID, Availability: "unavailable", ObservedAt: observed}
	if panelErr == nil {
		server.Availability = "available"
		clients, inbounds, enabled := int64(len(snapshot.clients)), int64(len(snapshot.inbounds)), int64(0)
		out.InboundReferences = map[string]auditreports.InboundStatistics{}
		for _, row := range snapshot.inbounds {
			if row.enabled {
				enabled++
			}
			for _, group := range []string{"banned", "regular", "unlimited", "euru"} {
				if !hasSegment(row.tags, group) {
					continue
				}
				count := out.InboundReferences[group]
				count.References++
				if row.enabled {
					count.Enabled++
				}
				out.InboundReferences[group] = count
			}
		}
		server.Clients, server.Inbounds, server.EnabledInbounds = &clients, &inbounds, &enabled
	} else {
		code := "PANEL_UNAVAILABLE"
		server.ErrorCode = &code
		snapshot = panelStatisticsSnapshot{}
	}
	out.Servers = []auditreports.StatisticsServer{server}
	out.Activity.ObservedAt = observed
	for _, a := range accounts {
		active, known := statisticsAccountActivity(a, baselines[a.ID], snapshot, config.PanelID, observed)
		switch {
		case !known:
			out.Activity.UnknownUsers++
		case active:
			out.Activity.KnownActiveUsers++
		default:
			out.Activity.KnownInactiveUsers++
		}
	}
	if out.Activity.UnknownUsers == 0 {
		out.Activity.ActiveUsers = &out.Activity.KnownActiveUsers
	}
	return out, nil
}

func statisticsAccountActivity(a accounts.Snapshot, b statisticsBaseline, snapshot panelStatisticsSnapshot, panelID string, now time.Time) (active, known bool) {
	if b.Unresolved {
		return false, false
	}
	var target ProvisionTarget
	raw, expected := b.TrialTarget, b.TrialID
	accessTarget := false
	if b.AccessID != nil {
		var t AccessTarget
		if json.Unmarshal(b.AccessTarget, &t) != nil || t.OperationID != *b.AccessID {
			return false, false
		}
		if !t.NoClientIntent {
			raw, expected, accessTarget = b.AccessTarget, b.AccessID, true
		}
	}
	if !accessTarget && (b.TrialID == nil || b.TrialStatus != "applied") {
		if a.AssignedPanelID == nil && !a.HadSubscription {
			return false, true
		}
		return false, false
	}
	if expected == nil || json.Unmarshal(raw, &target) != nil || target.OperationID != *expected {
		return false, false
	}
	if !accessTarget && target.Profile == "" {
		target.Profile = "regular"
	}
	if a.AssignedPanelID == nil || *a.AssignedPanelID != panelID || target.PanelID != panelID || target.PanelKey != a.PanelKey || target.VPNID != a.VpnID || target.SubID != a.SubID || target.VPNID == uuid.Nil || target.PanelKey == "" || target.SubID == "" || target.Banned != a.VpnBanned || target.DeviceCount < 0 || target.DeviceCount >= math.MaxInt64 || target.ExpiryTimeMS < 0 || target.TrafficLimitBytes < 0 || len(target.InboundIDs) == 0 {
		return false, false
	}
	if target.Profile != "regular" && target.Profile != "unlimited" && target.Profile != "euru" || a.AccessProfile != nil && *a.AccessProfile != target.Profile {
		return false, false
	}
	v, exists := snapshot.clients[a.PanelKey]
	limit := target.DeviceCount
	if limit > 0 {
		limit++
	}
	if !exists || v.PanelKey != a.PanelKey || v.VPNID != a.VpnID || v.SubID != a.SubID || v.ExpiryTimeMS != target.ExpiryTimeMS || v.LimitIP != limit || v.TrafficLimitBytes != target.TrafficLimitBytes || v.UsedTraffic == nil || *v.UsedTraffic < 0 || target.Banned && v.Enabled {
		return false, false
	}
	need, have := map[int64]bool{}, map[int64]bool{}
	for _, id := range target.InboundIDs {
		need[id] = true
	}
	managed, enabled := false, false
	for _, id := range v.InboundIDs {
		inbound, exists := snapshot.inbounds[id]
		if !exists {
			return false, false
		}
		have[id] = true
		isManaged := hasSegment(inbound.tags, "regular") || hasSegment(inbound.tags, "euru") || hasSegment(inbound.tags, "unlimited")
		if !need[id] && isManaged {
			return false, false
		}
		if !need[id] || !isManaged {
			continue
		}
		if !hasSegment(inbound.tags, target.Profile) && !(target.Profile == "unlimited" && hasSegment(inbound.tags, "regular")) {
			return false, false
		}
		managed = true
		enabled = enabled || inbound.enabled
	}
	for id := range need {
		if !have[id] {
			return false, false
		}
	}
	if !managed {
		return false, false
	}
	return enabled && v.Enabled && !a.VpnBanned && (v.ExpiryTimeMS == 0 || v.ExpiryTimeMS > now.UnixMilli()) && (v.TrafficLimitBytes == 0 || *v.UsedTraffic < v.TrafficLimitBytes), true
}
