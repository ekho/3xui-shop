package telegram

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func jsonReply(v any) *http.Response {
	b, _ := json.Marshal(map[string]any{"ok": true, "result": v})
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}
}

type actionRecorder struct {
	mu        sync.Mutex
	decisions []TrialDecision
	support   []SupportAction
	fail      bool
}

func (a *actionRecorder) Decide(_ context.Context, in TrialDecision) (Decision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.decisions = append(a.decisions, in)
	if a.fail {
		a.fail = false
		return Decision{}, &ActionError{Code: "SERVICE_UNAVAILABLE"}
	}
	return Decision{}, nil
}
func (a *actionRecorder) Reconsider(_ context.Context, in SupportAction) (Trial, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.support = append(a.support, in)
	if a.fail {
		a.fail = false
		return Trial{}, &ActionError{Code: "SERVICE_UNAVAILABLE"}
	}
	return Trial{}, nil
}
func (a *actionRecorder) Reconcile(ctx context.Context, in SupportAction) (Operation, error) {
	_, e := a.Reconsider(ctx, in)
	return Operation{}, e
}
func approvalFixture(t *testing.T) (*dispatcher, *actionRecorder, *int64) {
	t.Helper()
	a := &actionRecorder{}
	next := int64(20)
	h := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		var in struct {
			Chat    int64 `json:"chat_id"`
			Message int64 `json:"message_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(r.URL.Path, "answerCallbackQuery") {
			return jsonReply(true), nil
		}
		id := in.Message
		if id == 0 {
			next++
			id = next
		}
		return jsonReply(botapi.Message{ID: id, Chat: botapi.Chat{ID: in.Chat, Type: "private"}}), nil
	})}
	return newDispatcher(botapi.New("test-token", h), a, []int64{101, 202}), a, &next
}
func callback(id uuid.UUID, actor, message int64, action string) botapi.Update {
	return botapi.Update{ID: 1, Callback: &botapi.Callback{ID: uuid.NewString(), From: botapi.User{ID: actor}, Message: &botapi.Message{ID: message, Date: 1, Chat: botapi.Chat{ID: actor, Type: "private"}}, Data: "wt1:" + action + ":" + id.String()}}
}
func TestApprovalActorAndPayload(t *testing.T) {
	for _, kind := range []string{"allow", "bot", "other", "chat", "group", "inline", "inaccessible", "payload", "uuid", "nil"} {
		t.Run(kind, func(t *testing.T) {
			d, a, _ := approvalFixture(t)
			u := callback(uuid.New(), 101, 7, "a")
			switch kind {
			case "bot":
				u.Callback.From.IsBot = true
			case "other":
				u.Callback.From.ID = 999
			case "chat":
				u.Callback.Message.Chat.ID = 202
			case "group":
				u.Callback.Message.Chat.Type = "group"
			case "inline":
				u.Callback.Message = nil
			case "inaccessible":
				u.Callback.Message.Date = 0
			case "payload":
				u.Callback.Data = "wt1:approve:" + uuid.NewString()
			case "uuid":
				u.Callback.Data = "wt1:a:bad"
			case "nil":
				u.Callback.Data = "wt1:a:" + uuid.Nil.String()
			}
			if err := d.handle(context.Background(), u); err != nil {
				t.Fatal(err)
			}
			want := 0
			if kind == "allow" {
				want = 1
			}
			if len(a.decisions) != want {
				t.Fatal("actor/payload boundary")
			}
		})
	}
}
func TestApprovalCardEscapesHTML(t *testing.T) {
	id, op := uuid.New(), uuid.New()
	for _, status := range []string{"pending", "approved", "rejected", "provisioning", "needs_review", "active", "expired"} {
		p := TrialCard{RequestID: id, OperationID: &op, Status: status, Email: "<email>", DisplayName: "<name>", Comment: "<script>&", CreatedAt: time.Now()}
		text, k, err := renderCard(p)
		if err != nil || strings.Contains(text, "<email>") || strings.Contains(text, "<script>") || !strings.Contains(text, "&lt;script&gt;&amp;") {
			t.Fatal("unsafe card", err)
		}
		if status == "pending" && (k == nil || k.Rows[0][0].Data != "wt1:a:"+id.String()) {
			t.Fatal("callback compatibility")
		}
	}
	text, _, err := renderCard(TrialCard{RequestID: id, Status: "pending", DisplayName: "<name>", TelegramID: "101", CreatedAt: time.Now()})
	if err != nil || strings.Contains(text, "<name>") || !strings.Contains(text, "&lt;name&gt;") {
		t.Fatal("unsafe Telegram identity", err)
	}
}

func TestApprovalReasonBounds(t *testing.T) {
	d, a, next := approvalFixture(t)
	id := uuid.New()
	ctx := context.Background()
	if err := d.handle(ctx, callback(id, 101, 7, "c")); err != nil {
		t.Fatal(err)
	}
	u := botapi.Update{Message: &botapi.Message{ID: 8, Date: 1, From: &botapi.User{ID: 101}, Chat: botapi.Chat{ID: 101, Type: "private"}}}
	for _, invalid := range []string{"  ", strings.Repeat("🐴", 1001), "bad\x00reason"} {
		u.Message.Text = invalid
		if err := d.handle(ctx, u); err != nil {
			t.Fatal(err)
		}
		if d.pending[101].Reason != "" {
			t.Fatal("invalid reason accepted")
		}
	}
	u.Message.Text = strings.Repeat("🐴", 1000)
	if err := d.handle(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := d.handle(ctx, callback(id, 101, *next, "y")); err != nil {
		t.Fatal(err)
	}
	if len(a.support) != 1 || a.support[0].Reason != u.Message.Text {
		t.Fatal("Unicode limit/command")
	}
}
func TestApprovalConfirmationIsolation(t *testing.T) {
	d, a, next := approvalFixture(t)
	id := uuid.New()
	ctx := context.Background()
	start := callback(id, 101, 7, "s")
	if err := d.handle(ctx, start); err != nil {
		t.Fatal(err)
	}
	reason := botapi.Update{ID: 2, Message: &botapi.Message{ID: 8, Date: 1, From: &botapi.User{ID: 101}, Chat: botapi.Chat{ID: 101, Type: "private"}, Text: "Причина 🐴"}}
	if err := d.handle(ctx, reason); err != nil {
		t.Fatal(err)
	}
	old := *next
	// Start another dialogue for the same target; the old button must not
	// silently confirm a new reason.
	if err := d.handle(ctx, callback(id, 101, 7, "s")); err != nil {
		t.Fatal(err)
	}
	if err := d.handle(ctx, reason); err != nil {
		t.Fatal(err)
	}
	current := *next
	for _, u := range []botapi.Update{callback(id, 101, old, "y"), callback(id, 202, current, "y")} {
		if err := d.handle(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.support) != 0 {
		t.Fatal("old/other confirmation applied")
	}
	a.fail = true
	confirm := callback(id, 101, current, "y")
	if err := d.handle(ctx, confirm); err == nil {
		t.Fatal("lost domain reply hidden")
	}
	if err := d.handle(ctx, confirm); err != nil {
		t.Fatal(err)
	}
	if len(a.support) != 2 || a.support[0].Key != a.support[1].Key || a.support[0].Reason != "Причина 🐴" {
		t.Fatal("lost reply changed idempotency key")
	}
	restarted, again, _ := approvalFixture(t)
	if err := restarted.handle(ctx, confirm); err != nil || len(again.support) != 0 {
		t.Fatal("restart applied stale confirmation", err)
	}
}
