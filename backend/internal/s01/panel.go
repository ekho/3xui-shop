package s01

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Targets are persisted before the first panel write and never regenerated.
type ProvisionTarget struct {
	OperationID       uuid.UUID `json:"operation_id"`
	PanelID           string    `json:"panel_id"`
	PanelKey          string    `json:"panel_key"`
	VPNID             uuid.UUID `json:"vpn_id"`
	SubID             string    `json:"sub_id"`
	InboundIDs        []int64   `json:"inbound_ids"`
	ExpiryTimeMS      int64     `json:"expiry_time_ms"`
	DeviceCount       int64     `json:"device_count"`
	TrafficLimitBytes int64     `json:"traffic_limit_bytes"`
}
type PanelClientView struct {
	Raw                                      map[string]json.RawMessage
	PanelKey, SubID                          string
	VPNID                                    uuid.UUID
	ExpiryTimeMS, LimitIP, TrafficLimitBytes int64
	Enabled                                  bool
	InboundIDs                               []int64
	UsedTraffic                              *int64
}
type PanelClient struct {
	cfg     Config
	http    *http.Client
	loginMu sync.Mutex
	csrf    string
}

var errPanel = errors.New("panel unavailable or unconfirmed response")

func NewPanelClient(c Config) *PanelClient {
	jar, _ := cookiejar.New(nil)
	return &PanelClient{cfg: c, http: &http.Client{Timeout: 10 * time.Second, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: c.PanelRootCAs}}}}
}

func (p *PanelClient) Close() { p.http.CloseIdleConnections() }

type panelEnvelope struct {
	Success *bool           `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

func (p *PanelClient) call(ctx context.Context, method, path string, body any) (panelEnvelope, error) {
	var out panelEnvelope
	base, e := url.Parse(p.cfg.PanelURL)
	if e != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return out, errPanel
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + path
	base.RawPath = ""
	var data []byte
	if body != nil {
		data, e = json.Marshal(body)
		if e != nil {
			return out, errPanel
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(data))
	if e != nil {
		return out, errPanel
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.cfg.PanelToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.PanelToken)
	}
	p.loginMu.Lock()
	csrf := p.csrf
	p.loginMu.Unlock()
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, e := p.http.Do(req)
	if e != nil {
		return out, errPanel
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return out, errPanel
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if e != nil || len(raw) > 1024*1024 || json.Unmarshal(raw, &out) != nil || out.Success == nil {
		return out, errPanel
	}
	return out, nil
}
func (p *PanelClient) auth(ctx context.Context) error {
	if p.cfg.PanelToken != "" {
		return nil
	}
	p.loginMu.Lock()
	ready := p.csrf != ""
	p.loginMu.Unlock()
	if ready {
		return nil
	}
	// Each provisioning operation has its own client; no competing login writes.
	if p.cfg.PanelUsername == "" || p.cfg.PanelPassword == "" {
		return errPanel
	}
	out, e := p.call(ctx, "GET", "csrf-token", nil)
	var token string
	if e != nil || !*out.Success || json.Unmarshal(out.Obj, &token) != nil || token == "" {
		return errPanel
	}
	p.loginMu.Lock()
	p.csrf = token
	p.loginMu.Unlock()
	out, e = p.call(ctx, "POST", "login", map[string]string{"username": p.cfg.PanelUsername, "password": p.cfg.PanelPassword})
	if e != nil || !*out.Success {
		p.loginMu.Lock()
		p.csrf = ""
		p.loginMu.Unlock()
		return errPanel
	}
	return nil
}
func (p *PanelClient) RegularInboundIDs(ctx context.Context) ([]int64, error) {
	if p.auth(ctx) != nil {
		return nil, errPanel
	}
	out, e := p.call(ctx, "GET", "panel/api/inbounds/list", nil)
	if e != nil || !*out.Success {
		return nil, errPanel
	}
	var rows []struct {
		ID     int64  `json:"id"`
		Enable bool   `json:"enable"`
		Tag    string `json:"tag"`
	}
	if json.Unmarshal(out.Obj, &rows) != nil || rows == nil {
		return nil, errPanel
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, r := range rows {
		if r.ID <= 0 {
			return nil, errPanel
		}
		if r.Enable {
			for _, segment := range strings.Split(r.Tag, "-") {
				if segment == "regular" && !seen[r.ID] {
					seen[r.ID] = true
					ids = append(ids, r.ID)
				}
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}
func (p *PanelClient) GetClient(ctx context.Context, key string) (*PanelClientView, error) {
	if p.auth(ctx) != nil {
		return nil, errPanel
	}
	out, e := p.call(ctx, "GET", "panel/api/clients/get/"+url.PathEscape(key), nil)
	if e != nil {
		return nil, e
	}
	if !*out.Success {
		if out.Msg == "record not found" && (len(out.Obj) == 0 || string(out.Obj) == "null") {
			return nil, nil
		}
		return nil, errPanel
	}
	var obj struct {
		Client      map[string]json.RawMessage `json:"client"`
		InboundIDs  *[]int64                   `json:"inboundIds"`
		UsedTraffic *int64                     `json:"usedTraffic"`
	}
	if json.Unmarshal(out.Obj, &obj) != nil || obj.Client == nil || obj.InboundIDs == nil {
		return nil, errPanel
	}
	v := &PanelClientView{Raw: obj.Client, InboundIDs: *obj.InboundIDs, UsedTraffic: obj.UsedTraffic}
	for name, dest := range map[string]any{"email": &v.PanelKey, "id": &v.VPNID, "subId": &v.SubID, "expiryTime": &v.ExpiryTimeMS, "limitIp": &v.LimitIP, "totalGB": &v.TrafficLimitBytes, "enable": &v.Enabled} {
		raw, ok := obj.Client[name]
		if !ok || string(raw) == "null" || json.Unmarshal(raw, dest) != nil {
			return nil, errPanel
		}
	}
	if v.PanelKey != key || v.VPNID == uuid.Nil || v.SubID == "" || v.ExpiryTimeMS <= 0 || v.LimitIP < 0 || v.TrafficLimitBytes < 0 || v.UsedTraffic != nil && *v.UsedTraffic < 0 {
		return nil, errPanel
	}
	for _, id := range v.InboundIDs {
		if id <= 0 {
			return nil, errPanel
		}
	}
	return v, nil
}
func (p *PanelClient) AddClient(ctx context.Context, t ProvisionTarget) error {
	if p.auth(ctx) != nil {
		return errPanel
	}
	limit := t.DeviceCount
	if limit > 0 {
		limit++
	}
	out, e := p.call(ctx, "POST", "panel/api/clients/add", map[string]any{"client": map[string]any{"email": t.PanelKey, "id": t.VPNID, "subId": t.SubID, "expiryTime": t.ExpiryTimeMS, "limitIp": limit, "totalGB": t.TrafficLimitBytes, "enable": true, "flow": "xtls-rprx-vision"}, "inboundIds": t.InboundIDs})
	if e != nil || !*out.Success {
		return errPanel
	}
	return nil
}
func (p *PanelClient) Attach(ctx context.Context, key string, ids []int64) error {
	if p.auth(ctx) != nil {
		return errPanel
	}
	out, e := p.call(ctx, "POST", "panel/api/clients/"+url.PathEscape(key)+"/attach", map[string]any{"inboundIds": ids})
	if e != nil || !*out.Success {
		return errPanel
	}
	return nil
}
func panelMatches(v *PanelClientView, t ProvisionTarget, now time.Time) bool {
	limit := t.DeviceCount
	if limit > 0 {
		limit++
	}
	return v != nil && v.PanelKey == t.PanelKey && v.VPNID == t.VPNID && v.SubID == t.SubID && v.ExpiryTimeMS == t.ExpiryTimeMS && v.LimitIP == limit && v.TrafficLimitBytes == t.TrafficLimitBytes && (v.Enabled || now.UnixMilli() >= t.ExpiryTimeMS)
}
func missingInbounds(v *PanelClientView, t ProvisionTarget) []int64 {
	have := map[int64]bool{}
	for _, id := range v.InboundIDs {
		have[id] = true
	}
	missing := []int64{}
	for _, id := range t.InboundIDs {
		if !have[id] {
			missing = append(missing, id)
		}
	}
	return missing
}
