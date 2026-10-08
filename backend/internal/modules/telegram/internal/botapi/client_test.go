package botapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, code string
		retry      time.Duration
	}{
		{"json", 200, "{", "INVALID_RESPONSE", 0},
		{"large", 200, strings.Repeat("x", (1<<20)+1), "INVALID_RESPONSE", 0},
		{"message", 200, `{"ok":true,"result":{"message_id":0,"chat":{"id":101}}}`, "INVALID_RESPONSE", 0},
		{"chat", 200, `{"ok":true,"result":{"message_id":1,"chat":{"id":999}}}`, "INVALID_RESPONSE", 0},
		{"redirect", 302, "", "INVALID_RESPONSE", 0},
		{"auth", 401, `{"ok":false,"error_code":401,"description":"secret-body"}`, "UNAUTHORIZED", 0},
		{"conflict", 409, `{"ok":false,"error_code":409}`, "CONFLICT", 0},
		{"rate", 429, `{"ok":false,"error_code":429,"parameters":{"retry_after":7}}`, "RATE_LIMITED", 7 * time.Second},
		{"unchanged", 400, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`, "NOT_MODIFIED", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.telegram.org" {
					t.Fatal("unexpected origin")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{"Location": []string{"https://another.example/"}}}, nil
			})}
			_, err := New("test-token", h).SendMessage(context.Background(), 101, "card", nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code || apiErr.RetryAfter != tc.retry {
				t.Fatal("wrong safe error", err)
			}
			for _, secret := range []string{"test-token", "api.telegram.org", "secret-body"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("unsafe error")
				}
			}
		})
	}
	t.Run("network", func(t *testing.T) {
		h := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { return nil, errors.New("test-token secret-body") })}
		_, err := New("test-token", h).SendMessage(context.Background(), 101, "card", nil)
		if err == nil || err.Error() != "UNAVAILABLE" {
			t.Fatal("network details exposed", err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		h := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			if !errors.Is(r.Context().Err(), context.Canceled) {
				t.Fatal("cancellation not propagated")
			}
			return nil, r.Context().Err()
		})}
		_, err := New("test-token", h).SendMessage(ctx, 101, "card", nil)
		if err == nil || err.Error() != "UNAVAILABLE" {
			t.Fatal("unsafe cancellation", err)
		}
	})
}

func TestPollingTransportContract(t *testing.T) {
	var requests []string
	h := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.Path)
		var in struct {
			Offset         int64
			Limit, Timeout int
			Allowed        []string `json:"allowed_updates"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatal(err)
		}
		if in.Offset != 41 || in.Limit != 1 || in.Timeout != 30 || len(in.Allowed) != 3 || in.Allowed[2] != "pre_checkout_query" {
			t.Fatal("polling contract")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":[{"update_id":41,"callback_query":{"id":"cb","from":{"id":101},"data":"wt1:a:id"}}]}`))}, nil
	})}
	c := New("test-token", h)
	updates, err := c.GetUpdates(context.Background(), 41)
	if err != nil || len(updates) != 1 || updates[0].ID != 41 || updates[0].Callback.ID != "cb" || len(requests) != 1 {
		t.Fatal("updates mapping", err)
	}
	if h.CheckRedirect != nil {
		t.Fatal("caller client mutated")
	}
}

// Catches wrong provider currency, decimal scaling, recurrence and unsafe payout arguments.
func TestStarsWireContract(t *testing.T) {
	h := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		var in map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatal(err)
		}
		body := `{"ok":true,"result":true}`
		switch {
		case strings.HasSuffix(r.URL.Path, "createInvoiceLink"):
			if string(in["currency"]) != `"XTR"` || string(in["provider_token"]) != `""` || in["subscription_period"] != nil || string(in["payload"]) != `"stars:v1:00000000-0000-4000-8000-000000000001"` {
				t.Fatal("invoice provider boundary")
			}
			var prices []struct{ Amount int64 }
			if json.Unmarshal(in["prices"], &prices) != nil || len(prices) != 1 || prices[0].Amount != 100 {
				t.Fatal("integer Stars price lost")
			}
			body = `{"ok":true,"result":"https://t.me/$owned_invoice"}`
		case strings.HasSuffix(r.URL.Path, "answerPreCheckoutQuery"):
			if string(in["pre_checkout_query_id"]) != `"owned-query"` || string(in["ok"]) != "false" || string(in["error_message"]) != `"Unavailable"` {
				t.Fatal("precheckout refusal boundary")
			}
			if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > 5*time.Second {
				t.Fatal("precheckout provider deadline")
			}
		case strings.HasSuffix(r.URL.Path, "refundStarPayment"):
			if string(in["user_id"]) != "701" || string(in["telegram_payment_charge_id"]) != `"owned-charge"` {
				t.Fatal("refund payer/charge lost")
			}
		default:
			t.Fatal("unexpected Stars API")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	c := New("owned-test-token", h)
	ctx := context.Background()
	link, err := c.CreateStarsInvoice(ctx, "Subscription", "30 days", "stars:v1:00000000-0000-4000-8000-000000000001", 100, 0)
	if err != nil || link != "https://t.me/$owned_invoice" {
		t.Fatal("invoice response", err)
	}
	if err = c.AnswerStarsPreCheckout(ctx, "owned-query", false, "Unavailable"); err != nil {
		t.Fatal(err)
	}
	if err = c.RefundStars(ctx, 701, "owned-charge"); err != nil {
		t.Fatal(err)
	}
}
