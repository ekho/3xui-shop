package s01

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type profilePanel struct {
	mu                             sync.Mutex
	client                         map[string]any
	ids                            []int64
	traffic                        map[string]any
	fail, writes                   bool
	blockTraffic                   bool
	trafficStarted, trafficRelease chan struct{}
}

func profileFixture(t *testing.T, s *Service, client map[string]any, ids []int64) *profilePanel {
	t.Helper()
	p := &profilePanel{client: client, ids: ids, traffic: map[string]any{"email": client["email"], "uuid": client["id"], "subId": client["subId"], "up": int64(100), "down": int64(200)}}
	h := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var heldTraffic map[string]any
		p.mu.Lock()
		if r.URL.Path == "/panel/api/clients/traffic/"+client["email"].(string) && p.blockTraffic {
			p.blockTraffic = false
			heldTraffic = make(map[string]any, len(p.traffic))
			for k, v := range p.traffic {
				heldTraffic[k] = v
			}
			started, release := p.trafficStarted, p.trafficRelease
			p.mu.Unlock()
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			p.mu.Lock()
		}
		defer p.mu.Unlock()
		if r.Method != http.MethodGet {
			p.writes = true
			w.WriteHeader(405)
			return
		}
		if p.fail {
			w.WriteHeader(503)
			return
		}
		var obj any
		switch r.URL.Path {
		case "/panel/api/clients/get/" + client["email"].(string):
			record := make(map[string]any, len(p.client)+1)
			for k, v := range p.client {
				record[k] = v
			}
			record["uuid"], record["id"] = p.client["id"], 1
			obj = map[string]any{"client": record, "inboundIds": p.ids, "usedTraffic": 0}
		case "/panel/api/clients/traffic/" + client["email"].(string):
			if heldTraffic != nil {
				obj = heldTraffic
			} else {
				obj = p.traffic
			}
		case "/panel/api/inbounds/list":
			obj = []map[string]any{{"id": 1, "enable": true, "tag": "node-regular"}, {"id": 2, "enable": true, "tag": "node-regular-second"}, {"id": 3, "enable": true, "tag": "node-unlimited"}, {"id": 5, "enable": true, "tag": "unknown"}, {"id": 6, "enable": true, "tag": "node-euru"}}
		default:
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": obj})
	}))
	t.Cleanup(h.Close)
	s.cfg.PanelURL = h.URL
	s.cfg.PanelRootCAs = x509.NewCertPool()
	s.cfg.PanelRootCAs.AddCert(h.Certificate())
	return p
}

func profileApplied(t *testing.T) (*Service, uuid.UUID, *profilePanel) {
	t.Helper()
	s, e := fixture(t)
	p := panelFixture(t, s)
	account, op := approved(t, s, e, "profile@example.test")
	if err := s.Provision(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	client := make(map[string]any, len(p.client))
	for k, v := range p.client {
		client[k] = v
	}
	ids := append([]int64(nil), p.ids...)
	p.mu.Unlock()
	return s, account, profileFixture(t, s, client, ids)
}

func TestProfileStates(t *testing.T) {
	s, account, p := profileApplied(t)
	ctx := context.Background()
	p.mu.Lock()
	p.client["totalGB"] = int64(1024)
	p.mu.Unlock()
	check := func(want string, key bool) {
		t.Helper()
		v, err := s.Subscription(ctx, account)
		if err != nil || string(v.Status) != want {
			t.Fatalf("status want %s: %v %v", want, v.Status, err)
		}
		_, err = s.SubscriptionKey(ctx, account)
		if (err == nil) != key || want == "banned" && status(err) != 403 {
			t.Fatalf("key for %s: %v", want, err)
		}
	}
	check("active", true)
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET vpn_banned=true WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.client["enable"] = false
	p.mu.Unlock()
	check("banned", false)
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET vpn_banned=false WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	check("disabled", false)
	p.mu.Lock()
	p.client["enable"] = true
	p.traffic["up"] = int64(1024)
	p.traffic["down"] = int64(1)
	p.mu.Unlock()
	check("exhausted", false)
	p.mu.Lock()
	p.traffic["up"] = int64(100)
	p.traffic["down"] = int64(200)
	p.client["expiryTime"] = int64(1)
	p.mu.Unlock()
	check("expired", true)
	p.mu.Lock()
	p.client["expiryTime"] = int64(0)
	p.ids = []int64{5}
	p.mu.Unlock()
	check("needs_review", false)
	if p.writes {
		t.Fatal("profile read wrote to panel")
	}
}

func TestProfileMembership(t *testing.T) {
	s, account, p := profileApplied(t)
	p.mu.Lock()
	p.ids = []int64{1, 3}
	p.client["expiryTime"] = int64(0)
	p.client["totalGB"] = int64(100_000)
	p.client["limitIp"] = int64(8)
	p.mu.Unlock()
	v, err := s.Subscription(context.Background(), account)
	if err != nil || string(v.AccessProfile) != "unlimited" || v.ExpiresAt != nil || v.UnlimitedTraffic == nil || *v.UnlimitedTraffic || v.UnlimitedDevices == nil || *v.UnlimitedDevices || v.Devices != 7 {
		t.Fatal("inherited profile with finite limits", err)
	}
	p.mu.Lock()
	p.ids = []int64{1, 6}
	p.mu.Unlock()
	v, err = s.Subscription(context.Background(), account)
	if err != nil || string(v.Status) != "needs_review" {
		t.Fatal("conflicting membership", err)
	}
	if _, err = s.SubscriptionKey(context.Background(), account); status(err) != 409 {
		t.Fatal("conflicting membership key")
	}
}

func TestProfileOwnership(t *testing.T) {
	s, account, _ := profileApplied(t)
	ctx := context.Background()
	for _, change := range []string{
		`UPDATE trial_grants SET status='reserved' WHERE account_id=$1`,
		`UPDATE accounts SET panel_key='foreign' WHERE id=$1`,
	} {
		if _, err := s.pool.Exec(ctx, change, account); err != nil {
			t.Fatal(err)
		}
		v, err := s.Subscription(ctx, account)
		if err != nil || string(v.Status) != "needs_review" || v.TrafficUploadBytes != nil {
			t.Fatal("unowned profile exposed", err)
		}
		if _, err := s.SubscriptionKey(ctx, account); status(err) != 409 {
			t.Fatal("unowned key exposed")
		}
		var state string
		if err := s.pool.QueryRow(ctx, `SELECT status FROM trial_operations WHERE account_id=$1`, account).Scan(&state); err != nil || state != "applied" {
			t.Fatal("read changed operation", err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE trial_grants SET status='granted' WHERE account_id=$1`, account); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProfileTrafficBoundary(t *testing.T) {
	s, account, p := profileApplied(t)
	ctx := context.Background()
	p.mu.Lock()
	p.client["totalGB"] = int64(1024)
	p.mu.Unlock()
	v, err := s.Subscription(ctx, account)
	if err != nil || v.TrafficUploadBytes == nil || *v.TrafficUploadBytes != 100 || v.TrafficDownloadBytes == nil || *v.TrafficDownloadBytes != 200 || v.TrafficUsedBytes == nil || *v.TrafficUsedBytes != 300 || v.TrafficRemainingBytes == nil || *v.TrafficRemainingBytes != 724 || v.DataStale {
		t.Fatal("split traffic", err)
	}
	p.mu.Lock()
	p.traffic["up"] = int64(0)
	p.traffic["down"] = int64(0)
	p.client["totalGB"] = int64(0)
	p.client["limitIp"] = int64(0)
	p.mu.Unlock()
	v, err = s.Subscription(ctx, account)
	if err != nil || v.TrafficUsedBytes == nil || *v.TrafficUsedBytes != 0 || v.TrafficRemainingBytes != nil || v.UnlimitedTraffic == nil || !*v.UnlimitedTraffic || v.UnlimitedDevices == nil || !*v.UnlimitedDevices {
		t.Fatal("zero/unlimited", err)
	}
	for _, bad := range []map[string]any{
		{"up": int64(-1)}, {"up": int64(math.MaxInt64), "down": int64(1)}, {"uuid": uuid.NewString()}, {"email": "foreign"},
	} {
		p.mu.Lock()
		for k, x := range bad {
			p.traffic[k] = x
		}
		p.mu.Unlock()
		v, err = s.Subscription(ctx, account)
		if err != nil || !v.DataStale || v.TrafficUsedBytes == nil || *v.TrafficUsedBytes != 0 {
			t.Fatal("invalid observation overwrote cache", err)
		}
		if _, err := s.SubscriptionKey(ctx, account); status(err) != 409 {
			t.Fatal("invalid traffic exposed key")
		}
		p.mu.Lock()
		p.traffic = map[string]any{"email": p.client["email"], "uuid": p.client["id"], "subId": p.client["subId"], "up": int64(0), "down": int64(0)}
		p.mu.Unlock()
	}
}

func TestProfileCache(t *testing.T) {
	s, account, p := profileApplied(t)
	ctx := context.Background()
	p.mu.Lock()
	p.fail = true
	p.mu.Unlock()
	first, err := s.Subscription(ctx, account)
	if err != nil || !first.DataStale || first.TrafficUsedBytes != nil || first.TrafficUploadBytes != nil || first.ObservedAt != nil {
		t.Fatal("first outage fabricated traffic", err)
	}
	p.mu.Lock()
	p.fail = false
	p.mu.Unlock()
	v, err := s.Subscription(ctx, account)
	if err != nil || v.ObservedAt == nil {
		t.Fatal("initial observation", err)
	}
	p.mu.Lock()
	p.ids = []int64{1, 3}
	p.client["expiryTime"] = int64(0)
	p.client["limitIp"] = int64(8)
	p.client["totalGB"] = int64(100 * 1024 * 1024 * 1024)
	p.mu.Unlock()
	v, err = s.Subscription(ctx, account)
	if err != nil || string(v.AccessProfile) != "unlimited" || v.ExpiresAt != nil || v.Devices != 7 {
		t.Fatal("native profile change", err)
	}
	p.mu.Lock()
	p.fail = true
	p.mu.Unlock()
	stale, err := s.Subscription(ctx, account)
	if err != nil || !stale.DataStale || stale.ObservedAt == nil || !stale.ObservedAt.Equal(*v.ObservedAt) || stale.TrafficUploadBytes == nil || *stale.TrafficUploadBytes != 100 || stale.TrafficDownloadBytes == nil || *stale.TrafficDownloadBytes != 200 || stale.AccessProfile != v.AccessProfile || stale.Devices != 7 || stale.TrafficLimitBytes != v.TrafficLimitBytes || stale.ExpiresAt != nil || stale.TrafficRemainingBytes == nil || *stale.TrafficRemainingBytes != v.TrafficLimitBytes-300 {
		t.Fatal("cache outage", err)
	}
	if _, err := s.SubscriptionKey(ctx, account); status(err) != 409 {
		t.Fatal("stale key exposed")
	}
	if p.writes {
		t.Fatal("profile read wrote to panel")
	}
}

func TestProfileObservationOrder(t *testing.T) {
	s, account, _ := profileApplied(t)
	ctx := context.Background()
	var op uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id FROM trial_operations WHERE account_id=$1`, account).Scan(&op); err != nil {
		t.Fatal(err)
	}
	newer := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for _, x := range []struct {
		up, down int64
		at       time.Time
		snapshot profileSnapshot
	}{{20, 30, newer, profileSnapshot{ExpiryTimeMS: ptr(int64(0)), LimitIP: ptr(int64(8)), TrafficLimitBytes: ptr(int64(1000)), Enabled: ptr(true), Profile: "unlimited"}}, {1, 2, newer.Add(-time.Second), profileSnapshot{ExpiryTimeMS: ptr(int64(1)), LimitIP: ptr(int64(2)), TrafficLimitBytes: ptr(int64(3)), Enabled: ptr(false), Profile: "regular"}}} {
		if err := observeProfileTraffic(ctx, s.pool, op, x.up, x.down, x.at, x.snapshot); err != nil {
			t.Fatal(err)
		}
	}
	var up, down int64
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT traffic_up_bytes,traffic_down_bytes,profile_snapshot FROM trial_operations WHERE id=$1`, op).Scan(&up, &down, &raw); err != nil || up != 20 || down != 30 {
		t.Fatal("older observation replaced newer", err)
	}
	var snapshot profileSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Profile != "unlimited" || snapshot.LimitIP == nil || *snapshot.LimitIP != 8 {
		t.Fatal("older metadata replaced newer")
	}
}

func TestProfileOlderReadUsesCurrentObservation(t *testing.T) {
	s, account, p := profileApplied(t)
	ctx := context.Background()
	var clock atomic.Int64
	firstAt := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	clock.Store(firstAt.UnixNano())
	s.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	p.mu.Lock()
	p.blockTraffic = true
	p.trafficStarted = make(chan struct{})
	p.trafficRelease = make(chan struct{})
	p.traffic["up"], p.traffic["down"] = int64(1), int64(2)
	started, release := p.trafficStarted, p.trafficRelease
	p.mu.Unlock()
	type result struct {
		used, limit, devices int64
		profile              string
		observed             time.Time
		expiry               *time.Time
		err                  error
	}
	first := make(chan result, 1)
	go func() {
		v, err := s.Subscription(ctx, account)
		r := result{err: err, limit: v.TrafficLimitBytes, devices: v.Devices, expiry: v.ExpiresAt}
		if v.TrafficUsedBytes != nil {
			r.used = *v.TrafficUsedBytes
		}
		if v.AccessProfile != "" {
			r.profile = string(v.AccessProfile)
		}
		if v.ObservedAt != nil {
			r.observed = *v.ObservedAt
		}
		first <- r
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("older read did not reach traffic endpoint")
	}
	p.mu.Lock()
	p.ids = []int64{1, 3}
	p.client["expiryTime"] = int64(0)
	p.client["limitIp"] = int64(8)
	p.client["totalGB"] = int64(1000)
	p.traffic["up"], p.traffic["down"] = int64(20), int64(30)
	p.mu.Unlock()
	newerAt := firstAt.Add(time.Second)
	clock.Store(newerAt.UnixNano())
	newer, err := s.Subscription(ctx, account)
	close(release)
	if err != nil || newer.TrafficUsedBytes == nil || *newer.TrafficUsedBytes != 50 {
		t.Fatal("newer observation", err)
	}
	older := <-first
	if older.err != nil || older.used != 50 || older.limit != 1000 || older.devices != 7 || older.profile != "unlimited" || older.expiry != nil || !older.observed.Equal(newerAt) {
		t.Fatal("older response mixed observations", older.err)
	}
}
