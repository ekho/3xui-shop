package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"github.com/google/uuid"
)

func TestLegacyLateCryptoProviderProofOverHTTP(t *testing.T) {
	for _, provider := range []string{"cryptomus", "heleket"} {
		t.Run(provider, func(t *testing.T) {
			s, e, _, _, f := cryptoFixture(t, provider)
			legacyOrder, invoice := uuid.NewString(), uuid.NewString()
			f.payment["order_id"], f.payment["uuid"] = legacyOrder, invoice
			f.payment["status"], f.payment["payment_status"], f.payment["is_final"] = "paid", "paid", true
			f.payment["payment_amount"], f.payment["payer_amount"], f.payment["merchant_amount"] = "1.0", "1.0", "1.0"
			f.payment["updated_at"] = e.Clock().Format(time.RFC3339Nano)
			h := New(app.NewModules(s.pool, s.limiter, s.queue, s.cfg), s.pool, s.cfg.HTTP)
			body := signedCryptoBody(fmt.Sprintf(`{"type":"payment","uuid":"%s","order_id":"%s"}`, invoice, legacyOrder), provider)
			post := func(raw []byte) int {
				r := httptest.NewRequest("POST", "/webhooks/"+provider, bytes.NewReader(raw))
				r.Header.Set("Content-Type", "application/json")
				r.RemoteAddr = cryptoCaseFor(provider).source + ":12345"
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w.Code
			}
			for i := 0; i < 2; i++ {
				if code := post(body); code != http.StatusOK {
					t.Fatal("verified late payment", code)
				}
			}
			var amount int64
			var state string
			if err := e.Pool.QueryRow(context.Background(), `SELECT amount_minor,state FROM legacy_payment_receipts WHERE provider=$1 AND source_id=$2`, provider, invoice).Scan(&amount, &state); err != nil || amount != 12345 || state != "review" {
				t.Fatal("late money not retained", err)
			}
			f.payment["amount"] = "124.45"
			if code := post(body); code != http.StatusOK {
				t.Fatal("conflicting provider proof", code)
			}
			if err := e.Pool.QueryRow(context.Background(), `SELECT amount_minor,state FROM legacy_payment_receipts WHERE provider=$1 AND source_id=$2`, provider, invoice).Scan(&amount, &state); err != nil || amount != 12345 || state != "conflict" {
				t.Fatal("first proof overwritten", err)
			}
			var native int
			if err := e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM purchase_receipts`).Scan(&native); err != nil || native != 0 {
				t.Fatal("legacy proof minted native funding", err)
			}
		})
	}
}

func TestLegacyLateYooKassaProviderProofOverHTTP(t *testing.T) {
	s, e, _, _, f := kassaFixture(t)
	settleKassa(e, f)
	id := f.payment["id"].(string)
	h := New(app.NewModules(s.pool, s.limiter, s.queue, s.cfg), s.pool, s.cfg.HTTP)
	post := func() int {
		r := httptest.NewRequest("POST", "/webhooks/yookassa", bytes.NewBufferString(fmt.Sprintf(`{"type":"notification","event":"payment.succeeded","object":{"id":"%s"}}`, id)))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < 2; i++ {
		if code := post(); code != http.StatusOK {
			t.Fatal("verified late YooKassa", code)
		}
	}
	var amount int64
	var state string
	if err := e.Pool.QueryRow(context.Background(), `SELECT amount_minor,state FROM legacy_payment_receipts WHERE provider='yookassa' AND event_kind='paid' AND source_id=$1`, id).Scan(&amount, &state); err != nil || amount != 9007199254740993 || state != "review" {
		t.Fatal("late YooKassa not retained", err)
	}
	f.payment["refunded_amount"] = map[string]any{"value": "1.00", "currency": "RUB"}
	if code := post(); code != http.StatusOK {
		t.Fatal("late refund", code)
	}
	var refunded int64
	if err := e.Pool.QueryRow(context.Background(), `SELECT amount_minor FROM legacy_payment_receipts WHERE provider='yookassa' AND event_kind='refund_observed' AND source_id=$1`, id).Scan(&refunded); err != nil || refunded != 100 {
		t.Fatal("late refund not retained", err)
	}
	f.payment["amount"].(map[string]any)["value"] = "90071992547409.94"
	if code := post(); code != http.StatusOK {
		t.Fatal("conflicting provider proof", code)
	}
	if err := e.Pool.QueryRow(context.Background(), `SELECT amount_minor,state FROM legacy_payment_receipts WHERE provider='yookassa' AND event_kind='paid' AND source_id=$1`, id).Scan(&amount, &state); err != nil || amount != 9007199254740993 || state != "conflict" {
		t.Fatal("first proof overwritten", err)
	}
	var native int
	if err := e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM purchase_receipts`).Scan(&native); err != nil || native != 0 {
		t.Fatal("legacy proof minted native funding", err)
	}
}

// The actual Telegram Bot API polling decoder must retain an old packed charge.
func TestLegacyStarsLateRecurringAndRefundThroughNativeTelegram(t *testing.T) {
	for _, amount := range []int{100, 1} {
		t.Run(fmt.Sprint(amount), func(t *testing.T) {
			s, e, _, order := starsTestOrder(t)
			ctx := context.Background()
			var account uuid.UUID
			if err := e.Pool.QueryRow(ctx, `SELECT account_id FROM purchase_orders WHERE id=$1`, order.OrderId).Scan(&account); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET legacy_user_id=5 WHERE id=$1`, account); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(ctx, `INSERT INTO legacy_stars_imports(source_legacy_user_id,account_id,source_tg_id,source_snapshot) VALUES(5,$1,701,'{}')`, account); err != nil {
				t.Fatal(err)
			}
			makeUpdate := func(charge string, refunded, recurring bool, id int64) map[string]any {
				u := starsUpdate(order.OrderId, charge, refunded, id, e.Clock().Unix())
				field := "successful_payment"
				if refunded {
					field = "refunded_payment"
				}
				p := u["message"].(map[string]any)[field].(map[string]any)
				p["invoice_payload"] = "subscription:pay_telegram_stars:0:0:701:2:30:0:100.0"
				p["total_amount"] = amount
				if recurring {
					p["is_recurring"], p["is_first_recurring"] = true, false
				}
				return u
			}
			var beforeOrders, beforeJobs, beforeAccess, beforeBonuses int
			counts := `SELECT (SELECT count(*) FROM purchase_orders),(SELECT count(*) FROM river_job),(SELECT count(*) FROM access_operations),(SELECT count(*) FROM referrer_rewards)`
			if err := e.Pool.QueryRow(ctx, counts).Scan(&beforeOrders, &beforeJobs, &beforeAccess, &beforeBonuses); err != nil {
				t.Fatal(err)
			}
			updates := []map[string]any{
				makeUpdate("old-paid-charge", false, false, 11),
				makeUpdate("old-recurring-charge", false, true, 12),
				makeUpdate("old-recurring-charge", true, false, 13),
				makeUpdate("old-paid-charge", false, false, 14),
				makeUpdate("old-recurring-charge", false, true, 15),
				makeUpdate("old-recurring-charge", true, false, 16),
			}
			conflict := makeUpdate("old-paid-charge", false, false, 17)
			conflict["message"].(map[string]any)["successful_payment"].(map[string]any)["total_amount"] = amount + 1
			updates = append(updates, conflict)
			if err := starsRunUpdates(t, s, updates); err != nil {
				t.Fatal(err)
			}
			var count, orders, jobs, access, bonuses int
			if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM legacy_payment_receipts WHERE provider='telegram_stars' AND account_id=$1 AND amount_minor=$2 AND currency='XTR' `, account, amount).Scan(&count); err != nil || count != 3 {
				t.Fatalf("verified transport amount lost: count=%d err=%v", count, err)
			}
			var state string
			if err := e.Pool.QueryRow(ctx, `SELECT state FROM legacy_payment_receipts WHERE provider='telegram_stars' AND event_kind='paid' AND source_id='old-paid-charge'`).Scan(&state); err != nil || state != "conflict" {
				t.Fatal("Stars conflict not retained", err)
			}
			if err := e.Pool.QueryRow(ctx, counts).Scan(&orders, &jobs, &access, &bonuses); err != nil {
				t.Fatal(err)
			}
			if orders != beforeOrders || jobs != beforeJobs || access != beforeAccess || bonuses != beforeBonuses {
				t.Fatal("old Stars proof changed native effects")
			}
		})
	}
}

func TestLegacyYooKassaActualRefundOverHTTP(t *testing.T) {
	s, e, _, order, f := kassaFixture(t)
	settleKassa(e, f)
	paymentID, refundID := f.payment["id"].(string), uuid.NewString()
	f.refund = map[string]any{"id": refundID, "payment_id": paymentID, "status": "succeeded", "amount": map[string]any{"value": "1.00", "currency": "RUB"}, "created_at": e.Clock().Format(time.RFC3339Nano)}
	h := New(app.NewModules(s.pool, s.limiter, s.queue, s.cfg), s.pool, s.cfg.HTTP)
	post := func(refund, payment string) int {
		// The webhook carries a refund object. Its amount is only an untrusted hint.
		body := fmt.Sprintf(`{"type":"notification","event":"refund.succeeded","object":{"id":"%s","payment_id":"%s","status":"succeeded","amount":{"value":"999.00","currency":"RUB"},"created_at":"%s"}}`, refund, payment, e.Clock().Format(time.RFC3339Nano))
		r := httptest.NewRequest("POST", "/yookassa", bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, bad := range []struct{ refund, payment string }{{uuid.NewString(), paymentID}, {refundID, uuid.NewString()}} {
		if code := post(bad.refund, bad.payment); code == 200 {
			t.Fatal("unverified refund acknowledged")
		}
	}
	var count int
	if err := e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM legacy_payment_receipts`).Scan(&count); err != nil || count != 0 {
		t.Fatal("forged refund persisted", err)
	}
	for _, invalid := range []struct {
		field string
		value any
	}{
		{"status", "pending"},
		{"amount", map[string]any{"value": "1.00", "currency": "USD"}},
		{"created_at", e.Clock().Add(10 * time.Minute).Format(time.RFC3339Nano)},
	} {
		original := f.refund[invalid.field]
		f.refund[invalid.field] = invalid.value
		if code := post(refundID, paymentID); code == 200 {
			t.Fatal("invalid provider refund acknowledged", invalid.field)
		}
		f.refund[invalid.field] = original
	}
	f.payment["recipient"].(map[string]any)["account_id"] = "999999"
	if code := post(refundID, paymentID); code == 200 {
		t.Fatal("another merchant refund acknowledged")
	}
	f.payment["recipient"].(map[string]any)["account_id"] = "100001"
	for i := 0; i < 2; i++ {
		if code := post(refundID, paymentID); code != 200 {
			t.Fatal("actual refund rejected", code)
		}
	}
	var amount int64
	var reference, state string
	if err := e.Pool.QueryRow(context.Background(), `SELECT amount_minor,source_reference,state FROM legacy_payment_receipts WHERE provider='yookassa' AND event_kind='refunded' AND source_id=$1`, refundID).Scan(&amount, &reference, &state); err != nil || amount != 100 || reference != paymentID || state != "review" {
		t.Fatal("refund proof lost", err)
	}
	f.refund["amount"].(map[string]any)["value"] = "2.00"
	if code := post(refundID, paymentID); code != 200 {
		t.Fatal("refund conflict rejected", code)
	}
	if err := e.Pool.QueryRow(context.Background(), `SELECT amount_minor,state FROM legacy_payment_receipts WHERE provider='yookassa' AND event_kind='refunded' AND source_id=$1`, refundID).Scan(&amount, &state); err != nil || amount != 100 || state != "conflict" {
		t.Fatal("first refund proof overwritten", err)
	}
	if err := e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM legacy_payment_receipts`).Scan(&count); err != nil || count != 2 {
		t.Fatal("refund replay duplicated facts", err)
	}
	f.payment["refunded_amount"] = map[string]any{"value": "2.00", "currency": "RUB"}
	if err := s.payments.ReceiveYooKassa(context.Background(), paymentID); err != nil {
		t.Fatal("aggregate observation rejected", err)
	}
	var aggregate, individual, paid int
	if err := e.Pool.QueryRow(context.Background(), `SELECT
	 (SELECT count(*) FROM legacy_payment_receipts WHERE provider='yookassa' AND event_kind='refund_observed' AND source_id=$1 AND amount_minor=200 AND state='review'),
	 (SELECT count(*) FROM legacy_payment_receipts WHERE provider='yookassa' AND event_kind='refunded' AND source_id=$2 AND amount_minor=100 AND state='conflict'),
	 (SELECT count(*) FROM legacy_payment_receipts WHERE provider='yookassa' AND event_kind='paid' AND source_id=$1 AND state='review')`, paymentID, refundID).Scan(&aggregate, &individual, &paid); err != nil || aggregate != 1 || individual != 1 || paid != 1 {
		t.Fatal("aggregate refund confused with individual refund or paid proof", err)
	}
	for _, table := range []string{"purchase_receipts", "access_operations", "referrer_rewards"} {
		if err := e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("legacy refund produced %s: %v", table, err)
		}
	}
	manualCounts(t, s, order.OrderId, 0, 0)
}

func TestYooKassaNativeRefundHintRequiresReviewWithoutFunding(t *testing.T) {
	s, e, _, order, f := kassaFixture(t)
	syncKassa(t, s, order.OrderId)
	settleKassa(e, f)
	paymentID, refundID := f.payment["id"].(string), uuid.NewString()
	f.refund = map[string]any{"id": refundID, "payment_id": paymentID, "status": "succeeded", "amount": map[string]any{"value": "1.00", "currency": "RUB"}, "created_at": e.Clock().Format(time.RFC3339Nano)}
	for i := 0; i < 2; i++ {
		if err := s.payments.ReceiveYooKassaRefund(context.Background(), refundID, paymentID); err != nil {
			t.Fatal(err)
		}
	}
	// An arriving payment callback after the refund must not queue access.
	if err := s.payments.ReceiveYooKassa(context.Background(), paymentID); err != nil {
		t.Fatal(err)
	}
	manualCounts(t, s, order.OrderId, 1, 0)
	var review bool
	var grants, legacy int
	if err := e.Pool.QueryRow(context.Background(), `SELECT review_required,(SELECT count(*) FROM access_operations),(SELECT count(*) FROM legacy_payment_receipts) FROM purchase_orders WHERE id=$1`, order.OrderId).Scan(&review, &grants, &legacy); err != nil || !review || grants != 0 || legacy != 0 {
		t.Fatal("native refund caused funding or legacy journal mutation", err)
	}
}
