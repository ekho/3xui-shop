package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/support"
	"github.com/google/uuid"
)

func TestNativeTrialSupportOutage(t *testing.T) {
	f := openMode(t, true)
	ctx := context.Background()
	token := filepath.Join(t.TempDir(), "support-token")
	if os.WriteFile(token, []byte("974:abcdefghijklmnopqrstuvwx"), 0600) != nil {
		t.Fatal("owned support token fixture unavailable")
	}
	var failed atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/bot974:abcdefghijklmnopqrstuvwx/getWebhookInfo":
			io.WriteString(w, `{"ok":true,"result":{"url":""}}`)
		case "/bot974:abcdefghijklmnopqrstuvwx/getMe":
			failed.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
		default:
			t.Error("unexpected owned provider method")
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	start, stop := nativeNoticeBinary(t, f, handler, map[string]string{"TRIAL_ENABLED": "true", "TELEGRAM_ENABLED": "false", "SUPPORT_TELEGRAM_ENABLED": "true", "SUPPORT_BOT_TOKEN_FILE": token, "SUPPORT_GROUP_ID": "-10074002"})
	start()
	defer stop()
	wait(t, func() bool { return failed.Load() > 0 })
	operator, csrf, actor := f.signupAccount(t, nativeEmail("support-outage-operator"))
	if err := f.svc.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	status, raw, _ := f.send(t, operator, "POST", "/api/v1/operator/clients/trial", map[string]string{"telegram_id": "779", "display_name": "Owned outage trial", "locale": "en"}, csrf, uuid.NewString(), false)
	var created struct {
		OperationID uuid.UUID `json:"operation_id"`
	}
	if status != 201 || json.Unmarshal(raw, &created) != nil || created.OperationID == uuid.Nil {
		t.Fatal("HTTP unavailable after support startup failure", status)
	}
	wait(t, func() bool { return applied(f, created.OperationID) })
	assertNativePanel(t, f, created.OperationID)
}

func TestNativeTrialSupportInvalidConfiguration(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid_group", true: "missing_token"}[missing], func(t *testing.T) {
			f := openMode(t, true)
			ctx := context.Background()
			token := filepath.Join(t.TempDir(), "support-token")
			if !missing && os.WriteFile(token, []byte("974:abcdefghijklmnopqrstuvwx"), 0600) != nil {
				t.Fatal("owned support token fixture unavailable")
			}
			settings := map[string]string{"TRIAL_ENABLED": "true", "TELEGRAM_ENABLED": "false", "SUPPORT_TELEGRAM_ENABLED": "true", "SUPPORT_BOT_TOKEN_FILE": token, "SUPPORT_GROUP_ID": "-10074002", "AUDIT_MIRROR_ENABLED": "true"}
			if !missing {
				settings["SUPPORT_GROUP_ID"] = "not-a-group"
			}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("invalid optional channel reached provider")
				w.WriteHeader(http.StatusBadRequest)
			})
			start, stop := nativeNoticeBinary(t, f, handler, settings)
			start()
			defer stop()
			operator, csrf, actor := f.signupAccount(t, nativeEmail("support-invalid-operator"))
			if err := f.svc.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
				t.Fatal(err)
			}
			status, raw, _ := f.send(t, operator, "POST", "/api/v1/operator/clients/trial", map[string]string{"telegram_id": "779", "display_name": "Owned configuration trial", "locale": "en"}, csrf, uuid.NewString(), false)
			var created struct {
				OperationID uuid.UUID `json:"operation_id"`
			}
			if status != 201 || json.Unmarshal(raw, &created) != nil || created.OperationID == uuid.Nil {
				t.Fatal("HTTP unavailable after invalid support configuration", status)
			}
			wait(t, func() bool { return applied(f, created.OperationID) })
			assertNativePanel(t, f, created.OperationID)
		})
	}
}

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
	threads := map[int64]bool{}
	nextThread := int64(887)
	var documents, longTexts, rateAttempts int
	crashDocument := false
	documentEntered := make(chan struct{}, 1)
	fileBytes := []byte("owned support attachment bytes")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/file/bot973:abcdefghijklmnopqrstuvwx/documents/support.bin" {
			w.Write(fileBytes)
			return
		}
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
		if method == "sendDocument" {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if r.ParseMultipartForm(1<<20) != nil {
				t.Error("invalid owned multipart")
				return
			}
			defer r.MultipartForm.RemoveAll()
			file, _, err := r.FormFile("document")
			if err != nil {
				t.Error("missing document bytes")
				return
			}
			got, err := io.ReadAll(file)
			file.Close()
			if err != nil || !bytes.Equal(got, fileBytes) {
				t.Error("document bytes changed")
			}
			raw = map[string]json.RawMessage{}
			for _, name := range []string{"chat_id", "message_thread_id"} {
				raw[name] = json.RawMessage(r.FormValue(name))
			}
			raw["text"], _ = json.Marshal(r.FormValue("caption"))
		} else if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&raw) != nil {
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
		lost, throttled := false, false

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
			if in.Offset > acknowledged {
				acknowledged = in.Offset
			}
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
			nextThread++
			threads[nextThread] = true
			result = map[string]any{"message_thread_id": nextThread}
		case "getFile":
			var id string
			json.Unmarshal(raw["file_id"], &id)
			if id != "owned-support-file" {
				t.Error("unexpected file source")
			}
			result = map[string]any{"file_id": id, "file_path": "documents/support.bin", "file_size": len(fileBytes)}
		case "closeForumTopic", "reopenForumTopic":
			if in.ChatID != group || !threads[in.ThreadID] {
				t.Error("wrong native topic destination")
			}
			calls = append(calls, method)
			result = true
		case "answerCallbackQuery":
			calls = append(calls, method)
			result = true
		case "sendMessage", "sendDocument", "copyMessage":
			calls = append(calls, method)
			if in.ChatID == group && in.ThreadID > 1 && !threads[in.ThreadID] || in.ChatID != group && (in.ChatID != customerTG && in.ChatID != 771 || in.ThreadID != 0) || raw["parse_mode"] != nil {
				t.Error("private body escaped its personal topic or recipient")
			}
			if in.ChatID == group && in.ThreadID <= 1 && (raw["reply_markup"] == nil || strings.Contains(in.Text, "Owned private") || strings.Contains(in.Text, "Native command reason")) {
				t.Error("private body/reason escaped into General")
			}
			if method == "sendDocument" {
				documents++
				lost = crashDocument
			}
			if method == "sendMessage" && len([]rune(in.Text)) == 4000 {
				longTexts++
			}
			if in.Text == "Owned provider rate limit" {
				rateAttempts++
				throttled = rateAttempts == 1
			}
			result = map[string]any{"message_id": int64(100 + len(calls))}
			if method == "sendMessage" || method == "sendDocument" {
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
		if lost {
			documentEntered <- struct{}{}
			<-r.Context().Done()
			return
		}
		if throttled {
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 429, "description": "Too Many Requests", "parameters": map[string]any{"retry_after": 1}})
			return
		}
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
	start, stop := nativeNoticeBinary(t, f, handler, map[string]string{"TRIAL_ENABLED": "true", "TELEGRAM_ENABLED": "false", "SUPPORT_TELEGRAM_ENABLED": "true", "SUPPORT_BOT_TOKEN_FILE": token, "SUPPORT_GROUP_ID": "-10074001"})
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

	// The following cases use the current compiled application, not parent
	// module calls, for every support write, provider receipt and command.
	pushFields := func(update, id, chat, human, thread int64, fields map[string]any) {
		t.Helper()
		m := map[string]any{"message_id": id, "message_thread_id": thread, "chat": map[string]any{"id": chat, "type": map[bool]string{true: "private", false: "supergroup"}[chat > 0]}}
		if human != 0 {
			m["from"] = map[string]any{"id": human, "first_name": "Owned fixture", "is_bot": false}
		}
		for key, value := range fields {
			m[key] = value
		}
		mu.Lock()
		updates = append(updates, map[string]any{"update_id": update, "message": m})
		mu.Unlock()
	}
	settle := func() {
		t.Helper()
		wait(t, func() bool {
			var n int
			return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM support_telegram_deliveries WHERE status IN('queued','sending')`).Scan(&n) == nil && n == 0
		})
	}
	pushFields(2, 19, customerTG, customerTG, 0, map[string]any{"caption": "Owned media caption", "document": map[string]any{"file_id": "owned-support-file", "file_name": "../support.bin", "file_size": len(fileBytes)}})
	var media uuid.UUID
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT id FROM support_messages WHERE text='Owned media caption' AND attachment_name='support.bin' AND attachment_bytes=$1 AND telegram_only=false`, fileBytes).Scan(&media) == nil
	})
	status, downloaded, _ := f.send(t, client, "GET", "/api/v1/support/messages/"+media.String()+"/attachment", nil, "", "", false)
	if status != 200 || !bytes.Equal(downloaded, fileBytes) {
		t.Fatal("native protected bytes download failed", status)
	}
	pushFields(3, 20, customerTG, customerTG, 0, map[string]any{"future_owned_media": map[string]any{"owned": true}})
	wait(t, func() bool {
		var n int
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM support_messages WHERE telegram_only=true AND attachment_bytes IS NULL`).Scan(&n) == nil && n == 1
	})
	settle()

	// Real kill happens after the committed second-part marker and before ACK.
	// The first confirmed text must survive; restart cannot blindly resend bytes.
	longBody := strings.Repeat("ж", 4000)
	var body bytes.Buffer
	multipartBody := multipart.NewWriter(&body)
	multipartBody.WriteField("text", longBody)
	file, err := multipartBody.CreateFormFile("file", "support.bin")
	if err != nil {
		t.Fatal("owned multipart unavailable")
	}
	file.Write(fileBytes)
	multipartBody.Close()
	request, err := http.NewRequest("POST", f.public.URL+"/api/v1/support/messages", &body)
	if err != nil {
		t.Fatal("owned multipart request unavailable")
	}
	request.Header.Set("Content-Type", multipartBody.FormDataContentType())
	request.Header.Set("Origin", f.public.URL)
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("Idempotency-Key", uuid.NewString())
	mu.Lock()
	crashDocument = true
	mu.Unlock()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("owned multipart write failed")
	}
	var longMessage support.SupportMessage
	err = json.NewDecoder(response.Body).Decode(&longMessage)
	response.Body.Close()
	if response.StatusCode != 201 || err != nil || longMessage.Id == uuid.Nil {
		t.Fatal("native long multipart write failed", response.StatusCode)
	}
	select {
	case <-documentEntered:
	case <-time.After(20 * time.Second):
		t.Fatal("native document marker not reached")
	}
	var unresolved uuid.UUID
	var parts []byte
	if f.env.Pool.QueryRow(ctx, `SELECT id,parts FROM support_telegram_deliveries WHERE message_id=$1 AND status='sending'`, longMessage.Id).Scan(&unresolved, &parts) != nil {
		t.Fatal("committed sending intent missing")
	}
	var saved []support.TelegramPart
	if json.Unmarshal(parts, &saved) != nil || len(saved) != 2 || saved[0].Status != "sent" || saved[1].Status != "sending" {
		t.Fatal("partial committed markers not retained")
	}
	f.nativeCrash()
	if _, err = f.env.Pool.Exec(ctx, `UPDATE support_telegram_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, unresolved); err != nil {
		t.Fatal("owned lease clock advance failed")
	}
	mu.Lock()
	crashDocument = false
	updates = nil
	acknowledged = 0
	documentCount, longCount := documents, longTexts
	mu.Unlock()
	start()
	wait(t, func() bool {
		var state string
		return f.env.Pool.QueryRow(ctx, `SELECT status FROM support_telegram_deliveries WHERE id=$1`, unresolved).Scan(&state) == nil && state == "unknown"
	})
	mu.Lock()
	duplicate := documents != documentCount || longTexts != longCount
	mu.Unlock()
	if duplicate {
		t.Fatal("native restart resent unconfirmed parts without consent")
	}
	key := uuid.NewString()
	retryPath := "/api/v1/operator/clients/" + account.String() + "/support/telegram-deliveries/" + unresolved.String() + "/retry"
	status, retryBytes, _ := f.send(t, operator, "POST", retryPath, map[string]any{"confirmed": true, "reason": "Owned recovery"}, operatorCSRF, key, false)
	if status != 201 {
		t.Fatal("native confirmed retry failed", status)
	}
	settle()
	status, replayBytes, _ := f.send(t, operator, "POST", retryPath, map[string]any{"confirmed": true, "reason": "Owned recovery"}, operatorCSRF, key, false)
	if status != 200 || !bytes.Equal(retryBytes, replayBytes) {
		t.Fatal("native retry replay changed")
	}
	mu.Lock()
	duplicate = documents != documentCount+1 || longTexts != longCount
	mu.Unlock()
	if duplicate {
		t.Fatal("native retry repeated the confirmed text or wrong bytes")
	}
	post(client, "/api/v1/support/messages", "Owned provider rate limit", csrf)
	settle()
	mu.Lock()
	attempts := rateAttempts
	mu.Unlock()
	if attempts != 2 {
		t.Fatal("native 429 did not boundedly defer", attempts)
	}

	// Contact alone creates no account/access. Ban remains on the source when
	// registration later creates a UUID; guest history never becomes its history.
	push(1, 31, 771, 771, 0, "Owned guest request", "Owned guest")
	var guestTopic uuid.UUID
	var guestThread int64
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT id,thread_id FROM support_telegram_topics WHERE kind='guest' AND guest_tg_id=771 AND status='ready'`).Scan(&guestTopic, &guestThread) == nil
	})
	settle()
	var n int
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE telegram_id=771`).Scan(&n) != nil || n != 0 {
		t.Fatal("guest contact created account")
	}
	push(2, 32, group, operatorTG, guestThread, "/ban Native command reason", "operator")
	wait(t, func() bool {
		var banned bool
		return f.env.Pool.QueryRow(ctx, `SELECT banned FROM support_telegram_guest_bans WHERE telegram_id=771`).Scan(&banned) == nil && banned
	})
	settle()
	registered, _, err := f.svc.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 771, DisplayName: "Owned guest", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal("owned registration fixture failed")
	}
	push(3, 33, 771, 771, 0, "Owned banned after signup", "Owned guest")
	wait(t, func() bool { mu.Lock(); defer mu.Unlock(); return acknowledged >= 4 })
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM support_messages m JOIN support_conversations c ON c.id=m.conversation_id WHERE c.account_id=$1`, registered.Account.ID).Scan(&n) != nil || n != 0 {
		t.Fatal("guest bodies reassigned after signup")
	}
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM trial_grants WHERE account_id=$1`, registered.Account.ID).Scan(&n) != nil || n != 0 {
		t.Fatal("support guest gained trial")
	}

	// The existing subscription owner applies commands against the real panel.
	accessReleased := func() {
		t.Helper()
		wait(t, func() bool {
			owner, err := f.svc.VPN.OpenAccessOwner(ctx, account)
			if err != nil {
				return false
			}
			defer owner.Release()
			return owner.TryLock(ctx) == nil
		})
	}
	status, raw, _ = f.send(t, client, "POST", "/api/v1/trial-requests", map[string]string{"comment": "Owned current manual trial"}, csrf, uuid.NewString(), false)
	var trial subscriptions.TrialRequest
	if status != 201 || json.Unmarshal(raw, &trial) != nil {
		t.Fatal("native manual trial unavailable", status)
	}
	push(4, 40, group, operatorTG, 888, "/approve "+trial.RequestId.String()+" Native command reason", "operator")
	var trialOperation uuid.UUID
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT id FROM trial_operations WHERE account_id=$1 AND status='applied'`, account).Scan(&trialOperation) == nil
	})
	assertNativePanel(t, f, trialOperation)
	accessReleased()
	push(5, 41, group, operatorTG, 888, "/comp 1 Native command reason", "operator")
	var comp uuid.UUID
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT id FROM access_operations WHERE account_id=$1 AND kind='compensate' AND status='applied'`, account).Scan(&comp) == nil
	})
	settle()
	accessReleased()
	push(6, 42, group, operatorTG, 888, "/reset Native command reason", "operator")
	var resetReceipt support.TelegramCommandReceipt
	// Applied is committed before the worker releases its session lock. Wait
	// for that release before the next command and inspect its actual outcome.
	wait(t, func() bool {
		var result []byte
		return f.env.Pool.QueryRow(ctx, `SELECT result FROM support_telegram_receipts WHERE bot_id=973 AND chat_id=$1 AND message_id=42 AND action='command'`, group).Scan(&result) == nil && json.Unmarshal(result, &resetReceipt) == nil && resetReceipt.Result != nil
	})
	if resetReceipt.Result.Status == "failed" || resetReceipt.Result.OperationID == uuid.Nil {
		t.Fatal("native reset command rejected", resetReceipt.Result.Code)
	}
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM access_operations WHERE account_id=$1 AND kind='reset_traffic' AND status='applied'`, account).Scan(&n) == nil && n == 1
	})
	settle()
	push(7, 41, group, operatorTG, 888, "/comp 1 Native command reason", "renamed operator")
	wait(t, func() bool { mu.Lock(); defer mu.Unlock(); return acknowledged >= 8 })
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM access_operations WHERE account_id=$1`, account).Scan(&n) != nil || n != 2 {
		t.Fatal("logical command replay repeated subscription operation")
	}
	if f.svc.Accounts.ChangeOperatorRole(ctx, actor, false) != nil {
		t.Fatal("owned role revocation failed")
	}
	push(8, 43, group, operatorTG, 888, "/comp 2 Native command reason", "operator")
	wait(t, func() bool { mu.Lock(); defer mu.Unlock(); return acknowledged >= 9 })
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM access_operations WHERE account_id=$1`, account).Scan(&n) != nil || n != 2 {
		t.Fatal("revoked source changed subscription")
	}
	if f.svc.Accounts.ChangeOperatorRole(ctx, actor, true) != nil {
		t.Fatal("owned operator restore failed")
	}

	// Native event actor is genuinely absent, not a synthesized operator.
	pushFields(9, 44, group, 0, 888, map[string]any{"forum_topic_closed": map[string]any{}})
	wait(t, func() bool {
		var closed bool
		return f.env.Pool.QueryRow(ctx, `SELECT closed FROM support_telegram_topics WHERE account_id=$1`, account).Scan(&closed) == nil && closed
	})
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_system_events WHERE support_telegram->>'kind'='topic_closed' AND support_telegram->'actor_tg_id'='null'::jsonb AND support_telegram->'actor_account_id'='null'::jsonb`).Scan(&n) != nil || n != 1 {
		t.Fatal("native absent actor invented")
	}
	post(client, "/api/v1/support/messages", "Owned reopen contact", csrf)
	settle()
	var stillClosed bool
	if f.env.Pool.QueryRow(ctx, `SELECT closed FROM support_telegram_topics WHERE account_id=$1`, account).Scan(&stillClosed) != nil || stillClosed {
		t.Fatal("native topic projection unavailable")
	}
	if f.env.Pool.QueryRow(ctx, facts, account).Scan(&after) != nil || after != before {
		t.Fatal("support lifecycle changed retained identity/key")
	}
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM trial_grants WHERE account_id=$1`, account).Scan(&n) != nil || n != 1 {
		t.Fatal("support commands changed trial grant multiplicity")
	}
	push(10, 45, group, operatorTG, 0, "/pending", "operator")
	push(11, 46, group, operatorTG, 0, "/info", "operator")
	push(12, 47, group, operatorTG, 888, "/approve "+uuid.NewString()+" Native command reason", "operator")
	wait(t, func() bool { mu.Lock(); defer mu.Unlock(); return acknowledged >= 13 })
	settle()
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM trial_grants WHERE account_id=$1`, account).Scan(&n) != nil || n != 1 {
		t.Fatal("stale trial command created access")
	}
	mu.Lock()
	updates = append(updates, map[string]any{"update_id": int64(13), "callback_query": map[string]any{"id": "owned-legacy-callback", "from": map[string]any{"id": operatorTG, "is_bot": false}, "message": map[string]any{"message_id": int64(48), "date": int64(1), "message_thread_id": int64(888), "from": map[string]any{"id": int64(973), "is_bot": true}, "chat": map[string]any{"id": group, "type": "supergroup"}}, "data": "approval:" + trial.RequestId.String()}})
	mu.Unlock()
	wait(t, func() bool { mu.Lock(); defer mu.Unlock(); return acknowledged >= 14 })
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM access_operations WHERE account_id=$1`, account).Scan(&n) != nil || n != 2 {
		t.Fatal("legacy callback executed business action")
	}
	// The real CLI imports retained source snapshots without provider work or
	// changing nonempty financial/approval records. All data is owned fixture data.
	execute := func(query string, args ...any) {
		t.Helper()
		if _, err := f.env.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal("owned retained fixture write failed", err)
		}
	}
	execute(`UPDATE accounts SET legacy_user_id=987 WHERE id=$1`, registered.Account.ID)
	execute(`INSERT INTO legacy_approval_snapshots(account_id,source_legacy_user_id,source_tg_id,status) VALUES($1,987,9007199254740993,'approved')`, registered.Account.ID)
	old := time.Now().UTC().Add(-400 * 24 * time.Hour).Truncate(time.Microsecond)
	order := uuid.New()
	quote := `{"amount_minor":"9007199254740993","currency":"RUB","devices":2,"period_days":30,"plan_id":"00000000-0000-4000-8000-000000000123","profile":"regular","revision":1,"traffic_gb":15}`
	execute(`INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,payment_status,fulfillment_status,active,created_at,expires_at,paid_at,action) VALUES($1,$2,$3,$4,$5,9007199254740993,'AC','paid','needs_review',false,$6,$7,$6,'renew')`, order, account, uuid.New(), []byte{1}, quote, old, old.Add(time.Hour))
	execute(`INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,created_at) VALUES('native-support-receipt',$1,$2,9007199254740993,9007199254740900,'643','p2p-incoming',false,false,$2)`, order, old)
	execute(`INSERT INTO purchase_refunds(id,order_id,receipt_operation_id,payment_method,reference,returned_amount,returned_currency,reason,operator_account_id,created_at) VALUES($1,$2,'native-support-receipt','yoomoney','native-support-return','90071992547409.93','RUB','Owned retained return',$3,$4)`, uuid.New(), order, actor, old)
	legacyFacts := func() [32]byte {
		t.Helper()
		var facts string
		if f.env.Pool.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM purchase_orders x),(SELECT jsonb_agg(to_jsonb(x) ORDER BY operation_id) FROM purchase_receipts x),(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM purchase_refunds x),(SELECT jsonb_agg(to_jsonb(x) ORDER BY account_id) FROM legacy_approval_snapshots x),(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM accounts x))::text`).Scan(&facts) != nil {
			t.Fatal("owned finance/approval facts unavailable")
		}
		return sha256.Sum256([]byte(facts))
	}
	legacyBefore := legacyFacts()
	database, err := url.Parse(f.env.Pool.Config().ConnString())
	if err != nil {
		t.Fatal("owned CLI database unavailable")
	}
	database.Path = "/" + f.env.Pool.Config().ConnConfig.Database
	dbFile := filepath.Join(t.TempDir(), "import-database")
	if os.WriteFile(dbFile, []byte(database.String()), 0600) != nil {
		t.Fatal("owned CLI database file unavailable")
	}
	pkg := support.LegacySupportInput{Version: 1, BotID: 973, GroupID: group, Tickets: []support.LegacySupportRow{{SourceID: 9223372036854775807, TelegramID: 9007199254740993, ThreadID: new(int64), Status: "banned", CreatedAt: "2025-10-01T10:00:00.000001Z", UpdatedAt: "2025-10-01T10:00:00.000002Z"}, {SourceID: 21, TelegramID: 999, Status: "open", CreatedAt: "2025-10-01T10:00:00.000001Z", UpdatedAt: "2025-10-01T10:00:00.000002Z"}}}
	*pkg.Tickets[0].ThreadID = 990
	cli := func(flag string, pkg support.LegacySupportInput, expected support.LegacySupportResult) {
		t.Helper()
		raw, _ := json.Marshal(pkg)
		command := exec.Command(f.nativeBinary, "import-legacy-support", flag)
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "DATABASE_URL_FILE=" + dbFile}
		command.Stdin = bytes.NewReader(raw)
		out, err := command.Output()
		var result support.LegacySupportResult
		decoded := json.Unmarshal(out, &result) == nil
		if err != nil || !decoded || result != expected {
			var failure struct{ Error string }
			_ = json.Unmarshal(out, &failure)
			t.Fatalf("compiled support CLI failed %s: exit=%T decoded=%t result=%+v code=%s", flag, err, decoded, result, failure.Error)
		}
	}
	cli("--dry-run", pkg, support.LegacySupportResult{Inserted: 2, Orphans: 1})
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM legacy_support_imports`).Scan(&n) != nil || n != 0 {
		t.Fatal("native dry run retained writes")
	}
	mu.Lock()
	wireBefore = len(calls)
	mu.Unlock()
	cli("--apply", pkg, support.LegacySupportResult{Inserted: 2, Orphans: 1})
	cli("--apply", pkg, support.LegacySupportResult{Replayed: 2})
	var retainedSource []byte
	var retainedThread int64
	var nativeLegacyKind string
	if f.env.Pool.QueryRow(ctx, `SELECT i.source_json,t.thread_id,t.kind FROM legacy_support_imports i JOIN support_telegram_topics t USING(bot_id,group_id,source_id) WHERE i.source_id=9223372036854775807`).Scan(&retainedSource, &retainedThread, &nativeLegacyKind) != nil || retainedThread != 990 || nativeLegacyKind != "account" || !bytes.Contains(retainedSource, []byte("9007199254740993")) || !bytes.Contains(retainedSource, []byte(".000001Z")) {
		t.Fatal("native legacy source lost precision/original mapping")
	}
	if f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM support_telegram_topics WHERE source_id=21 AND kind='orphan' AND thread_id IS NULL`).Scan(&n) != nil || n != 1 {
		t.Fatal("native orphan acquired false destination")
	}
	mu.Lock()
	wireAfter = len(calls)
	mu.Unlock()
	if wireAfter != wireBefore || legacyFacts() != legacyBefore {
		t.Fatal("native import changed provider/identity/finance/approval facts")
	}
	// Re-imported source is immutable even when the caller changes its package.
	pkg.Tickets[0].Status = "open"
	raw, _ = json.Marshal(pkg)
	command := exec.Command(f.nativeBinary, "import-legacy-support", "--apply")
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "DATABASE_URL_FILE=" + dbFile}
	command.Stdin = bytes.NewReader(raw)
	out, err := command.Output()
	if err == nil || !bytes.Contains(out, []byte("IMPORT_SOURCE_CONFLICT")) {
		t.Fatal("compiled legacy mutation did not fail closed")
	}
	// Exact nonempty owner records remain after further support traffic.
	const retained = `SELECT jsonb_build_array((SELECT to_jsonb(a)-'updated_at' FROM accounts a WHERE id=$1),(SELECT jsonb_agg(to_jsonb(g)) FROM trial_grants g WHERE account_id=$1),(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM access_operations o WHERE account_id=$1))::text`
	var retainedBefore, retainedAfter string
	if f.env.Pool.QueryRow(ctx, retained, account).Scan(&retainedBefore) != nil {
		t.Fatal("retained business proof unavailable")
	}
	post(client, "/api/v1/support/messages", "Owned final business invariant", csrf)
	settle()
	if f.env.Pool.QueryRow(ctx, retained, account).Scan(&retainedAfter) != nil || sha256.Sum256([]byte(retainedBefore)) != sha256.Sum256([]byte(retainedAfter)) || legacyFacts() != legacyBefore {
		t.Fatal("support traffic changed nonempty business facts")
	}
}
