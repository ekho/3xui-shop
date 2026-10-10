package tests

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

// The provider fixture is selected before the freshly built server starts.
// SERVER_MANAGEMENT_NATIVE_STATE is an isolated TLS 3X-UI fixture, never the shared native stack.
func serverManagementFixture(t *testing.T) (*fixture, func() int, func()) {
	t.Helper()
	f := openMode(t, true)
	if state := os.Getenv("SERVER_MANAGEMENT_NATIVE_STATE"); state != "" {
		var metadata struct{ Purpose string }
		runtime, err := os.ReadFile(filepath.Join(state, "runtime.json"))
		if err != nil || json.Unmarshal(runtime, &metadata) != nil || metadata.Purpose != "server-management-acceptance" {
			t.Fatal("server-management provider must be the owned isolated fixture")
		}
		cert, err := os.ReadFile(filepath.Join(state, "cert.pem"))
		if err != nil {
			t.Fatal("owned panel certificate unavailable")
		}
		password, err := os.ReadFile(filepath.Join(state, "panel-password"))
		if err != nil {
			t.Fatal("owned panel password unavailable")
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(cert) {
			t.Fatal("owned panel certificate invalid")
		}
		f.cfg.VPN.Panel.PanelURL = "https://localhost:61444"
		f.cfg.VPN.Panel.PanelToken = ""
		f.cfg.VPN.Panel.PanelUsername = "local-operator"
		f.cfg.VPN.Panel.PanelPassword = strings.TrimSpace(string(password))
		f.cfg.VPN.Panel.PanelRootCAs = roots
		f.cfg.Subscriptions.SubscriptionBaseURL = "https://localhost:61444/sub/"
	}
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
	}))
	return f, start, stop
}

func serverManagementOperator(t *testing.T, f *fixture) (*http.Client, string, uuid.UUID) {
	t.Helper()
	client, csrf, actor := f.signupAccount(t, nativeEmail("server-management-infrastructure"))
	ctx := context.Background()
	if err := f.svc.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal("owned operator role unavailable", err)
	}
	if err := f.svc.Accounts.ChangeInfrastructureRole(ctx, actor, true); err != nil {
		t.Fatal("owned infrastructure role unavailable", err)
	}
	return client, csrf, actor
}

func serverManagementDetail(t *testing.T, raw []byte) wire.OperatorServerDetail {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal("managed server response is not an object", err)
	}
	for _, key := range []string{"subscription_base_url", "panel_key", "vpn_id", "password", "token"} {
		if _, exposed := fields[key]; exposed {
			t.Fatalf("managed server response exposed %s", key)
		}
	}
	var out wire.OperatorServerDetail
	if err := json.Unmarshal(raw, &out); err != nil || out.Id == "" || out.Name == "" || out.Host == "" {
		t.Fatal("managed server response lacks observable identity", err)
	}
	return out
}

func TestNativeServerManagementPrimaryRetirement(t *testing.T) {
	if os.Getenv("SERVER_MANAGEMENT_NATIVE_STATE") == "" {
		t.Skip("first-operation retirement requires the isolated TLS 3X-UI provider")
	}
	f, start, stop := serverManagementFixture(t)
	firstPID := start()
	client, csrf, actor := serverManagementOperator(t, f)
	ctx := context.Background()
	primary := f.cfg.Subscriptions.PanelID
	path := "/api/v1/operator/servers"
	card := path + "/" + url.PathEscape(primary)
	var rows int
	if err := f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM vpn_servers WHERE id=$1", primary).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("fresh primary was already persisted: rows=%d", rows)
	}
	status, raw, _ := f.send(t, client, "GET", path, nil, "", "", false)
	var before wire.OperatorServers
	if status != 200 || json.Unmarshal(raw, &before) != nil || len(before.Servers) != 1 || before.Servers[0].Id != primary {
		t.Fatalf("fresh primary list status=%d, servers=%d", status, len(before.Servers))
	}
	key := uuid.NewString()
	status, raw, _ = f.send(t, client, "POST", card+"/delete", map[string]bool{"confirmation": true}, csrf, key, false)
	if status != 200 {
		t.Fatalf("first server mutation primary delete status=%d, want 200", status)
	}
	retired := serverManagementDetail(t, raw)
	if retired.Id != primary || !retired.Retired || retired.Online {
		t.Fatal("first-operation delete did not return an offline tombstone")
	}
	if err := f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM vpn_servers WHERE id=$1 AND retired AND NOT online AND revision>0", primary).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("first-operation delete did not persist exactly one tombstone: rows=%d", rows)
	}
	checkAbsent := func() {
		t.Helper()
		status, body, _ := f.send(t, client, "GET", path, nil, "", "", false)
		var list wire.OperatorServers
		if status != 200 || json.Unmarshal(body, &list) != nil {
			t.Fatalf("retired server list status=%d", status)
		}
		for _, server := range list.Servers {
			if server.Id == primary {
				t.Fatal("retired configured primary returned to the list")
			}
		}
	}
	checkAbsent()
	if status, _, _ := f.send(t, client, "POST", path+"/sync", map[string]any{}, csrf, uuid.NewString(), false); status != 200 {
		t.Fatalf("sync after primary tombstone status=%d", status)
	}
	checkAbsent()
	stop()
	if next := start(); next <= 0 || next == firstPID {
		t.Fatal("fresh compiled server did not restart")
	}
	checkAbsent()
	status, raw, _ = f.send(t, client, "POST", card+"/delete", map[string]bool{"confirmation": true}, csrf, key, false)
	if status != 200 || !reflect.DeepEqual(serverManagementDetail(t, raw), retired) {
		t.Fatalf("repeat after restart changed first-operation delete result: status=%d", status)
	}
	var actions, audits int
	if err := f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM vpn_server_actions WHERE actor_id=$1 AND idempotency_key=$2 AND action='delete'", actor, key).Scan(&actions); err != nil || actions != 1 {
		t.Fatalf("delete replay action count=%d", actions)
	}
	if err := f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='server.delete' AND operator_account_id=$1", actor).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("delete replay audit count=%d", audits)
	}
	if err := f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM vpn_servers WHERE id=$1 AND retired AND NOT online", primary).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("restart or replay revived primary: rows=%d", rows)
	}
}

func TestNativeServerManagement(t *testing.T) {
	f, start, stop := serverManagementFixture(t)
	firstPID := start()
	ctx := context.Background()
	client, csrf, actor := serverManagementOperator(t, f)
	ordinary, _, ordinaryID := f.signupAccount(t, nativeEmail("server-management-ordinary"))
	if err := f.svc.Accounts.ChangeOperatorRole(ctx, ordinaryID, true); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/operator/servers"
	if status, _, _ := f.send(t, ordinary, "GET", path, nil, "", "", false); status != 403 {
		t.Fatalf("ordinary operator read status=%d, want 403", status)
	}
	status, raw, response := f.send(t, client, "GET", path, nil, "", "", false)
	var listed wire.OperatorServers
	if status != 200 || json.Unmarshal(raw, &listed) != nil || response.Header.Get("Cache-Control") != "no-store" || len(listed.Servers) == 0 {
		t.Fatalf("infrastructure list status=%d, servers=%d", status, len(listed.Servers))
	}
	primary := f.cfg.Subscriptions.PanelID
	found := false
	for _, server := range listed.Servers {
		if server.Id == primary && server.Host == f.cfg.VPN.Panel.PanelURL && !server.Retired {
			found = true
		}
	}
	if !found {
		t.Fatal("configured primary missing from compiled HTTP list")
	}
	if status, _, _ := f.send(t, ordinary, "POST", path, map[string]any{"name": "Denied", "host": "https://localhost:1", "max_clients": 0}, csrf, uuid.NewString(), false); status != 403 {
		t.Fatalf("ordinary operator create status=%d, want 403", status)
	}
	name := "Owned server management " + uuid.NewString()
	host := "https://localhost:1"
	if os.Getenv("SERVER_MANAGEMENT_NATIVE_STATE") != "" {
		host = "https://localhost:61449"
	}
	create := map[string]any{"name": name, "host": host, "max_clients": 0}
	key := uuid.NewString()
	status, raw, _ = f.send(t, client, "POST", path, create, csrf, key, false)
	if status != 201 {
		t.Fatalf("compiled create status=%d, want 201: %s", status, raw)
	}
	created := serverManagementDetail(t, raw)
	if created.Name != name || created.Host != host || created.MaxClients == nil || *created.MaxClients != 0 || created.Retired {
		t.Fatal("created server response changed accepted input")
	}
	status, raw, _ = f.send(t, client, "POST", path, create, csrf, key, false)
	if status != 200 || serverManagementDetail(t, raw).Id != created.Id {
		t.Fatalf("same create replay status=%d did not retain ID", status)
	}
	create["name"] = "Changed " + uuid.NewString()
	if status, _, _ = f.send(t, client, "POST", path, create, csrf, key, false); status != 409 {
		t.Fatalf("changed input under same key status=%d, want 409", status)
	}
	var registry, actions int
	if err := f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM vpn_servers WHERE id=$1", created.Id).Scan(&registry); err != nil || registry != 1 {
		t.Fatal("create replay duplicated or lost registry fact", err)
	}
	if err := f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM vpn_server_actions WHERE actor_id=$1 AND idempotency_key=$2 AND action='create'", actor, key).Scan(&actions); err != nil || actions != 1 {
		t.Fatal("create replay duplicated or lost action fact", err)
	}
	var auditCount int
	var auditReason string
	if err := f.env.Pool.QueryRow(ctx, "SELECT count(*),COALESCE(min(reason),'') FROM audit_events WHERE action='server.create' AND operator_account_id=$1", actor).Scan(&auditCount, &auditReason); err != nil || auditCount != 1 || strings.Contains(auditReason, host) || strings.Contains(auditReason, name) {
		t.Fatal("create replay duplicated audit or exposed server address/name", err)
	}
	createdPath := path + "/" + url.PathEscape(created.Id)
	if status, raw, _ = f.send(t, client, "GET", createdPath, nil, "", "", false); status != 200 || serverManagementDetail(t, raw).Id != created.Id {
		t.Fatalf("compiled card status=%d", status)
	}
	if status, _, _ = f.send(t, client, "POST", createdPath+"/delete", map[string]bool{"confirmation": false}, csrf, uuid.NewString(), false); status != 400 {
		t.Fatalf("unconfirmed delete status=%d, want 400", status)
	}
	if _, err := f.env.Pool.Exec(ctx, "UPDATE accounts SET assigned_panel_id=$1 WHERE id=$2", created.Id, ordinaryID); err != nil {
		t.Fatal("owned assigned-client fixture unavailable", err)
	}
	if status, raw, _ = f.send(t, client, "POST", createdPath+"/delete", map[string]bool{"confirmation": true}, csrf, uuid.NewString(), false); status != 409 || !strings.Contains(string(raw), "SERVER_IN_USE") {
		t.Fatalf("assigned server deletion status=%d, want SERVER_IN_USE", status)
	}
	if status, raw, _ = f.send(t, client, "GET", createdPath, nil, "", "", false); status != 200 || serverManagementDetail(t, raw).AssignedClients != 1 {
		t.Fatal("refused deletion changed assigned client or hid assigned count")
	}
	if _, err := f.env.Pool.Exec(ctx, "UPDATE accounts SET assigned_panel_id=NULL WHERE id=$1", ordinaryID); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("SERVER_MANAGEMENT_NATIVE_STATE") == "" {
		if status, _, _ = f.send(t, client, "POST", createdPath+"/delete", map[string]bool{"confirmation": true}, csrf, uuid.NewString(), false); status != 503 {
			t.Fatalf("unreachable provider deletion status=%d, want 503", status)
		}
		if status, raw, _ = f.send(t, client, "GET", createdPath, nil, "", "", false); status != 200 || serverManagementDetail(t, raw).Retired {
			t.Fatal("unreachable panel was mistaken for an empty server")
		}
	} else {
		status, raw, _ = f.send(t, client, "POST", createdPath+"/ping", map[string]any{}, csrf, uuid.NewString(), false)
		if status != 200 || !serverManagementDetail(t, raw).Online {
			t.Fatalf("added actual 3X-UI panel failed ping status=%d: %s", status, raw)
		}
	}
	primaryPath := path + "/" + url.PathEscape(primary)
	status, raw, _ = f.send(t, client, "POST", primaryPath+"/ping", map[string]any{}, csrf, uuid.NewString(), false)
	if status != 200 {
		t.Fatalf("compiled primary ping status=%d: %s", status, raw)
	}
	pinged := serverManagementDetail(t, raw)
	if pinged.Id != primary || pinged.ObservedAt == nil {
		t.Fatal("ping did not return an ordered observation")
	}
	if os.Getenv("SERVER_MANAGEMENT_NATIVE_STATE") != "" && !pinged.Online {
		t.Fatal("actual owned 3X-UI panel ping is offline")
	}
	if status, raw, _ = f.send(t, client, "POST", path+"/sync", map[string]any{}, csrf, uuid.NewString(), false); status != 200 {
		t.Fatalf("compiled pool sync status=%d: %s", status, raw)
	}
	snapshot := func() [32]byte {
		t.Helper()
		var facts string
		err := f.env.Pool.QueryRow(ctx, `SELECT jsonb_build_array(
		 (SELECT jsonb_agg(jsonb_build_array(id,vpn_id,sub_id,panel_key,assigned_panel_id) ORDER BY id) FROM accounts),
		 (SELECT jsonb_agg(jsonb_build_array(id,name,host,max_clients,retired) ORDER BY id) FROM vpn_servers),
		 (SELECT jsonb_agg(to_jsonb(x) ORDER BY actor_id,idempotency_key) FROM vpn_server_actions x),
		 (SELECT jsonb_agg(jsonb_build_array(id,action,reason) ORDER BY id) FROM audit_events WHERE action LIKE 'server.%'),
		 (SELECT jsonb_agg(to_jsonb(x) ORDER BY account_id) FROM vpn_server_reservations x),
		 (SELECT jsonb_agg(to_jsonb(x) ORDER BY account_id) FROM trial_grants x),
		 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM trial_operations x),
		 (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM access_operations x),
		 (SELECT jsonb_agg(to_jsonb(x) ORDER BY account_id) FROM infrastructure_operators x))::text`).Scan(&facts)
		if err != nil {
			t.Fatal("owned restart facts unavailable", err)
		}
		return sha256.Sum256([]byte(facts))
	}
	beforeRestart := snapshot()
	stop()
	if pid := start(); pid <= 0 || pid == firstPID {
		t.Fatal("freshly compiled server process did not restart")
	}
	if snapshot() != beforeRestart {
		t.Fatal("restart changed client identities, targets, grants, reservations, roles or action facts")
	}
	if status, raw, _ = f.send(t, client, "GET", createdPath, nil, "", "", false); status != 200 || serverManagementDetail(t, raw).Id != created.Id {
		t.Fatal("restart lost created server")
	}
	if err := f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM vpn_server_actions WHERE actor_id=$1 AND idempotency_key=$2", actor, key).Scan(&actions); err != nil || actions != 1 {
		t.Fatal("restart lost idempotency action", err)
	}
	if os.Getenv("SERVER_MANAGEMENT_NATIVE_STATE") != "" {
		status, raw, _ = f.send(t, client, "POST", createdPath+"/delete", map[string]bool{"confirmation": true}, csrf, uuid.NewString(), false)
		if status != 200 || !serverManagementDetail(t, raw).Retired {
			t.Fatalf("empty actual 3X-UI retirement status=%d: %s", status, raw)
		}
		if status, _, _ = f.send(t, client, "POST", path+"/sync", map[string]any{}, csrf, uuid.NewString(), false); status != 200 {
			t.Fatalf("sync after actual retirement status=%d", status)
		}
		if err := f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM vpn_servers WHERE id=$1 AND retired AND NOT online", created.Id).Scan(&registry); err != nil || registry != 1 {
			t.Fatal("sync resurrected actual retired server", err)
		}
		if status, _, _ = f.send(t, client, "GET", createdPath, nil, "", "", false); status != 404 {
			t.Fatalf("retired card status=%d, want 404", status)
		}
	}
	t.Log("PASS: compiled HTTP role, create replay/conflict, assigned deletion guard, ping/sync, restart; actual 3X-UI add/ping/delete conditional on SERVER_MANAGEMENT_NATIVE_STATE")
}

func TestServerManagementBrowser(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TESTS") != "1" {
		t.Skip("requires RUN_BROWSER_TESTS=1")
	}
	if os.Getenv("SERVER_MANAGEMENT_NATIVE_STATE") == "" {
		t.Skip("real browser deletion requires the isolated TLS 3X-UI provider")
	}
	f, start, _ := serverManagementFixture(t)
	start()
	operator, csrf, _ := serverManagementOperator(t, f)
	primaryID := f.cfg.Subscriptions.PanelID
	var primaryRowsBefore int
	if err := f.env.Pool.QueryRow(context.Background(), "SELECT count(*) FROM vpn_servers WHERE id=$1", primaryID).Scan(&primaryRowsBefore); err != nil {
		t.Fatal("owned primary registry preflight unavailable")
	}
	primaryStatus, primaryRaw, _ := f.send(t, operator, "POST", "/api/v1/operator/servers/"+url.PathEscape(primaryID)+"/ping", map[string]any{}, csrf, uuid.NewString(), false)
	primaryOnline := false
	if primaryStatus == 200 {
		primary := serverManagementDetail(t, primaryRaw)
		primaryOnline = primary.Id == primaryID && primary.Online
	}
	if !primaryOnline {
		panel := vpn.NewPanelClient(f.cfg.VPN.Panel)
		inbounds, providerErr := panel.RegularInboundIDs(context.Background())
		panel.Close()
		var primaryRowsAfter int
		var revision int64
		var recordedOnline, observed bool
		if err := f.env.Pool.QueryRow(context.Background(), "SELECT count(*),COALESCE(max(revision),0),COALESCE(bool_or(online),false),COALESCE(bool_or(observed_at IS NOT NULL),false) FROM vpn_servers WHERE id=$1", primaryID).Scan(&primaryRowsAfter, &revision, &recordedOnline, &observed); err != nil {
			t.Fatal("owned primary registry diagnostic unavailable")
		}
		t.Fatalf("compiled primary ping: status=%d online=%t; parent provider read=%t panel_error=%t inbounds=%d; registry rows before=%d after=%d revision=%d online=%t observed=%t", primaryStatus, primaryOnline, providerErr == nil, errors.Is(providerErr, vpn.ErrPanel), len(inbounds), primaryRowsBefore, primaryRowsAfter, revision, recordedOnline, observed)
	}
	targetName := "Browser managed panel " + uuid.NewString()
	createStatus, createRaw, _ := f.send(t, operator, "POST", "/api/v1/operator/servers", map[string]any{"name": targetName, "host": "https://localhost:61449", "max_clients": 0}, csrf, uuid.NewString(), false)
	if createStatus != 201 {
		t.Fatalf("owned browser panel creation status=%d: %s", createStatus, createRaw)
	}
	target := serverManagementDetail(t, createRaw)
	probeStatus, probeRaw, _ := f.send(t, operator, "POST", "/api/v1/operator/servers/"+url.PathEscape(target.Id)+"/ping", map[string]any{}, csrf, uuid.NewString(), false)
	probe := serverManagementDetail(t, probeRaw)
	if probeStatus != 200 || !probe.Online || !probe.CanDelete {
		t.Fatalf("owned browser panel prerequisite status=%d online=%t can_delete=%t", probeStatus, probe.Online, probe.CanDelete)
	}
	_, _, assigned := f.signupAccount(t, nativeEmail("server-management-browser-assigned"))
	dir := t.TempDir()
	ready, resume := filepath.Join(dir, "ready"), filepath.Join(dir, "resume")
	u, _ := url.Parse(f.public.URL)
	var cookie *http.Cookie
	for _, item := range operator.Jar.Cookies(u) {
		if item.Name == "__Host-session" {
			cookie = item
		}
	}
	if cookie == nil {
		t.Fatal("owned browser session unavailable")
	}
	input, err := json.Marshal(map[string]any{"origin": f.public.URL, "target": target.Id, "name": targetName, "ready": ready, "resume": resume, "cookie": map[string]any{"name": cookie.Name, "value": cookie.Value, "url": f.public.URL, "httpOnly": true, "secure": true, "sameSite": "Lax"}})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "server-management-browser.json")
	if err := os.WriteFile(file, input, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "scripts/server-management-acceptance.mjs")
	command.Dir = filepath.Join(f.root, "web")
	command.Env = append(os.Environ(), "SERVER_MANAGEMENT_BROWSER_INPUT="+file)
	var output strings.Builder
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal("owned browser failed to start", err)
	}
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("owned browser stopped before confirmation: %v: %s", err, output.String())
		case <-deadline.C:
			cancel()
			<-finished
			t.Fatalf("owned browser did not reach confirmation: %s", output.String())
		case <-time.After(25 * time.Millisecond):
		}
	}
	if _, err := f.env.Pool.Exec(context.Background(), "UPDATE accounts SET assigned_panel_id=$1 WHERE id=$2", target.Id, assigned); err != nil {
		t.Fatal("owned concurrent assignment failed", err)
	}
	if err := os.WriteFile(resume, []byte("assigned"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatalf("compiled HTTP browser failed: %s", output.String())
	}
}
