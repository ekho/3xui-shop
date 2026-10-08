package vpn

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type statisticsInbound struct {
	tags    []string
	enabled bool
}
type panelStatisticsSnapshot struct {
	inbounds map[int64]statisticsInbound
	clients  map[string]PanelClientView
}

func (p *PanelClient) statisticsSnapshot(ctx context.Context) (panelStatisticsSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out := panelStatisticsSnapshot{}
	if p.auth(ctx) != nil {
		return out, ErrPanel
	}
	inbounds, err := p.call(ctx, "GET", "panel/api/inbounds/list", nil)
	if err != nil || !*inbounds.Success {
		return out, ErrPanel
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(inbounds.Obj, &rows) != nil || rows == nil {
		return out, ErrPanel
	}
	out.inbounds = map[int64]statisticsInbound{}
	for _, row := range rows {
		var id int64
		var enabled bool
		var tag string
		for name, dest := range map[string]any{"id": &id, "enable": &enabled, "tag": &tag} {
			if len(row[name]) == 0 || string(row[name]) == "null" || json.Unmarshal(row[name], dest) != nil {
				return panelStatisticsSnapshot{}, ErrPanel
			}
		}
		if _, exists := out.inbounds[id]; id <= 0 || exists {
			return panelStatisticsSnapshot{}, ErrPanel
		}
		out.inbounds[id] = statisticsInbound{tags: strings.Split(tag, "-"), enabled: enabled}
	}
	clients, err := p.call(ctx, "GET", "panel/api/clients/list", nil)
	if err != nil || !*clients.Success {
		return panelStatisticsSnapshot{}, ErrPanel
	}
	rows = nil
	if json.Unmarshal(clients.Obj, &rows) != nil || rows == nil {
		return panelStatisticsSnapshot{}, ErrPanel
	}
	out.clients = map[string]PanelClientView{}
	ids := map[int64]bool{}
	for _, row := range rows {
		var id int64
		var v PanelClientView
		for name, dest := range map[string]any{"id": &id, "email": &v.PanelKey, "enable": &v.Enabled, "expiryTime": &v.ExpiryTimeMS, "limitIp": &v.LimitIP, "totalGB": &v.TrafficLimitBytes, "inboundIds": &v.InboundIDs} {
			// 3.7.0 serializes unattached clients' nil membership slice as null.
			if name == "inboundIds" && string(row[name]) == "null" {
				continue
			}
			if len(row[name]) == 0 || string(row[name]) == "null" || json.Unmarshal(row[name], dest) != nil {
				return panelStatisticsSnapshot{}, ErrPanel
			}
		}
		if id <= 0 || ids[id] || v.PanelKey == "" || v.ExpiryTimeMS < 0 || v.LimitIP < 0 || v.TrafficLimitBytes < 0 {
			return panelStatisticsSnapshot{}, ErrPanel
		}
		if _, duplicate := out.clients[v.PanelKey]; duplicate {
			return panelStatisticsSnapshot{}, ErrPanel
		}
		ids[id] = true
		for _, id := range v.InboundIDs {
			if id <= 0 {
				return panelStatisticsSnapshot{}, ErrPanel
			}
		}
		// Foreign protocol rows may lack a VPN UUID/subscription. They still count
		// as provider clients but cannot confirm one of our account identities.
		for name, dest := range map[string]any{"uuid": &v.VPNID, "subId": &v.SubID} {
			if len(row[name]) > 0 && string(row[name]) != "null" && string(row[name]) != `""` && json.Unmarshal(row[name], dest) != nil {
				return panelStatisticsSnapshot{}, ErrPanel
			}
		}
		var traffic map[string]json.RawMessage
		if json.Unmarshal(row["traffic"], &traffic) == nil && traffic != nil {
			// 3.7.0 joins list traffic by email; its nested UUID/subId are blank.
			// Bind only absent/blank fields to this unique enclosing client. A
			// conflicting identity, email or invalid counter still fails strictly.
			for name, identity := range map[string]string{"uuid": v.VPNID.String(), "subId": v.SubID} {
				if len(traffic[name]) == 0 || string(traffic[name]) == `""` {
					traffic[name], _ = json.Marshal(identity)
				}
			}
		}
		raw, _ := json.Marshal(traffic)
		if up, down, err := parsePanelTraffic(raw, v.PanelKey, v.VPNID, v.SubID); err == nil {
			used := up + down
			v.UsedTraffic = &used
		}
		out.clients[v.PanelKey] = v
	}
	return out, nil
}
