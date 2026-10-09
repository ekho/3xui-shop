package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/support"
	"github.com/google/uuid"
)

// The compiled server owns HTTP, SMTP, workers and support polling. Only the
// Telegram provider is simulated; the owned Docker panel remains 3X-UI 3.7.0.
func TestNativeTrialSupportText(t *testing.T) {
	f := openMode(t, true)
	ctx := context.Background()
	const group int64 = -10074001
	const customerTG, operatorTG int64 = 731, 732
	token := filepath.Join(t.TempDir(), "support-token")
	if os.WriteFile(token, []byte("973:abcdefghijklmnopqrstuvwx"), 0600) != nil {
		t.Fatal("owned support token unavailable")
	}
	var mu sync.Mutex
	var updates []map[string]any
	var calls []string
	var acknowledged int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasPrefix(r.URL.Path, "/bot973:abcdefghijklmnopqrstuvwx/") {
			t.Error("unexpected provider route or main poller")
			http.Error(w, "owned support only", 400)
			return
		}
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var in struct {
			Offset, ChatID, UserID, ThreadID, FromChatID, MessageID int64
			Text                                                    string
		}
		var raw map[string]json.RawMessage
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&raw) != nil {
			t.Error("invalid provider fixture input")
			return
		}
		json.Unmarshal(raw["offset"], &in.Offset)
		json.Unmarshal(raw["chat_id"], &in.ChatID)
		json.Unmarshal(raw["user_id"], &in.UserID)
		json.Unmarshal(raw["message_thread_id"], &in.ThreadID)
		json.Unmarshal(raw["from_chat_id"], &in.FromChatID)
		json.Unmarshal(raw["message_id"], &in.MessageID)
		json.Unmarshal(raw["text"], &in.Text)
		var result any
		mu.Lock()
		switch method {
		case "getWebhookInfo":
			result = map[string]string{"url": ""}
		case "getMe":
			result = map[string]any{"id": int64(973), "is_bot": true, "username": "owned_support"}
		case "getChat":
			if in.ChatID != group {
				t.Error("wrong support forum")
			}
			result = map[string]any{"id": group, "type": "supergroup", "is_forum": true}
		case "getChatMember":
			if in.ChatID != group || in.UserID != 973 {
				t.Error("wrong support administrator proof")
			}
			result = map[string]any{"user": map[string]any{"id": int64(973), "is_bot": true}, "status": "administrator", "can_manage_topics": true}
		case "getUpdates":
			acknowledged = in.Offset
			result = []map[string]any{}
			for len(updates) > 0 && updates[0]["update_id"].(int64) < in.Offset {
				updates = updates[1:]
			}
			if len(updates) > 0 {
				result = updates[:1]
			}
		case "createForumTopic":
			calls = append(calls, method)
			if in.ChatID != group {
				t.Error("wrong topic destination")
			}
			result = map[string]any{"message_thread_id": int64(888)}
		case "sendMessage", "copyMessage":
			calls = append(calls, method)
			if in.ChatID == group && in.ThreadID != 888 || in.ChatID != group && (in.ChatID != customerTG || in.ThreadID != 0) || raw["parse_mode"] != nil {
				t.Error("private body escaped its personal topic or recipient")
			}
			result = map[string]any{"message_id": int64(100 + len(calls))}
			if method == "sendMessage" {
				kind := "private"
				if in.ChatID == group {
					kind = "supergroup"
				}
				result = map[string]any{"message_id": int64(100 + len(calls)), "message_thread_id": in.ThreadID, "chat": map[string]any{"id": in.ChatID, "type": kind}}
			}
		default:
			t.Error("unexpected support method", method)
			result = true
		}
		mu.Unlock()
		if method == "getUpdates" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(25 * time.Millisecond):
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	})
	start, stop := nativeNoticeBinary(t, f, handler, map[string]string{"TELEGRAM_ENABLED": "false", "SUPPORT_TELEGRAM_ENABLED": "true", "SUPPORT_BOT_TOKEN_FILE": token, "SUPPORT_GROUP_ID": "-10074001"})
	firstPID := start()
	client, csrf, account := f.signupAccount(t, nativeEmail("support-client"))
	operator, operatorCSRF, actor := f.signupAccount(t, nativeEmail("support-operator"))
	if f.svc.Accounts.ChangeOperatorRole(ctx, actor, true) != nil {
		t.Fatal("owned operator grant failed")
	}
	if _, err := f.env.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=CASE WHEN id=$1 THEN $3::bigint ELSE $4::bigint END WHERE id IN($1,$2)`, account, actor, customerTG, operatorTG); err != nil {
		t.Fatal("owned binding fixture failed")
	}
	const facts = `SELECT jsonb_build_object('id',id,'vpn_id',vpn_id,'sub_id',sub_id,'panel_key',panel_key,'kind',kind,'email_key',email_key,'credential_version',credential_version)::text FROM accounts WHERE id=$1`
	var before string
	if f.env.Pool.QueryRow(ctx, facts, account).Scan(&before) != nil {
		t.Fatal("owned identity facts unavailable")
	}
	post := func(c *http.Client, path, text, token string) support.SupportMessage {
		t.Helper()
		status, raw, _ := f.send(t, c, "POST", path, map[string]string{"text": text}, token, uuid.NewString(), false)
		var m support.SupportMessage
		if status != 201 || json.Unmarshal(raw, &m) != nil || m.Id == uuid.Nil {
			t.Fatal("native support write failed", status)
		}
		return m
	}
	web := post(client, "/api/v1/support/messages", "Owned web request", csrf)
	var jobs int
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM support_telegram_deliveries`).Scan(&jobs) != nil || jobs != 2 {
		t.Fatal("compiled server did not enqueue topic and text", jobs)
	}
	wait(t, func() bool {
		var n int
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM support_telegram_deliveries WHERE status='sent'`).Scan(&n) == nil && n == 2
	})
	push := func(update, message, chat, human, thread int64, text, name string) {
		mu.Lock()
		defer mu.Unlock()
		updates = append(updates, map[string]any{"update_id": update, "message": map[string]any{"message_id": message, "message_thread_id": thread, "from": map[string]any{"id": human, "first_name": name, "is_bot": false}, "chat": map[string]any{"id": chat, "type": map[bool]string{true: "private", false: "supergroup"}[chat > 0]}, "text": text}})
	}
	push(9, 17, customerTG, customerTG, 0, "Owned private request", "first name")
	wait(t, func() bool {
		var n int
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM support_messages`).Scan(&n) == nil && n == 2
	})
	push(10, 18, group, operatorTG, 888, "Owned forum reply", "operator")
	wait(t, func() bool {
		var n int
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM support_messages`).Scan(&n) == nil && n == 3
	})
	post(operator, "/api/v1/operator/clients/"+account.String()+"/support/messages", "Owned web reply", operatorCSRF)
	wait(t, func() bool {
		var n int
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM support_telegram_deliveries WHERE status='sent'`).Scan(&n) == nil && n == 5
	})
	status, raw, _ := f.send(t, client, "GET", "/api/v1/support", nil, "", "", false)
	var history support.SupportResult
	if status != 200 || json.Unmarshal(raw, &history) != nil || len(history.Messages) != 4 {
		t.Fatal("protected HTTP common history failed", status)
	}
	seen := map[string]uuid.UUID{}
	for _, m := range history.Messages {
		seen[m.Text] = m.Id
	}
	if seen["Owned web request"] != web.Id || seen["Owned private request"] == uuid.Nil || seen["Owned forum reply"] == uuid.Nil || seen["Owned web reply"] == uuid.Nil {
		t.Fatal("source messages lost common IDs")
	}
	var audits int
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE support_message_id=$1 AND operator_account_id=$2 AND operator_tg_id=$3 AND operator_source='telegram_support'`, seen["Owned forum reply"], actor, operatorTG).Scan(&audits) != nil || audits != 1 {
		t.Fatal("actual Telegram operator audit lost")
	}
	stop()
	mu.Lock()
	updates = nil
	acknowledged = 0
	wireBefore := len(calls)
	mu.Unlock()
	push(1, 17, customerTG, customerTG, 0, "Owned private request", "renamed user")
	if start() == firstPID {
		t.Fatal("application did not restart")
	}
	wait(t, func() bool { mu.Lock(); defer mu.Unlock(); return acknowledged == 2 })
	var after string
	var messages, receipts int
	if f.env.Pool.QueryRow(ctx, facts, account).Scan(&after) != nil || after != before || f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM support_messages`).Scan(&messages) != nil || messages != 4 || f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM support_telegram_receipts`).Scan(&receipts) != nil || receipts != 2 {
		t.Fatal("restart replay changed identity or message facts")
	}
	mu.Lock()
	wireAfter := len(calls)
	mu.Unlock()
	if wireAfter != wireBefore {
		t.Fatal("restart replay sent duplicate wire parts")
	}
}
