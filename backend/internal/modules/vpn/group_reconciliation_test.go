package vpn

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"example.com/cabinet/backend/internal/modules/vpn/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func TestGroupMembershipDisabledRemovedUnknown(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": []map[string]any{
			{"id": 1, "enable": true, "tag": "region-regular-tcp"},
			{"id": 2, "enable": false, "tag": "euru"},
			{"id": 3, "enable": true, "tag": "nonregular"},
			{"id": 4, "enable": true, "tag": "unlimited"},
		}})
	}))
	defer server.Close()
	ca := x509.NewCertPool()
	ca.AddCert(server.Certificate())
	p := NewPanelClient(Config{PanelURL: server.URL, PanelToken: "synthetic", PanelRootCAs: ca})
	defer p.Close()
	ctx := context.Background()
	ids, err := p.ProfileInboundIDs(ctx, "unlimited")
	if err != nil || !reflect.DeepEqual(ids, []int64{1, 4}) {
		t.Fatalf("enabled unlimited inheritance: %v %v", ids, err)
	}
	attach, detach, err := p.MembershipDiff(ctx, []int64{2, 3, 99}, []int64{1, 4})
	if err != nil || !reflect.DeepEqual(attach, []int64{1, 4}) || !reflect.DeepEqual(detach, []int64{2}) {
		t.Fatalf("known disabled must detach; unknown and removed must survive: %v %v %v", attach, detach, err)
	}
	for _, invalid := range [][]int64{{0}, {-1}, {99}} {
		if _, _, err = p.MembershipDiff(ctx, []int64{1}, invalid); err == nil {
			t.Fatal("invalid desired membership accepted")
		}
	}
}

func TestGroupMalformedInboundRead(t *testing.T) {
	for _, row := range []map[string]any{{"id": 2, "tag": "euru"}, {"id": 2, "enable": true}, {"id": 2, "enable": nil, "tag": "euru"}} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": []map[string]any{{"id": 1, "enable": true, "tag": "regular"}, row}})
		}))
		ca := x509.NewCertPool()
		ca.AddCert(server.Certificate())
		p := NewPanelClient(Config{PanelURL: server.URL, PanelToken: "synthetic", PanelRootCAs: ca})
		_, err := p.ProfileInboundIDs(context.Background(), "regular")
		p.Close()
		server.Close()
		if err == nil {
			t.Fatal("partial provider inbound row accepted")
		}
	}
}

func TestGroupReconciliationProfilesAndManualDisable(t *testing.T) {
	for _, profile := range []string{"regular", "euru", "unlimited"} {
		t.Run(profile, func(t *testing.T) {
			s, account, g := groupFixture(t)
			ctx := context.Background()
			if _, err := s.pool.Exec(ctx, `UPDATE accounts SET access_profile=$2 WHERE id=$1`, account, profile); err != nil {
				t.Fatal(err)
			}
			g.inbounds = append(g.inbounds, map[string]any{"id": 5, "enable": true, "tag": "euru-new"})
			g.client["enable"] = false
			op, err := s.PrepareGroupReconciliation(ctx, account)
			if err != nil || op == uuid.Nil {
				t.Fatal("profile drift", err)
			}
			if err = s.ApplyAccess(ctx, op); err != nil {
				t.Fatal(err)
			}
			row, err := store.New(s.pool).AccessOperationByID(ctx, op)
			if err != nil || row.Status != "applied" {
				t.Fatal("group apply", err)
			}
			want := map[string][]int64{"regular": {3, 1}, "euru": {3, 5}, "unlimited": {3, 1, 4}}[profile]
			if !reflect.DeepEqual(g.ids, want) || g.client["enable"] != false || g.client["limitIp"] != int64(1) {
				t.Fatal("wrong memberships or manual disable overwritten")
			}
			tx, err := s.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			a, err := s.accounts.LookupTx(ctx, tx, account)
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.AccessBaselineTx(ctx, tx, account)
			if err != nil || tx.Commit(ctx) != nil {
				t.Fatal(err)
			}
			p, err := s.PanelFor(ctx, *a.AssignedPanelID)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			v, err := p.GetClient(ctx, a.PanelKey)
			if err != nil {
				t.Fatal(err)
			}
			confirmed, err := s.ConfirmedAccessProfile(ctx, b, a, v, p, true)
			if err != nil || confirmed != profile {
				t.Fatal("reconcile lost strict native baseline", err)
			}
		})
	}
}

func TestGroupReconciliationFailureBoundaries(t *testing.T) {
	for _, failure := range []string{"offline", "missing", "foreign", "unknown", "empty"} {
		t.Run(failure, func(t *testing.T) {
			s, account, g := groupFixture(t)
			ctx := context.Background()
			var notified []string
			s.WithGroupFailureNotifier(func(_ context.Context, _ pgx.Tx, _ uuid.UUID, code string) error {
				notified = append(notified, code)
				return nil
			})
			switch failure {
			case "offline":
				g.offline = true
			case "missing":
				g.client = nil
			case "foreign":
				g.client["uuid"] = uuid.NewString()
			case "unknown":
				if _, err := s.pool.Exec(ctx, `UPDATE accounts SET access_profile=NULL WHERE id=$1`, account); err != nil {
					t.Fatal(err)
				}
			case "empty":
				g.inbounds = []map[string]any{{"id": 3, "tag": "foreign", "enable": true}}
			}
			id, err := s.PrepareGroupReconciliation(ctx, account)
			if err == nil || id != uuid.Nil || len(g.writes) != 0 || len(notified) != 1 {
				t.Fatal("uncertain read changed client or lost alert", err)
			}
			var operations int
			if s.pool.QueryRow(ctx, `SELECT count(*) FROM access_operations WHERE account_id=$1`, account).Scan(&operations) != nil || operations != 0 {
				t.Fatal("unsafe operation prepared")
			}
			if failure == "offline" {
				g.offline = false
				id, err = s.PrepareGroupReconciliation(ctx, account)
				if err != nil || id == uuid.Nil {
					t.Fatal("offline recovery failed", err)
				}
			}
		})
	}
}

func TestGroupReconciliationPartialRecoveryAndStaleTarget(t *testing.T) {
	for _, failure := range []string{"partial", "retag", "removed"} {
		t.Run(failure, func(t *testing.T) {
			s, account, g := groupFixture(t)
			ctx := context.Background()
			old, err := s.PrepareGroupReconciliation(ctx, account)
			if err != nil || old == uuid.Nil {
				t.Fatal(err)
			}
			before, err := store.New(s.pool).AccessOperationByID(ctx, old)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "partial":
				g.failDetach = true
			case "retag":
				g.inbounds[0]["tag"] = "manual"
				g.inbounds = append(g.inbounds, map[string]any{"id": 5, "enable": true, "tag": "regular-new"})
			case "removed":
				g.inbounds = g.inbounds[1:]
				g.inbounds = append(g.inbounds, map[string]any{"id": 5, "enable": true, "tag": "regular-new"})
			}
			if err = s.ApplyAccess(ctx, old); err != nil {
				t.Fatal(err)
			}
			failed, err := store.New(s.pool).AccessOperationByID(ctx, old)
			if err != nil || failed.Status != "needs_review" || !bytes.Equal(before.Target, failed.Target) {
				t.Fatal("unconfirmed target was rewritten", err)
			}
			g.failDetach = false
			fresh, err := s.PrepareGroupReconciliation(ctx, account)
			if err != nil || fresh == uuid.Nil || fresh == old {
				t.Fatal("fresh reconciliation did not recover", err)
			}
			if err = s.ApplyAccess(ctx, fresh); err != nil {
				t.Fatal(err)
			}
			after, err := store.New(s.pool).AccessOperationByID(ctx, old)
			if err != nil || after.Status != "skipped" || !bytes.Equal(before.Target, after.Target) {
				t.Fatal("stale target history lost", err)
			}
			applied, err := store.New(s.pool).AccessOperationByID(ctx, fresh)
			if err != nil || applied.Status != "applied" {
				t.Fatal("recovery not applied", err)
			}
			if g.client["limitIp"] != int64(1) || g.client["totalGB"] != int64(1234) {
				t.Fatal("recovery changed limits")
			}
		})
	}
}

func TestGroupReconciliationBanOnlyAndUnresolvedOwner(t *testing.T) {
	s, account, g := groupFixture(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET vpn_banned=true WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	g.inbounds = []map[string]any{{"id": 3, "enable": true, "tag": "foreign"}}
	id, err := s.PrepareGroupReconciliation(ctx, account)
	if err != nil || id == uuid.Nil {
		t.Fatal("ban-only prepare", err)
	}
	if err = s.ApplyAccess(ctx, id); err != nil {
		t.Fatal(err)
	}
	row, err := store.New(s.pool).AccessOperationByID(ctx, id)
	if err != nil || row.Status != "applied" || g.client["enable"] != false || !reflect.DeepEqual(g.ids, []int64{2, 3}) {
		t.Fatal("empty tags weakened ban or detached unknown", err)
	}
	actor := account
	if _, err = s.pool.Exec(ctx, `INSERT INTO access_operations(id,account_id,operator_account_id,kind,status,reason,desired,target,created_at,updated_at) VALUES($1,$2,$2,'reset_traffic','pending','owned unresolved','{}','{}',now(),now())`, uuid.New(), actor); err != nil {
		t.Fatal(err)
	}
	writes := len(g.writes)
	id, err = s.PrepareGroupReconciliation(ctx, account)
	if err != nil || id != uuid.Nil || len(g.writes) != writes {
		t.Fatal("unresolved reset owner bypassed", err)
	}
}

type groupPanel struct {
	mu                  sync.Mutex
	client              map[string]any
	ids                 []int64
	inbounds            []map[string]any
	writes              []string
	offline, failDetach bool
}

func (g *groupPanel) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.offline {
		w.WriteHeader(503)
		return
	}
	var obj any
	success := true
	switch {
	case strings.HasSuffix(r.URL.Path, "/inbounds/list"):
		obj = g.inbounds
	case r.Method == "GET" && strings.Contains(r.URL.Path, "/clients/"):
		if g.client == nil {
			json.NewEncoder(w).Encode(map[string]any{"success": false, "msg": "record not found", "obj": nil})
			return
		}
		obj = map[string]any{"client": g.client, "inboundIds": g.ids, "usedTraffic": int64(42)}
	case r.Method == "POST":
		var in struct {
			InboundIDs []int64 `json:"inboundIds"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(400)
			return
		}
		g.writes = append(g.writes, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/attach"):
			for _, id := range in.InboundIDs {
				found := false
				for _, have := range g.ids {
					found = found || have == id
				}
				if !found {
					g.ids = append(g.ids, id)
				}
			}
		case strings.HasSuffix(r.URL.Path, "/detach"):
			if g.failDetach {
				success = false
				break
			}
			ids := []int64{}
			for _, id := range g.ids {
				remove := false
				for _, unwanted := range in.InboundIDs {
					remove = remove || id == unwanted
				}
				if !remove {
					ids = append(ids, id)
				}
			}
			g.ids = ids
		case strings.HasSuffix(r.URL.Path, "/bulkDisable"):
			g.client["enable"] = false
		default:
			w.WriteHeader(400)
			return
		}
	default:
		w.WriteHeader(404)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": success, "obj": obj})
}

func groupFixture(t *testing.T) (*Service, uuid.UUID, *groupPanel) {
	t.Helper()
	e, s, cfg, _, _ := poolFixture(t)
	account := managedActor(t, s, "group-fixture@example.test")
	a, err := s.accounts.Lookup(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	g := &groupPanel{client: map[string]any{"id": 17, "uuid": a.VpnID.String(), "email": a.PanelKey, "subId": a.SubID, "enable": true, "expiryTime": int64(1800000000000), "limitIp": int64(1), "totalGB": int64(1234)}, ids: []int64{2, 3}, inbounds: []map[string]any{{"id": 1, "enable": true, "tag": "region-regular-tcp"}, {"id": 2, "enable": false, "tag": "euru"}, {"id": 3, "enable": true, "tag": "foreign"}, {"id": 4, "enable": true, "tag": "unlimited"}}}
	server := httptest.NewTLSServer(http.HandlerFunc(g.serve))
	t.Cleanup(server.Close)
	cfg.Panel.PanelURL = server.URL
	cfg.Panel.PanelRootCAs.AddCert(server.Certificate())
	if _, err = s.pool.Exec(context.Background(), `UPDATE accounts SET assigned_panel_id=$2,access_profile='regular' WHERE id=$1`, account, cfg.PanelID); err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &AccessWorker{Service: s})
	queue, err := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{Workers: workers, Queues: map[string]river.QueueConfig{"provision": {MaxWorkers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	s.queue = func() *river.Client[pgx.Tx] { return queue }
	return s, account, g
}

func TestGroupReconciliationOwnedNoDuplicateAndBan(t *testing.T) {
	s, account, g := groupFixture(t)
	ctx := context.Background()
	op, err := s.PrepareGroupReconciliation(ctx, account)
	if err != nil || op == uuid.Nil {
		t.Fatalf("membership drift must enqueue one operation: %v", err)
	}
	again, err := s.PrepareGroupReconciliation(ctx, account)
	if err != nil || again != op {
		t.Fatal("pending operation duplicated")
	}
	before, err := store.New(s.pool).AccessOperationByID(ctx, op)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyAccess(ctx, op); err != nil {
		t.Fatal(err)
	}
	after, err := store.New(s.pool).AccessOperationByID(ctx, op)
	if err != nil || after.Status != "applied" || !reflect.DeepEqual(before.Target, after.Target) {
		t.Fatalf("not applied with frozen target: %s %v", after.Status, err)
	}
	g.mu.Lock()
	ids := append([]int64{}, g.ids...)
	limit := g.client["limitIp"]
	expiry := g.client["expiryTime"]
	quota := g.client["totalGB"]
	writes := len(g.writes)
	g.mu.Unlock()
	if !reflect.DeepEqual(ids, []int64{3, 1}) || limit != int64(1) || expiry != int64(1800000000000) || quota != int64(1234) {
		t.Fatal("membership reconciliation altered limits or unknown inbound")
	}
	if again, err = s.PrepareGroupReconciliation(ctx, account); err != nil || again != uuid.Nil {
		t.Fatal("matched membership is not a no-op")
	}
	if err = s.ApplyAccess(ctx, op); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	if len(g.writes) != writes {
		t.Fatal("applied operation wrote again")
	}
	g.mu.Unlock()
	if _, err = s.pool.Exec(ctx, `UPDATE accounts SET vpn_banned=true WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	ban, err := s.PrepareGroupReconciliation(ctx, account)
	if err != nil || ban == uuid.Nil {
		t.Fatal("ban drift not queued", err)
	}
	if err = s.ApplyAccess(ctx, ban); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.client["enable"] != false || g.client["limitIp"] != int64(1) {
		t.Fatal("ban failed or altered limitIP=1")
	}
	for _, path := range g.writes {
		if strings.Contains(path, "reset") || strings.HasSuffix(path, "/add") || strings.Contains(path, "delete") || strings.HasSuffix(path, "/update") {
			t.Fatal("group operation crossed write boundary")
		}
	}
}
