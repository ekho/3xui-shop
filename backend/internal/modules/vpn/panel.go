package vpn

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type PanelClient struct {
	cfg     Config
	http    *http.Client
	loginMu sync.Mutex
	csrf    string
}

var ErrPanel = errors.New("panel unavailable or unconfirmed response")
var ErrIdentity = errors.New("panel identity mismatch")
var ErrMembership = errors.New("unknown panel membership")
var ErrTraffic = errors.New("invalid panel traffic")

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
		return out, ErrPanel
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + path
	base.RawPath = ""
	var data []byte
	if body != nil {
		data, e = json.Marshal(body)
		if e != nil {
			return out, ErrPanel
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(data))
	if e != nil {
		return out, ErrPanel
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US")
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
		return out, ErrPanel
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return out, ErrPanel
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if e != nil || len(raw) > 1024*1024 || json.Unmarshal(raw, &out) != nil || out.Success == nil {
		return out, ErrPanel
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
		return ErrPanel
	}
	out, e := p.call(ctx, "GET", "csrf-token", nil)
	var token string
	if e != nil || !*out.Success || json.Unmarshal(out.Obj, &token) != nil || token == "" {
		return ErrPanel
	}
	p.loginMu.Lock()
	p.csrf = token
	p.loginMu.Unlock()
	out, e = p.call(ctx, "POST", "login", map[string]string{"username": p.cfg.PanelUsername, "password": p.cfg.PanelPassword})
	if e != nil || !*out.Success {
		p.loginMu.Lock()
		p.csrf = ""
		p.loginMu.Unlock()
		return ErrPanel
	}
	return nil
}
func (p *PanelClient) RegularInboundIDs(ctx context.Context) ([]int64, error) {
	if p.auth(ctx) != nil {
		return nil, ErrPanel
	}
	out, e := p.call(ctx, "GET", "panel/api/inbounds/list", nil)
	if e != nil || !*out.Success {
		return nil, ErrPanel
	}
	var rows []struct {
		ID     int64  `json:"id"`
		Enable bool   `json:"enable"`
		Tag    string `json:"tag"`
	}
	if json.Unmarshal(out.Obj, &rows) != nil || rows == nil {
		return nil, ErrPanel
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, r := range rows {
		if r.ID <= 0 {
			return nil, ErrPanel
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
		return nil, ErrPanel
	}
	out, e := p.call(ctx, "GET", "panel/api/clients/get/"+url.PathEscape(key), nil)
	if e != nil {
		return nil, e
	}
	if !*out.Success {
		if (out.Msg == "record not found" || out.Msg == " (record not found)" || out.Msg == "Obtain (record not found)") && (len(out.Obj) == 0 || string(out.Obj) == "null") {
			return nil, nil
		}
		return nil, ErrPanel
	}
	var obj struct {
		Client      map[string]json.RawMessage `json:"client"`
		InboundIDs  *[]int64                   `json:"inboundIds"`
		UsedTraffic *int64                     `json:"usedTraffic"`
	}
	if json.Unmarshal(out.Obj, &obj) != nil || obj.Client == nil || obj.InboundIDs == nil {
		return nil, ErrPanel
	}
	v := &PanelClientView{Raw: obj.Client, InboundIDs: *obj.InboundIDs, UsedTraffic: obj.UsedTraffic}
	// Native readback uses uuid; numeric id is the panel's database primary key.
	for name, dest := range map[string]any{"email": &v.PanelKey, "uuid": &v.VPNID, "subId": &v.SubID, "expiryTime": &v.ExpiryTimeMS, "limitIp": &v.LimitIP, "totalGB": &v.TrafficLimitBytes, "enable": &v.Enabled} {
		raw, ok := obj.Client[name]
		if !ok || string(raw) == "null" || json.Unmarshal(raw, dest) != nil {
			return nil, ErrPanel
		}
	}
	if v.PanelKey != key || v.VPNID == uuid.Nil || v.SubID == "" {
		return nil, ErrIdentity
	}
	if v.ExpiryTimeMS < 0 || v.LimitIP < 0 || v.TrafficLimitBytes < 0 || v.UsedTraffic != nil && *v.UsedTraffic < 0 {
		return nil, ErrPanel
	}
	for _, id := range v.InboundIDs {
		if id <= 0 {
			return nil, ErrPanel
		}
	}
	return v, nil
}
func (p *PanelClient) Traffic(ctx context.Context, key string, id uuid.UUID, subID string) (int64, int64, error) {
	if p.auth(ctx) != nil {
		return 0, 0, ErrPanel
	}
	out, e := p.call(ctx, "GET", "panel/api/clients/traffic/"+url.PathEscape(key), nil)
	if e != nil || !*out.Success {
		return 0, 0, ErrPanel
	}
	return parsePanelTraffic(out.Obj, key, id, subID)
}

func parsePanelTraffic(raw json.RawMessage, key string, id uuid.UUID, subID string) (int64, int64, error) {
	var row struct {
		Email string    `json:"email"`
		UUID  uuid.UUID `json:"uuid"`
		SubID string    `json:"subId"`
		Up    int64     `json:"up"`
		Down  int64     `json:"down"`
	}
	var fields map[string]json.RawMessage
	if id == uuid.Nil || subID == "" || json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, &row) != nil {
		return 0, 0, ErrTraffic
	}
	for _, name := range []string{"email", "uuid", "subId", "up", "down"} {
		if len(fields[name]) == 0 || string(fields[name]) == "null" {
			return 0, 0, ErrTraffic
		}
	}
	if row.Email != key || row.UUID != id || row.SubID != subID {
		return 0, 0, ErrIdentity
	}
	if row.Up < 0 || row.Down < 0 || row.Up > math.MaxInt64-row.Down {
		return 0, 0, ErrTraffic
	}
	return row.Up, row.Down, nil
}
func (p *PanelClient) AccessProfile(ctx context.Context, ids []int64) (string, error) {
	if p.auth(ctx) != nil {
		return "", ErrPanel
	}
	out, e := p.call(ctx, "GET", "panel/api/inbounds/list", nil)
	if e != nil || !*out.Success {
		return "", ErrPanel
	}
	var rows []struct {
		ID  int64  `json:"id"`
		Tag string `json:"tag"`
	}
	if json.Unmarshal(out.Obj, &rows) != nil || rows == nil {
		return "", ErrPanel
	}
	tags := make(map[int64]string, len(rows))
	for _, r := range rows {
		if r.ID <= 0 || tags[r.ID] != "" {
			return "", ErrMembership
		}
		tags[r.ID] = r.Tag
	}
	if len(ids) == 0 {
		return "", ErrMembership
	}
	groups := map[string]bool{}
	for _, id := range ids {
		tag, ok := tags[id]
		if !ok {
			return "", ErrMembership
		}
		known := false
		for _, segment := range strings.Split(tag, "-") {
			switch segment {
			case "regular", "euru", "unlimited":
				groups[segment] = true
				known = true
			}
		}
		if !known {
			return "", ErrMembership
		}
	}
	if groups["euru"] && (groups["regular"] || groups["unlimited"]) {
		return "", ErrMembership
	}
	for _, profile := range []string{"unlimited", "euru", "regular"} {
		if groups[profile] {
			return profile, nil
		}
	}
	return "", ErrMembership
}
func (p *PanelClient) AddClient(ctx context.Context, t ProvisionTarget) error {
	if p.auth(ctx) != nil {
		return ErrPanel
	}
	limit := t.DeviceCount
	if limit > 0 {
		limit++
	}
	out, e := p.call(ctx, "POST", "panel/api/clients/add", map[string]any{"client": map[string]any{"email": t.PanelKey, "id": t.VPNID, "subId": t.SubID, "expiryTime": t.ExpiryTimeMS, "limitIp": limit, "totalGB": t.TrafficLimitBytes, "enable": true, "flow": "xtls-rprx-vision"}, "inboundIds": t.InboundIDs})
	if e != nil || !*out.Success {
		return ErrPanel
	}
	return nil
}
func (p *PanelClient) Attach(ctx context.Context, key string, ids []int64) error {
	if p.auth(ctx) != nil {
		return ErrPanel
	}
	out, e := p.call(ctx, "POST", "panel/api/clients/"+url.PathEscape(key)+"/attach", map[string]any{"inboundIds": ids})
	if e != nil || !*out.Success {
		return ErrPanel
	}
	return nil
}

func (p *PanelClient) inboundTags(ctx context.Context) (map[int64][]string, error) {
	if p.auth(ctx) != nil {
		return nil, ErrPanel
	}
	out, e := p.call(ctx, "GET", "panel/api/inbounds/list", nil)
	if e != nil || !*out.Success {
		return nil, ErrPanel
	}
	var rows []struct {
		ID     int64  `json:"id"`
		Enable bool   `json:"enable"`
		Tag    string `json:"tag"`
	}
	if json.Unmarshal(out.Obj, &rows) != nil || rows == nil {
		return nil, ErrPanel
	}
	tags := map[int64][]string{}
	for _, r := range rows {
		if r.ID <= 0 || tags[r.ID] != nil {
			return nil, ErrMembership
		}
		if r.Enable {
			tags[r.ID] = strings.Split(r.Tag, "-")
		} else {
			tags[r.ID] = []string{}
		}
	}
	return tags, nil
}

func hasSegment(segments []string, wanted string) bool {
	for _, segment := range segments {
		if segment == wanted {
			return true
		}
	}
	return false
}

func (p *PanelClient) ProfileInboundIDs(ctx context.Context, profile string) ([]int64, error) {
	if profile != "regular" && profile != "euru" && profile != "unlimited" {
		return nil, ErrMembership
	}
	tags, e := p.inboundTags(ctx)
	if e != nil {
		return nil, e
	}
	ids := []int64{}
	for id, segments := range tags {
		if hasSegment(segments, profile) || profile == "unlimited" && hasSegment(segments, "regular") {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		return nil, ErrMembership
	}
	return ids, nil
}

// Only IDs belonging to a known managed profile are detached. Other memberships survive.
func (p *PanelClient) MembershipDiff(ctx context.Context, current, desired []int64) ([]int64, []int64, error) {
	tags, e := p.inboundTags(ctx)
	if e != nil {
		return nil, nil, e
	}
	need := map[int64]bool{}
	have := map[int64]bool{}
	for _, id := range desired {
		if _, ok := tags[id]; !ok {
			return nil, nil, ErrMembership
		}
		need[id] = true
	}
	for _, id := range current {
		if _, ok := tags[id]; !ok {
			return nil, nil, ErrMembership
		}
		have[id] = true
	}
	attach, detach := []int64{}, []int64{}
	for id := range need {
		if !have[id] {
			attach = append(attach, id)
		}
	}
	for id := range have {
		if !need[id] && (hasSegment(tags[id], "regular") || hasSegment(tags[id], "euru") || hasSegment(tags[id], "unlimited")) {
			detach = append(detach, id)
		}
	}
	sort.Slice(attach, func(i, j int) bool { return attach[i] < attach[j] })
	sort.Slice(detach, func(i, j int) bool { return detach[i] < detach[j] })
	return attach, detach, nil
}

func (p *PanelClient) Detach(ctx context.Context, key string, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if p.auth(ctx) != nil {
		return ErrPanel
	}
	out, e := p.call(ctx, "POST", "panel/api/clients/"+url.PathEscape(key)+"/detach", map[string]any{"inboundIds": ids})
	if e != nil || !*out.Success {
		return ErrPanel
	}
	return nil
}

func (p *PanelClient) UpdateAccess(ctx context.Context, v *PanelClientView, t AccessTarget) error {
	if p.auth(ctx) != nil || v == nil || v.PanelKey != t.PanelKey || v.VPNID != t.VPNID || v.SubID != t.SubID {
		return ErrIdentity
	}
	data := make(map[string]json.RawMessage, len(v.Raw))
	for k, value := range v.Raw {
		data[k] = value
	}
	// GET returns a ClientRecord (numeric id, UUID, CSV allowedIPs), while
	// update binds a Client (UUID id, string-array allowedIPs).
	var recordUUID string
	if json.Unmarshal(data["uuid"], &recordUUID) != nil {
		return ErrIdentity
	}
	parsed, err := uuid.Parse(recordUUID)
	if err != nil || parsed != t.VPNID {
		return ErrIdentity
	}
	data["id"], err = json.Marshal(t.VPNID.String())
	if err != nil {
		return ErrPanel
	}
	if raw, ok := data["allowedIPs"]; ok {
		var csv string
		var ips []string
		if json.Unmarshal(raw, &csv) == nil {
			for _, part := range strings.Split(csv, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					ips = append(ips, trimmed)
				}
			}
		} else if json.Unmarshal(raw, &ips) != nil {
			return ErrPanel
		}
		if len(ips) == 0 {
			delete(data, "allowedIPs")
		} else if data["allowedIPs"], err = json.Marshal(ips); err != nil {
			return ErrPanel
		}
	}
	limit := t.DeviceCount
	if limit > 0 {
		limit++
	}
	for name, value := range map[string]any{"expiryTime": t.ExpiryTimeMS, "limitIp": limit, "totalGB": t.TrafficLimitBytes} {
		raw, e := json.Marshal(value)
		if e != nil {
			return ErrPanel
		}
		data[name] = raw
	}
	if t.Banned {
		data["enable"] = json.RawMessage("false")
	} else if t.RestoreEnabled || t.Enable {
		data["enable"] = json.RawMessage("true")
	}
	out, e := p.call(ctx, "POST", "panel/api/clients/update/"+url.PathEscape(t.PanelKey), data)
	if e != nil || !*out.Success {
		return ErrPanel
	}
	return nil
}

func (p *PanelClient) ResetAccessTraffic(ctx context.Context, key string) error {
	if p.auth(ctx) != nil {
		return ErrPanel
	}
	out, e := p.call(ctx, "POST", "panel/api/clients/resetTraffic/"+url.PathEscape(key), map[string]any{})
	if e != nil || !*out.Success {
		return ErrPanel
	}
	return nil
}

func (p *PanelClient) DisableAccess(ctx context.Context, key string) error {
	if p.auth(ctx) != nil {
		return ErrPanel
	}
	out, e := p.call(ctx, "POST", "panel/api/clients/bulkDisable", map[string]any{"emails": []string{key}})
	if e != nil || !*out.Success {
		return ErrPanel
	}
	return nil
}
func Matches(v *PanelClientView, t ProvisionTarget, now time.Time) bool {
	limit := t.DeviceCount
	if limit > 0 {
		limit++
	}
	return v != nil && v.PanelKey == t.PanelKey && v.VPNID == t.VPNID && v.SubID == t.SubID && v.ExpiryTimeMS == t.ExpiryTimeMS && v.LimitIP == limit && v.TrafficLimitBytes == t.TrafficLimitBytes && (t.Banned && !v.Enabled || !t.Banned && (v.Enabled || now.UnixMilli() >= t.ExpiryTimeMS))
}
func MissingInbounds(v *PanelClientView, t ProvisionTarget) []int64 {
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
