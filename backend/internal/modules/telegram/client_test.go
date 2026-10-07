package telegram

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
)

func clientFixture(t *testing.T, h *http.Client) *Runtime {
	t.Helper()
	c, err := NewClient("https://cabinet.example.test", &accounts.Service{}, &payments.Service{}, &notifications.Service{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{Enabled: true, Token: testToken, Operators: []int64{101}}, h, &actionRecorder{}, &outboxRecorder{}, c)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func clientMessage(actor int64, text string) botapi.Update {
	return botapi.Update{ID: 11, Message: &botapi.Message{ID: 7, Date: 1, From: &botapi.User{ID: actor, LanguageCode: "ru"}, Chat: botapi.Chat{ID: actor, Type: "private"}, Text: text}}
}
func TestClientTelegramStart(t *testing.T) {
	for _, tc := range []struct{ text, lang, want string }{
		{"/start 701", "ru", "https://t.me/fixture_bot?startapp=701"},
		{"/start Campaign_01-x", "en", "https://t.me/fixture_bot?startapp=Campaign_01-x"},
		{"/start@fixture_bot", "ru", "https://cabinet.example.test/mini-app/cabinet?lang=ru"},
		{"/support", "en", "https://cabinet.example.test/mini-app/cabinet/support?lang=en"},
	} {
		t.Run(tc.text+"/"+tc.lang, func(t *testing.T) {
			var sent []botapi.InlineKeyboard
			var menu map[string]any
			h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
				switch {
				case strings.HasSuffix(req.URL.Path, "getMe"):
					return jsonReply(map[string]any{"id": 123456789, "is_bot": true, "username": "fixture_bot", "has_main_web_app": true}), nil
				case strings.HasSuffix(req.URL.Path, "setChatMenuButton"):
					json.NewDecoder(req.Body).Decode(&menu)
					return jsonReply(true), nil
				case strings.HasSuffix(req.URL.Path, "sendMessage"):
					var in struct {
						Chat   int64                 `json:"chat_id"`
						Markup botapi.InlineKeyboard `json:"reply_markup"`
					}
					json.NewDecoder(req.Body).Decode(&in)
					sent = append(sent, in.Markup)
					return jsonReply(botapi.Message{ID: 70, Chat: botapi.Chat{ID: in.Chat}}), nil
				default:
					t.Fatalf("unexpected external method: %s", strings.TrimPrefix(req.URL.Path, "/bot"+testToken+"/"))
					return nil, nil
				}
			})}
			r := clientFixture(t, h)
			if err := r.clients.start(context.Background(), r.api, testToken); err != nil {
				t.Fatal(err)
			}
			u := clientMessage(701, tc.text)
			u.Message.From.LanguageCode = tc.lang
			if err := r.handle(context.Background(), u); err != nil {
				t.Fatal(err)
			}
			if len(sent) != 1 || len(sent[0].Rows) == 0 {
				t.Fatal("client did not receive a launch button")
			}
			button := sent[0].Rows[0][0]
			got := button.URL
			if button.WebApp != nil {
				got = button.WebApp.URL
			}
			if got != tc.want {
				t.Fatalf("launch route %q want %q", got, tc.want)
			}
			raw, _ := json.Marshal(menu)
			if !strings.Contains(string(raw), `"url":"https://cabinet.example.test/mini-app/cabinet"`) {
				t.Fatal("menu did not use configured cabinet")
			}
		})
	}
}
func TestClientTelegramPrivateActor(t *testing.T) {
	h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		t.Fatal("unsafe actor reached transport")
		return nil, nil
	})}
	for _, kind := range []string{"group", "other-chat", "bot", "no-user", "bad-id", "zero-date"} {
		t.Run(kind, func(t *testing.T) {
			r := clientFixture(t, h)
			r.clients.botID = 123456789
			r.clients.username = "fixture_bot"
			u := clientMessage(701, "/start")
			switch kind {
			case "group":
				u.Message.Chat.Type = "group"
			case "other-chat":
				u.Message.Chat.ID = 702
			case "bot":
				u.Message.From.IsBot = true
			case "no-user":
				u.Message.From = nil
			case "bad-id":
				u.Message.From.ID = -1
			case "zero-date":
				u.Message.Date = 0
			}
			if err := r.handle(context.Background(), u); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestClientTelegramInput(t *testing.T) {
	for _, text := range []string{"/start a b", "/start a/b", "/start " + strings.Repeat("x", 65), "/start \x00", "/start " + strings.Repeat("ж", 32)} {
		t.Run(text, func(t *testing.T) {
			var messages int
			h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
				var in struct {
					Chat   int64                  `json:"chat_id"`
					Markup *botapi.InlineKeyboard `json:"reply_markup"`
				}
				json.NewDecoder(req.Body).Decode(&in)
				if in.Markup != nil && len(in.Markup.Rows) > 0 {
					t.Fatal("invalid input got deep link")
				}
				messages++
				return jsonReply(botapi.Message{ID: 70, Chat: botapi.Chat{ID: in.Chat}}), nil
			})}
			r := clientFixture(t, h)
			r.clients.botID = 123456789
			r.clients.username = "fixture_bot"
			if err := r.handle(context.Background(), clientMessage(701, text)); err != nil {
				t.Fatal(err)
			}
			if messages != 1 {
				t.Fatal("invalid input did not receive safe refusal")
			}
		})
	}
}
func TestClientTelegramStartupIdentity(t *testing.T) {
	for _, who := range []map[string]any{
		{"id": 1, "is_bot": true, "username": "fixture_bot", "has_main_web_app": true},
		{"id": 123456789, "is_bot": false, "username": "fixture_bot", "has_main_web_app": true},
		{"id": 123456789, "is_bot": true, "username": "bad/name", "has_main_web_app": true},
		{"id": 123456789, "is_bot": true, "username": "fixture_bot", "has_main_web_app": false},
	} {
		h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(req.URL.Path, "getMe") {
				t.Fatal("invalid identity changed menu")
			}
			return jsonReply(who), nil
		})}
		r := clientFixture(t, h)
		if err := r.clients.start(context.Background(), r.api, testToken); err == nil {
			t.Fatal("unsafe bot identity accepted")
		}
	}
	for _, origin := range []string{"http://cabinet.example.test", "https://user:password@cabinet.example.test", "https://cabinet.example.test/x", "https://cabinet.example.test?x=y", "https://cabinet.example.test#x", "https://"} {
		if _, err := NewClient(origin, &accounts.Service{}, &payments.Service{}, &notifications.Service{}); err == nil {
			t.Fatal("unsafe origin accepted")
		}
	}
}
func TestClientTelegramOperatorCommand(t *testing.T) {
	h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "answerCallbackQuery") {
			return jsonReply(true), nil
		}
		var in struct {
			Chat int64 `json:"chat_id"`
		}
		json.NewDecoder(req.Body).Decode(&in)
		return jsonReply(botapi.Message{ID: 70, Chat: botapi.Chat{ID: in.Chat}}), nil
	})}
	r := clientFixture(t, h)
	r.clients.botID = 123456789
	r.clients.username = "fixture_bot"
	target := uuid.New()
	r.dispatcher.pending[101] = &confirmation{SupportAction: SupportAction{TargetID: target, ActorID: 101, Key: uuid.New()}, action: "s", messageID: 7}
	if err := r.handle(context.Background(), clientMessage(101, "/start")); err != nil {
		t.Fatal(err)
	}
	if r.dispatcher.pending[101] != nil {
		t.Fatal("command became support reason")
	}
	if err := r.handle(context.Background(), callback(target, 101, 7, "a")); err != nil {
		t.Fatal(err)
	}
	if len(r.dispatcher.actions.(*actionRecorder).decisions) != 1 {
		t.Fatal("client channel took operator approval")
	}
}
