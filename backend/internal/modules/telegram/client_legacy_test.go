package telegram

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"net/http"
	"strings"
	"testing"
)

func legacyCallback(t *testing.T, data string) botapi.Update {
	t.Helper()
	var out botapi.Update
	raw, _ := json.Marshal(map[string]any{"update_id": 12, "callback_query": map[string]any{"id": "owned-callback", "from": map[string]any{"id": 701, "language_code": "ru"}, "data": data, "message": map[string]any{"message_id": 42, "date": 1, "from": map[string]any{"id": 123456789, "is_bot": true}, "chat": map[string]any{"id": 701, "type": "private"}, "reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "Old button", "callback_data": data}}}}}}})
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestClientTelegramLegacyNavigation(t *testing.T) {
	for _, tc := range []struct {
		data, destination string
		clear             bool
	}{
		{"start", "/mini-app/cabinet?lang=ru", false}, {"main_menu", "/mini-app/cabinet?lang=ru", false}, {"profile", "/mini-app/cabinet?lang=ru", false},
		{"show_key", "/mini-app/cabinet?lang=ru#connection-title", false}, {"download", "/mini-app/cabinet?lang=ru#connection-title", false},
		{"platform_android", "/mini-app/cabinet?lang=ru&platform=android#connection-title", false}, {"download_show_qr", "/mini-app/cabinet?lang=ru#connection-title", false},
		{"support", "/mini-app/cabinet/support?lang=ru", false}, {"vpn_not_working", "/mini-app/cabinet/support?lang=ru", false}, {"how_to_connect", "/mini-app/cabinet?lang=ru#connection-title", false},
		{"subscription:subscription:0:0:0:0:0:0:0", "/mini-app/catalogue?lang=ru", false},
		{"subscription:extend:0:0:701:0:0:0:0", "/mini-app/cabinet/renew?lang=ru", false},
		{"subscription:devices:1:0:701:0:0:0:0", "/mini-app/cabinet/renew?lang=ru", false},
		{"subscription:duration:0:1:701:0:0:0:0", "/mini-app/cabinet/change-plan?lang=ru", false},
		{"subscription:get_trial:0:0:701:0:0:0:0", "/mini-app/cabinet?lang=ru", false},
		{"subscription:pay_yoomoney:0:0:701:1:30:15:0", "/mini-app/cabinet/history?kind=legacy&lang=ru", false},
		{"close_notification", "", true}, {"redirect_to_download", "/mini-app/cabinet?lang=ru#connection-title", true},
	} {
		t.Run(tc.data, func(t *testing.T) {
			var destinations []string
			clears := 0
			h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
				switch {
				case strings.HasSuffix(req.URL.Path, "answerCallbackQuery"):
					return jsonReply(true), nil
				case strings.HasSuffix(req.URL.Path, "editMessageReplyMarkup"):
					clears++
					return jsonReply(botapi.Message{ID: 42, Chat: botapi.Chat{ID: 701}}), nil
				case strings.HasSuffix(req.URL.Path, "sendMessage"):
					var in struct {
						Markup botapi.InlineKeyboard `json:"reply_markup"`
					}
					if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
						t.Fatal(err)
					}
					destinations = append(destinations, in.Markup.Rows[0][0].WebApp.URL)
					return jsonReply(botapi.Message{ID: 43, Chat: botapi.Chat{ID: 701}}), nil
				default:
					t.Fatal("unexpected legacy transport")
					return nil, nil
				}
			})}
			r := clientFixture(t, h)
			r.clients.botID = 123456789
			r.clients.username = "fixture_bot"
			if err := r.handle(context.Background(), legacyCallback(t, tc.data)); err != nil {
				t.Fatal(err)
			}
			if clears != map[bool]int{true: 1, false: 0}[tc.clear] {
				t.Fatal("legacy cosmetic action lost")
			}
			if tc.destination == "" {
				if len(destinations) != 0 {
					t.Fatal("close opened account screen")
				}
			} else if len(destinations) != 1 || destinations[0] != "https://cabinet.example.test"+tc.destination {
				t.Fatal("legacy navigation not preserved", destinations)
			}
		})
	}
}

func TestClientTelegramLegacyActor(t *testing.T) {
	for _, mode := range []string{"group", "foreign-chat", "bot-actor", "foreign-bot", "no-sender", "inaccessible", "missing-button", "foreign-target", "overflow", "too-long"} {
		t.Run(mode, func(t *testing.T) {
			u := legacyCallback(t, "show_key")
			switch mode {
			case "group":
				u.Callback.Message.Chat.Type = "group"
			case "foreign-chat":
				u.Callback.Message.Chat.ID = 702
			case "bot-actor":
				u.Callback.From.IsBot = true
			case "foreign-bot":
				u.Callback.Message.From.ID = 222
			case "no-sender":
				u.Callback.Message.From = nil
			case "inaccessible":
				u.Callback.Message.Date = 0
			case "missing-button":
				u.Callback.Data = "profile"
			case "foreign-target":
				u = legacyCallback(t, "subscription:pay_yoomoney:0:0:702:1:30:15:1")
			case "overflow":
				u = legacyCallback(t, "subscription:devices:0:0:701:9223372036854775808:0:0:0")
			case "too-long":
				u = legacyCallback(t, strings.Repeat("x", 65))
			}
			h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
				if !strings.HasSuffix(req.URL.Path, "answerCallbackQuery") {
					t.Fatal("unsafe callback reached send/clear")
				}
				return jsonReply(true), nil
			})}
			r := clientFixture(t, h)
			r.clients.botID = 123456789
			r.clients.username = "fixture_bot"
			if err := r.handle(context.Background(), u); err != nil {
				t.Fatal(err)
			}
		})
	}
}
