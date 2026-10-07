package tests

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"fmt"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func nativeClientCallback(bot *nativeBot, actor int64, message nativeMessage, data string) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.sequence++
	bot.updates = append(bot.updates, map[string]any{"update_id": bot.sequence, "callback_query": map[string]any{"id": uuid.NewString(), "from": map[string]any{"id": actor, "language_code": "en"}, "message": map[string]any{"message_id": message.ID, "date": 1, "from": map[string]any{"id": 123456789, "is_bot": true}, "chat": map[string]any{"id": actor, "type": "private"}, "reply_markup": message.Markup}, "data": data}})
}

func TestNativeTrialClientTelegram(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := openMode(t, true, pub)
	ctx := context.Background()
	bot := &nativeBot{}
	_, stop := launchNative(t, f, bot, true, true, true)
	tg := int64(uuid.New().ID()) + 1000000000
	const source = "LegacyRef_42-x"
	bot.reason(tg, "/start "+source)
	wait(t, func() bool { return bot.message(tg, "Подписка,").ID > 0 })
	start := bot.message(tg, "Подписка,")
	var keyboard struct {
		Rows [][]struct {
			URL    string `json:"url"`
			Data   string `json:"callback_data"`
			WebApp *struct {
				URL string `json:"url"`
			} `json:"web_app"`
		} `json:"inline_keyboard"`
	}
	if json.Unmarshal(start.Markup, &keyboard) != nil || len(keyboard.Rows) == 0 || keyboard.Rows[0][0].URL != "https://t.me/fixture_bot?startapp="+source {
		t.Fatal("native start lost raw source")
	}
	var count int
	if err = f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM accounts").Scan(&count); err != nil || count != 0 {
		t.Fatal("start created an account before consent", err)
	}
	login := func(accepted bool, payload string) wire.MiniAppSessionResult {
		t.Helper()
		input := map[string]string{"init_data": testkit.SignedMiniAppData(key, 123456789, time.Now(), fmt.Sprintf(`{"id":%d,"first_name":"Owned client","language_code":"en"}`, tg), payload)}
		if accepted {
			input["accepted_terms_version"], input["accepted_privacy_version"] = "1", "1"
		}
		status, raw, _ := f.send(t, f.public.Client(), "POST", "/api/v1/telegram/mini-app/session", input, "", "", false)
		want := 409
		if accepted {
			want = 200
		}
		if status != want {
			t.Fatal("native signed Mini consent", status)
		}
		var out wire.MiniAppSessionResult
		if accepted && json.Unmarshal(raw, &out) != nil {
			t.Fatal("native Mini session output")
		}
		return out
	}
	login(false, source)
	if err = f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM accounts").Scan(&count); err != nil || count != 0 {
		t.Fatal("unsigned consent invented identity", err)
	}
	auth := login(true, source)
	second := login(true, "Different_source")
	if auth.Account.AccountId != second.Account.AccountId {
		t.Fatal("relaunch changed UUID")
	}
	var stored string
	if err = f.env.Pool.QueryRow(ctx, "SELECT telegram_start_param FROM accounts WHERE id=$1", auth.Account.AccountId).Scan(&stored); err != nil || stored != source {
		t.Fatal("signed first source not preserved", err)
	}
	status, raw, _ := f.send(t, f.public.Client(), "POST", "/api/v1/trial-requests", map[string]string{"comment": "Owned native client trial"}, auth.CsrfToken, uuid.NewString(), false, auth.SessionToken)
	var trial wire.TrialRequest
	if status != 201 || json.Unmarshal(raw, &trial) != nil {
		t.Fatal("native Mini trial request", status)
	}
	card := cardFor(t, bot, 101, trial.RequestId)
	bot.mu.Lock()
	bot.clientBlockedChat = tg
	bot.mu.Unlock()
	bot.callback(101, card.ID, "a", trial.RequestId, uuid.NewString())
	wait(t, func() bool { return trialStatus(f, trial.RequestId) == "approved" })
	op := operationFor(t, f, trial.RequestId)
	wait(t, func() bool { return applied(f, op) })
	assertNativePanel(t, f, op)
	operator, csrf, operatorTrial := f.signup(t, nativeEmail("client-channel-operator"))
	var actor uuid.UUID
	if err = f.env.Pool.QueryRow(ctx, "SELECT account_id FROM trial_requests WHERE id=$1", operatorTrial.RequestId).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	if status, _, _ = f.send(t, f.public.Client(), "POST", "/api/v1/support/messages", map[string]string{"text": "Owned customer request"}, auth.CsrfToken, uuid.NewString(), false, auth.SessionToken); status != 201 {
		t.Fatal("native customer support", status)
	}
	if status, _, _ = f.send(t, operator, "POST", "/api/v1/operator/clients/"+auth.Account.AccountId.String()+"/support/messages", map[string]string{"text": "PRIVATE_NATIVE_OPERATOR_REPLY"}, csrf, uuid.NewString(), false); status != 201 {
		t.Fatal("native operator reply", status)
	}
	const facts = `SELECT vpn_id::text||':'||sub_id||':'||panel_key FROM accounts WHERE id=$1`
	var before string
	if err = f.env.Pool.QueryRow(ctx, facts, auth.Account.AccountId).Scan(&before); err != nil {
		t.Fatal(err)
	}
	stop()
	if err = f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM client_telegram_deliveries WHERE account_id=$1 AND state='pending'", auth.Account.AccountId).Scan(&count); err != nil || count != 3 {
		t.Fatal("trial/reply facts not retained before restart", count, err)
	}
	if _, err = f.env.Pool.Exec(ctx, "UPDATE client_telegram_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE account_id=$1", auth.Account.AccountId); err != nil {
		t.Fatal(err)
	}
	queue, err := river.NewClient(riverpgxv5.New(f.env.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var handler http.Handler
	f.public = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(f.public.Close)
	f.cfg.HTTP.CabinetOrigin = f.public.URL
	f.svc = app.NewModules(f.env.Pool, f.env.Redis, queue, &f.cfg)
	f.svc.MiniApp = telegram.NewMiniApp(123456789, pub, f.svc.Accounts, time.Now)
	handler = httpapi.New(f.svc, f.env.Pool, f.cfg.HTTP)
	bot.mu.Lock()
	bot.clientBlockedChat = 0
	bot.mu.Unlock()
	launchNative(t, f, bot, true, true, true)
	wait(t, func() bool {
		var n int
		return f.env.Pool.QueryRow(ctx, "SELECT count(*) FROM client_telegram_deliveries WHERE account_id=$1 AND state='sent'", auth.Account.AccountId).Scan(&n) == nil && n == 3
	})
	var after string
	var grants, operations int
	if err = f.env.Pool.QueryRow(ctx, `SELECT vpn_id::text||':'||sub_id||':'||panel_key,(SELECT count(*) FROM trial_grants WHERE account_id=$1),(SELECT count(*) FROM trial_operations WHERE account_id=$1) FROM accounts WHERE id=$1`, auth.Account.AccountId).Scan(&after, &grants, &operations); err != nil || after != before || grants != 1 || operations != 1 {
		t.Fatal("delivery restart repeated or changed business access", err)
	}
	bot.mu.Lock()
	menus := append([]string(nil), bot.menus...)
	messages := append([]nativeMessage(nil), bot.messages...)
	bot.mu.Unlock()
	if len(menus) != 2 || menus[1] != f.cfg.HTTP.CabinetOrigin+"/mini-app/cabinet" {
		t.Fatal("restart retained obsolete deployment origin")
	}
	for _, m := range messages {
		if m.Chat == tg && (strings.Contains(m.Text, "PRIVATE_NATIVE") || strings.Contains(m.Text, before) || strings.Contains(m.Text, "SessionToken")) {
			t.Fatal("client notification leaked private business data")
		}
	}
	auth = login(true, "Still_later_source")
	status, raw, _ = f.send(t, f.public.Client(), "GET", "/api/v1/subscription/key", nil, "", "", false, auth.SessionToken)
	if status != 200 || !strings.Contains(string(raw), "subscription_url") {
		t.Fatal("native current owner lost connection after restart", status)
	}
	packed := fmt.Sprintf("subscription:pay_yoomoney:0:0:%d:1:30:15:100.00", tg)
	payment := "owned-native-" + uuid.NewString()
	hash := sha256.Sum256([]byte(payment))
	if _, err = f.env.Pool.Exec(ctx, `INSERT INTO legacy_payment_transactions(source_id,account_id,source_legacy_user_id,source_tg_id,source_payment_id,payment_id_hash,subscription,status,created_at,updated_at,imported_at) VALUES(11,$1,$2,$2,$3,$4,$5,'completed',now(),now(),now())`, auth.Account.AccountId, tg, payment, hash[:], packed); err != nil {
		t.Fatal(err)
	}
	markup, _ := json.Marshal(map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "Owned old invoice", "callback_data": packed}}}})
	nativeClientCallback(bot, tg, nativeMessage{ID: 800001, Chat: tg, Markup: markup}, packed)
	wait(t, func() bool {
		m := bot.message(tg, "Your subscription,")
		return strings.Contains(string(m.Markup), "legacy_source_id=11")
	})
	if status, raw, _ = f.send(t, f.public.Client(), "POST", "/api/v1/payment-history", map[string]string{"kind": "legacy", "legacy_source_id": "11"}, auth.CsrfToken, "", false, auth.SessionToken); status != 200 || !strings.Contains(string(raw), `"source_id":"11"`) {
		t.Fatal("native legacy route not readable by current owner", status)
	}
	notice := bot.message(tg, "There is an update in your cabinet.")
	if json.Unmarshal(notice.Markup, &keyboard) != nil || len(keyboard.Rows) != 2 || keyboard.Rows[0][0].WebApp == nil {
		t.Fatal("native delivered message lacks safe buttons")
	}
	nativeClientCallback(bot, tg, notice, keyboard.Rows[1][0].Data)
	wait(t, func() bool {
		m := bot.message(tg, "There is an update in your cabinet.")
		return string(m.Markup) == `{"inline_keyboard":[]}`
	})
}
