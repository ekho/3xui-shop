package telegram

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// Catches starting a second poller for one bot ID, or accepting plaintext and
// noncanonical destinations that cannot identify the configured forum.
func TestSupportRuntimeConfiguration(t *testing.T) {
	const token = "973:abcdefghijklmnopqrstuvwx"
	file := filepath.Join(t.TempDir(), "support-token")
	if err := os.WriteFile(file, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"valid", "disabled", "plaintext", "positive_group", "zero_group", "noncanonical", "overflow", "same_bot", "other_bot"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("SUPPORT_TELEGRAM_ENABLED", "true")
			t.Setenv("SUPPORT_BOT_TOKEN_FILE", file)
			t.Setenv("SUPPORT_BOT_TOKEN", "")
			t.Setenv("SUPPORT_GROUP_ID", "-10074001")
			switch kind {
			case "disabled":
				t.Setenv("SUPPORT_TELEGRAM_ENABLED", "false")
				t.Setenv("SUPPORT_BOT_TOKEN_FILE", "")
			case "plaintext":
				t.Setenv("SUPPORT_BOT_TOKEN", token)
			case "positive_group":
				t.Setenv("SUPPORT_GROUP_ID", "10074001")
			case "zero_group":
				t.Setenv("SUPPORT_GROUP_ID", "0")
			case "noncanonical":
				t.Setenv("SUPPORT_GROUP_ID", "-010074001")
			case "overflow":
				t.Setenv("SUPPORT_GROUP_ID", "-4503599627370496")
			}
			cfg, err := LoadSupportConfig()
			valid := kind == "valid" || kind == "disabled" || kind == "same_bot" || kind == "other_bot"
			if valid {
				if err != nil {
					t.Fatal("valid config rejected", err)
				}
			} else {
				if err == nil {
					t.Fatal("unsafe support config accepted", kind)
				}
				return
			}
			if kind == "disabled" {
				if cfg.Enabled || cfg.Token != "" {
					t.Fatal("disabled channel reads secrets")
				}
				return
			}
			if kind == "same_bot" || kind == "other_bot" {
				cfg = SupportConfig{Enabled: true, Token: token, GroupID: -10074001}
			}
			if !cfg.Enabled || cfg.Token != token || cfg.GroupID != -10074001 {
				t.Fatal("support configuration lost")
			}
			main := Config{Enabled: true, Token: "974:abcdefghijklmnopqrstuvwx", Operators: []int64{732}}
			if kind == "same_bot" {
				main.Token = "973:zyxwvutsrqponmlkjihgfedcba"
			}
			err = ValidatePollingBots(main, cfg)
			if kind == "same_bot" {
				if err == nil {
					t.Fatal("same bot ID admitted two pollers")
				}
			} else if err != nil {
				t.Fatal("independent channels rejected", err)
			}
		})
	}
}

func supportRuntimeFixture(t *testing.T) (*supportBridge, *testkit.Env, uuid.UUID) {
	t.Helper()
	e := testkit.Open(t)
	ctx := context.Background()
	a := accounts.New(e.Pool, e.Redis, nil, accounts.Config{TermsVersion: "1", PrivacyVersion: "1", RateNamespace: uuid.NewString(), Now: e.Clock})
	c, _, err := a.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 731, DisplayName: "owned customer", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	op := uuid.New()
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id) VALUES($1,'operator@example.test','ru','fixture',$2,$3,'aaaaaaaaaaaaaaaa',$4,'1','1',732)`, op, e.Clock(), uuid.New(), "acct_"+op.String()); err != nil {
		t.Fatal(err)
	}
	if a.ChangeOperatorRole(ctx, op, true) != nil {
		t.Fatal("operator")
	}
	owner := support.New(e.Pool, e.Redis, a, uuid.NewString(), e.Clock, nil)
	access := subscriptions.New(e.Pool, a, nil, nil, nil, func() subscriptions.Config { return subscriptions.Config{} }, e.Clock)
	h := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		var in struct {
			Chat   int64                  `json:"chat_id"`
			Thread int64                  `json:"message_thread_id"`
			Parse  string                 `json:"parse_mode"`
			Text   string                 `json:"text"`
			Markup *botapi.InlineKeyboard `json:"reply_markup"`
		}
		if err = json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatal("request JSON", err)
		}
		switch path.Base(r.URL.Path) {
		case "getMe":
			return jsonReply(botapi.User{ID: 973, IsBot: true, Username: "fixture_bot"}), nil
		case "getChat":
			return jsonReply(botapi.Chat{ID: -10074001, Type: "supergroup", IsForum: true}), nil
		case "getChatMember":
			return jsonReply(botapi.ChatMember{User: botapi.User{ID: 973, IsBot: true}, Status: "administrator", CanManageTopics: true}), nil
		case "createForumTopic":
			return jsonReply(map[string]int64{"message_thread_id": 888}), nil
		case "copyMessage":
			return jsonReply(map[string]int64{"message_id": 91}), nil
		case "sendMessage":
			if in.Chat < 0 && (in.Parse != "" || strings.Contains(in.Text, "private guest body")) {
				t.Fatal("General privacy")
			}
			kind := "private"
			if in.Chat < 0 {
				kind = "supergroup"
			}
			return jsonReply(botapi.Message{ID: 92, Chat: botapi.Chat{ID: in.Chat, Type: kind}, ThreadID: in.Thread}), nil
		case "answerCallbackQuery":
			return jsonReply(true), nil
		default:
			t.Fatal("unexpected support method", path.Base(r.URL.Path))
			return nil, nil
		}
	})}
	r, err := NewSupport(SupportConfig{Enabled: true, Token: "973:abcdefghijklmnopqrstuvwx", GroupID: -10074001}, h, "https://cabinet.example.test", a, owner, access)
	if err != nil || r.support.start(ctx) != nil {
		t.Fatal("support startup", err)
	}
	return r.support, e, c.Account.ID
}

func TestUnavailableSupportRuntime(t *testing.T) {
	r := NewUnavailableSupport()
	state := r.State()
	if !state.Enabled || !state.Degraded || state.Code != "INVALID_CONFIGURATION" || r.api != nil {
		t.Fatal("invalid configuration was hidden or allowed provider access")
	}
	if safeCode(r.Run(context.Background())) != "INVALID_CONFIGURATION" || r.State() != state {
		t.Fatal("degraded startup state was lost")
	}
}

func TestSupportRuntimeDocumentedCommandForms(t *testing.T) {
	b, e, customer := supportRuntimeFixture(t)
	vpnOwner := vpn.New(e.Pool, b.authority, nil, func() vpn.Settings { return vpn.Settings{PanelID: "owned-test"} }, e.Clock, nil, vpn.PurchaseHooks{})
	b.access = subscriptions.New(e.Pool, b.authority, nil, vpnOwner, nil, func() subscriptions.Config { return subscriptions.Config{} }, e.Clock)
	ctx := context.Background()
	if _, _, err := b.owner.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "owned runtime command", "", nil); err != nil {
		t.Fatal(err)
	}
	create, err := b.owner.ClaimTelegramDelivery(ctx)
	if err != nil || create == nil || b.owner.DeliverTelegram(ctx, *create, b.send) != nil {
		t.Fatal("owned topic", err)
	}
	job, err := b.owner.ClaimTelegramDelivery(ctx)
	if err != nil || job == nil || b.owner.DeliverTelegram(ctx, *job, func(context.Context, support.TelegramPart) (support.TelegramOutcome, error) {
		return support.TelegramOutcome{Status: "failed", Code: "FORBIDDEN"}, nil
	}) != nil {
		t.Fatal("owned failed delivery", err)
	}
	for i, form := range []struct{ text, action, reason string }{
		{"/reset Owned runtime reason", "reset", "Owned runtime reason"},
		{"/retry " + job.ID.String(), "retry", "Telegram /retry (generated)"},
	} {
		m := &botapi.Message{ID: int64(40 + i), Date: 1, From: &botapi.User{ID: 732}, Chat: botapi.Chat{ID: b.cfg.GroupID, Type: "supergroup"}, ThreadID: 888, Text: form.text}
		if err := b.handle(ctx, botapi.Update{ID: int64(20 + i), Message: m}); err != nil {
			t.Fatal("documented command runtime", err)
		}
		var raw []byte
		if e.Pool.QueryRow(ctx, `SELECT result FROM support_telegram_receipts WHERE message_id=$1 AND action='command'`, m.ID).Scan(&raw) != nil {
			t.Fatal("documented command never reached owner", form.action)
		}
		var receipt support.TelegramCommandReceipt
		if json.Unmarshal(raw, &receipt) != nil || receipt.Input.Action != form.action || receipt.Input.Reason != form.reason || form.action == "retry" && (!receipt.AwaitingConfirmation || !receipt.Input.GeneratedReason) {
			t.Fatal("command argument/confirmation lost", form.action)
		}
	}
}

func TestSupportRuntimeRouting(t *testing.T) {
	t.Run("too-long-feedback", func(t *testing.T) {
		b, e, _ := supportRuntimeFixture(t)
		ctx := context.Background()
		calls := 0
		b.api = botapi.New(b.cfg.Token, &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
			if path.Base(r.URL.Path) != "sendMessage" {
				t.Fatal("unexpected length feedback method")
			}
			var in struct {
				Text   string `json:"text"`
				ChatID int64  `json:"chat_id"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.ChatID != 731 || !strings.Contains(in.Text, "4000") || strings.Contains(in.Text, strings.Repeat("ж", 10)) {
				t.Fatal("private message body leaked as feedback")
			}
			calls++
			return jsonReply(botapi.Message{ID: 92, Chat: botapi.Chat{ID: 731, Type: "private"}}), nil
		})})
		if err := b.handle(ctx, clientMessage(731, strings.Repeat("ж", 4001))); err != nil {
			t.Fatal(err)
		}
		var messages int
		if calls != 1 || e.Pool.QueryRow(ctx, `SELECT count(*) FROM support_messages`).Scan(&messages) != nil || messages != 0 {
			t.Fatal("oversized message lacked feedback or was truncated", calls)
		}
	})
	t.Run("guest-and-start", func(t *testing.T) {
		b, e, _ := supportRuntimeFixture(t)
		ctx := context.Background()
		if err := b.handle(ctx, clientMessage(913, "private guest body")); err != nil {
			t.Fatal(err)
		}
		if err := b.handle(ctx, clientMessage(914, "/start")); err != nil {
			t.Fatal(err)
		}
		var topics, accountsN, messages int
		if e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM support_telegram_topics),(SELECT count(*) FROM accounts),(SELECT count(*) FROM support_messages)`).Scan(&topics, &accountsN, &messages) != nil || topics != 1 || accountsN != 2 || messages != 0 {
			t.Fatal("runtime guest isolated", topics, accountsN, messages)
		}
		for i := 0; i < 2; i++ {
			job, err := b.owner.ClaimTelegramDelivery(ctx)
			if err != nil || job == nil {
				t.Fatal("guest relay", err)
			}
			if b.owner.DeliverTelegram(ctx, *job, b.send) != nil {
				t.Fatal("guest wire")
			}
		}
	})
	t.Run("general-pending", func(t *testing.T) {
		b, e, _ := supportRuntimeFixture(t)
		ctx := context.Background()
		u := clientMessage(732, "/pending@fixture_bot")
		u.Message.Chat = botapi.Chat{ID: -10074001, Type: "supergroup"}
		if err := b.handle(ctx, u); err != nil {
			t.Fatal(err)
		}
		var receipts, topics int
		if e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM support_telegram_receipts),(SELECT count(*) FROM support_telegram_topics)`).Scan(&receipts, &topics) != nil || receipts != 1 || topics != 0 {
			t.Fatal("General missing source receipt", receipts, topics)
		}
		job, err := b.owner.ClaimTelegramDelivery(ctx)
		if err != nil || job == nil {
			t.Fatal("General ACK", err)
		}
		if err = b.owner.DeliverTelegram(ctx, *job, b.send); err != nil {
			t.Fatal("General wire", err)
		}
		var sent bool
		if e.Pool.QueryRow(ctx, `SELECT status='sent' FROM support_telegram_deliveries WHERE id=$1`, job.ID).Scan(&sent) != nil || !sent {
			t.Fatal("General transport unsupported")
		}
	})
	t.Run("native-anonymous", func(t *testing.T) {
		b, e, customer := supportRuntimeFixture(t)
		ctx := context.Background()
		if _, _, err := b.owner.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "owned native topic", "", nil); err != nil {
			t.Fatal(err)
		}
		job, err := b.owner.ClaimTelegramDelivery(ctx)
		if err != nil || job == nil {
			t.Fatal(err)
		}
		if b.owner.DeliverTelegram(ctx, *job, b.send) != nil {
			t.Fatal("native topic")
		}
		var message botapi.Message
		if json.Unmarshal([]byte(`{"message_id":81,"message_thread_id":888,"date":1,"chat":{"id":-10074001,"type":"supergroup"},"forum_topic_closed":{}}`), &message) != nil {
			t.Fatal("fixture")
		}
		if err = b.handle(ctx, botapi.Update{ID: 9, Message: &message}); err != nil {
			t.Fatal(err)
		}
		var closed bool
		if e.Pool.QueryRow(ctx, `SELECT status='closed' FROM support_conversations WHERE account_id=$1`, customer).Scan(&closed) != nil || !closed {
			t.Fatal("native event dropped")
		}
		var invented bool
		if e.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_system_events WHERE support_telegram->>'kind'='topic_closed' AND support_telegram->>'actor_account_id' IS NOT NULL)`).Scan(&invented) != nil || invented {
			t.Fatal("anonymous actor invented")
		}
	})
}

func TestSupportRuntimeCallbacks(t *testing.T) {
	b, e, customer := supportRuntimeFixture(t)
	ctx := context.Background()
	if _, _, err := b.owner.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "callback fixture", "", nil); err != nil {
		t.Fatal(err)
	}
	create, err := b.owner.ClaimTelegramDelivery(ctx)
	if err != nil || create == nil {
		t.Fatal(err)
	}
	if err = b.owner.DeliverTelegram(ctx, *create, func(context.Context, support.TelegramPart) (support.TelegramOutcome, error) {
		return support.TelegramOutcome{Status: "unknown"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	var topic uuid.UUID
	if e.Pool.QueryRow(ctx, `SELECT id FROM support_telegram_topics WHERE account_id=$1`, customer).Scan(&topic) != nil {
		t.Fatal("topic")
	}
	op, _, err := b.authority.ResolveTelegramContext(ctx, 732)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := b.owner.BeginTelegramCommand(op, support.TelegramSource{BotID: 973, GroupID: -10074001, ChatID: -10074001, MessageID: 71, UpdateID: 1, ActorID: 732, Action: "command", Digest: [32]byte{1}}, topic, support.TelegramCommandInput{Action: "bind", ThreadID: 888, Reason: "owned callback"})
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := b.owner.ClaimTelegramDelivery(ctx)
	if err != nil || prompt == nil {
		t.Fatal(err)
	}
	if err = b.owner.DeliverTelegram(ctx, *prompt, func(context.Context, support.TelegramPart) (support.TelegramOutcome, error) {
		return support.TelegramOutcome{Status: "sent", MessageID: 91}, nil
	}); err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id) VALUES($1,'other-operator@example.test','ru','fixture',$2,$3,'bbbbbbbbbbbbbbbb',$4,'1','1',734)`, other, e.Clock(), uuid.New(), "acct_"+other.String()); err != nil {
		t.Fatal(err)
	}
	if err = b.authority.ChangeOperatorRole(ctx, other, true); err != nil {
		t.Fatal(err)
	}
	message := botapi.Message{ID: 91, Date: 1, From: &botapi.User{ID: 973, IsBot: true}, Chat: botapi.Chat{ID: -10074001, Type: "supergroup"}}
	callback := botapi.Callback{ID: "owned-cancel", From: botapi.User{ID: 732}, Message: &message, Data: "sp1:x:" + intent.ID.String()}
	for _, kind := range []string{"other-bot", "inaccessible", "other-operator", "guest"} {
		m := message
		c := callback
		c.Message = &m
		c.ID = kind
		switch kind {
		case "other-bot":
			m.From = &botapi.User{ID: 974, IsBot: true}
		case "inaccessible":
			m.Date = 0
		case "other-operator":
			c.From.ID = 734
		case "guest":
			c.From.ID = 913
		}
		if err = b.handle(ctx, botapi.Update{ID: 3, Callback: &c}); err != nil {
			t.Fatal("invalid callback handling", err)
		}
		var pending bool
		if e.Pool.QueryRow(ctx, `SELECT (result->>'AwaitingConfirmation')::boolean FROM support_telegram_receipts WHERE id=$1`, intent.ID).Scan(&pending) != nil || !pending {
			t.Fatal("wrong callback changed intent", kind)
		}
	}
	for i := 0; i < 2; i++ {
		if err = b.handle(ctx, botapi.Update{ID: int64(4 - i), Callback: &callback}); err != nil {
			t.Fatal("valid callback/replay", err)
		}
	}
	var cancelled bool
	var receipts int
	if e.Pool.QueryRow(ctx, `SELECT result->'Result'->>'Status'='cancelled' FROM support_telegram_receipts WHERE id=$1`, intent.ID).Scan(&cancelled) != nil || !cancelled {
		t.Fatal("runtime cancellation not persisted")
	}
	if e.Pool.QueryRow(ctx, `SELECT count(*) FROM support_telegram_receipts`).Scan(&receipts) != nil || receipts != 2 {
		t.Fatal("callback duplicated receipt", receipts)
	}
	for _, data := range []string{"approval:approve:731", "comp_reset_731"} {
		c := callback
		c.Data = data
		c.ID = "legacy"
		if err = b.handle(ctx, botapi.Update{ID: 8, Callback: &c}); err != nil {
			t.Fatal("legacy navigation", err)
		}
	}
	var changes int
	if e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM trial_requests)+(SELECT count(*) FROM trial_operations)+(SELECT count(*) FROM support_telegram_topics WHERE status='ready')`).Scan(&changes) != nil || changes != 0 {
		t.Fatal("legacy callback reinterpreted as mutation")
	}
}
