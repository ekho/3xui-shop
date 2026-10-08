package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/modules/payments"
)

// Catches the native adapter silently sending a one-shot invoice for a recurring quote.
func TestStarsRecurringInvoice(t *testing.T) {
	for _, period := range []int64{0, 2592000, 1} {
		t.Run(strconv.FormatInt(period, 10), func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if !strings.HasSuffix(r.URL.Path, "createInvoiceLink") {
					t.Fatal("unexpected provider call")
				}
				var in map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&in) != nil {
					t.Fatal("native invoice body")
				}
				if period == 0 && in["subscription_period"] != nil || period == 2592000 && string(in["subscription_period"]) != "2592000" {
					t.Fatal("wrong native recurring period")
				}
				if string(in["currency"]) != `"XTR"` || string(in["provider_token"]) != `""` {
					t.Fatal("Stars native currency/provider boundary")
				}
				return jsonReply("https://t.me/$owned_invoice"), nil
			})}
			runtime := runtimeFixture(t, client, &actionRecorder{}, &outboxRecorder{})
			runtime.started = true
			var invoice payments.StarsInvoice
			raw, _ := json.Marshal(map[string]any{"Payload": "stars:v1:00000000-0000-4000-8000-000000000001", "Title": "Subscription", "Description": "30 days", "Amount": 100, "SubscriptionPeriod": period})
			json.Unmarshal(raw, &invoice)
			_, err := runtime.StarsGateway().Invoice(context.Background(), invoice)
			if period == 1 {
				if err == nil || calls != 0 {
					t.Fatal("unsupported recurring period reached provider")
				}
			} else if err != nil || calls != 1 {
				t.Fatal("valid native invoice lost", err)
			}
		})
	}
}

// Catches native false/400/lost replies being accepted as successful billing control.
func TestStarsSubscriptionGateway(t *testing.T) {
	for _, mode := range []string{"true", "false", "400", "invalid", "lost"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			var canceled bool
			client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if !strings.HasSuffix(r.URL.Path, "editUserStarSubscription") {
					t.Fatal("wrong native control method")
				}
				var in struct {
					Payer    int64  `json:"user_id"`
					Charge   string `json:"telegram_payment_charge_id"`
					Canceled bool   `json:"is_canceled"`
				}
				if json.NewDecoder(r.Body).Decode(&in) != nil || in.Payer != 701 || in.Charge != "owned-first-charge" || in.Canceled != canceled {
					t.Fatal("native control coordinates changed")
				}
				if mode == "lost" {
					return nil, context.DeadlineExceeded
				}
				if mode == "400" {
					return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":400}`))}, nil
				}
				if mode == "invalid" {
					return jsonReply("true"), nil
				}
				return jsonReply(mode == "true"), nil
			})}
			runtime := runtimeFixture(t, client, &actionRecorder{}, &outboxRecorder{})
			runtime.started = true
			g := runtime.StarsGateway()
			if g.EditSubscription == nil {
				t.Fatal("native subscription control missing")
			}
			for _, value := range []bool{true, false} {
				canceled = value
				err := g.EditSubscription(context.Background(), 701, "owned-first-charge", value)
				if (err == nil) != (mode == "true") {
					t.Fatal("native control result forged")
				}
			}
			if calls != 2 {
				t.Fatal("native setter was not called")
			}
		})
	}
}

// Catches rejecting Telegram's first-seen random IDs after an idle week and
// omitting native subscription status from the actual polling request.
func TestStarsSubscriptionUpdateTransport(t *testing.T) {
	client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		var in struct {
			Allowed []string `json:"allowed_updates"`
			Offset  int64    `json:"offset"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Offset != 100 {
			t.Fatal("native poll boundary")
		}
		found := false
		for _, s := range in.Allowed {
			if s == "subscription" {
				found = true
			}
		}
		if !found {
			t.Error("native subscription updates not requested")
		}
		return jsonReply([]map[string]any{{"update_id": 2, "subscription": map[string]any{"user": map[string]any{"id": 701, "is_bot": false}, "state": "active", "invoice_payload": "owned-payload"}}}), nil
	})}
	runtime := runtimeFixture(t, client, &actionRecorder{}, &outboxRecorder{})
	got, err := runtime.api.GetUpdates(context.Background(), 100)
	if err != nil || len(got) != 1 || got[0].ID != 2 {
		t.Fatal("first-seen random update ID rejected", err)
	}
}
