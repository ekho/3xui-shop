package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"github.com/google/uuid"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testToken = "123456789:abcdefghijklmnopqrstuvwxyz012345678"

type outboxRecorder struct {
	results []DeliveryOutcome
	err     error
}

func (*outboxRecorder) Claim(context.Context) (*Delivery, error) { return nil, nil }
func (o *outboxRecorder) Complete(_ context.Context, _ Delivery, in DeliveryOutcome) error {
	o.results = append(o.results, in)
	return o.err
}
func runtimeFixture(t *testing.T, h *http.Client, a TrialActions, o Outbox) *Runtime {
	t.Helper()
	r, err := New(Config{Enabled: true, Token: testToken, Operators: []int64{101, 202}}, h, a, o, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestPollingAcknowledgesAfterHandling(t *testing.T) {
	a := &actionRecorder{fail: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var offsets []int64
	u := callback(uuid.New(), 101, 7, "a")
	u.ID = 11
	h := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "answerCallbackQuery") {
			return jsonReply(true), nil
		}
		var in struct{ Offset int64 }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatal(err)
		}
		offsets = append(offsets, in.Offset)
		if len(offsets) == 3 {
			cancel()
			return nil, context.Canceled
		}
		return jsonReply([]botapi.Update{u}), nil
	})}
	r := runtimeFixture(t, h, a, &outboxRecorder{})
	if err := r.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(offsets) != 3 || offsets[0] != 0 || offsets[1] != 0 || offsets[2] != 12 || len(a.decisions) != 2 || a.decisions[0].CallbackID != a.decisions[1].CallbackID {
		t.Fatal("update was acknowledged before successful handling")
	}
}

func TestPollingReceivesLowerIDAfterIdle(t *testing.T) {
	for _, outage := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty_queue", true: "provider_outage"}[outage], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := &actionRecorder{}
			old, next := callback(uuid.New(), 101, 7, "a"), callback(uuid.New(), 202, 8, "a")
			old.ID, next.ID = 900, 27
			now := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
			var offsets []int64
			h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
				if strings.HasSuffix(req.URL.Path, "answerCallbackQuery") {
					return jsonReply(true), nil
				}
				var in struct{ Offset int64 }
				if json.NewDecoder(req.Body).Decode(&in) != nil {
					t.Fatal("invalid owned polling input")
				}
				offsets = append(offsets, in.Offset)
				switch len(offsets) {
				case 1:
					return jsonReply([]botapi.Update{old}), nil
				case 2:
					// The next provider ID is random after a week without updates.
					now = now.Add(8 * 24 * time.Hour)
					if outage {
						return nil, errors.New("owned provider outage")
					}
					return jsonReply([]botapi.Update{}), nil
				case 3:
					// getUpdates confirms lower IDs before returning the response.
					if in.Offset > next.ID {
						return jsonReply([]botapi.Update{}), nil
					}
					return jsonReply([]botapi.Update{next}), nil
				default:
					cancel()
					return nil, context.Canceled
				}
			})}
			r := runtimeFixture(t, h, a, &outboxRecorder{})
			r.now = func() time.Time { return now }
			if err := r.poll(ctx); err != nil {
				t.Fatal(err)
			}
			if len(offsets) != 4 || offsets[0] != 0 || offsets[1] != 901 || offsets[2] != 0 || offsets[3] != 28 || len(a.decisions) != 2 {
				t.Fatal("new lower update was acknowledged without handling", offsets, len(a.decisions))
			}
		})
	}
}

func TestPollingPermanentPromptFailure(t *testing.T) {
	for _, stage := range []string{"prompt", "confirmation"} {
		for _, code := range []int{400, 403} {
			t.Run(stage+"/"+http.StatusText(code), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				target := uuid.New()
				first := callback(target, 101, 7, "s")
				first.ID = 11
				if stage == "confirmation" {
					first = botapi.Update{ID: 11, Message: &botapi.Message{ID: 8, Date: 1, From: &botapi.User{ID: 101}, Chat: botapi.Chat{ID: 101, Type: "private"}, Text: "Support review"}}
				}
				next := callback(uuid.New(), 202, 7, "a")
				next.ID = 12
				var offsets []int64
				h := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "sendMessage") {
						body, _ := json.Marshal(map[string]any{"ok": false, "error_code": code})
						return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
					}
					if strings.HasSuffix(r.URL.Path, "answerCallbackQuery") {
						return jsonReply(true), nil
					}
					var in struct{ Offset int64 }
					json.NewDecoder(r.Body).Decode(&in)
					offsets = append(offsets, in.Offset)
					if len(offsets) == 3 || (len(offsets) == 2 && in.Offset != 12) {
						cancel()
						return nil, context.Canceled
					}
					if in.Offset == 12 {
						return jsonReply([]botapi.Update{next}), nil
					}
					return jsonReply([]botapi.Update{first}), nil
				})}
				a := &actionRecorder{}
				r := runtimeFixture(t, h, a, &outboxRecorder{})
				if stage == "confirmation" {
					r.dispatcher.pending[101] = &confirmation{SupportAction: SupportAction{TargetID: target, ActorID: 101, Key: uuid.New()}, action: "s", messageID: 7}
				}
				if err := r.poll(ctx); err != nil {
					t.Fatal(err)
				}
				if len(offsets) != 3 || offsets[1] != 12 || offsets[2] != 13 || len(a.decisions) != 1 || a.decisions[0].ActorID != 202 || r.dispatcher.pending[101] != nil {
					t.Fatal("permanent prompt failure blocked the next operator or retained the dialogue")
				}
			})
		}
	}
}

func TestPollingCosmeticRateLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u := callback(uuid.New(), 101, 7, "a")
	u.ID = 11
	next := callback(uuid.New(), 202, 7, "a")
	next.ID = 12
	var r *Runtime
	var limited time.Time
	var offsets []int64
	replies := 0
	h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "answerCallbackQuery") {
			replies++
			if replies == 1 {
				limited = time.Now()
				return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":429,"parameters":{"retry_after":1}}`))}, nil
			}
			if time.Since(limited) < time.Second {
				t.Error("next reply ignored retry_after")
			}
			return jsonReply(true), nil
		}
		var in struct{ Offset int64 }
		json.NewDecoder(req.Body).Decode(&in)
		offsets = append(offsets, in.Offset)
		if len(offsets) == 2 && (r.State().Code != "RATE_LIMITED" || time.Since(limited) < time.Second) {
			t.Error("rate limit state or waiting was lost")
		}
		if len(offsets) == 3 {
			cancel()
			return nil, context.Canceled
		}
		if in.Offset > u.ID {
			return jsonReply([]botapi.Update{next}), nil
		}
		return jsonReply([]botapi.Update{u}), nil
	})}
	a := &actionRecorder{}
	r = runtimeFixture(t, h, a, &outboxRecorder{})
	if err := r.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(offsets) != 3 || offsets[1] != 0 || offsets[2] != 12 || len(a.decisions) != 2 || a.decisions[0].CallbackID != a.decisions[1].CallbackID {
		t.Fatal("rate limit retry changed the committed command identity")
	}
}
func TestUnsupportedPaymentNotAcknowledged(t *testing.T) {
	for _, body := range []string{
		`{"update_id":12,"pre_checkout_query":{"id":"payment"}}`,
		`{"update_id":12,"message":{"message_id":1,"successful_payment":{"currency":"XTR"}}}`,
		`{"update_id":12,"message":{"message_id":1,"refunded_payment":{"currency":"XTR"}}}`,
	} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			h := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":[` + body + `]}`))}, nil
			})}
			r := runtimeFixture(t, h, &actionRecorder{}, &outboxRecorder{})
			if err := r.poll(context.Background()); err == nil || calls != 1 || r.State().Code != "UNSUPPORTED_PAYMENT" {
				t.Fatal("payment consumed by partial runtime", err)
			}
		})
	}
}

func TestWebhookConflictRetainsUpdates(t *testing.T) {
	calls := 0
	h := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "getWebhookInfo") {
			t.Fatal("webhook modified or polling started")
		}
		return jsonReply(botapi.WebhookInfo{URL: "https://example.test/hook"}), nil
	})}
	r := runtimeFixture(t, h, &actionRecorder{}, &outboxRecorder{})
	if err := r.Run(context.Background()); err == nil || calls != 1 || r.State().Code != "WEBHOOK_CONFIGURED" {
		t.Fatal("webhook ownership ignored", err)
	}
}
func TestDeliveryUnknownResult(t *testing.T) {
	o := &outboxRecorder{}
	calls := 0
	h := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) { calls++; return nil, context.DeadlineExceeded })}
	r := runtimeFixture(t, h, &actionRecorder{}, o)
	err := r.deliver(context.Background(), Delivery{ID: uuid.New(), ChatID: 101, LeaseExpiresAt: time.Now().Add(time.Minute), Card: TrialCard{RequestID: uuid.New(), Email: "trial@example.test", Status: "pending", CreatedAt: time.Now()}})
	if err == nil || calls != 1 || len(o.results) != 0 {
		t.Fatal("unknown send falsely completed", err)
	}
}
func TestDeliveryLateLease(t *testing.T) {
	o := &outboxRecorder{}
	calls := 0
	h := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) { calls++; return jsonReply(true), nil })}
	r := runtimeFixture(t, h, &actionRecorder{}, o)
	if err := r.deliver(context.Background(), Delivery{ChatID: 101, LeaseExpiresAt: time.Now().Add(-time.Second)}); err == nil || calls != 0 || len(o.results) != 0 {
		t.Fatal("expired lease used")
	}
}
func TestTelegramDisabledWithoutToken(t *testing.T) {
	t.Setenv("TELEGRAM_ENABLED", "")
	t.Setenv("BOT_TOKEN_FILE", "/missing-token")
	t.Setenv("BOT_TOKEN", "")
	cfg, err := LoadConfig(nil)
	if err != nil || cfg.Enabled {
		t.Fatal("disabled reads token", err)
	}
	r, err := New(cfg, nil, nil, nil, nil)
	if err != nil || r.State().Enabled {
		t.Fatal(err)
	}
	if err = r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TELEGRAM_ENABLED", "true")
	if _, err = LoadConfig([]int64{101}); err == nil {
		t.Fatal("missing token accepted")
	}
	p := filepath.Join(t.TempDir(), "token")
	if err = os.WriteFile(p, []byte(testToken), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOT_TOKEN_FILE", p)
	if _, err = LoadConfig([]int64{-1}); err == nil {
		t.Fatal("bad operator accepted")
	}
	if _, err = LoadConfig([]int64{101}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOT_TOKEN", "conflicting-value")
	if _, err = LoadConfig([]int64{101}); err == nil {
		t.Fatal("plaintext token accepted")
	}
}
func TestDeliveryEditFallbackAndLateCompletion(t *testing.T) {
	for _, mode := range []string{"unchanged", "fallback", "late"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			o := &outboxRecorder{}
			if mode == "late" {
				o.err = &ActionError{Code: "REQUEST_STATE_CONFLICT"}
			}
			h := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 && mode != "late" {
					description := "message to edit not found"
					if mode == "unchanged" {
						description = "message is not modified"
					}
					return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":400,"description":"` + description + `"}`))}, nil
				}
				var in struct {
					Chat    int64 `json:"chat_id"`
					Message int64 `json:"message_id"`
				}
				json.NewDecoder(r.Body).Decode(&in)
				id := in.Message
				if id == 0 {
					id = 99
				}
				return jsonReply(botapi.Message{ID: id, Chat: botapi.Chat{ID: in.Chat}}), nil
			})}
			r := runtimeFixture(t, h, &actionRecorder{}, o)
			message := int64(42)
			err := r.deliver(context.Background(), Delivery{ChatID: 101, LeaseExpiresAt: time.Now().Add(time.Minute), Card: TrialCard{RequestID: uuid.New(), Email: "trial@example.test", Status: "approved", CreatedAt: time.Now(), TargetMessageID: &message}})
			if mode == "late" {
				if !errors.Is(err, o.err) {
					t.Fatal("late completion concealed", err)
				}
				return
			}
			want := 1
			if mode == "fallback" {
				want = 2
			}
			if err != nil || calls != want || len(o.results) != 1 || o.results[0].Kind != "sent" {
				t.Fatal("edit outcome", err)
			}
		})
	}
}
