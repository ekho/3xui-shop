package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/telegram"
	"github.com/google/uuid"
)

func TestNoticeTelegramTransport(t *testing.T) {
	_, s, e, cfg, _, client, operator := noticeFixture(t, 1)
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=733 WHERE id=$1`, client.id); err != nil {
		t.Fatal(err)
	}
	type call struct {
		method string
		body   map[string]any
	}
	calls := make(chan call, 20)
	updates := make(chan map[string]any, 1)
	date := time.Now().Unix()
	h := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		method := path.Base(r.URL.Path)
		result := any(true)
		switch method {
		case "getWebhookInfo":
			result = map[string]string{"url": ""}
		case "getMe":
			result = map[string]any{"id": 123456789, "is_bot": true, "username": "fixture_bot", "has_main_web_app": true}
		case "setChatMenuButton":
		case "getUpdates":
			select {
			case u := <-updates:
				result = []any{u}
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		case "sendMessage", "editMessageText", "deleteMessage", "editMessageReplyMarkup", "answerCallbackQuery":
			var in map[string]any
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				return nil, errors.New("owned request parse")
			}
			calls <- call{method, in}
			if method == "sendMessage" || method == "editMessageText" || method == "editMessageReplyMarkup" {
				result = map[string]any{"message_id": 42, "date": date, "chat": map[string]any{"id": 733, "type": "private"}, "from": map[string]any{"id": 123456789, "is_bot": true}, "reply_markup": in["reply_markup"]}
			}
		default:
			return nil, errors.New("unexpected owned method")
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "result": result})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})}
	runtime, err := app.NewTelegram(telegram.Config{Enabled: true, Token: "123456789:abcdefghijklmnopqrstuvwxyz012345678", Operators: []int64{101, 202}}, s.Modules, cfg.HTTP.CabinetOrigin, h)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runtime.Run(runCtx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("owned Telegram runtime did not drain")
		}
	}()
	next := func(method string) call {
		t.Helper()
		select {
		case c := <-calls:
			if c.method != method {
				t.Fatalf("expected %s got %s", method, c.method)
			}
			return c
		case <-time.After(10 * time.Second):
			var state string
			var attempts int
			e.Pool.QueryRow(ctx, `SELECT state,attempts FROM client_telegram_deliveries ORDER BY sequence DESC LIMIT 1`).Scan(&state, &attempts)
			t.Fatalf("no %s; runtime=%s state=%s attempts=%d", method, runtime.State().Code, state, attempts)
		}
		return call{}
	}
	settled := func(preview uuid.UUID) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			out, err := s.Notices.Confirm(ctx, operator.id, preview)
			if err != nil {
				t.Fatal(err)
			}
			if out.Telegram.Succeeded == 1 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("wire ACK not recorded", out.Telegram)
			}
			time.Sleep(time.Millisecond)
		}
	}
	p := sendNotice(t, s, client, operator, "<b>Actual notice</b>")
	first := next("sendMessage")
	if first.body["text"] != "<b>Actual notice</b>" || first.body["parse_mode"] != "HTML" {
		t.Fatal("transport did not use immutable normalized body")
	}
	markup := first.body["reply_markup"].(map[string]any)
	rows := markup["inline_keyboard"].([]any)
	closeData := rows[1].([]any)[0].(map[string]any)["callback_data"].(string)
	if !strings.HasPrefix(closeData, "on1:") {
		t.Fatal("notice close does not use actual delete proof")
	}
	settled(p.ID)
	edit, err := s.Notices.Preview(ctx, operator.id, notifications.NoticePreviewInput{Mode: "edit", ExpectedRevision: 1, Body: "<i>Edited notice</i>", Reason: "Owned edit transport"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Notices.Confirm(ctx, operator.id, edit.ID); err != nil {
		t.Fatal(err)
	}
	changed := next("editMessageText")
	if changed.body["text"] != "<i>Edited notice</i>" || changed.body["message_id"] != float64(42) {
		t.Fatal("edit replaced rather than edited recorded message")
	}
	settled(edit.ID)
	withdraw, err := s.Notices.Preview(ctx, operator.id, notifications.NoticePreviewInput{Mode: "delete", ExpectedRevision: 2, Reason: "Owned delete transport"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Notices.Confirm(ctx, operator.id, withdraw.ID); err != nil {
		t.Fatal(err)
	}
	removed := next("deleteMessage")
	if removed.body["message_id"] != float64(42) {
		t.Fatal("delete missing recorded proof")
	}
	settled(withdraw.ID)
	p = sendNotice(t, s, client, operator, "Close this notice")
	first = next("sendMessage")
	settled(p.ID)
	markup = first.body["reply_markup"].(map[string]any)
	rows = markup["inline_keyboard"].([]any)
	closeData = rows[1].([]any)[0].(map[string]any)["callback_data"].(string)
	updates <- map[string]any{"update_id": 11, "callback_query": map[string]any{"id": "owned-close", "data": closeData, "from": map[string]any{"id": 733, "is_bot": false, "language_code": "en"}, "message": map[string]any{"message_id": 42, "date": date, "chat": map[string]any{"id": 733, "type": "private"}, "from": map[string]any{"id": 123456789, "is_bot": true}, "reply_markup": markup}}}
	next("deleteMessage")
	next("answerCallbackQuery")
	inbox, err := s.Notices.Read(ctx, client.id, 1)
	if err != nil || inbox.Total != 0 {
		t.Fatal("Telegram close did not dismiss own cabinet record", inbox.Total, err)
	}
}
