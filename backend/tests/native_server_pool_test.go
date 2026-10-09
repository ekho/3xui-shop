package tests

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func TestNativeTrialServerPool(t *testing.T) {
	f := openMode(t, true)
	ctx := context.Background()
	secondURL, secondBase := "https://localhost:59449", "https://localhost:59449/sub/"
	var offline atomic.Bool
	var secondFake *panel
	if os.Getenv("NATIVE_DOCKER_STATE") == "" {
		secondFake = &panel{clients: map[string]map[string]any{}}
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if offline.Load() {
				w.WriteHeader(503)
				return
			}
			if r.Method == "POST" && r.URL.Path == "/panel/api/setting/all" {
				json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": map[string]any{"subEnable": true, "subURI": "https://second.example.test/sub/"}})
				return
			}
			secondFake.serve(w, r)
		}))
		t.Cleanup(server.Close)
		f.cfg.VPN.Panel.PanelRootCAs.AddCert(server.Certificate())
		secondURL, secondBase = server.URL, "https://second.example.test/sub/"
	}
	register := func(id, host string, capacity int64) {
		t.Helper()
		tx, err := f.env.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = f.svc.VPN.RegisterServerTx(ctx, tx, vpn.ServerInput{ID: id, Name: id, Host: host, MaxClients: capacity}); err != nil || tx.Commit(ctx) != nil {
			t.Fatal("owned native server registration failed", err)
		}
	}
	if err := f.svc.VPN.SyncServers(ctx); err != nil {
		t.Fatal(err)
	}
	register(f.cfg.Subscriptions.PanelID, f.cfg.VPN.Panel.PanelURL, 0)
	register("second", secondURL, 10)
	bot := &nativeBot{}
	start, stop := nativeNoticeBinary(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response, err := bot.RoundTrip(r)
		if err != nil {
			http.Error(w, "owned Bot API unavailable", 503)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	}), map[string]string{"TRIAL_ENABLED": "true"})
	pid := start()
	issue := func(prefix string) (*http.Client, uuid.UUID, uuid.UUID) {
		t.Helper()
		client, _, request := f.signup(t, nativeEmail(prefix))
		card := cardFor(t, bot, 101, request.RequestId)
		bot.callback(101, card.ID, "a", request.RequestId, "")
		wait(t, func() bool { return trialStatus(f, request.RequestId) == "approved" })
		op := operationFor(t, f, request.RequestId)
		wait(t, func() bool { return applied(f, op) })
		var account uuid.UUID
		if f.env.Pool.QueryRow(ctx, "SELECT account_id FROM trial_operations WHERE id=$1", op).Scan(&account) != nil {
			t.Fatal("owned native grant missing")
		}
		return client, account, op
	}
	firstClient, first, firstOp := issue("pool-second")
	register(f.cfg.Subscriptions.PanelID, f.cfg.VPN.Panel.PanelURL, 10)
	register("second", secondURL, 0)
	_, second, secondOp := issue("pool-primary")
	identity, err := f.svc.Accounts.Lookup(ctx, first)
	other, otherErr := f.svc.Accounts.Lookup(ctx, second)
	if err != nil || otherErr != nil || identity.AssignedPanelID == nil || *identity.AssignedPanelID != "second" || other.AssignedPanelID == nil || *other.AssignedPanelID != f.cfg.Subscriptions.PanelID {
		t.Fatal("compiled worker lost the two concrete assignments")
	}
	key := func() wire.SubscriptionKey {
		t.Helper()
		status, raw, response := f.send(t, firstClient, "GET", "/api/v1/subscription/key", nil, "", "", false)
		var out wire.SubscriptionKey
		if status != 200 || json.Unmarshal(raw, &out) != nil || response.Header.Get("Cache-Control") != "no-store" || out.SubscriptionUrl != secondBase+identity.SubID {
			t.Fatal("compiled key changed assigned base or identity", status)
		}
		return out
	}
	beforeKey := key()
	snapshot := func() [32]byte {
		t.Helper()
		var raw string
		if f.env.Pool.QueryRow(ctx, `SELECT jsonb_build_array(
		 (SELECT jsonb_agg(jsonb_build_array(id,vpn_id,sub_id,panel_key,assigned_panel_id) ORDER BY id) FROM accounts),
		 (SELECT jsonb_agg(jsonb_build_array(id,account_id,panel_id,target,status) ORDER BY id) FROM trial_operations),
		 (SELECT jsonb_agg(to_jsonb(g) ORDER BY account_id) FROM trial_grants g),
		 (SELECT count(*) FROM vpn_server_reservations),(SELECT count(*) FROM purchase_receipts))::text`).Scan(&raw) != nil {
			t.Fatal("native pool snapshot unavailable")
		}
		return sha256.Sum256([]byte(raw))
	}
	before := snapshot()
	stop()
	if next := start(); next == pid || next <= 0 {
		t.Fatal("fresh compiled process did not restart")
	}
	if key().SubscriptionUrl != beforeKey.SubscriptionUrl || snapshot() != before || !applied(f, firstOp) || !applied(f, secondOp) {
		t.Fatal("restart changed pool operation, grant or keys")
	}
	readback, err := f.svc.VPN.PanelFor(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	view, err := readback.GetClient(ctx, identity.PanelKey)
	readback.Close()
	if err != nil || view == nil || view.VPNID != identity.VpnID || view.SubID != identity.SubID {
		t.Fatal("actual second panel changed native identity")
	}
	actorClient, csrf, actor := f.signupAccount(t, nativeEmail("pool-operator"))
	if f.svc.Accounts.ChangeOperatorRole(ctx, actor, true) != nil {
		t.Fatal("owned operator role unavailable")
	}
	status, raw, _ := f.send(t, actorClient, "POST", "/api/v1/operator/reports/statistics", wire.StatisticsInput{}, csrf, "", false)
	var report auditreports.StatisticsReport
	if status != 200 || json.Unmarshal(raw, &report) != nil || len(report.Servers) != 2 {
		t.Fatal("compiled report lost a registered server", status)
	}
	if os.Getenv("NATIVE_DOCKER_STATE") != "" {
		if report.Activity.ActiveUsers == nil || *report.Activity.ActiveUsers != 2 || report.Activity.UnknownUsers != 0 {
			t.Fatal("actual pool bulk reads failed to confirm its own accounts")
		}
		for _, server := range report.Servers {
			if server.Availability != "available" || server.Clients == nil || *server.Clients < 1 {
				t.Fatal("actual pool report failed to retain a provider count")
			}
		}
	} else if report.Activity.ActiveUsers != nil || report.Activity.UnknownUsers != 2 {
		t.Fatal("unsupported fixture bulk read fabricated activity")
	}
	beforeOutage := snapshot()
	if os.Getenv("NATIVE_DOCKER_STATE") != "" {
		state := filepath.Clean(os.Getenv("NATIVE_DOCKER_STATE"))
		if state != filepath.Join(f.root, ".superpowers", "acceptance", "native-docker") {
			t.Fatal("unexpected Docker fixture owner")
		}
		data, err := os.ReadFile(filepath.Join(state, "runtime.json"))
		var metadata struct{ Project string }
		if err != nil || json.Unmarshal(data, &metadata) != nil || !regexp.MustCompile(`^cabinet-native(?:-[a-z0-9-]+)?$`).MatchString(metadata.Project) {
			t.Fatal("owned native Docker project unavailable")
		}
		compose := func(action string) {
			t.Helper()
			command := exec.Command("docker", "compose", "--project-name", metadata.Project, "--env-file", filepath.Join(state, "public.env"), "-f", "deploy/acceptance/compose.acceptance.yml", "-f", "deploy/acceptance/compose.local.yml", "-f", "deploy/acceptance/compose.native.yml", action, "panel-second")
			command.Dir = f.root
			if command.Run() != nil {
				t.Fatal("owned second-panel transition failed")
			}
		}
		t.Cleanup(func() { compose("start") })
		compose("stop")
	} else {
		offline.Store(true)
	}
	if status, _, _ = f.send(t, firstClient, "GET", "/api/v1/subscription/key", nil, "", "", false); status != 409 {
		t.Fatal("assigned outage exposed a different subscription", status)
	}
	if status, _, _ = f.send(t, firstClient, "GET", "/healthz", nil, "", "", false); status != 200 {
		t.Fatal("one panel failure stopped the monolith")
	}
	primary, err := f.svc.VPN.PanelFor(ctx, f.cfg.Subscriptions.PanelID)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := primary.GetClient(ctx, identity.PanelKey)
	primary.Close()
	if err != nil || foreign != nil || !strings.HasPrefix(beforeKey.SubscriptionUrl, secondBase) {
		t.Fatal("assigned outage moved a client to primary")
	}
	if snapshot() != beforeOutage {
		t.Fatal("assigned outage changed stored grant/target/identity")
	}
	t.Log("PASS: fresh compiled pool, concrete assignments/base discovery, restart/grant/key preservation and assigned outage without remap; Bot API simulated")
}
