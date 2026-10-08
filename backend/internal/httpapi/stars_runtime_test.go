package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/telegram"
	"github.com/google/uuid"
)

type starsTransport func(*http.Request) (*http.Response, error)

func (f starsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func starsReply(out any) *http.Response {
	b, _ := json.Marshal(map[string]any{"ok": true, "result": out})
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}
}
func starsUpdate(order uuid.UUID, charge string, refunded bool, id, date int64) map[string]any {
	payment := map[string]any{"currency": "XTR", "total_amount": 100, "invoice_payload": "stars:v1:" + order.String(), "telegram_payment_charge_id": charge, "provider_payment_charge_id": ""}
	field := "successful_payment"
	if refunded {
		field = "refunded_payment"
	}
	return map[string]any{"update_id": id, "message": map[string]any{"message_id": id, "date": date, "chat": map[string]any{"id": 701, "type": "private"}, "from": map[string]any{"id": 701, "is_bot": false, "language_code": "en"}, field: payment}}
}
func starsRunUpdates(t *testing.T, s *regressionFixture, updates []map[string]any, preCheckoutCodes ...int) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := &http.Client{Transport: starsTransport(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(req.URL.Path, "getWebhookInfo"):
			return starsReply(map[string]any{"url": ""}), nil
		case strings.HasSuffix(req.URL.Path, "getMe"):
			return starsReply(map[string]any{"id": 123, "is_bot": true, "username": "fixture_bot", "has_main_web_app": true}), nil
		case strings.HasSuffix(req.URL.Path, "setChatMenuButton"):
			return starsReply(true), nil
		case strings.HasSuffix(req.URL.Path, "sendMessage"):
			var in struct {
				Chat int64 `json:"chat_id"`
			}
			json.NewDecoder(req.Body).Decode(&in)
			return starsReply(map[string]any{"message_id": 900, "chat": map[string]any{"id": in.Chat}}), nil
		case strings.HasSuffix(req.URL.Path, "answerPreCheckoutQuery"):
			code := preCheckoutCodes[0]
			if len(preCheckoutCodes) > 1 {
				preCheckoutCodes = preCheckoutCodes[1:]
			}
			if code == 200 {
				return starsReply(true), nil
			}
			b, _ := json.Marshal(map[string]any{"ok": false, "error_code": code, "description": "owned query is too old or already answered", "parameters": map[string]int{"retry_after": 1}})
			return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
		case strings.HasSuffix(req.URL.Path, "getUpdates"):
			var in struct{ Offset int64 }
			if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
				t.Error(err)
				return nil, err
			}
			for _, u := range updates {
				if u["update_id"].(int64) >= in.Offset {
					return starsReply([]map[string]any{u}), nil
				}
			}
			cancel()
			return nil, context.Canceled
		default:
			t.Error("unexpected owned Stars transport method")
			return nil, context.Canceled
		}
	})}
	modules := app.NewModules(s.pool, s.limiter, s.queue, s.cfg)
	r, err := app.NewTelegram(telegram.Config{Enabled: true, Token: "123:owned-local-test-token-0000", Operators: []int64{901}}, modules, s.cfg.HTTP.CabinetOrigin, h)
	if err != nil {
		t.Fatal(err)
	}
	return r.Run(ctx)
}

// A closed query has no money effect and must not pin later genuine charges.
// Transient answer failures still retry before acknowledging the query.
func TestStarsRuntimePreCheckoutLiveness(t *testing.T) {
	for _, code := range []int{400, 429, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			s, e, _, order := starsTestOrder(t)
			codes := []int{code}
			if code != 400 {
				codes = append(codes, 200)
			}
			query := map[string]any{"update_id": int64(10), "pre_checkout_query": map[string]any{"id": "owned-closed-query", "from": map[string]any{"id": 701}, "currency": "XTR", "total_amount": 100, "invoice_payload": "stars:v1:" + order.OrderId.String()}}
			paid := starsUpdate(order.OrderId, "after-pre-checkout", false, 11, e.Clock().Unix()+1)
			if err := starsRunUpdates(t, s, []map[string]any{query, paid}, codes...); err != nil {
				t.Fatal(err)
			}
			var receipts, audits, jobs int
			if err := e.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM purchase_receipts WHERE order_id=$1),(SELECT count(*) FROM audit_events WHERE account_id=(SELECT account_id FROM purchase_orders WHERE id=$1) AND action='stars_payment_received'),(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=$1::uuid::text)`, order.OrderId).Scan(&receipts, &audits, &jobs); err != nil || receipts != 1 || audits != 1 || jobs != 1 {
				t.Fatal("answer failure pinned a genuine charge or duplicated its receipt/audit/job", err)
			}
		})
	}
}

// Catches acknowledging a genuine payment without its durable receipt/job.
func TestStarsRuntimeSettles(t *testing.T) {
	s, e, _, order := starsTestOrder(t)
	if err := starsRunUpdates(t, s, []map[string]any{starsUpdate(order.OrderId, "owned-charge", false, 11, e.Clock().Unix()+1)}); err != nil {
		t.Fatal("owned one-time payment was not handled", err)
	}
	var receipts, jobs int
	if err := e.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM purchase_receipts WHERE order_id=$1),(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=$1::uuid::text)`, order.OrderId).Scan(&receipts, &jobs); err != nil || receipts != 1 || jobs != 1 {
		t.Fatal("payment acknowledged without durable receipt/job", err)
	}
	got, err := s.payments.PurchaseOrder(context.Background(), order.Quote.PlanId, order.OrderId)
	if err == nil || got.OrderId != uuid.Nil {
		t.Fatal("order ownership bypassed")
	}
}

// Catches an early refund blocking ordered polling or a later charge granting access.
func TestStarsRuntimeRefundBeforePayment(t *testing.T) {
	s, e, _, order := starsTestOrder(t)
	updates := []map[string]any{starsUpdate(order.OrderId, "early-refund-charge", true, 11, e.Clock().Unix()+2), starsUpdate(order.OrderId, "early-refund-charge", false, 12, e.Clock().Unix()+1)}
	if err := starsRunUpdates(t, s, updates); err != nil {
		t.Fatal("early refund stopped owned polling", err)
	}
	var receipts, refunds, jobs int
	var honest bool
	if err := e.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM purchase_receipts WHERE order_id=$1),(SELECT count(*) FROM purchase_refunds WHERE order_id=$1),(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=$1::uuid::text),EXISTS(SELECT 1 FROM purchase_refunds WHERE order_id=$1 AND source='telegram' AND operator_account_id IS NULL AND returned_amount='100' AND returned_currency='XTR')`, order.OrderId).Scan(&receipts, &refunds, &jobs, &honest); err != nil || receipts != 1 || refunds != 1 || jobs != 0 || !honest {
		t.Fatal("refund-before-charge lost proof or funded access", err)
	}
}
