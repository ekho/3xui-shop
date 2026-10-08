package tests

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/campaigns"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/wire"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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
func nativeCampaign(t *testing.T, f *fixture) (uuid.UUID, campaigns.Campaign) {
	t.Helper()
	client, csrf, actor := f.signupAccount(t, nativeEmail("campaign-operator"))
	if f.svc.Accounts.ChangeOperatorRole(context.Background(), actor, true) != nil {
		t.Fatal("owned campaign operator unavailable")
	}
	status, body, _ := f.send(t, client, "POST", "/api/v1/operator/campaigns", wire.CampaignCreateInput{Name: "Owned native " + uuid.NewString(), Reason: "Native campaign acceptance"}, csrf, uuid.NewString(), false)
	var campaign campaigns.Campaign
	if status != 201 || json.Unmarshal(body, &campaign) != nil || campaign.Code == nil {
		t.Fatal("owned campaign creation failed", status)
	}
	return actor, campaign
}
func assertNativeCampaign(t *testing.T, f *fixture, actor uuid.UUID, campaign campaigns.Campaign, account uuid.UUID, channel string, granted int64) {
	t.Helper()
	ctx := context.Background()
	identity, err := f.svc.Accounts.Lookup(ctx, account)
	if err != nil || identity.ID != account || identity.RegistrationSourceCode == nil || *identity.RegistrationSourceCode != *campaign.Code {
		t.Fatal("native first campaign source missing or replaced")
	}
	tx, err := f.env.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("owned cohort read unavailable")
	}
	defer tx.Rollback(ctx)
	cohort, err := f.svc.Campaigns.CohortTx(ctx, tx, campaign.CampaignID)
	if err != nil || len(cohort) != 1 || cohort[0] != account {
		t.Fatal("native campaign cohort changed account UUID")
	}
	card, err := f.svc.Campaigns.Detail(ctx, actor, campaign.CampaignID)
	if err != nil || card.Statistics.Users != 1 || card.Statistics.Trials.TrialUsers != granted || card.Statistics.LegacyNameUsers != 0 || card.Statistics.LegacyTrialUsed != 0 || (channel == "web" && (card.Statistics.WebRegistrations != 1 || card.Statistics.TelegramRegistrations != 0)) || (channel == "telegram" && (card.Statistics.WebRegistrations != 0 || card.Statistics.TelegramRegistrations != 1)) {
		t.Fatal("native campaign statistics lost source or grant proof")
	}
}
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
	Markup   json.RawMessage
}
type nativeStarsEdit struct {
	Payer    int64  `json:"user_id"`
	Charge   string `json:"telegram_payment_charge_id"`
	Canceled bool   `json:"is_canceled"`
}
type nativeBot struct {
	mu                  sync.Mutex
	updates             []map[string]any
	messages            []nativeMessage
	sequence, messageID int64
	offline             bool
	rateLimitReplies    int
	clientBlockedChat   int64
	menus               []string
	starsInvoices       []string
	starsPreChecks      []bool
	starsPeriods        []int64
	starsEdits          []nativeStarsEdit
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
	case "getMe":
		b.mu.Unlock()
		return nativeReply(map[string]any{"id": 123456789, "is_bot": true, "username": "fixture_bot", "has_main_web_app": true}), nil
	case "setChatMenuButton":
		var menu struct {
			WebApp struct {
				URL string `json:"url"`
			} `json:"web_app"`
		}
		if err := json.Unmarshal(body["menu_button"], &menu); err != nil {
			b.mu.Unlock()
			return nil, err
		}
		b.menus = append(b.menus, menu.WebApp.URL)
		b.mu.Unlock()
		return nativeReply(true), nil
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
	case "createInvoiceLink":
		var currency, payload string
		var period int64
		json.Unmarshal(body["currency"], &currency)
		json.Unmarshal(body["payload"], &payload)
		json.Unmarshal(body["subscription_period"], &period)
		if currency != "XTR" || !strings.HasPrefix(payload, "stars:v1:") {
			b.mu.Unlock()
			return nil, errors.New("invalid owned native invoice")
		}
		b.starsInvoices = append(b.starsInvoices, payload)
		b.starsPeriods = append(b.starsPeriods, period)
		b.mu.Unlock()
		return nativeReply("https://t.me/$Owned_native_invoice"), nil
	case "editUserStarSubscription":
		var edit nativeStarsEdit
		raw, _ := json.Marshal(body)
		if json.Unmarshal(raw, &edit) != nil || edit.Payer <= 0 || edit.Charge == "" {
			b.mu.Unlock()
			return nil, errors.New("invalid owned native Stars edit")
		}
		b.starsEdits = append(b.starsEdits, edit)
		b.mu.Unlock()
		return nativeReply(true), nil
	case "answerPreCheckoutQuery":
		var accepted bool
		json.Unmarshal(body["ok"], &accepted)
		b.starsPreChecks = append(b.starsPreChecks, accepted)
		b.mu.Unlock()
		return nativeReply(true), nil
	case "answerCallbackQuery":
		if b.rateLimitReplies > 0 {
			b.rateLimitReplies--
			b.mu.Unlock()
			return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":429,"parameters":{"retry_after":1}}`))}, nil
		}
		b.mu.Unlock()
		return nativeReply(true), nil
	case "sendMessage", "editMessageText", "editMessageReplyMarkup":
		if method == "sendMessage" && in.Chat == b.clientBlockedChat {
			b.mu.Unlock()
			return nil, errors.New("owned client transport unavailable")
		}
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
				b.messages[i].Markup = body["reply_markup"]
				found = true
			}
		}
		if !found {
			b.messages = append(b.messages, nativeMessage{ID: id, Chat: in.Chat, Text: in.Text, Markup: body["reply_markup"]})
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
func launchNative(t *testing.T, f *fixture, bot *nativeBot, enabled, provision bool, clients ...bool) (*telegram.Runtime, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	workers := river.NewWorkers()
	river.AddWorker(workers, &notifications.MailWorker{Service: f.svc.MailDelivery})
	river.AddWorker(workers, &vpn.ProvisionWorker{Service: f.svc.VPN})
	river.AddWorker(workers, &vpn.AccessWorker{Service: f.svc.VPN})
	river.AddWorker(workers, &vpn.MonthlyResetWorker{Service: f.svc.VPN})
	river.AddWorker(workers, &payments.PurchaseWorker{Service: f.svc.Payments})
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
	origin := ""
	if len(clients) > 0 && clients[0] {
		origin = f.cfg.HTTP.CabinetOrigin
	}
	tg, err := app.NewTelegram(telegram.Config{Enabled: enabled, Token: "123456789:abcdefghijklmnopqrstuvwxyz012345678", Operators: f.cfg.Accounts.Operators}, f.svc, origin, &http.Client{Transport: bot})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := make(chan error, 2)
	svc, server := f.svc, f.public.Config
	var schedulerDone sync.WaitGroup
	schedulerDone.Add(2)
	go func() { defer schedulerDone.Done(); scheduler <- svc.VPN.RunMonthlyResetScheduler(ctx) }()
	go func() { defer schedulerDone.Done(); scheduler <- svc.Payments.RunStarsSubscriptionScheduler(ctx) }()
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

func TestNativeTrialIdentityRecovery(t *testing.T) {
	f := openMode(t, true)
	ctx := context.Background()
	bot := &nativeBot{}
	_, stop := launchNative(t, f, bot, false, true)
	operator, csrf, operatorTrial := f.signup(t, nativeEmail("identity-operator"))
	var actor uuid.UUID
	if err := f.env.Pool.QueryRow(ctx, `SELECT account_id FROM trial_requests WHERE id=$1`, operatorTrial.RequestId).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	// Unique signed-safe fixture ID, unrelated to any live Telegram person.
	tgID := int64(uuid.New().ID()) + 1000000000
	status, body, _ := f.send(t, operator, "POST", "/api/v1/operator/clients/trial", map[string]string{"telegram_id": fmt.Sprint(tgID), "display_name": "Owned identity recovery", "locale": "en"}, csrf, uuid.NewString(), false)
	var created struct {
		Client struct {
			AccountID uuid.UUID `json:"account_id"`
		} `json:"client"`
		OperationID uuid.UUID `json:"operation_id"`
	}
	if status != 201 || json.Unmarshal(body, &created) != nil || created.Client.AccountID == uuid.Nil {
		t.Fatal("owned Telegram activation", status)
	}
	wait(t, func() bool { return applied(f, created.OperationID) })
	assertNativePanel(t, f, created.OperationID)
	const facts = `SELECT (to_jsonb(a)-ARRAY['kind','email_key','password_hash','verified_at','terms_version','privacy_version','policy_accepted_at','credential_version','telegram_id','telegram_login_disabled','original_kind'])::text FROM accounts a WHERE id=$1`
	var before, after string
	if err := f.env.Pool.QueryRow(ctx, facts, created.Client.AccountID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	old, raw, err := f.svc.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: tgID, DisplayName: "Owned identity recovery", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	// Consent and first payload were set by the explicit Mini session, before capturing invariants.
	if err = f.env.Pool.QueryRow(ctx, facts, created.Client.AccountID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	email := nativeEmail("identity-recovered")
	status, body, _ = f.send(t, operator, "POST", "/api/v1/operator/clients/"+created.Client.AccountID.String()+"/identity-recovery", map[string]any{"email": email, "current_password": "fixture password with Unicode ✨", "reason": "Owned support proof", "confirmed": true}, csrf, uuid.NewString(), false)
	var proof wire.IdentityRecoveryAccepted
	if status != 202 || json.Unmarshal(body, &proof) != nil {
		t.Fatal("native recovery request", status)
	}
	wait(t, func() bool {
		for _, letter := range f.letters(t, email) {
			if strings.Contains(letter, "To: "+email) && strings.Contains(letter, "/recover-account") {
				return true
			}
		}
		return false
	})
	var token string
	for _, letter := range f.letters(t, email) {
		if strings.Contains(letter, "To: "+email) && strings.Contains(letter, "/recover-account") {
			match := regexp.MustCompile(`#token=([A-Za-z0-9_-]{43})`).FindStringSubmatch(letter)
			if len(match) == 2 {
				token = match[1]
			}
		}
	}
	if token == "" {
		t.Fatal("recovery purpose missing from TLS mail")
	}
	if _, err = f.svc.Accounts.AuthenticateTelegram(ctx, raw, false); err == nil {
		t.Fatal("old Mini not quarantined")
	}
	stop()
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
	launchNative(t, f, bot, false, true)
	if _, err = f.svc.Accounts.AuthenticateTelegram(ctx, raw, false); err == nil {
		t.Fatal("restart removed quarantine")
	}
	anonymous := f.public.Client()
	status, _, reply := f.send(t, anonymous, "POST", "/api/v1/auth/identity-recovery", map[string]string{"token": token, "new_password": "fixture recovered password ✨", "accepted_terms_version": "1", "accepted_privacy_version": "1"}, "", "", false)
	if status != 200 || len(reply.Cookies()) != 0 {
		t.Fatal("recovery must be explicit without automatic login", status)
	}
	if err = f.env.Pool.QueryRow(ctx, facts, created.Client.AccountID).Scan(&after); err != nil || before != after {
		t.Fatal("restart/recovery changed unrelated activation facts", err)
	}
	recovered, err := f.svc.Accounts.GetIdentity(ctx, created.Client.AccountID)
	if err != nil || old.Account.ID != created.Client.AccountID || recovered.SourceKind != "telegram" || !recovered.IndependentLogin || recovered.TelegramLinked {
		t.Fatal("recovery lost original account/source", err)
	}
	if _, _, err = f.svc.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: tgID, DisplayName: "Old Telegram", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"}); err == nil {
		t.Fatal("retired identity recreated account")
	}
	assertNativePanel(t, f, created.OperationID)
	status, body, _ = f.send(t, anonymous, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "fixture recovered password ✨"}, "", "", false)
	var login wire.LoginResult
	if status != 200 || json.Unmarshal(body, &login) != nil || login.Account.AccountId != created.Client.AccountID {
		t.Fatal("new credential login changed owner", status)
	}
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
	actor, campaign := nativeCampaign(t, f)
	owner, _, r := f.signup(t, nativeEmail("native-restart"), *campaign.Code)
	status, body, _ := f.send(t, owner, "GET", "/api/v1/me", nil, "", "", false)
	var identity wire.AccountResult
	if status != 200 || json.Unmarshal(body, &identity) != nil {
		t.Fatal("owned native identity read failed")
	}
	assertNativeCampaign(t, f, actor, campaign, identity.Account.AccountId, "web", 0)
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
	assertNativeCampaign(t, f, actor, campaign, identity.Account.AccountId, "web", 1)
}

func TestNativeTrialReports(t *testing.T) {
	f := openMode(t, true)
	ctx := context.Background()
	bot := &nativeBot{}
	_, stop := launchNative(t, f, bot, true, true)
	actor, campaign := nativeCampaign(t, f)
	_, _, request := f.signup(t, nativeEmail("native-statistics"), *campaign.Code)
	card := cardFor(t, bot, 101, request.RequestId)
	bot.callback(101, card.ID, "a", request.RequestId, "")
	wait(t, func() bool { return trialStatus(f, request.RequestId) == "approved" })
	op := operationFor(t, f, request.RequestId)
	wait(t, func() bool { return applied(f, op) })
	assertNativePanel(t, f, op)
	var account uuid.UUID
	if f.env.Pool.QueryRow(ctx, `SELECT account_id FROM trial_operations WHERE id=$1`, op).Scan(&account) != nil {
		t.Fatal("native report grant owner missing")
	}
	assertNativeCampaign(t, f, actor, campaign, account, "web", 1)
	// Finish writers before proving report reads preserve the persisted state.
	stop()
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
	operator := f.public.Client()
	operator.Jar, _ = cookiejar.New(nil)
	identity, err := f.svc.Accounts.Lookup(ctx, actor)
	if err != nil || identity.EmailKey == nil {
		t.Fatal("owned report operator unavailable")
	}
	status, body, _ := f.send(t, operator, "POST", "/api/v1/auth/login", map[string]string{"email": *identity.EmailKey, "password": "fixture password with Unicode ✨"}, "", "", false)
	var login wire.LoginResult
	if status != 200 || json.Unmarshal(body, &login) != nil || login.Account.AccountId != actor {
		t.Fatal("owned report login unavailable", status)
	}
	status, body, _ = f.send(t, operator, "POST", "/api/v1/operator/campaigns", wire.CampaignCreateInput{Name: "Owned empty " + uuid.NewString(), Reason: "Native empty report"}, login.CsrfToken, uuid.NewString(), false)
	var empty campaigns.Campaign
	if status != 201 || json.Unmarshal(body, &empty) != nil {
		t.Fatal("owned empty report cohort unavailable", status)
	}
	snapshot := func() [32]byte {
		t.Helper()
		var state string
		if err := f.env.Pool.QueryRow(ctx, `SELECT json_build_array(
		 (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a),
		 (SELECT jsonb_agg(to_jsonb(a) ORDER BY account_id) FROM trial_grants a),
		 (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM trial_operations a),
		 (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM access_operations a),
		 (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM audit_events a),
		 (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM river_job a))::text`).Scan(&state); err != nil {
			t.Fatal("native read-only report proof unavailable")
		}
		return sha256.Sum256([]byte(state))
	}
	before := snapshot()
	read := func(id *uuid.UUID) auditreports.StatisticsReport {
		t.Helper()
		status, body, _ := f.send(t, operator, "POST", "/api/v1/operator/reports/statistics", wire.StatisticsInput{CampaignId: id}, login.CsrfToken, "", false)
		var out auditreports.StatisticsReport
		if status != 200 || json.Unmarshal(body, &out) != nil || out.Version != "statistics-v1" || out.DatabaseObservedAt.IsZero() || out.Activity.ObservedAt.IsZero() || out.InfrastructureScope != "global" {
			t.Fatal("native report response invalid", status)
		}
		if (id == nil) != (out.CampaignID == nil) || (id != nil && *id != *out.CampaignID) {
			t.Fatal("native report scope changed")
		}
		return out
	}
	global, cohort, zero := read(nil), read(&campaign.CampaignID), read(&empty.CampaignID)
	if global.Users != 2 || cohort.Users != 1 || zero.Users != 0 || global.Trials.TrialUsers != 1 || cohort.Trials.TrialUsers != 1 || zero.Trials.TrialUsers != 0 || global.Payments.PaidOrders != 0 || cohort.Payments.PaidOrders != 0 || zero.Payments.PaidOrders != 0 || global.Conversions.TrialPercent == nil || *global.Conversions.TrialPercent != "50.00" || cohort.Conversions.TrialPercent == nil || *cohort.Conversions.TrialPercent != "100.00" || zero.Conversions.TrialPercent != nil || zero.Activity.ActiveUsers == nil || *zero.Activity.ActiveUsers != 0 {
		t.Fatal("native report lost UUID, grant or empty cohort")
	}
	if os.Getenv("NATIVE_DOCKER_STATE") != "" {
		for _, report := range []auditreports.StatisticsReport{global, cohort} {
			if report.Activity.ActiveUsers == nil || *report.Activity.ActiveUsers != 1 || report.Activity.UnknownUsers != 0 || len(report.Servers) != 1 || report.Servers[0].Availability != "available" || report.Servers[0].Clients == nil || *report.Servers[0].Clients < 1 || report.Servers[0].ErrorCode != nil {
				t.Fatal("actual 3X-UI3.7.0 bulk report did not confirm native access")
			}
		}
		if global.Activity.KnownInactiveUsers != 1 || cohort.Activity.KnownInactiveUsers != 0 || zero.Servers[0].Clients == nil || *zero.Servers[0].Clients < 1 {
			t.Fatal("native activity or global infrastructure scope lost")
		}
	} else if global.Activity.ActiveUsers != nil || cohort.Activity.UnknownUsers != 1 || global.Servers[0].Availability != "unavailable" {
		t.Fatal("fixture without bulk API must retain unknown activity")
	}
	if before != snapshot() {
		t.Fatal("native report changed persisted accounts, grants, access, audit or jobs")
	}
	if os.Getenv("NATIVE_DOCKER_STATE") != "" {
		assertNativePanel(t, f, op)
	} else {
		f.panel.mu.Lock()
		defer f.panel.mu.Unlock()
		// Each of the three reads reaches the fake's unsupported bulk GET.
		if f.panel.adds != 1 || f.panel.forbidden != 3 || len(f.panel.clients) != 1 {
			t.Fatal("unavailable report changed the owned fake client")
		}
	}
}
