package tests

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
)

// Only the provider transport is simulated. HTTP, authentication, module wiring,
// jobs, SMTP and restarts run through the freshly built cmd/server executable.
func nativeNoticeBinary(t *testing.T, f *fixture, handler http.Handler, childSettings ...map[string]string) (func() int, func()) {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, value []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if os.WriteFile(path, value, 0600) != nil {
			t.Fatal("owned binary fixture file unavailable")
		}
		return path
	}
	root := func(address string, roots *x509.CertPool) []byte {
		t.Helper()
		host, _, err := net.SplitHostPort(address)
		if err != nil || host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
			t.Fatal("TLS fixture must be loopback")
		}
		connection, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", address, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatal("owned TLS fixture unavailable")
		}
		defer connection.Close()
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: connection.ConnectionState().PeerCertificates[0].Raw})
	}
	key, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("owned Telegram TLS key unavailable")
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"api.telegram.org"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, key, private)
	if err != nil {
		t.Fatal("owned Telegram TLS certificate unavailable")
	}
	bot := httptest.NewUnstartedServer(handler)
	bot.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}, MinVersion: tls.VersionTLS12}
	bot.StartTLS()
	t.Cleanup(bot.Close)
	var outside atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "api.telegram.org:443" {
			outside.Add(1)
			http.Error(w, "owned provider only", 403)
			return
		}
		upstream, err := net.DialTimeout("tcp", bot.Listener.Addr().String(), 3*time.Second)
		if err != nil {
			http.Error(w, "owned provider unavailable", 503)
			return
		}
		defer upstream.Close()
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		if _, err = io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		done := make(chan struct{})
		go func() { io.Copy(upstream, connection); upstream.Close(); close(done) }()
		io.Copy(connection, upstream)
		connection.Close()
		<-done
	}))
	t.Cleanup(proxy.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("owned backend port unavailable")
	}
	address := listener.Addr().String()
	listener.Close()
	target, _ := url.Parse("http://" + address)
	ingress := httputil.NewSingleHostReverseProxy(target)
	ingress.ErrorLog = log.New(io.Discard, "", 0)
	director := ingress.Director
	sourceIP := net.ParseIP("2001:db8::")
	unique := uuid.New()
	copy(sourceIP[8:], unique[:8])
	ingress.Director = func(r *http.Request) {
		director(r)
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", sourceIP.String())
	}
	frontend := f.public.Config.Handler
	f.public.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/webhooks/") || r.URL.Path == "/healthz" {
			ingress.ServeHTTP(w, r)
			return
		}
		frontend.ServeHTTP(w, r)
	})
	panel, err := url.Parse(f.cfg.VPN.Panel.PanelURL)
	if err != nil {
		t.Fatal("owned panel URL unavailable")
	}
	database, err := url.Parse(f.env.Pool.Config().ConnString())
	if err != nil || database.Host == "" || !strings.HasPrefix(f.env.Pool.Config().ConnConfig.Database, "platform_test_") {
		t.Fatal("owned test database URL unavailable")
	}
	// ConnString retains the parsed admin URL after the fixture changes Database.
	database.Path = "/" + f.env.Pool.Config().ConnConfig.Database
	settings := map[string]string{
		"LISTEN_ADDRESS": address, "CABINET_ORIGIN": f.cfg.HTTP.CabinetOrigin, "TRUSTED_PROXY_CIDRS": "127.0.0.0/8",
		"DATABASE_URL_FILE": write("database-url", []byte(database.String())), "REDIS_URL_FILE": os.Getenv("TEST_REDIS_URL_FILE"),
		"MAIL_KEY_FILE": write("mail-key", []byte(base64.StdEncoding.EncodeToString(f.cfg.Mail.MailKey))), "CODE_KEY_FILE": write("code-key", []byte(base64.StdEncoding.EncodeToString(f.cfg.Accounts.CodeKey))),
		"TERMS_VERSION": "1", "PRIVACY_VERSION": "1", "TRIAL_ENABLED": "false", "PANEL_ID": f.cfg.Subscriptions.PanelID,
		"PANEL_URL": panel.String(), "SUBSCRIPTION_BASE_URL": f.cfg.Subscriptions.SubscriptionBaseURL, "PANEL_CA_FILE": write("panel-ca", root(panel.Host, f.cfg.VPN.Panel.PanelRootCAs)),
		"SMTP_ADDRESS": f.cfg.Mail.SMTPAddress, "SMTP_FROM": f.cfg.Mail.SMTPFrom, "SMTP_CA_FILE": write("smtp-ca", root(f.cfg.Mail.SMTPAddress, f.cfg.Mail.SMTPRootCAs)),
		"LEGACY_BOT_API_ENABLED": "false", "TELEGRAM_ENABLED": "true", "BOT_OPERATOR_IDS": "101,202",
		"BOT_TOKEN_FILE": write("bot-token", []byte("123456789:abcdefghijklmnopqrstuvwxyz012345678")),
		"HTTPS_PROXY":    proxy.URL, "SSL_CERT_FILE": write("bot-ca", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), "SSL_CERT_DIR": filepath.Join(dir, "empty-ca-directory"),
		"GODEBUG": "x509sslcertoverrideplatform=1",
	}
	if f.cfg.VPN.Panel.PanelToken != "" {
		settings["PANEL_TOKEN_FILE"] = write("panel-token", []byte(f.cfg.VPN.Panel.PanelToken))
	} else {
		settings["PANEL_USERNAME"] = f.cfg.VPN.Panel.PanelUsername
		settings["PANEL_PASSWORD_FILE"] = write("panel-password", []byte(f.cfg.VPN.Panel.PanelPassword))
	}
	if f.cfg.Mail.SMTPUser != "" {
		settings["SMTP_USER"] = f.cfg.Mail.SMTPUser
		settings["SMTP_PASSWORD_FILE"] = write("smtp-password", []byte(f.cfg.Mail.SMTPPassword))
	}
	binary := filepath.Join(dir, "server")
	f.nativeBinary = binary
	build := exec.Command("go", "build", "-race", "-o", binary, "./cmd/server")
	build.Dir = filepath.Join(f.root, "backend")
	if build.Run() != nil {
		t.Fatal("fresh owned server build failed")
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal("fresh owned binary unavailable")
	}
	t.Logf("fresh cmd/server SHA256 %x", sha256.Sum256(data))
	var process *exec.Cmd
	var output *os.File
	f.nativeCrash = func() {
		t.Helper()
		if process == nil || process.Process.Kill() != nil {
			t.Fatal("owned application crash failed")
		}
		err := process.Wait()
		process = nil
		output.Close()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatal("owned application did not exit by SIGKILL")
		}
	}
	stop := func() {
		if process == nil {
			return
		}
		command := process
		process = nil
		if command.Process.Signal(syscall.SIGTERM) != nil {
			t.Error("owned server stop failed")
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Error("owned server exit failed")
			}
		case <-time.After(25 * time.Second):
			command.Process.Kill()
			<-done
			t.Error("owned server stop timed out")
		}
		output.Close()
	}
	t.Cleanup(func() {
		stop()
		if t.Failed() {
			if data, err := os.ReadFile(filepath.Join(dir, "server.log")); err == nil {
				path := filepath.Join(f.root, ".superpowers", "acceptance", "c28-notices")
				if strings.HasPrefix(t.Name(), "TestNativeTrialAudit") {
					path = filepath.Join(f.root, ".superpowers", "acceptance", "c29-audit-history")
				} else if strings.HasPrefix(t.Name(), "TestNativeTrialSupport") {
					path = filepath.Join(f.root, ".superpowers", "acceptance", "c37-support-proxy")
				} else if strings.HasPrefix(t.Name(), "TestNativeTrialServerPool") {
					path = filepath.Join(f.root, ".superpowers", "acceptance", "c39-server-pool")
				}
				if os.MkdirAll(path, 0700) == nil {
					os.WriteFile(filepath.Join(path, "native-server-failure-"+uuid.NewString()+".log"), data, 0600)
				}
			}
		}
		if outside.Load() != 0 {
			t.Error("unexpected external proxy route")
		}
	})
	start := func() int {
		t.Helper()
		if process != nil {
			t.Fatal("only one owned application process allowed")
		}
		process = exec.Command(binary, "serve")
		process.Env = []string{"PATH=" + os.Getenv("PATH")}
		for _, overrides := range childSettings {
			for name, value := range overrides {
				settings[name] = value
			}
		}
		for name, value := range settings {
			process.Env = append(process.Env, name+"="+value)
		}
		output, err = os.OpenFile(filepath.Join(dir, "server.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal("private server log unavailable")
		}
		process.Stdout, process.Stderr = output, output
		if process.Start() != nil {
			t.Fatal("owned server start failed")
		}
		wait(t, func() bool {
			response, err := f.public.Client().Get(f.public.URL + "/healthz")
			if err != nil {
				return false
			}
			response.Body.Close()
			return response.StatusCode == 200
		})
		return process.Process.Pid
	}
	return start, stop
}

func TestNativeNotices(t *testing.T) {
	f := openMode(t, true)
	ctx := context.Background()
	upstream, err := url.Parse(f.cfg.VPN.Panel.PanelURL)
	if err != nil {
		t.Fatal("owned panel URL invalid")
	}
	panelProxy := httputil.NewSingleHostReverseProxy(upstream)
	panelDirector := panelProxy.Director
	panelProxy.Director = func(r *http.Request) { panelDirector(r); r.Host = upstream.Host }
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.cfg.VPN.Panel.PanelRootCAs, MinVersion: tls.VersionTLS12}}
	panelProxy.Transport = transport
	t.Cleanup(transport.CloseIdleConnections)
	var panelWrites atomic.Int32
	panelHTTP := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && (r.Method != "POST" || r.URL.Path != "/login") {
			panelWrites.Add(1)
			http.Error(w, "read-only owned panel", 405)
			return
		}
		panelProxy.ServeHTTP(w, r)
	}))
	t.Cleanup(panelHTTP.Close)
	f.cfg.VPN.Panel.PanelURL = panelHTTP.URL
	f.cfg.VPN.Panel.PanelRootCAs = x509.NewCertPool()
	f.cfg.VPN.Panel.PanelRootCAs.AddCert(panelHTTP.Certificate())
	bot := &nativeBot{}
	type call struct {
		Method string
		Body   map[string]json.RawMessage
	}
	var mu sync.Mutex
	var calls []call
	var lostChat, crashChat int64
	var answers atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasPrefix(r.URL.Path, "/bot123456789:abcdefghijklmnopqrstuvwxyz012345678/") {
			t.Error("unexpected fake Bot API route")
			http.Error(w, "invalid fixture route", 400)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Error("owned Bot API body unavailable")
			return
		}
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var body map[string]json.RawMessage
		if json.Unmarshal(raw, &body) != nil {
			t.Error("owned Bot API body invalid")
			return
		}
		var chat int64
		json.Unmarshal(body["chat_id"], &chat)
		mu.Lock()
		if method == "sendMessage" || method == "editMessageText" || method == "deleteMessage" {
			calls = append(calls, call{method, body})
		}
		lose := method == "sendMessage" && chat == lostChat
		crash := method == "sendMessage" && chat == crashChat
		if lose {
			lostChat = 0
		}
		mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(raw))
		response, err := bot.RoundTrip(r)
		if crash && err == nil {
			defer response.Body.Close()
			<-r.Context().Done() // Copy exists; stop the real process before it receives an ACK.
			return
		}
		if err != nil || lose {
			connection, _, e := w.(http.Hijacker).Hijack()
			if e == nil {
				connection.Close()
			}
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
		if method == "answerCallbackQuery" {
			answers.Add(1)
		}
	})
	start, stop := nativeNoticeBinary(t, f, handler)
	firstPID := start()
	operator, csrf, actor := f.signupAccount(t, nativeEmail("notice-operator"))
	if f.svc.Accounts.ChangeOperatorRole(ctx, actor, true) != nil {
		t.Fatal("owned operator grant failed")
	}
	email := nativeEmail("notice-client")
	client, clientCSRF, account := f.signupAccount(t, email)
	const chat int64 = 733
	if _, err := f.env.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=$2 WHERE id=$1`, account, chat); err != nil {
		t.Fatal("owned Telegram binding fixture failed")
	}
	post := func(owner *http.Client, path string, in any, token string, want int) []byte {
		t.Helper()
		status, body, _ := f.send(t, owner, "POST", path, in, token, "", false)
		if status != want {
			t.Fatal("owned notice HTTP status", path, status)
		}
		return body
	}
	post(client, "/api/v1/notices/preferences", map[string]bool{"email_enabled": true}, clientCSRF, 200)
	preview := func(in map[string]any) notifications.NoticePreview {
		t.Helper()
		var p notifications.NoticePreview
		if json.Unmarshal(post(operator, "/api/v1/operator/notices/previews", in, csrf, 201), &p) != nil || p.ID == uuid.Nil {
			t.Fatal("owned preview invalid")
		}
		return p
	}
	confirm := func(p notifications.NoticePreview) notifications.NoticeBatchResult {
		t.Helper()
		var out notifications.NoticeBatchResult
		if json.Unmarshal(post(operator, "/api/v1/operator/notices/previews/"+p.ID.String()+"/confirm", map[string]bool{"confirmed": true}, csrf, 200), &out) != nil || out.Notice == nil || out.Notice.ID != p.ID {
			t.Fatal("owned confirmation invalid")
		}
		return out
	}
	last := func() notifications.NoticeBatchResult {
		t.Helper()
		status, raw, _ := f.send(t, operator, "GET", "/api/v1/operator/notices/last", nil, "", "", false)
		var out notifications.NoticeBatchResult
		if status != 200 || json.Unmarshal(raw, &out) != nil {
			t.Fatal("owned Last invalid", status)
		}
		return out
	}
	inbox := func(owner *http.Client) notifications.NoticeResult {
		t.Helper()
		status, raw, _ := f.send(t, owner, "GET", "/api/v1/notices?page=1", nil, "", "", false)
		var out notifications.NoticeResult
		if status != 200 || json.Unmarshal(raw, &out) != nil {
			t.Fatal("owned inbox invalid", status)
		}
		return out
	}
	trace := func() []call { mu.Lock(); defer mu.Unlock(); return append([]call{}, calls...) }
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if f.env.Pool.QueryRow(ctx, query, args...).Scan(&n) != nil {
			t.Fatal("owned fact count unavailable")
		}
		return n
	}
	snapshot := func() [32]byte {
		t.Helper()
		var state string
		if f.env.Pool.QueryRow(ctx, `SELECT jsonb_build_array(
	 (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a),
	 (SELECT jsonb_agg(to_jsonb(a) ORDER BY account_id) FROM trial_grants a),
	 (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM trial_operations a),
	 (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM access_operations a),
	 (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM purchase_orders a),
	 (SELECT jsonb_agg(to_jsonb(a) ORDER BY operation_id) FROM purchase_receipts a),
	 (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM purchase_refunds a))::text`).Scan(&state) != nil {
			t.Fatal("owned business snapshot unavailable")
		}
		return sha256.Sum256([]byte(state))
	}
	mailCount := func(text string) int {
		n := 0
		for _, letter := range f.letters(t, email) {
			if strings.Contains(letter, text) {
				if strings.Contains(letter, "<b>Native notice") {
					t.Fatal("notice mail is not plain text")
				}
				n++
			}
		}
		return n
	}
	p := preview(map[string]any{"mode": "send", "audience": "all", "body": "<b>Native notice original</b> 🙂", "reason": "Native all snapshot"})
	if p.RecipientCount != 2 || p.TelegramCount != 1 || p.EmailCount != 1 || len(trace()) != 0 || count(`SELECT count(*) FROM notices`) != 0 || count(`SELECT count(*) FROM mail_deliveries WHERE kind='operator_notice'`) != 0 {
		t.Fatal("preview delivered or counted wrong audience")
	}
	late, _, _ := f.signupAccount(t, nativeEmail("notice-late"))
	before := snapshot()
	confirm(p)
	wait(t, func() bool { out := last(); return out.Telegram.Succeeded == 1 && out.Email.Succeeded == 1 })
	if len(inbox(client).Notices) != 1 || len(inbox(late).Notices) != 0 || mailCount(p.Text) != 1 {
		t.Fatal("compiled audience/plain mail differs from preview")
	}
	first := trace()
	if len(first) != 1 {
		t.Fatal("actual Bot API send count differs from frozen audience")
	}
	var text, parse string
	json.Unmarshal(first[0].Body["text"], &text)
	json.Unmarshal(first[0].Body["parse_mode"], &parse)
	if len(first) != 1 || first[0].Method != "sendMessage" || text != p.HTML || parse != "HTML" {
		t.Fatal("actual Bot API did not receive normalized HTML")
	}
	copy := bot.message(chat, p.HTML)
	if copy.ID <= 0 {
		t.Fatal("actual Telegram copy missing")
	}
	jobs := count(`SELECT count(*) FROM river_job WHERE kind='mail_delivery'`)
	stop()
	secondPID := start()
	if secondPID == firstPID {
		t.Fatal("compiled process did not restart")
	}
	if confirm(p).Telegram.Succeeded != 1 || count(`SELECT count(*) FROM river_job WHERE kind='mail_delivery'`) != jobs || len(trace()) != 1 || mailCount(p.Text) != 1 || snapshot() != before {
		t.Fatal("restart/replay duplicated delivery or changed business facts")
	}
	edit := preview(map[string]any{"mode": "edit", "expected_revision": 1, "body": "<i>Native notice edited</i>", "reason": "Native edit"})
	if edit.TelegramCount != 1 {
		t.Fatal("recorded current Telegram copy unavailable for edit")
	}
	confirm(edit)
	wait(t, func() bool { return last().Telegram.Succeeded == 1 })
	changed := trace()
	if len(changed) != 2 {
		t.Fatal("actual Bot API edit count differs from recorded copies")
	}
	var message int64
	json.Unmarshal(changed[1].Body["message_id"], &message)
	json.Unmarshal(changed[1].Body["text"], &text)
	if len(changed) != 2 || changed[1].Method != "editMessageText" || message != copy.ID || text != edit.HTML || mailCount(p.Text) != 1 || mailCount(edit.Text) != 0 || count(`SELECT count(*) FROM mail_deliveries WHERE kind='operator_notice'`) != 1 {
		t.Fatal("edit retargeted message or sent another mail")
	}
	var keyboard struct {
		Rows [][]struct {
			Data string `json:"callback_data"`
		} `json:"inline_keyboard"`
	}
	copy = bot.message(chat, edit.HTML)
	if json.Unmarshal(copy.Markup, &keyboard) != nil || len(keyboard.Rows) != 2 || !strings.HasPrefix(keyboard.Rows[1][0].Data, "on1:") {
		t.Fatal("owned close keyboard missing")
	}
	var date int64
	if f.env.Pool.QueryRow(ctx, `SELECT extract(epoch FROM telegram_message_at)::bigint FROM notice_actions WHERE preview_id=$1 AND telegram_state='succeeded'`, edit.ID).Scan(&date) != nil {
		t.Fatal("known Telegram date unavailable")
	}
	callback := func(actor int64) {
		bot.mu.Lock()
		defer bot.mu.Unlock()
		bot.sequence++
		bot.updates = append(bot.updates, map[string]any{"update_id": bot.sequence, "callback_query": map[string]any{"id": uuid.NewString(), "from": map[string]any{"id": actor, "language_code": "en"}, "message": map[string]any{"message_id": copy.ID, "date": date, "from": map[string]any{"id": 123456789, "is_bot": true}, "chat": map[string]any{"id": actor, "type": "private"}, "reply_markup": copy.Markup}, "data": keyboard.Rows[1][0].Data}})
	}
	callback(734)
	wait(t, func() bool { return answers.Load() == 1 })
	if len(trace()) != 2 || len(inbox(client).Notices) != 1 {
		t.Fatal("foreign callback closed owned message")
	}
	callback(chat)
	wait(t, func() bool { return len(inbox(client).Notices) == 0 })
	wait(t, func() bool { return len(trace()) == 3 })
	if trace()[2].Method != "deleteMessage" {
		t.Fatal("known own close did not delete actual Telegram copy")
	}
	withdraw := preview(map[string]any{"mode": "delete", "expected_revision": 2, "reason": "Native withdraw"})
	confirm(withdraw)
	if len(inbox(operator).Notices) != 0 || mailCount(p.Text) != 1 || snapshot() != before {
		t.Fatal("withdraw changed delivered mail or business facts")
	}
	post(client, "/api/v1/notices/preferences", map[string]bool{"email_enabled": false}, clientCSRF, 200)
	mu.Lock()
	lostChat = chat
	mu.Unlock()
	unknown := preview(map[string]any{"mode": "send", "audience": "personal", "account_id": account, "body": "Native notice unknown ACK", "reason": "Native lost acknowledgement"})
	confirm(unknown)
	// Claim marks uncertainty before the wire call; stop only after the fake
	// provider has a copy and the lost response has reached a terminal outcome.
	wait(t, func() bool {
		return last().Telegram.Unknown == 1 && bot.message(chat, unknown.HTML).ID > 0 &&
			count(`SELECT count(*) FROM client_telegram_deliveries d JOIN notice_actions a ON a.id=d.notice_action_id WHERE a.preview_id=$1 AND d.state='skipped'`, unknown.ID) == 1
	})
	unknownCalls := len(trace())
	stop()
	thirdPID := start()
	if thirdPID == secondPID {
		t.Fatal("compiled process did not restart twice")
	}
	if confirm(unknown).Telegram.Unknown != 1 || len(trace()) != unknownCalls {
		t.Fatal("unknown acknowledgement was retried after restart")
	}
	deleteUnknown := preview(map[string]any{"mode": "delete", "expected_revision": 1, "reason": "No guessed Telegram message"})
	if deleteUnknown.TelegramCount != 0 {
		t.Fatal("unknown copy has invented delete proof")
	}
	confirm(deleteUnknown)
	if len(trace()) != unknownCalls || bot.message(chat, unknown.HTML).ID == 0 || len(inbox(client).Notices) != 0 {
		t.Fatal("unknown Telegram copy falsely deleted or cabinet not withdrawn")
	}
	mu.Lock()
	crashChat = chat
	mu.Unlock()
	crashed := preview(map[string]any{"mode": "send", "audience": "personal", "account_id": account, "body": "Native crash before notice outcome commit", "reason": "Native uncertain recovery"})
	confirm(crashed)
	wait(t, func() bool { return bot.message(chat, crashed.HTML).ID > 0 })
	crashCalls := len(trace())
	stop()
	if count(`SELECT count(*) FROM client_telegram_deliveries d JOIN notice_actions a ON a.id=d.notice_action_id WHERE a.preview_id=$1 AND d.state='pending' AND a.telegram_state='unknown'`, crashed.ID) != 1 {
		t.Fatal("real process did not stop in uncertain pending window")
	}
	if _, err := f.env.Pool.Exec(ctx, `UPDATE client_telegram_deliveries d SET lease_expires_at=clock_timestamp()-interval '1 second' FROM notice_actions a WHERE a.id=d.notice_action_id AND a.preview_id=$1`, crashed.ID); err != nil {
		t.Fatal("owned recovery lease expiry failed")
	}
	mu.Lock()
	crashChat = 0
	mu.Unlock()
	if start() == thirdPID {
		t.Fatal("compiled notice crash process did not restart")
	}
	wait(t, func() bool {
		return count(`SELECT count(*) FROM client_telegram_deliveries d JOIN notice_actions a ON a.id=d.notice_action_id WHERE a.preview_id=$1 AND d.state<>'pending'`, crashed.ID) == 1
	})
	if confirm(crashed).Telegram.Unknown != 1 || len(trace()) != crashCalls || bot.message(chat, crashed.HTML).ID == 0 {
		t.Fatal("real-process uncertain copy repeated or hidden after lease recovery")
	}
	unknownCalls = crashCalls
	var oldFacts string
	if f.env.Pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(a) ORDER BY id)::text FROM notice_actions a WHERE preview_id=$1`, p.ID).Scan(&oldFacts) != nil {
		t.Fatal("old delivered facts unavailable")
	}
	post(client, "/api/v1/notices/preferences", map[string]bool{"email_enabled": true}, clientCSRF, 200)
	stale := preview(map[string]any{"mode": "send", "audience": "personal", "account_id": account, "body": "Native stale recipient", "reason": "Native binding and consent guard"})
	post(client, "/api/v1/notices/preferences", map[string]bool{"email_enabled": false}, clientCSRF, 200)
	if _, err := f.env.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=734 WHERE id=$1`, account); err != nil {
		t.Fatal("owned binding change failed")
	}
	guarded := confirm(stale)
	if guarded.Telegram.Skipped != 1 || guarded.Email.Skipped != 1 || len(trace()) != unknownCalls {
		t.Fatal("stale recipient reached a changed channel")
	}
	role := preview(map[string]any{"mode": "send", "audience": "personal", "account_id": account, "body": "Native stale actor", "reason": "Native current role guard"})
	if f.svc.Accounts.ChangeOperatorRole(ctx, actor, false) != nil {
		t.Fatal("owned role revoke failed")
	}
	post(operator, "/api/v1/operator/notices/previews/"+role.ID.String()+"/confirm", map[string]bool{"confirmed": true}, csrf, 403)
	var retained string
	if f.env.Pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(a) ORDER BY id)::text FROM notice_actions a WHERE preview_id=$1`, p.ID).Scan(&retained) != nil || retained != oldFacts || len(trace()) != unknownCalls {
		t.Fatal("current role guard changed previous delivery facts")
	}
	panel := vpn.NewPanelClient(f.cfg.VPN.Panel)
	defer panel.Close()
	if _, err := panel.RegularInboundIDs(ctx); err != nil {
		t.Fatal("owned TLS panel readback unavailable")
	}
	if panelWrites.Load() != 0 {
		t.Fatal("notice flow attempted a panel data write")
	}
	t.Log("PASS: fresh compiled HTTP/jobs/Telegram/TLS SMTP; frozen all audience, replay and uncertain recovery across three process restarts, same message edit/delete/own close, lost ACK without guessed delete, current role/binding/consent and preserved business/delivery facts")
}
