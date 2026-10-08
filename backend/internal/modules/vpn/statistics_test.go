package vpn

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/google/uuid"
)

// These are flattened 3.7.0 list records, not the nested get-client response.
func statisticsPanelRecord(id uuid.UUID) map[string]any {
	return map[string]any{"id": 7, "email": "acct_fixture", "uuid": id, "subId": "fixture", "enable": true, "expiryTime": 0, "totalGB": 0, "limitIp": 0, "inboundIds": []int64{1, 1}, "traffic": map[string]any{"email": "acct_fixture", "uuid": id, "subId": "fixture", "up": 17, "down": 25}}
}

func TestPanelStatisticsSnapshot(t *testing.T) {
	id := uuid.New()
	var calls atomic.Int64
	var mutation atomic.Bool
	clients := []map[string]any{statisticsPanelRecord(id)}
	inbounds := []map[string]any{{"id": 1, "enable": true, "tag": "region-regular-tcp"}, {"id": 2, "enable": false, "tag": "regular"}, {"id": 3, "enable": true, "tag": "nonregular"}, {"id": 4, "enable": true, "tag": "euru-unlimited-banned"}}
	mode := ""
	h := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fixture" {
			mutation.Store(true)
		}
		if mode == "deadline" {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
			return
		}
		if mode == "oversize" {
			w.Write([]byte(strings.Repeat(" ", 1024*1024+1)))
			return
		}
		if mode == "malformed" {
			w.Write([]byte("{"))
			return
		}
		if mode == "outage" {
			w.WriteHeader(503)
			return
		}
		var obj any
		switch r.URL.Path {
		case "/fixture/panel/api/inbounds/list":
			obj = inbounds
		case "/fixture/panel/api/clients/list":
			obj = clients
		default:
			mutation.Store(true)
			w.WriteHeader(404)
			return
		}
		if mode == "null" {
			obj = nil
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": obj})
	}))
	defer h.Close()
	roots := x509.NewCertPool()
	roots.AddCert(h.Certificate())
	p := NewPanelClient(Config{PanelURL: h.URL + "/fixture", PanelToken: "fixture", PanelRootCAs: roots})
	defer p.Close()
	snapshot, err := p.statisticsSnapshot(context.Background())
	if err != nil || calls.Load() != 2 || mutation.Load() || len(snapshot.inbounds) != 4 || len(snapshot.clients) != 1 || snapshot.clients["acct_fixture"].UsedTraffic == nil || *snapshot.clients["acct_fixture"].UsedTraffic != 42 {
		t.Fatal("bounded bulk snapshot / flattened identity / exact traffic", err)
	}
	for _, tc := range []struct {
		name           string
		change         func(map[string]any)
		unknownTraffic bool
	}{
		{"negative", func(v map[string]any) { v["traffic"].(map[string]any)["up"] = -1 }, true},
		{"overflow", func(v map[string]any) { v["traffic"].(map[string]any)["up"] = int64(math.MaxInt64) }, true},
		{"missing-traffic", func(v map[string]any) { delete(v, "traffic") }, true},
		{"missing-counter", func(v map[string]any) { delete(v["traffic"].(map[string]any), "up") }, true},
		{"wrong-traffic-identity", func(v map[string]any) { v["traffic"].(map[string]any)["uuid"] = uuid.New() }, true},
		{"negative-quota", func(v map[string]any) { v["totalGB"] = -1 }, false},
		{"missing-control", func(v map[string]any) { delete(v, "enable") }, false},
		{"invalid-membership", func(v map[string]any) { v["inboundIds"] = []int{0} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := statisticsPanelRecord(id)
			tc.change(v)
			clients = []map[string]any{v}
			snap, e := p.statisticsSnapshot(context.Background())
			if tc.unknownTraffic {
				if e != nil || snap.clients["acct_fixture"].UsedTraffic != nil {
					t.Fatal("invalid traffic became known or lost unrelated counts")
				}
			} else if e == nil {
				t.Fatal("malformed control accepted")
			}
		})
	}
	clients = []map[string]any{statisticsPanelRecord(id), statisticsPanelRecord(id)}
	if _, err := p.statisticsSnapshot(context.Background()); err == nil {
		t.Fatal("duplicate panel client identity accepted")
	}
	clients = []map[string]any{statisticsPanelRecord(id)}
	for _, name := range []string{"malformed", "oversize", "outage", "null", "deadline"} {
		t.Run(name, func(t *testing.T) {
			mode = name
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if _, err := p.statisticsSnapshot(ctx); err == nil {
				t.Fatal("unconfirmed bulk response accepted")
			}
		})
	}
	if mutation.Load() {
		t.Fatal("report requested a per-user endpoint or provider mutation")
	}
}

func TestPanelStatisticsActivity(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	panel, profile := "fixture", "regular"
	id, op := uuid.New(), uuid.New()
	a := accounts.Snapshot{ID: uuid.New(), VpnID: id, PanelKey: "acct_fixture", SubID: "fixture", AssignedPanelID: &panel, AccessProfile: &profile, Restricted: true}
	target := ProvisionTarget{OperationID: op, PanelID: panel, PanelKey: a.PanelKey, VPNID: id, SubID: a.SubID, InboundIDs: []int64{1, 2}, Profile: profile}
	raw, _ := json.Marshal(target)
	base := statisticsBaseline{AccessBaseline: AccessBaseline{TrialID: &op, TrialStatus: "applied", TrialTarget: raw}}
	used := int64(42)
	view := PanelClientView{PanelKey: a.PanelKey, VPNID: id, SubID: a.SubID, Enabled: true, InboundIDs: []int64{1, 2, 2}, UsedTraffic: &used}
	for _, tc := range []struct {
		name          string
		change        func(*accounts.Snapshot, *statisticsBaseline, *PanelClientView, map[int64]statisticsInbound)
		active, known bool
	}{
		{"unlimited-expiry-quota-restricted", nil, true, true},
		{"pending", func(_ *accounts.Snapshot, b *statisticsBaseline, _ *PanelClientView, _ map[int64]statisticsInbound) {
			b.Unresolved = true
		}, false, false},
		{"identity", func(_ *accounts.Snapshot, _ *statisticsBaseline, v *PanelClientView, _ map[int64]statisticsInbound) {
			v.VPNID = uuid.New()
		}, false, false},
		{"sub-id", func(_ *accounts.Snapshot, _ *statisticsBaseline, v *PanelClientView, _ map[int64]statisticsInbound) {
			v.SubID = "different"
		}, false, false},
		{"panel-id", func(a *accounts.Snapshot, _ *statisticsBaseline, _ *PanelClientView, _ map[int64]statisticsInbound) {
			other := "different"
			a.AssignedPanelID = &other
		}, false, false},
		{"missing-traffic", func(_ *accounts.Snapshot, _ *statisticsBaseline, v *PanelClientView, _ map[int64]statisticsInbound) {
			v.UsedTraffic = nil
		}, false, false},
		{"changed-limit", func(_ *accounts.Snapshot, _ *statisticsBaseline, v *PanelClientView, _ map[int64]statisticsInbound) {
			v.LimitIP = 2
		}, false, false},
		{"unknown-membership", func(_ *accounts.Snapshot, _ *statisticsBaseline, v *PanelClientView, _ map[int64]statisticsInbound) {
			v.InboundIDs = []int64{1, 99}
		}, false, false},
		{"retagged", func(_ *accounts.Snapshot, _ *statisticsBaseline, _ *PanelClientView, m map[int64]statisticsInbound) {
			m[1] = statisticsInbound{tags: []string{"euru"}, enabled: true}
		}, false, false},
		{"disabled-client", func(_ *accounts.Snapshot, _ *statisticsBaseline, v *PanelClientView, _ map[int64]statisticsInbound) {
			v.Enabled = false
		}, false, true},
		{"expired-confirmed", func(_ *accounts.Snapshot, b *statisticsBaseline, v *PanelClientView, _ map[int64]statisticsInbound) {
			t := target
			t.ExpiryTimeMS = now.UnixMilli() - 1
			b.TrialTarget, _ = json.Marshal(t)
			v.ExpiryTimeMS = t.ExpiryTimeMS
		}, false, true},
		{"exhausted-confirmed", func(_ *accounts.Snapshot, b *statisticsBaseline, v *PanelClientView, _ map[int64]statisticsInbound) {
			t := target
			t.TrafficLimitBytes = used
			b.TrialTarget, _ = json.Marshal(t)
			v.TrafficLimitBytes = used
		}, false, true},
		{"ban-confirmed-disabled", func(a *accounts.Snapshot, b *statisticsBaseline, v *PanelClientView, _ map[int64]statisticsInbound) {
			t := target
			t.Banned = true
			b.TrialTarget, _ = json.Marshal(t)
			a.VpnBanned = true
			v.Enabled = false
		}, false, true},
		{"applied-access-over-trial", func(_ *accounts.Snapshot, b *statisticsBaseline, _ *PanelClientView, _ map[int64]statisticsInbound) {
			b.AccessID, b.AccessTarget, b.TrialID, b.TrialStatus = &op, b.TrialTarget, nil, ""
		}, true, true},
		{"disabled-inbounds", func(_ *accounts.Snapshot, _ *statisticsBaseline, _ *PanelClientView, m map[int64]statisticsInbound) {
			for k, v := range m {
				v.enabled = false
				m[k] = v
			}
		}, false, true},
		{"unconfirmed-legacy", func(_ *accounts.Snapshot, b *statisticsBaseline, _ *PanelClientView, _ map[int64]statisticsInbound) {
			*b = statisticsBaseline{}
		}, false, false},
		{"ban-drift", func(a *accounts.Snapshot, _ *statisticsBaseline, _ *PanelClientView, _ map[int64]statisticsInbound) {
			a.VpnBanned = true
		}, false, false},
		{"no-client-intent-trial-fallback", func(_ *accounts.Snapshot, b *statisticsBaseline, _ *PanelClientView, _ map[int64]statisticsInbound) {
			access := AccessTarget{OperationID: op, NoClientIntent: true}
			b.AccessID = &op
			b.AccessTarget, _ = json.Marshal(access)
		}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b, v := a, base, view
			m := map[int64]statisticsInbound{1: {tags: []string{"regular"}, enabled: true}, 2: {tags: []string{"regular"}, enabled: true}}
			if tc.change != nil {
				tc.change(&a, &b, &v, m)
			}
			active, known := statisticsAccountActivity(a, b, panelStatisticsSnapshot{inbounds: m, clients: map[string]PanelClientView{a.PanelKey: v}}, panel, now)
			if active != tc.active || known != tc.known {
				t.Fatalf("active/known got%v/%v want%v/%v", active, known, tc.active, tc.known)
			}
		})
	}
	a.AssignedPanelID = nil
	if active, known := statisticsAccountActivity(a, statisticsBaseline{}, panelStatisticsSnapshot{}, panel, now); active || !known {
		t.Fatal("confirmed absence became panel-dependent unknown")
	}
}
