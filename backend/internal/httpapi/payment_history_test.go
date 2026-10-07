package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/wire"

	"github.com/google/uuid"
)

// Losing the reader, its ownership check or an empty typed array breaks this result.
func TestPaymentHistoryHTTP(t *testing.T) {
	h, e, cfg := httpFixture(t)
	client := supportLogin(t, h, e, cfg, "history@example.test")
	for _, kind := range []string{"orders", "receipts", "legacy"} {
		r := supportRequest(h, &client, "POST", "/api/v1/payment-history", "application/json", []byte(`{"kind":"`+kind+`"}`), cfg.HTTP.CabinetOrigin, uuid.Nil)
		if r.Code != 200 {
			t.Fatalf("own %s history: got %d, want 200", kind, r.Code)
		}
		for _, field := range []string{`"orders":[]`, `"receipts":[]`, `"legacy_transactions":[]`, `"has_more":false`} {
			if !strings.Contains(r.Body.String(), field) {
				t.Fatalf("empty typed page missing %s", field)
			}
		}
	}
	ctx := context.Background()
	other := supportLogin(t, h, e, cfg, "history-operator@example.test")
	read := func(session *supportSession, path string, in any, status int) wire.PaymentHistoryPage {
		t.Helper()
		r := supportRequest(h, session, "POST", path, "application/json", mustJSON(t, in), cfg.HTTP.CabinetOrigin, uuid.Nil)
		if r.Code != status || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("history boundary: got %d, want %d", r.Code, status)
		}
		var out wire.PaymentHistoryPage
		if status == 200 && json.Unmarshal(r.Body.Bytes(), &out) != nil {
			t.Fatal("invalid history response")
		}
		for _, private := range []string{"checkout", "private-transfer-details", "never-export-provider", "source_tg_id", "subscription:"} {
			if strings.Contains(r.Body.String(), private) {
				t.Fatal("private source data leaked")
			}
		}
		return out
	}
	path := "/api/v1/payment-history"
	input := map[string]any{"kind": "orders"}
	read(nil, path, input, 401)
	read(&other, "/api/v1/operator/clients/"+client.id.String()+"/payment-history", input, 403)
	for _, bad := range []any{
		map[string]any{"kind": "orders", "account_id": other.id.String()},
		map[string]any{"kind": "orders", "before_id": uuid.NewString()},
		map[string]any{"kind": "orders", "before_created_at": "2026-10-01T01:02:03Z"},
		map[string]any{"kind": "orders", "before_created_at": "2026-10-01T01:02:03.1234567Z", "before_id": uuid.NewString()},
		map[string]any{"kind": "orders", "before_created_at": "2026-10-01T01:02:03Z", "before_id": uuid.Nil.String()},
		map[string]any{"kind": "legacy", "before_created_at": "2026-10-01T01:02:03Z", "before_id": "9223372036854775808"},
		map[string]any{"kind": "receipts", "before_created_at": "2026-10-01T01:02:03Z", "before_id": "\x00"},
		map[string]any{"kind": "unknown"},
	} {
		read(&client, path, bad, 400)
	}
	read(&client, path+"?account_id="+other.id.String(), input, 400)
	noCSRF := client
	noCSRF.csrf = ""
	read(&noCSRF, path, input, 403)
	if r := supportRequest(h, &client, "POST", path, "application/json", mustJSON(t, input), "https://attacker.example", uuid.Nil); r.Code != 403 {
		t.Fatal("foreign Origin accepted")
	}
	created := time.Date(2026, 10, 1, 1, 2, 3, 123456000, time.UTC)
	quote := `{"amount_minor":"9007199254740993","currency":"RUB","devices":2,"period_days":30,"plan_id":"00000000-0000-4000-8000-000000000123","profile":"regular","revision":1,"traffic_gb":15}`
	ids := make([]uuid.UUID, 51)
	for i := range ids {
		ids[i] = uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1))
		if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,payment_status,fulfillment_status,active,created_at,expires_at,paid_at,action) VALUES($1,$2,$3,$4,$5,9007199254740993,'AC','paid','needs_review',false,$6,$7,$6,'renew')`, ids[i], client.id, uuid.New(), []byte{1}, quote, created, created.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	first := read(&client, path, input, 200)
	if len(first.Orders) != 50 || !first.HasMore || first.Orders[0].OrderId != ids[50] || first.Orders[49].OrderId != ids[1] || first.Orders[0].Quote.AmountMinor != "9007199254740993" || first.Orders[0].Action != "renew" || first.Orders[0].PaymentStatus != "paid" || first.Orders[0].FulfillmentStatus != "needs_review" {
		t.Fatal("order quote/status or tied-date first page changed")
	}
	if page := read(&other, path, input, 200); len(page.Orders) != 0 {
		t.Fatal("self reader exposed another account")
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,active,created_at,expires_at) VALUES($1,$2,$3,$4,$5,9007199254740993,'AC',false,$6,$7)`, uuid.New(), client.id, uuid.New(), []byte{2}, quote, created.Add(time.Minute), created.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	last := first.Orders[49]
	older := map[string]any{"kind": "orders", "before_created_at": last.CreatedAt, "before_id": last.OrderId.String()}
	if page := read(&client, path, older, 200); page.HasMore || len(page.Orders) != 1 || page.Orders[0].OrderId != ids[0] {
		t.Fatal("new insertion duplicated/skipped older tied-date rows")
	}
	for _, receipt := range []struct {
		id, currency, kind  string
		net                 *int64
		codepro, unaccepted bool
		reason              *string
		proof               any
	}{
		{"ym-funded", "643", "p2p-incoming", ptr(int64(9007199254740900)), false, false, nil, nil},
		{"ym-review", "840", "p2p-incoming", ptr(int64(0)), true, true, ptr("PAYMENT_REVIEW_REQUIRED"), nil},
		{"yk-unknown-net", "RUB", "yookassa.succeeded", nil, false, false, nil, map[string]any{"provider": "yookassa", "merchant": "never-export-provider"}},
	} {
		var proof []byte
		if receipt.proof != nil {
			proof = mustJSON(t, receipt.proof)
		}
		if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,review_reason,created_at,provider_data) VALUES($1,$2,$3,9007199254740993,$4,$5,$6,$7,$8,$9,$3,$10)`, receipt.id, ids[0], created, receipt.net, receipt.currency, receipt.kind, receipt.codepro, receipt.unaccepted, receipt.reason, proof); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE purchase_orders SET funding_operation_id='ym-funded' WHERE id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	receipts := read(&client, path, map[string]any{"kind": "receipts"}, 200).Receipts
	if len(receipts) != 3 || receipts[0].OperationId != "ym-review" || receipts[0].Currency != nil || receipts[0].RawCurrency != "840" || !receipts[0].ReviewRequired || !receipts[0].Codepro || !receipts[0].Unaccepted || receipts[1].NetMinor == nil || *receipts[1].NetMinor != "9007199254740900" || !receipts[1].FundsOrder || receipts[1].Currency == nil || *receipts[1].Currency != "RUB" || receipts[2].NetMinor != nil {
		t.Fatal("receipt facts were inferred, lost or conflated with issuance")
	}
	operatorPath := "/api/v1/operator/clients/" + client.id.String() + "/payment-history"
	if _, err := e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, other.id, created); err != nil {
		t.Fatal(err)
	}
	if page := read(&other, operatorPath, input, 200); len(page.Orders) != 50 {
		t.Fatal("operator did not read selected target")
	}
	read(&other, "/api/v1/operator/clients/"+uuid.NewString()+"/payment-history", input, 404)
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, client.id); err != nil {
		t.Fatal(err)
	}
	read(&client, path, input, 403)
	read(&other, operatorPath, input, 200)
	if _, err := e.Pool.Exec(ctx, `DELETE FROM operator_accounts WHERE account_id=$1`, other.id); err != nil {
		t.Fatal(err)
	}
	read(&other, operatorPath, input, 403)
	if r := supportRequest(h, &other, "POST", "/api/v1/auth/logout", "", nil, cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 204 {
		t.Fatal("logout")
	}
	read(&other, path, input, 401)
}

// Disabling all checkout configuration must not erase saved provider facts.
func TestPaymentHistoryStoredMethods(t *testing.T) {
	h, e, cfg := httpFixture(t)
	client := supportLogin(t, h, e, cfg, "stored-methods@example.test")
	ctx := context.Background()
	created := time.Date(2026, 10, 1, 1, 2, 3, 0, time.UTC)
	for _, method := range []string{"yoomoney", "manual", "yookassa", "cryptomus", "heleket"} {
		id := uuid.New()
		currency, paymentType := "RUB", "AC"
		var details *string
		switch method {
		case "manual":
			paymentType = "MANUAL"
			details = ptr("private-transfer-details")
		case "yookassa":
			paymentType = "YOOKASSA"
		case "cryptomus":
			paymentType = "CRYPTOMUS"
			currency = "USD"
		case "heleket":
			paymentType = "HELEKET"
			currency = "USD"
		}
		quote := fmt.Sprintf(`{"amount_minor":"19900","currency":"%s","devices":2,"period_days":30,"plan_id":"00000000-0000-4000-8000-000000000123","profile":"regular","revision":1,"traffic_gb":15}`, currency)
		if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_method,payment_type,manual_details,active,created_at,expires_at) VALUES($1,$2,$3,$4,$5,19900,$6,$7,$8,false,$9,$10)`, id, client.id, uuid.New(), []byte{1}, quote, method, paymentType, details, created, created.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		net := ptr(int64(19900))
		raw, notification := "643", "p2p-incoming"
		var proof []byte
		switch method {
		case "manual":
			notification = "manual_confirmation"
		case "yookassa":
			raw, notification = "RUB", "yookassa.succeeded"
			net = nil
			proof = mustJSON(t, map[string]any{"provider": "yookassa", "private": "never-export-provider"})
		case "cryptomus", "heleket":
			raw, notification = "USD", method+".paid"
			net = nil
			proof = mustJSON(t, map[string]any{"provider": method, "invoice_id": uuid.NewString(), "merchant_id": "never-export-provider", "order_id": id.String(), "status": "paid", "payment_status": "paid", "is_final": true, "amount_minor": "19900", "currency": "USD", "payment_amount": "1.0000000000000000000000001", "payer_amount": "0.9999999999999999999999999", "merchant_amount": "0.9000000000000000000000000", "payer_currency": "BTC", "created_at": created.Format(time.RFC3339)})
		}
		if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,created_at,provider_data) VALUES($1,$2,$3,19900,$4,$5,$6,false,false,$3,$7)`, "stored-"+method, id, created, net, raw, notification, proof); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() string {
		t.Helper()
		var value string
		if err := e.Pool.QueryRow(ctx, `SELECT md5(jsonb_build_array((SELECT jsonb_agg(p ORDER BY id) FROM purchase_orders p),(SELECT jsonb_agg(r ORDER BY operation_id) FROM purchase_receipts r),(SELECT jsonb_agg(a ORDER BY id) FROM access_operations a),(SELECT jsonb_agg(j ORDER BY id) FROM river_job j),(SELECT jsonb_agg(e ORDER BY id) FROM audit_events e))::text)`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	read := func(in any) wire.PaymentHistoryPage {
		t.Helper()
		r := supportRequest(h, &client, "POST", "/api/v1/payment-history", "application/json", mustJSON(t, in), cfg.HTTP.CabinetOrigin, uuid.Nil)
		var page wire.PaymentHistoryPage
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || strings.Contains(r.Body.String(), "never-export-provider") || strings.Contains(r.Body.String(), "private-transfer-details") {
			t.Fatal("saved history unavailable or private provider data exposed", r.Code)
		}
		return page
	}
	before := snapshot()
	page := read(map[string]any{"kind": "receipts"})
	if len(page.Receipts) != 5 {
		t.Fatal("disabled methods disappeared")
	}
	for _, r := range page.Receipts {
		if r.GrossMinor != "19900" || r.FundsOrder || r.Currency == nil {
			t.Fatal("receipt principal/link changed")
		}
		switch r.PaymentMethod {
		case "manual":
			if r.Source != "operator" || r.NetMinor == nil || r.RawCurrency != "643" || *r.Currency != "RUB" {
				t.Fatal("manual proof became provider proof")
			}
		case "yoomoney":
			if r.Source != "provider" || r.NetMinor == nil || *r.Currency != "RUB" {
				t.Fatal("YooMoney money facts changed")
			}
		case "yookassa":
			if r.NetMinor != nil || r.CryptoAmounts != nil {
				t.Fatal("unknown YooKassa net was invented")
			}
		case "cryptomus", "heleket":
			if r.NetMinor != nil || *r.Currency != "USD" || r.CryptoAmounts == nil || r.CryptoAmounts.PaymentAmount != "1.0000000000000000000000001" || r.CryptoAmounts.PayerAmount != "0.9999999999999999999999999" || r.CryptoAmounts.MerchantAmount != "0.9000000000000000000000000" || r.CryptoAmounts.PayerCurrency != "BTC" {
				t.Fatal("crypto amounts were rounded/converted or net invented")
			}
		}
	}
	if orders := read(map[string]any{"kind": "orders"}); len(orders.Orders) != 5 {
		t.Fatal("old quotes disappeared")
	}
	if snapshot() != before {
		t.Fatal("history changed money/native/jobs/audit")
	}
	var order uuid.UUID
	if err := e.Pool.QueryRow(ctx, `SELECT id FROM purchase_orders WHERE payment_method='yoomoney'`).Scan(&order); err != nil {
		t.Fatal(err)
	}
	for i := range 51 {
		if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,created_at) VALUES($1,$2,$3,1,1,'643','p2p-incoming',false,false,$3)`, fmt.Sprintf("page-%04d", i), order, created.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	page = read(map[string]any{"kind": "receipts"})
	if !page.HasMore || len(page.Receipts) != 50 || page.Receipts[0].OperationId != "page-0050" || page.Receipts[49].OperationId != "page-0001" {
		t.Fatal("tied receipt page ordering lost")
	}
	last := page.Receipts[49]
	before = snapshot()
	page = read(map[string]any{"kind": "receipts", "before_created_at": last.CreatedAt, "before_id": last.OperationId})
	if page.HasMore || len(page.Receipts) != 6 || page.Receipts[0].OperationId != "page-0000" || snapshot() != before {
		t.Fatal("receipt cursor skipped/duplicated rows or wrote data")
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, client.id, created); err != nil {
		t.Fatal(err)
	}
	m := app.NewModules(e.Pool, e.Redis, nil, &cfg)
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	telegram, err := m.Accounts.CreateTelegram(ctx, tx, accounts.TelegramInput{TelegramID: 701, DisplayName: "Local legacy target", Locale: "ru"})
	if err != nil || tx.Commit(ctx) != nil {
		t.Fatal("Telegram target fixture", err)
	}
	r := supportRequest(h, &client, "POST", "/api/v1/operator/clients/"+telegram.ID.String()+"/payment-history", "application/json", []byte(`{"kind":"orders"}`), cfg.HTTP.CabinetOrigin, uuid.Nil)
	if r.Code != 200 {
		t.Fatal("operator cannot read Telegram-only target", r.Code)
	}
}
