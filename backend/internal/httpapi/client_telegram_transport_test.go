package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/telegram"
	"io"
	"net/http"
	"path"
	"strings"
	"testing"
	"time"
)

func TestClientTelegramTransport(t *testing.T) {
	for _, mode := range []string{"ru", "en", "order", "forbidden", "bad-request", "lost", "rate-limit"} {
		t.Run(mode, func(t *testing.T) {
			_, e, cfg := httpFixture(t)
			cfg.Accounts.Now = e.Clock
			modules := app.NewModules(e.Pool, e.Redis, nil, &cfg)
			ctx := context.Background()
			lang := "ru"
			if mode == "en" {
				lang = "en"
			}
			a, _, err := modules.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 701, DisplayName: "Owned fixture", Locale: lang}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			route, destination := "support", "/mini-app/cabinet/support"
			if mode == "order" {
				route, destination = "orders:11111111-1111-4111-8111-111111111111", "/mini-app/orders/11111111-1111-4111-8111-111111111111"
			}
			if err = modules.Notifications.EnqueueClientTx(ctx, tx, notifications.ClientNotice{AccountID: a.Account.ID, TelegramID: 701, CredentialVersion: a.Account.CredentialVersion, Locale: lang, EventKey: "fixture:transport", Route: route}, e.Clock()); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			sent := make(chan map[string]any, 1)
			h := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				result := `true`
				switch path.Base(r.URL.Path) {
				case "getWebhookInfo":
					result = `{"url":""}`
				case "getMe":
					result = `{"id":123456789,"is_bot":true,"username":"fixture_bot","has_main_web_app":true}`
				case "setChatMenuButton":
				case "getUpdates":
					<-r.Context().Done()
					return nil, r.Context().Err()
				case "sendMessage":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						return nil, err
					}
					sent <- body
					switch mode {
					case "lost":
						return nil, errors.New("owned lost response")
					case "forbidden":
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":403}`))}, nil
					case "bad-request":
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":400}`))}, nil
					case "rate-limit":
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":429,"parameters":{"retry_after":30}}`))}, nil
					}
					result = `{"message_id":42,"chat":{"id":701,"type":"private"}}`
				default:
					return nil, errors.New("unexpected owned transport method")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":` + result + `}`))}, nil
			})}
			tg, err := app.NewTelegram(telegram.Config{Enabled: true, Token: "123456789:abcdefghijklmnopqrstuvwxyz012345678", Operators: []int64{101, 202}}, modules, cfg.HTTP.CabinetOrigin, h)
			if err != nil {
				t.Fatal(err)
			}
			runCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- tg.Run(runCtx) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(2 * time.Second):
					t.Error("client/poll/operator loops did not drain")
				}
			}()
			var body map[string]any
			select {
			case body = <-sent:
			case <-time.After(2 * time.Second):
				t.Fatal("client delivery loop did not send committed notice")
			}
			want := "В кабинете есть обновление."
			if lang == "en" {
				want = "There is an update in your cabinet."
			}
			if body["chat_id"] != float64(701) || body["text"] != want {
				t.Fatal("notice leaked details or used wrong recipient/locale")
			}
			keyboard := body["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
			open := keyboard[0].([]any)[0].(map[string]any)["web_app"].(map[string]any)["url"]
			closeData, _ := keyboard[1].([]any)[0].(map[string]any)["callback_data"].(string)
			if open != cfg.HTTP.CabinetOrigin+destination+"?lang="+lang || !strings.HasPrefix(closeData, "cn1:") {
				t.Fatal("notice used unsecured or unbound navigation")
			}
			wantState, wantCode := "sent", ""
			switch mode {
			case "forbidden":
				wantState = "failed"
			case "bad-request":
				wantState = "failed"
			case "lost":
				wantState = "pending"
				wantCode = "UNAVAILABLE"
			case "rate-limit":
				wantState = "pending"
				wantCode = "RATE_LIMITED"
			}
			deadline := time.Now().Add(2 * time.Second)
			var state string
			var message *int64
			for {
				if err = e.Pool.QueryRow(ctx, "SELECT state,message_id FROM client_telegram_deliveries WHERE account_id=$1", a.Account.ID).Scan(&state, &message); err != nil {
					t.Fatal(err)
				}
				if state == wantState && (wantCode == "" || tg.State().Code == wantCode) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("transport outcome not recorded", state, tg.State().Code)
				}
				time.Sleep(time.Millisecond)
			}
			if (wantState == "sent" && (message == nil || *message != 42)) || (wantState != "sent" && message != nil) {
				t.Fatal("unproven delivery recorded as sent")
			}
		})
	}
}
