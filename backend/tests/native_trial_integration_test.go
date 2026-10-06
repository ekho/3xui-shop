package tests

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func configureNativeDocker(t *testing.T, f *fixture) {
	t.Helper()
	state := os.Getenv("NATIVE_DOCKER_STATE")
	if state == "" {
		return
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(state, name))
		if err != nil {
			t.Fatal("owned Docker prerequisite missing")
		}
		return strings.TrimSpace(string(b))
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(read("cert.pem"))) {
		t.Fatal("owned Docker CA invalid")
	}
	f.cfg.VPN.Panel.PanelURL, f.cfg.VPN.Panel.PanelUsername, f.cfg.VPN.Panel.PanelPassword = "https://localhost:59444", "local-operator", read("panel-password")
	f.cfg.VPN.Panel.PanelToken, f.cfg.VPN.Panel.PanelRootCAs = "", roots
	f.cfg.Subscriptions.SubscriptionBaseURL = "https://localhost:59445/sub/"
	f.cfg.Mail.SMTPAddress, f.cfg.Mail.SMTPUser, f.cfg.Mail.SMTPPassword = "localhost:59447", "local-service", read("smtp-password")
	f.cfg.Mail.SMTPRootCAs = roots
	f.mail = nil
}

func (f *fixture) letters(t *testing.T, email string) []string {
	t.Helper()
	if f.mail != nil {
		return f.mail.Letters()
	}
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.cfg.Mail.SMTPRootCAs, MinVersion: tls.VersionTLS12}}}
	defer c.CloseIdleConnections()
	read := func(path string, out any) {
		response, err := c.Get("https://localhost:59446" + path)
		if err != nil {
			t.Fatal("owned Mailpit unavailable")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out) != nil {
			t.Fatal("owned Mailpit response invalid")
		}
	}
	var inbox struct {
		Messages []struct {
			ID string
			To []struct{ Address string }
		}
	}
	read("/api/v1/search?query="+url.QueryEscape("to:"+email), &inbox)
	var letters []string
	for _, m := range inbox.Messages {
		owned := false
		for _, to := range m.To {
			if to.Address == email {
				owned = true
			}
		}
		if !owned {
			continue
		}
		var detail struct{ Text string }
		read("/api/v1/message/"+url.PathEscape(m.ID), &detail)
		letters = append(letters, "To: "+email+"\n"+detail.Text)
	}
	return letters
}

func nativeEmail(prefix string) string { return prefix + "-" + uuid.NewString() + "@example.test" }
func assertNativePanel(t *testing.T, f *fixture, operations ...uuid.UUID) {
	t.Helper()
	if os.Getenv("NATIVE_DOCKER_STATE") == "" {
		f.panel.mu.Lock()
		defer f.panel.mu.Unlock()
		if f.panel.adds != len(operations) || f.panel.forbidden != 0 {
			t.Fatal("native panel identity changed")
		}
		return
	}
	client := vpn.NewPanelClient(f.cfg.VPN.Panel)
	defer client.Close()
	for _, op := range operations {
		var raw []byte
		if err := f.env.Pool.QueryRow(context.Background(), `SELECT target FROM trial_operations WHERE id=$1`, op).Scan(&raw); err != nil {
			t.Fatal("native target missing")
		}
		var target vpn.ProvisionTarget
		if json.Unmarshal(raw, &target) != nil {
			t.Fatal("native target invalid")
		}
		view, err := client.GetClient(context.Background(), target.PanelKey)
		if err != nil || view == nil || view.VPNID != target.VPNID || view.SubID != target.SubID || view.ExpiryTimeMS != target.ExpiryTimeMS {
			t.Fatal("Docker panel readback changed identity/expiry")
		}
	}
}

type nativeMessage struct {
	ID, Chat int64
	Text     string
}
type nativeBot struct {
	mu                  sync.Mutex
	updates             []map[string]any
	messages            []nativeMessage
	sequence, messageID int64
	offline             bool
	rateLimitReplies    int
}

func nativeReply(v any) *http.Response {
	b, _ := json.Marshal(map[string]any{"ok": true, "result": v})
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}
}
func (b *nativeBot) RoundTrip(r *http.Request) (*http.Response, error) {
	var in struct {
		Offset, Chat, Message int64
		Text                  string
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, errors.New("invalid fake request")
	}
	json.Unmarshal(body["offset"], &in.Offset)
	json.Unmarshal(body["chat_id"], &in.Chat)
	json.Unmarshal(body["message_id"], &in.Message)
	json.Unmarshal(body["text"], &in.Text)
	b.mu.Lock()
	if b.offline {
		b.mu.Unlock()
		return nil, errors.New("owned fake Telegram unavailable")
	}
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	switch method {
	case "getWebhookInfo":
		b.mu.Unlock()
		return nativeReply(map[string]any{"url": ""}), nil
	case "getUpdates":
		var remaining []map[string]any
		for _, u := range b.updates {
			if u["update_id"].(int64) >= in.Offset {
				remaining = append(remaining, u)
			}
		}
		b.updates = remaining
		if len(remaining) > 0 {
			out := remaining[0]
			b.mu.Unlock()
			return nativeReply([]map[string]any{out}), nil
		}
		b.mu.Unlock()
		timer := time.NewTimer(50 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-timer.C:
			return nativeReply([]any{}), nil
		}
	case "answerCallbackQuery":
		if b.rateLimitReplies > 0 {
			b.rateLimitReplies--
			b.mu.Unlock()
			return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":429,"parameters":{"retry_after":1}}`))}, nil
		}
		b.mu.Unlock()
		return nativeReply(true), nil
	case "sendMessage", "editMessageText", "editMessageReplyMarkup":
		id := in.Message
		if id == 0 {
			b.messageID++
			id = b.messageID
		}
		found := false
		for i, m := range b.messages {
			if m.ID == id && m.Chat == in.Chat {
				if method != "editMessageReplyMarkup" {
					b.messages[i].Text = in.Text
				}
				found = true
			}
		}
		if !found {
			b.messages = append(b.messages, nativeMessage{ID: id, Chat: in.Chat, Text: in.Text})
		}
		b.mu.Unlock()
		return nativeReply(map[string]any{"message_id": id, "date": 1, "chat": map[string]any{"id": in.Chat, "type": "private"}}), nil
	default:
		b.mu.Unlock()
		return nil, errors.New("unsupported fake Telegram method")
	}
}
func (b *nativeBot) message(chat int64, text string) nativeMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.messages) - 1; i >= 0; i-- {
		m := b.messages[i]
		if m.Chat == chat && strings.Contains(m.Text, text) {
			return m
		}
	}
	return nativeMessage{}
}
func (b *nativeBot) callback(actor, message int64, action string, target uuid.UUID, callbackID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sequence++
	if callbackID == "" {
		callbackID = uuid.NewString()
	}
	b.updates = append(b.updates, map[string]any{"update_id": b.sequence, "callback_query": map[string]any{"id": callbackID, "from": map[string]any{"id": actor, "is_bot": false}, "message": map[string]any{"message_id": message, "date": 1, "chat": map[string]any{"id": actor, "type": "private"}}, "data": "wt1:" + action + ":" + target.String()}})
}
func (b *nativeBot) reason(actor int64, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sequence++
	b.updates = append(b.updates, map[string]any{"update_id": b.sequence, "message": map[string]any{"message_id": 1000 + b.sequence, "date": 1, "from": map[string]any{"id": actor, "is_bot": false}, "chat": map[string]any{"id": actor, "type": "private"}, "text": text}})
}
func launchNative(t *testing.T, f *fixture, bot *nativeBot, enabled, provision bool) (*telegram.Runtime, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	workers := river.NewWorkers()
	river.AddWorker(workers, &notifications.MailWorker{Service: f.svc.MailDelivery})
	river.AddWorker(workers, &vpn.ProvisionWorker{Service: f.svc.VPN})
	river.AddWorker(workers, &vpn.AccessWorker{Service: f.svc.VPN})
	river.AddWorker(workers, &vpn.MonthlyResetWorker{Service: f.svc.VPN})
	queues := map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}}
	if provision {
		queues["provision"] = river.QueueConfig{MaxWorkers: 2}
	}
	worker, err := river.NewClient(riverpgxv5.New(f.env.Pool), &river.Config{Workers: workers, Queues: queues, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	f.workers = worker
	if err = worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	tg, err := app.NewTelegram(telegram.Config{Enabled: enabled, Token: "123456789:abcdefghijklmnopqrstuvwxyz012345678", Operators: f.cfg.Accounts.Operators}, f.svc.Subscriptions, f.svc.Notifications, &http.Client{Transport: bot})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := make(chan error, 1)
	svc, server := f.svc, f.public.Config
	var schedulerDone sync.WaitGroup
	schedulerDone.Add(1)
	go func() { defer schedulerDone.Done(); scheduler <- svc.VPN.RunMonthlyResetScheduler(ctx) }()
	done := make(chan error, 1)
	go func() { done <- app.Serve(ctx, server, make(chan error), scheduler, tg) }()
	stop := sync.OnceFunc(func() {
		cancel()
		stop, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		if err := worker.StopAndCancel(stop); err != nil {
			t.Error("native worker shutdown failed")
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error("native application shutdown failed")
			}
		case <-stop.Done():
			t.Error("native application shutdown timed out")
		}
		schedulerDone.Wait()
	})
	t.Cleanup(stop)
	return tg, stop
}
func trialStatus(f *fixture, id uuid.UUID) string {
	var s string
	f.env.Pool.QueryRow(context.Background(), `SELECT status FROM trial_requests WHERE id=$1`, id).Scan(&s)
	return s
}
func operationFor(t *testing.T, f *fixture, r uuid.UUID) uuid.UUID {
	t.Helper()
	var op uuid.UUID
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT operation_id FROM trial_requests WHERE id=$1`, r).Scan(&op); err != nil {
		t.Fatal("operation not committed")
	}
	return op
}
func applied(f *fixture, op uuid.UUID) bool {
	var s string
	f.env.Pool.QueryRow(context.Background(), `SELECT status FROM trial_operations WHERE id=$1`, op).Scan(&s)
	return s == "applied"
}
func cardFor(t *testing.T, bot *nativeBot, actor int64, r uuid.UUID) nativeMessage {
	t.Helper()
	wait(t, func() bool { return bot.message(actor, r.String()).ID > 0 })
	return bot.message(actor, r.String())
}
func TestNativeTrialFlow(t *testing.T) {
	f := openMode(t, true)
	bot := &nativeBot{rateLimitReplies: 1}
	launchNative(t, f, bot, true, true)
	owner, _, a := f.signup(t, nativeEmail("native-owner"))
	first := cardFor(t, bot, 101, a.RequestId)
	second := cardFor(t, bot, 202, a.RequestId)
	callbackID := uuid.NewString()
	bot.callback(101, first.ID, "a", a.RequestId, callbackID)
	bot.callback(202, second.ID, "r", a.RequestId, "")
	bot.callback(101, first.ID, "a", a.RequestId, callbackID)
	wait(t, func() bool { return trialStatus(f, a.RequestId) == "approved" })
	op := operationFor(t, f, a.RequestId)
	wait(t, func() bool { return applied(f, op) })
	status, body, response := f.send(t, owner, "GET", "/api/v1/subscription/key", nil, "", "", false)
	if status != 200 || response.Header.Get("Cache-Control") != "no-store" || !strings.Contains(string(body), "subscription_url") {
		t.Fatal("owner key unavailable")
	}
	// Model an operator-review checkpoint on the already persisted target. The
	// Telegram action must reconcile that operation, without a second grant/key.
	_, err := f.env.Pool.Exec(context.Background(), `UPDATE trial_operations SET status='needs_review' WHERE id=$1`, op)
	if err != nil {
		t.Fatal("controlled review checkpoint failed")
	}
	_, err = f.env.Pool.Exec(context.Background(), `INSERT INTO telegram_deliveries(id,request_id,operation_id,chat_id,kind,payload,state,created_at,available_at) VALUES ($1,$2,$3,101,'provision_review','{}','pending',clock_timestamp(),clock_timestamp())`, uuid.New(), a.RequestId, op)
	if err != nil {
		t.Fatal("controlled review card failed")
	}
	wait(t, func() bool {
		return strings.Contains(bot.message(101, a.RequestId.String()).Text, "Нужна проверка поддержки")
	})
	review := bot.message(101, a.RequestId.String())
	bot.callback(101, review.ID, "c", op, "")
	wait(t, func() bool { return bot.message(101, "Укажите причину").ID > 0 })
	bot.reason(101, "Native reconcile")
	wait(t, func() bool { return bot.message(101, "Подтвердите действие").ID > 0 })
	confirmReview := bot.message(101, "Подтвердите действие")
	bot.callback(101, confirmReview.ID, "y", op, "")
	var reconcileActor int64
	wait(t, func() bool {
		return f.env.Pool.QueryRow(context.Background(), `SELECT operator_tg_id FROM audit_events WHERE action='provision_reconcile_requested' AND operation_id=$1 AND reason='Native reconcile'`, op).Scan(&reconcileActor) == nil
	})
	if reconcileActor != 101 {
		t.Fatal("reconcile actor changed")
	}
	wait(t, func() bool { return applied(f, op) })
	other, _, b := f.signup(t, nativeEmail("native-reconsider"))
	decline := cardFor(t, bot, 202, b.RequestId)
	bot.callback(202, decline.ID, "r", b.RequestId, "")
	wait(t, func() bool { return trialStatus(f, b.RequestId) == "rejected" })
	status, body, _ = f.send(t, other, "GET", "/api/v1/subscription/key", nil, "", "", false)
	if status == 200 || strings.Contains(string(body), "subscription_url") {
		t.Fatal("other account received owner key")
	}
	bot.callback(202, decline.ID, "s", b.RequestId, "")
	wait(t, func() bool { return bot.message(202, "Укажите причину").ID > 0 })
	bot.reason(202, "Native support review")
	wait(t, func() bool { return bot.message(202, "Подтвердите действие").ID > 0 })
	confirmation := bot.message(202, "Подтвердите действие")
	bot.callback(202, confirmation.ID, "y", b.RequestId, "")
	var reconsidered uuid.UUID
	wait(t, func() bool {
		return f.env.Pool.QueryRow(context.Background(), `SELECT id FROM trial_requests WHERE previous_request_id=$1`, b.RequestId).Scan(&reconsidered) == nil
	})
	card := cardFor(t, bot, 101, reconsidered)
	bot.callback(101, card.ID, "a", reconsidered, "")
	wait(t, func() bool { return trialStatus(f, reconsidered) == "approved" })
	secondOp := operationFor(t, f, reconsidered)
	wait(t, func() bool { return applied(f, secondOp) })
	var grants, ops, callbacks int
	var actor int64
	var reason string
	err = f.env.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM trial_grants),(SELECT count(*) FROM trial_operations),(SELECT count(*) FROM decision_callbacks WHERE id=$1)`, callbackID).Scan(&grants, &ops, &callbacks)
	if err != nil || grants != 2 || ops != 2 || callbacks != 1 {
		t.Fatal("duplicate native issuance")
	}
	err = f.env.Pool.QueryRow(context.Background(), `SELECT operator_tg_id,reason FROM audit_events WHERE action='trial_reconsidered' AND request_id=$1`, reconsidered).Scan(&actor, &reason)
	if err != nil || actor != 202 || reason != "Native support review" {
		t.Fatal("support actor/reason lost")
	}
	assertNativePanel(t, f, op, secondOp)
}
func TestNativeTrialTelegramOutage(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "outage", false: "disabled"}[enabled], func(t *testing.T) {
			f := openMode(t, true)
			bot := &nativeBot{}
			tg, _ := launchNative(t, f, bot, enabled, true)
			email := nativeEmail("native-outage")
			_, _, r := f.signup(t, email)
			if enabled {
				card := cardFor(t, bot, 101, r.RequestId)
				bot.callback(101, card.ID, "a", r.RequestId, "")
				wait(t, func() bool { return trialStatus(f, r.RequestId) == "approved" })
				bot.mu.Lock()
				bot.offline = true
				bot.mu.Unlock()
				wait(t, func() bool { return tg.State().Degraded })
			} else {
				if _, err := app.NewTrialBridge(f.svc.Subscriptions, f.svc.Notifications).Decide(context.Background(), telegram.TrialDecision{RequestID: r.RequestId, ActorID: 101, Action: "approve", CallbackID: uuid.NewString()}); err != nil {
					t.Fatal(err)
				}
			}
			op := operationFor(t, f, r.RequestId)
			wait(t, func() bool { return applied(f, op) })
			_, _, other := f.signup(t, nativeEmail("native-web-review"))
			var account uuid.UUID
			if err := f.env.Pool.QueryRow(context.Background(), `SELECT id FROM accounts WHERE email_key=$1`, email).Scan(&account); err != nil {
				t.Fatal(err)
			}
			if err := f.svc.Accounts.ChangeOperatorRole(context.Background(), account, true); err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.svc.Subscriptions.DecideOperatorTrial(context.Background(), account, other.RequestId, uuid.New(), subscriptions.OperatorDecisionInput{Decision: "approve"}); err != nil {
				t.Fatal("web decision depends on Telegram", err)
			}
			otherOp := operationFor(t, f, other.RequestId)
			wait(t, func() bool { return applied(f, otherOp) })
			assertNativePanel(t, f, op, otherOp)
		})
	}
}

func TestNativeTrialRestart(t *testing.T) {
	f := openMode(t, true)
	bot := &nativeBot{}
	_, stop := launchNative(t, f, bot, true, false)
	_, _, r := f.signup(t, nativeEmail("native-restart"))
	card := cardFor(t, bot, 101, r.RequestId)
	callbackID := uuid.NewString()
	bot.callback(101, card.ID, "a", r.RequestId, callbackID)
	wait(t, func() bool { return trialStatus(f, r.RequestId) == "approved" })
	op := operationFor(t, f, r.RequestId)
	var before string
	if err := f.env.Pool.QueryRow(context.Background(), `SELECT vpn_id::text||':'||sub_id||':'||panel_key FROM accounts WHERE id=(SELECT account_id FROM trial_operations WHERE id=$1)`, op).Scan(&before); err != nil {
		t.Fatal(err)
	}
	stop()
	// Reconstruct all application services and the HTTP/worker/Telegram lifecycle
	// against the same DB; the external fixture providers remain independent.
	queue, err := river.NewClient(riverpgxv5.New(f.env.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var handler http.Handler
	f.public = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(f.public.Close)
	f.cfg.HTTP.CabinetOrigin = f.public.URL
	f.svc = app.NewModules(f.env.Pool, f.env.Redis, queue, &f.cfg)
	handler = httpapi.New(f.svc, f.env.Pool, f.cfg.HTTP)
	launchNative(t, f, bot, true, true)
	bot.callback(101, card.ID, "a", r.RequestId, callbackID)
	wait(t, func() bool { return applied(f, op) })
	var after string
	var grants, operations int
	err = f.env.Pool.QueryRow(context.Background(), `SELECT vpn_id::text||':'||sub_id||':'||panel_key,(SELECT count(*) FROM trial_grants),(SELECT count(*) FROM trial_operations) FROM accounts WHERE id=(SELECT account_id FROM trial_operations WHERE id=$1)`, op).Scan(&after, &grants, &operations)
	if err != nil || after != before || grants != 1 || operations != 1 || operationFor(t, f, r.RequestId) != op {
		t.Fatal("restart regenerated grant/keys")
	}
	assertNativePanel(t, f, op)
}
