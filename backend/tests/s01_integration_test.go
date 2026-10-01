// These fixtures are test binaries only. They expose no public verification shortcut.
package tests

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/s01"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oapi-codegen/runtime/types"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type panel struct {
	mu               sync.Mutex
	clients          map[string]map[string]any
	adds, forbidden  int
	loseReply        bool
	blocked, release chan struct{}
}

func (p *panel) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	reply := func(ok bool, obj any) { json.NewEncoder(w).Encode(map[string]any{"success": ok, "obj": obj}) }
	switch {
	case r.Method == "GET" && r.URL.Path == "/panel/api/inbounds/list":
		reply(true, []map[string]any{{"id": 1, "enable": true, "tag": "regular-tcp"}, {"id": 91, "enable": true, "tag": "unknown"}})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/panel/api/clients/get/"):
		key := strings.TrimPrefix(r.URL.Path, "/panel/api/clients/get/")
		c := p.clients[key]
		if c == nil {
			json.NewEncoder(w).Encode(map[string]any{"success": false, "msg": "record not found", "obj": nil})
			return
		}
		if p.blocked != nil {
			close(p.blocked)
			p.blocked = nil
			select {
			case <-r.Context().Done():
				return
			case <-p.release:
			}
		}
		reply(true, map[string]any{"client": c, "inboundIds": []int{1}, "usedTraffic": 0})
	case r.Method == "POST" && r.URL.Path == "/panel/api/clients/add":
		var b struct {
			Client     map[string]any `json:"client"`
			InboundIDs []int          `json:"inboundIds"`
		}
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		if d.Decode(&b) != nil {
			w.WriteHeader(400)
			return
		}
		key, _ := b.Client["email"].(string)
		if !strings.HasPrefix(key, "acct_") || len(b.InboundIDs) != 1 || b.InboundIDs[0] != 1 || p.clients[key] != nil {
			p.forbidden++
			w.WriteHeader(409)
			return
		}
		p.adds++
		p.clients[key] = b.Client
		if p.loseReply {
			p.loseReply = false
			c, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				c.Close()
			}
			return
		}
		reply(true, nil)
	default:
		p.forbidden++
		w.WriteHeader(405)
	}
}

type fixture struct {
	env                 *testkit.Env
	svc                 *s01.Service
	cfg                 s01.Config
	public, internal    *httptest.Server
	mail                *testkit.SMTP
	panel               *panel
	workers             *river.Client[pgx.Tx]
	root, ca, tokenFile string
}

func open(t *testing.T) *fixture {
	t.Helper()
	e := testkit.Open(t)
	f := &fixture{env: e, mail: testkit.MailServer(t), panel: &panel{clients: map[string]map[string]any{}, loseReply: true}}
	f.root, _ = filepath.Abs("../..")
	ps := httptest.NewTLSServer(http.HandlerFunc(f.panel.serve))
	t.Cleanup(ps.Close)
	queue, err := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	f.cfg = s01.Config{TermsVersion: "1", PrivacyVersion: "1", MailKey: bytes.Repeat([]byte{3}, 32), CodeKey: bytes.Repeat([]byte{4}, 32), RateNamespace: uuid.NewString(), Operators: []int64{101}, AdapterToken: strings.Repeat("f", 43), PanelID: "dedicated-test", TrialEnabled: true, TrialPeriodDays: 3, TrialTrafficGB: 15, TrialDevices: 1, PanelURL: ps.URL, PanelToken: "fixture-panel", SubscriptionBaseURL: "https://subscriptions.example.test/sub/", SMTPAddress: f.mail.Address, SMTPRootCAs: f.mail.Roots, SMTPFrom: "sender@example.test"}
	f.cfg.PanelRootCAs = x509.NewCertPool()
	f.cfg.PanelRootCAs.AddCert(ps.Certificate())
	var handler http.Handler
	f.public = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			handler.ServeHTTP(w, r)
			return
		}
		path := filepath.Join(f.root, "web", "dist", filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			path = filepath.Join(f.root, "web", "dist", "index.html")
		}
		http.ServeFile(w, r, path)
	}))
	t.Cleanup(f.public.Close)
	f.cfg.CabinetOrigin = f.public.URL
	f.svc = s01.NewService(e.Pool, e.Redis, queue, f.cfg)
	handler = httpapi.New(f.svc, f.cfg)
	f.internal = httptest.NewTLSServer(handler)
	t.Cleanup(f.internal.Close)
	dir := t.TempDir()
	f.ca = filepath.Join(dir, "ca.pem")
	f.tokenFile = filepath.Join(dir, "adapter-token")
	os.WriteFile(f.ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.internal.Certificate().Raw}), 0600)
	os.WriteFile(f.tokenFile, []byte(f.cfg.AdapterToken), 0600)
	workers := river.NewWorkers()
	river.AddWorker(workers, &s01.MailWorker{Service: f.svc})
	river.AddWorker(workers, &s01.ProvisionWorker{Service: f.svc})
	f.workers, err = river.NewClient(riverpgxv5.New(e.Pool), &river.Config{Workers: workers, Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}, "provision": {MaxWorkers: 2}}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		f.workers.Stop(ctx)
	})
	return f
}
func (f *fixture) send(t *testing.T, c *http.Client, method, path string, body any, csrf, key string, internal bool) (int, []byte, *http.Response) {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	base := f.public.URL
	if internal {
		base = f.internal.URL
	}
	req, err := http.NewRequest(method, base+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if internal {
		req.Header.Set("Authorization", "Bearer "+f.cfg.AdapterToken)
	} else if method == "POST" {
		req.Header.Set("Origin", f.cfg.CabinetOrigin)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal("fixture HTTP transport failed")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b, resp
}
func wait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("controlled fixture condition timed out")
}
func (f *fixture) signup(t *testing.T, email string) (*http.Client, string, wire.TrialRequest) {
	t.Helper()
	client := *f.public.Client()
	c := &client
	jar, _ := cookiejar.New(nil)
	c.Jar = jar
	c.Timeout = 15 * time.Second
	status, b, _ := f.send(t, c, "POST", "/api/v1/auth/register", wire.RegisterInput{Email: types.Email(email), Locale: "en", AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"}, "", "", false)
	if status != 202 {
		t.Fatal("register", status)
	}
	var out wire.RegistrationAccepted
	json.Unmarshal(b, &out)
	var delivery uuid.UUID
	f.env.Pool.QueryRow(context.Background(), `SELECT id FROM mail_deliveries WHERE challenge_id=$1`, out.ChallengeId).Scan(&delivery)
	if f.svc.SendMail(context.Background(), delivery) != nil {
		t.Fatal("TLS SMTP send")
	}
	var token string
	for _, letter := range f.mail.Letters() {
		if strings.Contains(letter, "To: "+email) {
			match := regexp.MustCompile(`#token=([A-Za-z0-9_-]{43})`).FindStringSubmatch(letter)
			if len(match) == 2 {
				token = match[1]
			}
		}
	}
	if token == "" {
		t.Fatal("no verification token delivered over SMTP")
	}
	status, _, _ = f.send(t, c, "POST", "/api/v1/auth/verify-email", map[string]string{"token": token, "new_password": "fixture password with Unicode ✨"}, "", "", false)
	if status != 200 {
		t.Fatal("verify", status)
	}
	status, b, _ = f.send(t, c, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "fixture password with Unicode ✨"}, "", "", false)
	if status != 200 {
		t.Fatal("login", status)
	}
	var login wire.LoginResult
	json.Unmarshal(b, &login)
	status, b, _ = f.send(t, c, "POST", "/api/v1/trial-requests", map[string]string{"comment": "integration"}, login.CsrfToken, uuid.NewString(), false)
	if status != 201 {
		t.Fatal("request", status)
	}
	var trial wire.TrialRequest
	json.Unmarshal(b, &trial)
	return c, login.CsrfToken, trial
}
func (f *fixture) python(t *testing.T, requestID uuid.UUID) {
	t.Helper()
	cmd := exec.Command("poetry", "run", "python", "-m", "unittest", "tests.test_web_trial_contract", "-v")
	cmd.Dir = f.root
	cmd.Env = append(os.Environ(), "S01_CONTRACT_URL="+f.internal.URL, "S01_CONTRACT_TOKEN_FILE="+f.tokenFile, "S01_CONTRACT_CA_FILE="+f.ca, "S01_CONTRACT_REQUEST_ID="+requestID.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real Python consumer failed: %s", out)
	}
}
func TestS01FlowAndFailures(t *testing.T) {
	f := open(t)
	c, csrf, r := f.signup(t, "integration@example.test")
	ctx := context.Background()
	var n int
	f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM telegram_deliveries WHERE state='pending'`).Scan(&n)
	if n != 1 {
		t.Fatal("bot down lost request")
	}
	status, _, _ := f.send(t, c, "POST", "/internal/v1/telegram/jobs/claim", map[string]any{"limit": 1}, "", "", false)
	if status != 404 {
		t.Fatal("public internal ingress accessible")
	}
	f.python(t, r.RequestId) // Actor comes from a parsed aiogram Update; no provision worker yet.
	var op uuid.UUID
	f.env.Pool.QueryRow(ctx, `SELECT operation_id FROM trial_requests WHERE id=$1`, r.RequestId).Scan(&op)
	var grant string
	f.env.Pool.QueryRow(ctx, `SELECT status FROM trial_grants WHERE operation_id=$1`, op).Scan(&grant)
	if grant != "reserved" {
		t.Fatal("decision did not reserve")
	}
	// Restrict another approved account before any provision workers start.
	_, _, restricted := f.signup(t, "restricted@example.test")
	f.python(t, restricted.RequestId)
	if _, err := f.env.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=(SELECT account_id FROM trial_requests WHERE id=$1)`, restricted.RequestId); err != nil {
		t.Fatal(err)
	}
	if err := f.workers.Start(ctx); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool {
		var s string
		f.env.Pool.QueryRow(ctx, `SELECT status FROM trial_operations WHERE id=$1`, op).Scan(&s)
		return s == "applied"
	})
	status, b, resp := f.send(t, c, "GET", "/api/v1/subscription/key", nil, "", "", false)
	if status != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("key read", status)
	}
	var key wire.SubscriptionKey
	if json.Unmarshal(b, &key) != nil || key.SubscriptionUrl == "" {
		t.Fatal("key shape")
	}
	f.panel.mu.Lock()
	adds, forbidden := f.panel.adds, f.panel.forbidden
	f.panel.mu.Unlock()
	if adds != 1 || forbidden != 0 {
		t.Fatal("lost reply caused another write")
	}
	f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM trial_grants WHERE status='granted'`).Scan(&n)
	if n != 1 {
		t.Fatal("grant not singular")
	}
	// Durable connection boundary: after the external add, force the local apply to fail.
	_, err := f.env.Pool.Exec(ctx, `CREATE FUNCTION fail_apply() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='applied' THEN RAISE EXCEPTION 'controlled fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_apply BEFORE UPDATE ON trial_operations FOR EACH ROW EXECUTE FUNCTION fail_apply();`)
	if err != nil {
		t.Fatal(err)
	}
	second, secondCSRF, r2 := f.signup(t, "recover@example.test")
	f.python(t, r2.RequestId)
	var op2 uuid.UUID
	f.env.Pool.QueryRow(ctx, `SELECT operation_id FROM trial_requests WHERE id=$1`, r2.RequestId).Scan(&op2)
	wait(t, func() bool {
		var s string
		f.env.Pool.QueryRow(ctx, `SELECT status FROM trial_operations WHERE id=$1`, op2).Scan(&s)
		return s == "needs_review"
	})
	var before []byte
	f.env.Pool.QueryRow(ctx, `SELECT target FROM trial_operations WHERE id=$1`, op2).Scan(&before)
	f.env.Pool.Exec(ctx, `DROP TRIGGER fail_apply ON trial_operations; DROP FUNCTION fail_apply()`)
	status, _, _ = f.send(t, f.internal.Client(), "POST", "/internal/v1/trial-operations/"+op2.String()+"/reconcile", map[string]any{"operator_tg_id": 101, "reason": "recover original operation"}, "", uuid.NewString(), true)
	if status != 202 {
		t.Fatal("reconcile", status)
	}
	wait(t, func() bool {
		var s string
		f.env.Pool.QueryRow(ctx, `SELECT status FROM trial_operations WHERE id=$1`, op2).Scan(&s)
		return s == "applied"
	})
	var after []byte
	f.env.Pool.QueryRow(ctx, `SELECT target FROM trial_operations WHERE id=$1`, op2).Scan(&after)
	if !bytes.Equal(before, after) {
		t.Fatal("recovery changed targets")
	}
	f.panel.mu.Lock()
	adds, forbidden = f.panel.adds, f.panel.forbidden
	f.panel.mu.Unlock()
	if adds != 2 || forbidden != 0 {
		t.Fatal("recovery duplicated client or touched unknown groups")
	}
	status, _, _ = f.send(t, second, "POST", "/api/v1/auth/logout", nil, secondCSRF, "", false)
	if status != 204 {
		t.Fatal("logout", status)
	}
	status, _, _ = f.send(t, second, "GET", "/api/v1/subscription/key", nil, "", "", false)
	if status != 401 {
		t.Fatal("revoked key exposed")
	}
	status, _, _ = f.send(t, c, "POST", "/api/v1/trial-requests", map[string]any{"comment": "x", "unknown": true}, csrf, uuid.NewString(), false)
	if status != 400 {
		t.Fatal("unknown field", status)
	}
	status, _, _ = f.send(t, c, "GET", "/api/v1/me?unknown=1", nil, "", "", false)
	if status != 400 {
		t.Fatal("unknown query", status)
	}
	if os.Getenv("S01_RUN_BROWSER") == "1" {
		f.browser(t)
	}
}
func (f *fixture) browser(t *testing.T) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "mail.json")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				for _, letter := range f.mail.Letters() {
					if strings.Contains(letter, "To: browser@example.test") {
						m := regexp.MustCompile(`#token=([A-Za-z0-9_-]{43})`).FindStringSubmatch(letter)
						if len(m) == 2 {
							b, _ := json.Marshal(map[string]string{"token": m[1]})
							tmp := file + ".tmp"
							os.WriteFile(tmp, b, 0600)
							os.Rename(tmp, file)
						}
					}
				}
			}
		}
	}()
	defer func() { close(stop); <-done }()
	cmd := exec.Command("npm", "run", "test:e2e")
	cmd.Dir = filepath.Join(f.root, "web")
	cmd.Env = append(os.Environ(), "S01_E2E_MODE=real", "S01_TEST_ORIGIN="+f.public.URL, "S01_TEST_MAIL_FILE="+file, "S01_CONTRACT_URL="+f.internal.URL, "S01_CONTRACT_TOKEN_FILE="+f.tokenFile, "S01_CONTRACT_CA_FILE="+f.ca)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real browser flow failed: %s", out)
	}
}

func TestS01HTTPContractPaths(t *testing.T) {
	f := open(t)
	c, csrf, r := f.signup(t, "contract@example.test")
	raw, err := os.ReadFile(filepath.Join(f.root, "docs", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if json.Unmarshal(raw, &schema) != nil {
		t.Fatal("canonical schema parse")
	}
	count := 0
	for path, methods := range schema.Paths {
		path = strings.ReplaceAll(path, "{id}", r.RequestId.String())
		for method := range methods {
			if method != "get" && method != "post" {
				continue
			}
			count++
			private := strings.HasPrefix(path, "/internal/")
			client := c
			if private {
				client = f.internal.Client()
			}
			status, _, _ := f.send(t, client, strings.ToUpper(method), path+"?unknown=1", nil, csrf, uuid.NewString(), private)
			if status != 400 {
				t.Errorf("%s %s unknown query: %d", method, path, status)
			}
		}
	}
	if count != 15 {
		t.Fatal("canonical operation count", count)
	}
}

func TestS01BackupRestore(t *testing.T) {
	f := open(t)
	ctx := context.Background()
	c, _, r := f.signup(t, "restore@example.test")
	f.python(t, r.RequestId)
	var op uuid.UUID
	f.env.Pool.QueryRow(ctx, `SELECT operation_id FROM trial_requests WHERE id=$1`, r.RequestId).Scan(&op)
	blocked := make(chan struct{})
	f.panel.mu.Lock()
	f.panel.blocked = blocked
	f.panel.release = make(chan struct{})
	f.panel.mu.Unlock()
	running, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := f.workers.Start(running); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked:
	case <-time.After(15 * time.Second):
		t.Fatal("panel read barrier")
	}
	var state, grant string
	var before []byte
	f.env.Pool.QueryRow(ctx, `SELECT o.status,g.status,o.target FROM trial_operations o JOIN trial_grants g ON g.operation_id=o.id WHERE o.id=$1`, op).Scan(&state, &grant, &before)
	if state != "provisioning" || grant != "reserved" || len(before) == 0 {
		t.Fatal("backup point must retain running operation and reserved grant")
	}
	var jobState string
	if err := f.env.Pool.QueryRow(ctx, `SELECT state FROM river_job WHERE kind='s01_provision' AND args->>'operation_id'=$1`, op.String()).Scan(&jobState); err != nil || jobState != "running" {
		t.Fatal("backup point must include a running River job", jobState, err)
	}
	cfg := f.env.Pool.Config().ConnConfig
	if cfg.Host != "127.0.0.1" || cfg.Port != 55491 {
		t.Fatal("restore fixture requires local controlled Compose PostgreSQL")
	}
	lookup := exec.Command("docker", "compose", "-f", filepath.Join(f.root, "deploy/s01/compose.test.yml"), "ps", "-q", "postgres")
	id, err := lookup.Output()
	container := strings.TrimSpace(string(id))
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{12,64}$`).MatchString(container) {
		t.Fatal("controlled PostgreSQL container prerequisite")
	}
	dump := filepath.Join(t.TempDir(), "backup.dump")
	file, err := os.OpenFile(dump, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("docker", "exec", container, "pg_dump", "-U", "s01_test", "-Fc", "--no-owner", "--no-privileges", cfg.Database)
	command.Stdout = file
	err = command.Run()
	file.Close()
	if err != nil {
		t.Fatal("controlled pg_dump failed")
	}
	// Stop the original worker; the dump already captured the uncertain external write.
	cancel()
	stop, stopDone := context.WithTimeout(ctx, 5*time.Second)
	if err := f.workers.StopAndCancel(stop); err != nil {
		t.Fatal("worker cancellation", err)
	}
	stopDone()
	name := "s01_restore_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = f.env.Pool.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.env.Pool.Exec(context.Background(), `DROP DATABASE `+name+` WITH (FORCE)`) })
	file, err = os.Open(dump)
	if err != nil {
		t.Fatal(err)
	}
	command = exec.Command("docker", "exec", "-i", container, "pg_restore", "-U", "s01_test", "--no-owner", "--no-privileges", "-d", name)
	command.Stdin = file
	err = command.Run()
	file.Close()
	if err != nil {
		t.Fatal("controlled pg_restore failed")
	}
	pc := f.env.Pool.Config().Copy()
	pc.ConnConfig.Database = name
	restored, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.Close)
	if err = restored.QueryRow(ctx, `SELECT state FROM river_job WHERE kind='s01_provision' AND args->>'operation_id'=$1`, op.String()).Scan(&jobState); err != nil || jobState != "running" {
		t.Fatal("dump did not preserve running River job", jobState, err)
	}
	// Advance only the recovery clock, keeping running state and all provisioning intent intact.
	if _, err = restored.Exec(ctx, `UPDATE river_job SET attempted_at=now()-interval '4 minutes' WHERE kind='s01_provision' AND state='running'`); err != nil {
		t.Fatal(err)
	}
	queue, err := river.NewClient(riverpgxv5.New(restored), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	svc := s01.NewService(restored, f.env.Redis, queue, f.cfg)
	workers := river.NewWorkers()
	river.AddWorker(workers, &s01.ProvisionWorker{Service: svc})
	worker, err := river.NewClient(riverpgxv5.New(restored), &river.Config{Workers: workers, Queues: map[string]river.QueueConfig{"provision": {MaxWorkers: 1}}, RescueStuckJobsAfter: s01.ProvisionRescueAfter, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		worker.Stop(stop)
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		var s string
		restored.QueryRow(ctx, `SELECT status FROM trial_operations WHERE id=$1`, op).Scan(&s)
		if s == "applied" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restored running job was not reconciled", s)
		}
		time.Sleep(25 * time.Millisecond)
	}
	var after []byte
	var n int
	restored.QueryRow(ctx, `SELECT target FROM trial_operations WHERE id=$1`, op).Scan(&after)
	restored.QueryRow(ctx, `SELECT count(*) FROM trial_grants WHERE status='granted'`).Scan(&n)
	if !bytes.Equal(before, after) || n != 1 {
		t.Fatal("restored operation regenerated identity/expiry or grant")
	}
	f.panel.mu.Lock()
	adds, forbidden := f.panel.adds, f.panel.forbidden
	f.panel.mu.Unlock()
	if adds != 1 || forbidden != 0 {
		t.Fatal("restore changed external client")
	}
	server := httptest.NewTLSServer(httpapi.New(svc, f.cfg))
	defer server.Close()
	client := *server.Client()
	client.Jar = c.Jar
	copy := *f
	copy.public = server
	status, _, response := copy.send(t, &client, "GET", "/api/v1/subscription/key", nil, "", "", false)
	if status != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("restored owner access", status)
	}
}
