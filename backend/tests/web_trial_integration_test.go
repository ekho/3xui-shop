// These fixtures are test binaries only. They expose no public verification shortcut.
package tests

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"example.com/cabinet/backend/db"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/oapi-codegen/runtime/types"
	"github.com/pressly/goose/v3"
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
	mu                        sync.Mutex
	clients                   map[string]map[string]any
	adds, forbidden, requests int
	bulkUnavailable           int
	loseReply                 bool
	blocked, release          chan struct{}
}

func (p *panel) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests++
	w.Header().Set("Content-Type", "application/json")
	reply := func(ok bool, obj any) { json.NewEncoder(w).Encode(map[string]any{"success": ok, "obj": obj}) }
	switch {
	case r.Method == "GET" && r.URL.Path == "/panel/api/clients/list":
		p.bulkUnavailable++
		w.WriteHeader(http.StatusMethodNotAllowed)
	case r.Method == "GET" && r.URL.Path == "/panel/api/inbounds/list":
		reply(true, []map[string]any{{"id": 1, "enable": true, "tag": "regular-tcp"}, {"id": 91, "enable": true, "tag": "unknown"}})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/panel/api/clients/get/"):
		key := strings.TrimPrefix(r.URL.Path, "/panel/api/clients/get/")
		c := p.clients[key]
		if c == nil {
			json.NewEncoder(w).Encode(map[string]any{"success": false, "msg": "Obtain (record not found)", "obj": nil})
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
		record := make(map[string]any, len(c)+1)
		for key, value := range c {
			record[key] = value
		}
		record["uuid"], record["id"] = c["id"], 1
		reply(true, map[string]any{"client": record, "inboundIds": []int{1}, "usedTraffic": 0})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/panel/api/clients/traffic/"):
		key := strings.TrimPrefix(r.URL.Path, "/panel/api/clients/traffic/")
		c := p.clients[key]
		email, _ := c["email"].(string)
		id, _ := c["id"].(string)
		subID, _ := c["subId"].(string)
		if email != key || id == "" || subID == "" {
			reply(false, nil)
			return
		}
		reply(true, map[string]any{"email": email, "uuid": id, "subId": subID, "up": int64(0), "down": int64(0)})
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/panel/api/clients/resetTraffic/"):
		key := strings.TrimPrefix(r.URL.Path, "/panel/api/clients/resetTraffic/")
		c := p.clients[key]
		// This owned fixture always reports zero traffic. A confirmed reset
		// acknowledges that same state without changing access or identity.
		reply(c != nil && c["email"] == key && c["id"] != nil && c["subId"] != nil, nil)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/panel/api/clients/update/"):
		key := strings.TrimPrefix(r.URL.Path, "/panel/api/clients/update/")
		var body map[string]any
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		current := p.clients[key]
		if decoder.Decode(&body) != nil || current == nil || body["email"] != key || body["id"] != current["id"] || body["subId"] != current["subId"] {
			reply(false, nil)
			return
		}
		p.clients[key] = body
		reply(true, nil)
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

func TestPanelUnsupportedBulkReadIsReadOnly(t *testing.T) {
	client := map[string]any{"email": "acct_owned", "id": uuid.NewString(), "subId": "abcdefghijklmnop"}
	p := &panel{clients: map[string]map[string]any{"acct_owned": client}}
	before, _ := json.Marshal(p.clients)
	read := httptest.NewRecorder()
	p.serve(read, httptest.NewRequest(http.MethodGet, "/panel/api/clients/list", nil))
	if read.Code != http.StatusMethodNotAllowed || p.bulkUnavailable != 1 || p.forbidden != 0 || p.adds != 0 {
		t.Fatal("unsupported read was classified as a forbidden write")
	}
	write := httptest.NewRecorder()
	p.serve(write, httptest.NewRequest(http.MethodPost, "/panel/api/clients/list", nil))
	if write.Code != http.StatusMethodNotAllowed || p.forbidden != 1 {
		t.Fatal("unsupported write escaped the fixture guard")
	}
	duplicate := httptest.NewRecorder()
	p.serve(duplicate, httptest.NewRequest(http.MethodPost, "/panel/api/clients/add", strings.NewReader(`{"client":{"email":"acct_owned"},"inboundIds":[1]}`)))
	after, _ := json.Marshal(p.clients)
	if duplicate.Code != http.StatusConflict || p.forbidden != 2 || p.adds != 0 || !bytes.Equal(before, after) {
		t.Fatal("duplicate write changed the owned client or escaped the guard")
	}
}

func TestPanelTrafficFixtureMatchesOwnedClient(t *testing.T) {
	ownedID := uuid.New()
	p := &panel{clients: map[string]map[string]any{
		"acct_owned": {"email": "acct_owned", "id": ownedID.String(), "subId": "abcdefghijklmnop"},
	}}
	read := func(key string) (int, bool, map[string]json.RawMessage) {
		t.Helper()
		recorder := httptest.NewRecorder()
		p.serve(recorder, httptest.NewRequest(http.MethodGet, "/panel/api/clients/traffic/"+key, nil))
		var envelope struct {
			Success bool                       `json:"success"`
			Obj     map[string]json.RawMessage `json:"obj"`
		}
		if recorder.Code == http.StatusOK && json.Unmarshal(recorder.Body.Bytes(), &envelope) != nil {
			t.Fatal("invalid fixture traffic response")
		}
		return recorder.Code, envelope.Success, envelope.Obj
	}
	status, found, row := read("acct_owned")
	if status != http.StatusOK || !found || len(row) != 5 || string(row["email"]) != `"acct_owned"` ||
		string(row["uuid"]) != `"`+ownedID.String()+`"` || string(row["subId"]) != `"abcdefghijklmnop"` ||
		string(row["up"]) != "0" || string(row["down"]) != "0" {
		t.Fatal("owned native traffic shape")
	}
	status, found, row = read("acct_foreign")
	if status != http.StatusOK || found || row != nil || p.forbidden != 0 || p.adds != 0 {
		t.Fatal("unknown client must not expose traffic or make writes")
	}
}

func TestPanelAccessFixturePreservesOwnedClient(t *testing.T) {
	id := uuid.New()
	key, sub := "acct_owned", "abcdefghijklmnop"
	p := &panel{clients: map[string]map[string]any{key: {"email": key, "id": id.String(), "subId": sub, "enable": true, "expiryTime": time.Now().Add(24 * time.Hour).UnixMilli(), "limitIp": 2, "totalGB": 1024, "flow": "owned-flow"}}}
	server := httptest.NewTLSServer(http.HandlerFunc(p.serve))
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client := vpn.NewPanelClient(vpn.Config{PanelURL: server.URL, PanelToken: "fixture-panel", PanelRootCAs: roots})
	t.Cleanup(client.Close)
	view, err := client.GetClient(context.Background(), key)
	if err != nil || view == nil {
		t.Fatal("owned update baseline unavailable", err)
	}
	target := vpn.AccessTarget{PanelKey: key, VPNID: id, SubID: sub, InboundIDs: []int64{1}, ExpiryTimeMS: view.ExpiryTimeMS + 2592000000, DeviceCount: 2, TrafficLimitBytes: 2048, Enable: true}
	if err = client.UpdateAccess(context.Background(), view, target); err != nil {
		t.Fatal("owned fixture does not implement current access update", err)
	}
	updated, err := client.GetClient(context.Background(), key)
	if err != nil || updated == nil || updated.VPNID != id || updated.SubID != sub || updated.ExpiryTimeMS != target.ExpiryTimeMS || updated.LimitIP != 3 || updated.TrafficLimitBytes != 2048 || !updated.Enabled || p.clients[key]["flow"] != "owned-flow" || p.adds != 0 {
		t.Fatal("owned update changed identity or failed readback", err)
	}
	before, _ := json.Marshal(p.clients[key])
	for field, value := range map[string]string{"id": uuid.NewString(), "email": "acct_foreign", "subId": "foreignsubidvalue"} {
		var bad map[string]any
		json.Unmarshal(before, &bad)
		bad[field] = value
		body, _ := json.Marshal(bad)
		response := httptest.NewRecorder()
		p.serve(response, httptest.NewRequest(http.MethodPost, "/panel/api/clients/update/"+key, bytes.NewReader(body)))
		var result struct{ Success bool }
		if json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Success {
			t.Fatal("owned update fixture accepted foreign identity", field)
		}
		after, _ := json.Marshal(p.clients[key])
		if !bytes.Equal(before, after) || len(p.clients) != 1 {
			t.Fatal("refused update changed owned client")
		}
	}
}

type fixture struct {
	env                 *testkit.Env
	svc                 *app.Modules
	cfg                 app.Config
	public, internal    *httptest.Server
	mail                *testkit.SMTP
	panel               *panel
	workers             *river.Client[pgx.Tx]
	root, ca, tokenFile string
	native              bool
	nativeCrash         func()
	nativeBinary        string
}

func open(t *testing.T) *fixture {
	return openMode(t, false)
}

func openMode(t *testing.T, native bool, miniKey ...ed25519.PublicKey) *fixture {
	t.Helper()
	e := testkit.Open(t)
	f := &fixture{env: e, mail: testkit.MailServer(t), panel: &panel{clients: map[string]map[string]any{}, loseReply: true}, native: native}
	f.root, _ = filepath.Abs("../..")
	ps := httptest.NewTLSServer(http.HandlerFunc(f.panel.serve))
	t.Cleanup(ps.Close)
	queue, err := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	f.cfg = app.Config{Accounts: accounts.Config{TermsVersion: "1", PrivacyVersion: "1", CodeKey: bytes.Repeat([]byte{4}, 32), RateNamespace: uuid.NewString(), Operators: []int64{101}}, Mail: notifications.MailConfig{MailKey: bytes.Repeat([]byte{3}, 32), SMTPAddress: f.mail.Address, SMTPRootCAs: f.mail.Roots, SMTPFrom: "sender@example.test"}, HTTP: app.HTTPConfig{AdapterToken: strings.Repeat("f", 43)}, Subscriptions: subscriptions.Config{PanelID: "dedicated-test", TrialEnabled: true, TrialPeriodDays: 3, TrialTrafficGB: 15, TrialDevices: 1, SubscriptionBaseURL: "https://subscriptions.example.test/sub/"}, VPN: vpn.Settings{Panel: vpn.Config{PanelURL: ps.URL, PanelToken: "fixture-panel"}}}
	f.cfg.VPN.Panel.PanelRootCAs = x509.NewCertPool()
	f.cfg.VPN.Panel.PanelRootCAs.AddCert(ps.Certificate())
	if native {
		f.cfg.Accounts.Operators = []int64{101, 202}
		f.cfg.HTTP.AdapterToken = ""
		configureNativeDocker(t, f)
	}
	var handler http.Handler
	f.public = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config.json" {
			w.Header().Set("Cache-Control", "no-store")
			http.ServeFile(w, r, filepath.Join(f.root, "web", "scripts", "local-public-config.json"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" || r.URL.Path == "/webhooks/yoomoney" || r.URL.Path == "/webhooks/yookassa" || r.URL.Path == "/webhooks/cryptomus" || r.URL.Path == "/webhooks/heleket" {
			handler.ServeHTTP(w, r)
			return
		}
		path := filepath.Join(f.root, "web", "dist", filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			entry := "index.html"
			if r.URL.Path == "/mini-app" || strings.HasPrefix(r.URL.Path, "/mini-app/") {
				entry = "mini-app.html"
			}
			path = filepath.Join(f.root, "web", "dist", entry)
		}
		http.ServeFile(w, r, path)
	}))
	t.Cleanup(f.public.Close)
	f.cfg.HTTP.CabinetOrigin = f.public.URL
	f.svc = app.NewModules(e.Pool, e.Redis, queue, &f.cfg)
	if len(miniKey) > 0 {
		f.svc.MiniApp = telegram.NewMiniApp(123456789, miniKey[0], f.svc.Accounts, time.Now)
	}
	handler = httpapi.New(f.svc, f.env.Pool, f.cfg.HTTP)
	if !native {
		f.internal = httptest.NewTLSServer(handler)
		t.Cleanup(f.internal.Close)
		dir := t.TempDir()
		f.ca = filepath.Join(dir, "ca.pem")
		f.tokenFile = filepath.Join(dir, "adapter-token")
		os.WriteFile(f.ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.internal.Certificate().Raw}), 0600)
		os.WriteFile(f.tokenFile, []byte(f.cfg.HTTP.AdapterToken), 0600)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &notifications.MailWorker{Service: f.svc.MailDelivery})
	river.AddWorker(workers, &vpn.ProvisionWorker{Service: f.svc.VPN})
	if native {
		river.AddWorker(workers, &vpn.AccessWorker{Service: f.svc.VPN})
		river.AddWorker(workers, &vpn.MonthlyResetWorker{Service: f.svc.VPN})
	}
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
func (f *fixture) send(t *testing.T, c *http.Client, method, path string, body any, csrf, key string, internal bool, miniToken ...string) (int, []byte, *http.Response) {
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
		req.Header.Set("Authorization", "Bearer "+f.cfg.HTTP.AdapterToken)
	} else if method == "POST" {
		req.Header.Set("Origin", f.cfg.HTTP.CabinetOrigin)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if len(miniToken) > 0 {
		req.Header.Set("Authorization", "Bearer "+miniToken[0])
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
func (f *fixture) signupAccount(t *testing.T, email string, source ...string) (*http.Client, string, uuid.UUID) {
	t.Helper()
	var sourceCode *string
	if len(source) > 0 {
		if len(source) != 1 {
			t.Fatal("owned source fixture invalid")
		}
		sourceCode = &source[0]
	}
	client := *f.public.Client()
	c := &client
	jar, _ := cookiejar.New(nil)
	c.Jar = jar
	c.Timeout = 15 * time.Second
	status, b, _ := f.send(t, c, "POST", "/api/v1/auth/register", wire.RegisterInput{Email: types.Email(email), Locale: "en", AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1", SourceCode: sourceCode}, "", "", false)
	if status != 202 {
		t.Fatal("register", status)
	}
	var out wire.RegistrationAccepted
	json.Unmarshal(b, &out)
	var delivery uuid.UUID
	f.env.Pool.QueryRow(context.Background(), `SELECT id FROM mail_deliveries WHERE challenge_id=$1`, out.ChallengeId).Scan(&delivery)
	if !f.native {
		if f.svc.MailDelivery.SendMail(context.Background(), delivery) != nil {
			t.Fatal("TLS SMTP send")
		}
	} else {
		wait(t, func() bool {
			for _, letter := range f.letters(t, email) {
				if strings.Contains(letter, "To: "+email) {
					return true
				}
			}
			return false
		})
	}
	var token string
	for _, letter := range f.letters(t, email) {
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
	if json.Unmarshal(b, &login) != nil || login.Account.AccountId == uuid.Nil {
		t.Fatal("owned account response invalid")
	}
	return c, login.CsrfToken, login.Account.AccountId
}
func (f *fixture) signup(t *testing.T, email string, source ...string) (*http.Client, string, wire.TrialRequest) {
	t.Helper()
	c, csrf, _ := f.signupAccount(t, email, source...)
	status, b, _ := f.send(t, c, "POST", "/api/v1/trial-requests", map[string]string{"comment": "integration"}, csrf, uuid.NewString(), false)
	if status != 201 {
		t.Fatal("request", status)
	}
	var trial wire.TrialRequest
	json.Unmarshal(b, &trial)
	return c, csrf, trial
}
func (f *fixture) python(t *testing.T, requestID uuid.UUID) {
	t.Helper()
	cmd := exec.Command("poetry", "run", "python", "-m", "unittest", "tests.test_web_trial_contract", "-v")
	cmd.Dir = f.root
	cmd.Env = append(os.Environ(), "TRIAL_CONTRACT_URL="+f.internal.URL, "TRIAL_CONTRACT_TOKEN_FILE="+f.tokenFile, "TRIAL_CONTRACT_CA_FILE="+f.ca, "TRIAL_CONTRACT_REQUEST_ID="+requestID.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real Python consumer failed: %s", out)
	}
}
func TestWebTrialFlowAndFailures(t *testing.T) {
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
	if os.Getenv("RUN_BROWSER_TESTS") == "1" {
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
	cmd.Env = append(os.Environ(), "E2E_MODE=real", "TEST_ORIGIN="+f.public.URL, "TEST_MAIL_FILE="+file, "TRIAL_CONTRACT_URL="+f.internal.URL, "TRIAL_CONTRACT_TOKEN_FILE="+f.tokenFile, "TRIAL_CONTRACT_CA_FILE="+f.ca)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real browser flow failed: %s", out)
	}
}

func TestWebTrialHTTPContractPaths(t *testing.T) {
	f := open(t)
	status, body, response := f.send(t, f.public.Client(), "GET", "/config.json", nil, "", "", false)
	var publicConfig map[string]string
	if status != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") ||
		json.Unmarshal(body, &publicConfig) != nil || publicConfig["termsVersion"] != f.cfg.Accounts.TermsVersion ||
		publicConfig["privacyVersion"] != f.cfg.Accounts.PrivacyVersion {
		t.Fatal("browser must receive deployment policy configuration as uncached JSON")
	}
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
	if count == 0 {
		t.Fatal("no canonical HTTP operations checked")
	}
}

func TestWebTrialBackupRestore(t *testing.T) {
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
	if err := f.env.Pool.QueryRow(ctx, `SELECT state FROM river_job WHERE kind='trial_provision' AND args->>'operation_id'=$1`, op.String()).Scan(&jobState); err != nil || jobState != "running" {
		t.Fatal("backup point must include a running River job", jobState, err)
	}
	cfg := f.env.Pool.Config().ConnConfig
	container := controlledPostgresContainer(t, f.root, cfg)
	dump := filepath.Join(t.TempDir(), "backup.dump")
	file, err := os.OpenFile(dump, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("docker", "exec", container, "pg_dump", "-U", "platform_test", "-Fc", "--no-owner", "--no-privileges", cfg.Database)
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
	name := "trial_restore_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = f.env.Pool.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.env.Pool.Exec(context.Background(), `DROP DATABASE `+name+` WITH (FORCE)`) })
	file, err = os.Open(dump)
	if err != nil {
		t.Fatal(err)
	}
	command = exec.Command("docker", "exec", "-i", container, "pg_restore", "-U", "platform_test", "--no-owner", "--no-privileges", "-d", name)
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
	// Retained pool history blocks downgrade; migration replay must preserve it.
	metadataDB := stdlib.OpenDBFromPool(restored)
	defer metadataDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, metadataDB, os.DirFS(filepath.Join(f.root, "backend/db/migrations")))
	if err != nil {
		t.Fatal(err)
	}
	var restoredReservations int
	if err = restored.QueryRow(ctx, `SELECT count(*) FROM vpn_server_reservations WHERE trial_operation_id=$1`, op).Scan(&restoredReservations); err != nil || restoredReservations != 1 {
		t.Fatal("restored backup lost the retained server reservation", err)
	}
	if _, err = provider.DownTo(ctx, 36); err != nil {
		t.Fatal("restored migrations did not roll back to server-pool version 36", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil || version != 36 {
		t.Fatal("server-pool downgrade guard was not selected", version, err)
	}
	if _, err = provider.DownTo(ctx, 34); err == nil || !strings.Contains(err.Error(), "server pool downgrade blocked") {
		t.Fatal("restored server history allowed downgrade", err)
	}
	version, err = provider.GetDBVersion(ctx)
	if err != nil || version != 36 {
		t.Fatal("server-pool downgrade guard did not retain version 36", version, err)
	}
	if err = db.Migrate(ctx, restored); err != nil {
		t.Fatal("restored additive migration")
	}
	var reservedPanel string
	if err = restored.QueryRow(ctx, `SELECT server_id FROM vpn_server_reservations WHERE trial_operation_id=$1`, op).Scan(&reservedPanel); err != nil || reservedPanel != f.cfg.Subscriptions.PanelID {
		t.Fatal("restored migration changed the durable reservation", err)
	}
	maintenance, err := os.ReadFile(filepath.Join(f.root, "backend/db/maintenance/post_restore_auth.sql"))
	if err != nil {
		t.Fatal("restore maintenance source")
	}
	if _, err = restored.Exec(ctx, string(maintenance)); err != nil {
		t.Fatal("restore session/proof invalidation")
	}
	if err = restored.QueryRow(ctx, `SELECT state FROM river_job WHERE kind='trial_provision' AND args->>'operation_id'=$1`, op.String()).Scan(&jobState); err != nil || jobState != "running" {
		t.Fatal("dump did not preserve running River job", jobState, err)
	}
	// Advance only the recovery clock, keeping running state and all provisioning intent intact.
	if _, err = restored.Exec(ctx, `UPDATE river_job SET attempted_at=now()-interval '4 minutes' WHERE kind='trial_provision' AND state='running'`); err != nil {
		t.Fatal(err)
	}
	queue, err := river.NewClient(riverpgxv5.New(restored), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewModules(restored, f.env.Redis, queue, &f.cfg)
	workers := river.NewWorkers()
	river.AddWorker(workers, &vpn.ProvisionWorker{Service: svc.VPN})
	worker, err := river.NewClient(riverpgxv5.New(restored), &river.Config{Workers: workers, Queues: map[string]river.QueueConfig{"provision": {MaxWorkers: 1}}, RescueStuckJobsAfter: vpn.ProvisionRescueAfter, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var leaseSeconds float64
	if err = restored.QueryRow(ctx, `SELECT COALESCE(max(EXTRACT(epoch FROM expires_at-now())),0)::float8 FROM river_leader`).Scan(&leaseSeconds); err != nil {
		t.Fatal("restored leader lease diagnostic")
	}
	t.Logf("restored leader lease remaining: %.1fs", leaseSeconds)
	t.Cleanup(func() {
		stop, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		worker.Stop(stop)
	})
	// A dump retains the leader's 15s lease; rescue runs every 30s after election.
	deadline := time.Now().Add(60 * time.Second)
	for {
		var s string
		restored.QueryRow(ctx, `SELECT status FROM trial_operations WHERE id=$1`, op).Scan(&s)
		if s == "applied" {
			break
		}
		if time.Now().After(deadline) {
			var state string
			restored.QueryRow(ctx, `SELECT state FROM river_job WHERE kind='trial_provision'`).Scan(&state)
			t.Fatal("restored running job was not reconciled", s, "job", state)
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
	server := httptest.NewTLSServer(httpapi.New(svc, restored, f.cfg.HTTP))
	defer server.Close()
	client := *server.Client()
	client.Jar = c.Jar
	copy := *f
	copy.public = server
	status, _, response := copy.send(t, &client, "GET", "/api/v1/subscription/key", nil, "", "", false)
	if status != 401 {
		t.Fatal("restored cookie must require new login", status)
	}
	status, _, _ = copy.send(t, &client, "POST", "/api/v1/auth/login", wire.LoginInput{Email: "restore@example.test", Password: "fixture password with Unicode ✨"}, "", "", false)
	if status != 200 {
		t.Fatal("restored backup credentials login", status)
	}
	status, _, response = copy.send(t, &client, "GET", "/api/v1/subscription/key", nil, "", "", false)
	if status != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("restored owner access", status)
	}
}
