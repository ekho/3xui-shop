package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func manualFixture(t *testing.T) (*regressionFixture, *testkit.Env, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	s, e, account, plan := purchaseFixture(t)
	s.cfg.Payments.ManualEnabled = true
	s.cfg.Payments.ManualCardDetails = "<b>Local test recipient</b>\nReference: your order ID"
	actor := verified(t, s, e, "manual-operator@example.test")
	if err := s.changeOperatorRole(context.Background(), actor, true); err != nil {
		t.Fatal(err)
	}
	return s, e, account, plan, actor
}

func manualOrder(t *testing.T, s *regressionFixture, account, plan uuid.UUID) payments.PurchaseOrder {
	t.Helper()
	out, err := s.payments.CreatePurchaseOrder(context.Background(), account, uuid.New(), payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "manual", PaymentType: "MANUAL", PlanId: plan, Revision: 1, PeriodDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func manualCode(err error, code string) bool { return catalogueCode(paymentError(err), code) }

func manualCounts(t *testing.T, s *regressionFixture, id uuid.UUID, receipts, jobs int) {
	t.Helper()
	var r, j int
	err := s.pool.QueryRow(context.Background(), `SELECT
	 (SELECT count(*) FROM purchase_receipts WHERE order_id=$1),
	 (SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=$1::text)`, id).Scan(&r, &j)
	if err != nil || r != receipts || j != jobs {
		t.Fatalf("receipt/job counts %d/%d, want %d/%d: %v", r, j, receipts, jobs, err)
	}
}

func TestManualPaymentReportDecisionAndSnapshot(t *testing.T) {
	s, _, account, plan, actor := manualFixture(t)
	ctx := context.Background()
	order := manualOrder(t, s, account, plan)
	if order.Checkout != nil || order.PaymentMethod != "manual" || order.ManualPayment == nil || !order.ManualPayment.CanReport || order.ManualPayment.State != "not_reported" || order.Quote.AmountMinor != "9007199254740993" {
		t.Fatalf("manual quote: %+v", order)
	}
	original := order.ManualPayment.Instructions
	s.cfg.Payments.ManualCardDetails = "Changed test recipient"
	current, err := s.payments.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || current.ManualPayment.Instructions != original {
		t.Fatal("instruction snapshot changed", err)
	}
	if _, err = s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, uuid.New(), payments.ManualPaymentDecisionInput{Decision: "reject", Reason: "not reported"}); !manualCode(err, "PURCHASE_ORDER_CONFLICT") {
		t.Fatal("unreported decision", err)
	}
	key := uuid.New()
	s.cfg.Payments.ManualEnabled = false
	methods, err := s.payments.PaymentMethods(ctx, account)
	if err != nil || len(methods.Methods) != 1 || methods.Methods[0].Id != "yoomoney" {
		t.Fatal("disabled manual method remains available", err)
	}
	if _, err = s.payments.CreatePurchaseOrder(ctx, account, uuid.New(), payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "manual", PaymentType: "MANUAL", PlanId: plan, Revision: 1, PeriodDays: 30}); !manualCode(err, "PAYMENT_METHOD_UNAVAILABLE") {
		t.Fatal("disabled method created a new order", err)
	}
	reported, err := s.payments.ReportManualPayment(ctx, account, order.OrderId, key)
	if err != nil || reported.PaymentStatus != "pending" || reported.CanCancel || reported.CanPay || reported.ManualPayment.State != "pending" || reported.ManualPayment.CanReport {
		t.Fatalf("report changed money or cancel state: %+v %v", reported, err)
	}
	for _, k := range []uuid.UUID{key, uuid.New()} {
		if _, err = s.payments.ReportManualPayment(ctx, account, order.OrderId, k); err != nil {
			t.Fatal("report replay", err)
		}
	}
	manualCounts(t, s, order.OrderId, 0, 0)
	var audits int
	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE account_id=$1 AND action='manual_payment_reported'", account).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("duplicate report audit", audits, err)
	}
	if _, err = s.payments.CancelPurchaseOrder(ctx, account, order.OrderId, uuid.New()); !manualCode(err, "PURCHASE_ORDER_CONFLICT") {
		t.Fatal("reported order canceled", err)
	}
	s.cfg.Payments.ManualEnabled = false
	s.now = func() time.Time { return time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC) }
	current, err = s.payments.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || current.Expired || current.ManualPayment.State != "pending" || current.ManualPayment.Instructions != original {
		t.Fatal("waiting report expired/lost", err)
	}
	if _, err = s.payments.CreatePurchaseOrder(ctx, account, uuid.New(), payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "yoomoney", PaymentType: "PC", PlanId: plan, Revision: 1, PeriodDays: 30}); !manualCode(err, "PURCHASE_ORDER_CONFLICT") {
		t.Fatal("reported order released after deadline", err)
	}
	wrong := "1"
	input := payments.ManualPaymentDecisionInput{Decision: "approve", Reason: "Checked bank receipt", ConfirmedAmountMinor: &wrong}
	if _, err = s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, uuid.New(), input); !manualCode(err, "INVALID_INPUT") {
		t.Fatal("wrong amount accepted", err)
	}
	input.ConfirmedAmountMinor = &order.Quote.AmountMinor
	decisionKey := uuid.New()
	paid, err := s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, decisionKey, input)
	if err != nil || paid.PaymentStatus != "paid" || paid.FulfillmentStatus != "queued" || paid.ManualPayment.State != "approved" {
		t.Fatalf("late/disabled approve: %+v %v", paid, err)
	}
	if _, err = s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, decisionKey, input); err != nil {
		t.Fatal("decision replay", err)
	}
	manualCounts(t, s, order.OrderId, 1, 1)
	var source, amount string
	var when time.Time
	if err = s.pool.QueryRow(ctx, "SELECT notification_type,gross_minor::text,occurred_at FROM purchase_receipts WHERE order_id=$1", order.OrderId).Scan(&source, &amount, &when); err != nil || source != "manual_confirmation" || amount != order.Quote.AmountMinor || !when.Equal(s.now()) {
		t.Fatal("manual provenance", source, amount, err)
	}
	if err = s.payments.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal("shared fulfillment", err)
	}
	if err = s.payments.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal("shared fulfillment replay", err)
	}
	current, err = s.payments.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || current.AccessOperationId == nil {
		t.Fatal("shared access target missing", err)
	}
	if err = s.applyAccess(ctx, *current.AccessOperationId); err != nil {
		t.Fatal("shared access worker", err)
	}
	if err = s.applyAccess(ctx, *current.AccessOperationId); err != nil {
		t.Fatal("shared access replay", err)
	}
	current, err = s.payments.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || current.FulfillmentStatus != "applied" || current.AccessOperationId == nil {
		t.Fatalf("manual not applied: %+v %v", current, err)
	}
	if err = s.changeOperatorRole(ctx, actor, false); err != nil {
		t.Fatal(err)
	}
	_, err = s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, decisionKey, input)
	if denial, ok := paymentError(err).(*apiError); !ok || denial.Status != 403 {
		t.Fatal("revoked replay", err)
	}
}

func TestManualPaymentTerminalRacesAndRollback(t *testing.T) {
	t.Run("unreported-expiry", func(t *testing.T) {
		s, _, account, plan, _ := manualFixture(t)
		ctx := context.Background()
		order := manualOrder(t, s, account, plan)
		s.now = func() time.Time { return order.ExpiresAt }
		if _, err := s.payments.ReportManualPayment(ctx, account, order.OrderId, uuid.New()); !manualCode(err, "PURCHASE_ORDER_CONFLICT") {
			t.Fatal("late report accepted", err)
		}
		fresh := manualOrder(t, s, account, plan)
		if fresh.OrderId == order.OrderId {
			t.Fatal("unreported expired order blocks a new purchase")
		}
		manualCounts(t, s, order.OrderId, 0, 0)
	})
	for _, second := range []string{"approve", "reject"} {
		t.Run("race-"+second, func(t *testing.T) {
			s, _, account, plan, actor := manualFixture(t)
			ctx := context.Background()
			order := manualOrder(t, s, account, plan)
			if _, err := s.payments.ReportManualPayment(ctx, account, order.OrderId, uuid.New()); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			out := make(chan error, 2)
			for _, decision := range []string{"approve", second} {
				wg.Add(1)
				go func(d string) {
					defer wg.Done()
					in := payments.ManualPaymentDecisionInput{Decision: d, Reason: "Bank checked"}
					if d == "approve" {
						in.ConfirmedAmountMinor = &order.Quote.AmountMinor
					}
					_, err := s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, uuid.New(), in)
					out <- err
				}(decision)
			}
			wg.Wait()
			close(out)
			success := 0
			for err := range out {
				if err == nil {
					success++
				} else if !manualCode(err, "PURCHASE_ORDER_CONFLICT") {
					t.Fatal(err)
				}
			}
			if success != 1 {
				t.Fatal("terminal race successes", success)
			}
			got, err := s.payments.PurchaseOrder(ctx, account, order.OrderId)
			if err != nil {
				t.Fatal(err)
			}
			expected := 0
			if got.ManualPayment.State == "approved" {
				expected = 1
			} else if got.ManualPayment.State != "rejected" {
				t.Fatal("nonterminal decision")
			}
			manualCounts(t, s, order.OrderId, expected, expected)
			var n int
			if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE account_id=$1 AND action IN ('manual_payment_approved','manual_payment_rejected')", account).Scan(&n); err != nil || n != 1 {
				t.Fatal("decision audit race", n, err)
			}
		})
	}
	for _, kind := range []string{"queue", "audit"} {
		t.Run("rollback-"+kind, func(t *testing.T) {
			s, _, account, plan, actor := manualFixture(t)
			ctx := context.Background()
			order := manualOrder(t, s, account, plan)
			if _, err := s.payments.ReportManualPayment(ctx, account, order.OrderId, uuid.New()); err != nil {
				t.Fatal(err)
			}
			working := s
			if kind == "queue" {
				working = newRegressionFixture(s.pool, s.limiter, nil, *s.cfg)
				working.now = s.now
			} else if _, err := s.pool.Exec(ctx, "DROP TABLE audit_events"); err != nil {
				t.Fatal(err)
			}
			if _, err := working.payments.DecideManualPayment(ctx, actor, account, order.OrderId, uuid.New(), payments.ManualPaymentDecisionInput{Decision: "approve", Reason: "Bank checked", ConfirmedAmountMinor: &order.Quote.AmountMinor}); !manualCode(err, "SERVICE_UNAVAILABLE") {
				t.Fatal("rollback error", err)
			}
			got, err := s.payments.PurchaseOrder(ctx, account, order.OrderId)
			if err != nil || got.PaymentStatus != "pending" || got.ManualPayment.State != "pending" {
				t.Fatal("partial approval", err)
			}
			manualCounts(t, s, order.OrderId, 0, 0)
		})
	}
	t.Run("reject-terminal-and-new-order", func(t *testing.T) {
		s, _, account, plan, actor := manualFixture(t)
		ctx := context.Background()
		order := manualOrder(t, s, account, plan)
		if _, err := s.payments.ReportManualPayment(ctx, account, order.OrderId, uuid.New()); err != nil {
			t.Fatal(err)
		}
		key := uuid.New()
		in := payments.ManualPaymentDecisionInput{Decision: "reject", Reason: "Transfer not found"}
		got, err := s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, key, in)
		if err != nil || got.PaymentStatus != "canceled" || got.ManualPayment.State != "rejected" || got.ManualPayment.Reason == nil || *got.ManualPayment.Reason != in.Reason {
			t.Fatalf("reject: %+v %v", got, err)
		}
		if _, err = s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, key, in); err != nil {
			t.Fatal("reject replay", err)
		}
		in.Decision = "approve"
		in.ConfirmedAmountMinor = &order.Quote.AmountMinor
		if _, err = s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, uuid.New(), in); !manualCode(err, "PURCHASE_ORDER_CONFLICT") {
			t.Fatal("reject then approve", err)
		}
		manualCounts(t, s, order.OrderId, 0, 0)
		manualOrder(t, s, account, plan)
	})
}

func manualLogin(t *testing.T, h http.Handler, cfg app.Config, email string) supportSession {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": "my long safe password ✨"})
	r := request(h, "POST", "/api/v1/auth/login", string(body), cfg.HTTP.CabinetOrigin)
	var out wire.LoginResult
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || len(r.Result().Cookies()) != 1 {
		t.Fatal("manual fixture login", r.Code)
	}
	return supportSession{out.Account.AccountId, r.Result().Cookies()[0], out.CsrfToken}
}

func TestManualPaymentBoundariesAndInbox(t *testing.T) {
	s, e, account, plan, actor := manualFixture(t)
	ctx := context.Background()
	order := manualOrder(t, s, account, plan)
	h := New(app.NewModules(s.pool, s.limiter, s.queue, s.cfg), s.pool, s.cfg.HTTP)
	customer := manualLogin(t, h, *s.cfg, "purchase@example.test")
	operator := manualLogin(t, h, *s.cfg, "manual-operator@example.test")
	path := "/api/v1/orders/" + order.OrderId.String() + "/manual-report"
	for _, tc := range []struct {
		body, origin string
		key          uuid.UUID
		status       int
	}{{`{}`, "https://attacker.example.test", uuid.New(), 403}, {`{}`, s.cfg.HTTP.CabinetOrigin, uuid.Nil, 400}, {`{"amount_minor":"1"}`, s.cfg.HTTP.CabinetOrigin, uuid.New(), 400}, {strings.Repeat("a", 16385), s.cfg.HTTP.CabinetOrigin, uuid.New(), 400}} {
		if r := supportRequest(h, &customer, "POST", path, "application/json", []byte(tc.body), tc.origin, tc.key); r.Code != tc.status {
			t.Fatal("report boundary", r.Code, tc.status)
		}
	}
	noCSRF := customer
	noCSRF.csrf = ""
	if r := supportRequest(h, &noCSRF, "POST", path, "application/json", []byte(`{}`), s.cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 403 {
		t.Fatal("report CSRF", r.Code)
	}
	if r := supportRequest(h, &operator, "POST", path, "application/json", []byte(`{}`), s.cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 404 {
		t.Fatal("foreign report", r.Code)
	}
	if r := supportRequest(h, &customer, "GET", "/api/v1/operator/manual-payments", "", nil, "", uuid.Nil); r.Code != 403 {
		t.Fatal("client inbox", r.Code)
	}
	if r := supportRequest(h, &customer, "POST", path, "application/json", []byte(`{}`), s.cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 200 {
		t.Fatal("actual report", r.Code)
	}
	decisionPath := "/api/v1/operator/clients/" + account.String() + "/orders/" + order.OrderId.String() + "/manual-decision"
	for _, body := range []string{`{"decision":"approve","reason":"Checked"}`, `{"decision":"approve","reason":"Checked","confirmed_amount_minor":"1"}`, `{"decision":"reject","reason":"bad\u0000reason"}`, `{"decision":"reject","reason":"Checked","operator_account_id":"` + actor.String() + `"}`} {
		if r := supportRequest(h, &operator, "POST", decisionPath, "application/json", []byte(body), s.cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 400 {
			t.Fatal("decision boundary", r.Code)
		}
	}
	page, err := s.payments.ManualPaymentRequests(ctx, actor, nil)
	if err != nil || len(page.Items) != 1 || page.HasMore || page.NextCursor != nil || page.Items[0].AccountId != account {
		t.Fatalf("inbox: %+v %v", page, err)
	}
	quote, _ := json.Marshal(order.Quote)
	for i := 0; i < 51; i++ {
		a, id := uuid.New(), uuid.New()
		if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,kind)
	 SELECT $2,$3,locale,password_hash,verified_at,$4,$5,$6,terms_version,privacy_version,'web' FROM accounts WHERE id=$1`, account, a, fmt.Sprintf("manual-page-%d@example.test", i), uuid.New(), strings.ReplaceAll(uuid.NewString(), "-", "")[:16], uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		// Controlled persisted fixtures exercise the real cursor query without51 password hashes/panel probes.
		when := e.Clock().Add(time.Duration(i+1) * time.Second)
		if _, err = e.Pool.Exec(ctx, `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_method,payment_type,manual_details,manual_reported_at,created_at,expires_at)
	 VALUES($1,$2,$3,$4,$5,9007199254740993,'manual','MANUAL','Local page fixture',$6,$7,$8)`, id, a, uuid.New(), make([]byte, 32), quote, when, e.Clock(), e.Clock().Add(30*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	page, err = s.payments.ManualPaymentRequests(ctx, actor, nil)
	if err != nil || len(page.Items) != 50 || !page.HasMore || page.NextCursor == nil {
		t.Fatal("first cursor page", len(page.Items), err)
	}
	next, err := s.payments.ManualPaymentRequests(ctx, actor, page.NextCursor)
	if err != nil || len(next.Items) != 2 || next.HasMore || next.NextCursor != nil {
		t.Fatal("second cursor page", len(next.Items), err)
	}
	if _, err = s.payments.ManualPaymentRequests(ctx, actor, &plan); !manualCode(err, "INVALID_INPUT") {
		t.Fatal("foreign cursor", err)
	}
	if r := supportRequest(h, &operator, "GET", "/api/v1/operator/manual-payments?after=bad", "", nil, "", uuid.Nil); r.Code != 400 {
		t.Fatal("cursor boundary", r.Code)
	}
	if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", account); err != nil {
		t.Fatal(err)
	}
	if _, err = s.payments.ReportManualPayment(ctx, account, order.OrderId, uuid.New()); !manualCode(err, "ACCOUNT_RESTRICTED") {
		t.Fatal("restricted report", err)
	}
}

func TestManualPaymentCrossMethodFunding(t *testing.T) {
	s, _, account, plan, actor := manualFixture(t)
	ctx := context.Background()
	order := manualOrder(t, s, account, plan)
	if _, err := s.payments.ReportManualPayment(ctx, account, order.OrderId, uuid.New()); err != nil {
		t.Fatal(err)
	}
	fields := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {"wrong-method-receipt"}, "amount": {"90071992547409.00"}, "withdraw_amount": {"90071992547409.93"}, "currency": {"643"}, "datetime": {"2026-10-01T00:01:00Z"}, "label": {order.OrderId.String()}, "codepro": {"false"}, "unaccepted": {"false"}}
	fields.Set("sign", yooMoneySignature(fields, s.cfg.Payments.YooMoneyNotificationSecret))
	if err := s.payments.ReceiveYooMoney(ctx, fields); err != nil {
		t.Fatal("authenticated cross-method fact lost", err)
	}
	got, err := s.payments.PurchaseOrder(ctx, account, order.OrderId)
	if err != nil || got.PaymentStatus != "pending" || !got.ReviewRequired || got.FulfillmentStatus != "needs_review" {
		t.Fatalf("provider bypassed manual authority: %+v %v", got, err)
	}
	manualCounts(t, s, order.OrderId, 1, 0)
	if _, err = s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, uuid.New(), payments.ManualPaymentDecisionInput{Decision: "approve", Reason: "Checked", ConfirmedAmountMinor: &order.Quote.AmountMinor}); !manualCode(err, "PURCHASE_ORDER_CONFLICT") {
		t.Fatal("disputed method auto-approved", err)
	}
}

func TestManualPaymentConfig(t *testing.T) {
	for _, tc := range []struct{ name, value, path, inline string }{{"invalid-flag", "bad", "", ""}, {"missing-file", "true", "", ""}, {"unreadable", "true", "missing", ""}, {"conflict", "true", "valid", "inline"}, {"empty", "true", "empty", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHOP_PAYMENT_MANUAL_ENABLED", tc.value)
			t.Setenv("SHOP_PAYMENT_YOOMONEY_ENABLED", "")
			t.Setenv("YOOMONEY_NOTIFICATION_SECRET_FILE", "")
			t.Setenv("YOOMONEY_NOTIFICATION_SECRET", "")
			t.Setenv("MANUAL_CARD_DETAILS", tc.inline)
			file := ""
			if tc.path != "" {
				file = filepath.Join(t.TempDir(), "manual-details")
				if tc.path != "missing" {
					value := "Synthetic test recipient"
					if tc.path == "empty" {
						value = " \n "
					}
					if err := os.WriteFile(file, []byte(value), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			t.Setenv("MANUAL_CARD_DETAILS_FILE", file)
			if _, err := app.LoadConfig(); err == nil || (!strings.Contains(err.Error(), "MANUAL_CARD_DETAILS") && !strings.Contains(err.Error(), "SHOP_PAYMENT_MANUAL_ENABLED")) {
				t.Fatal("manual configuration accepted or unsafe error", err)
			}
		})
	}
	for _, details := range []string{"", " \n ", "bad\x00details", string([]byte{0xff}), strings.Repeat("я", 2001)} {
		cfg := app.Config{}
		cfg.Payments.ManualEnabled = true
		cfg.Payments.ManualCardDetails = details
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "MANUAL_CARD_DETAILS") {
			t.Fatal("invalid details accepted", err)
		}
	}
}

func TestManualPaymentImmutabilityAndDowngrade(t *testing.T) {
	s, _, account, plan, actor := manualFixture(t)
	ctx := context.Background()
	order := manualOrder(t, s, account, plan)
	for _, sql := range []string{"UPDATE purchase_orders SET manual_details='changed' WHERE id=$1", "UPDATE purchase_orders SET payment_status='paid',paid_at=created_at WHERE id=$1", "UPDATE purchase_orders SET manual_actor_id=account_id,manual_decided_at=created_at,manual_reason='forged' WHERE id=$1"} {
		if _, err := s.pool.Exec(ctx, sql, order.OrderId); err == nil {
			t.Fatal("manual proof constraint bypassed")
		}
	}
	if _, err := s.payments.ReportManualPayment(ctx, account, order.OrderId, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE purchase_orders SET manual_reported_at=manual_reported_at+interval '1 second' WHERE id=$1", order.OrderId); err == nil {
		t.Fatal("claim timestamp mutable")
	}
	if _, err := s.payments.DecideManualPayment(ctx, actor, account, order.OrderId, uuid.New(), payments.ManualPaymentDecisionInput{Decision: "reject", Reason: "Not found"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE purchase_orders SET manual_reason='rewrite decision' WHERE id=$1", order.OrderId); err == nil {
		t.Fatal("terminal decision mutable")
	}
	source, err := os.ReadFile("../../db/migrations/00016_manual_payment.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(source), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("migration Down missing")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, parts[1]); err == nil || !strings.Contains(err.Error(), "manual payment downgrade blocked") {
		t.Fatal("manual history downgrade", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	manualCounts(t, s, order.OrderId, 0, 0)
	if _, err = s.payments.PurchaseOrder(ctx, account, order.OrderId); err != nil {
		t.Fatal("downgrade removed order", err)
	}
	t.Run("existing-yoomoney-down-up-preserved", func(t *testing.T) {
		other, _, customer, offer := purchaseFixture(t)
		before, err := other.payments.CreatePurchaseOrder(ctx, customer, uuid.New(), payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "yoomoney", PaymentType: "PC", PlanId: offer, Revision: 1, PeriodDays: 30})
		if err != nil {
			t.Fatal(err)
		}
		tx, err := other.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, parts[1]); err != nil {
			t.Fatal("compatible downgrade", err)
		}
		if _, err = tx.Exec(ctx, parts[0]); err != nil {
			t.Fatal("compatible upgrade", err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		after, err := other.payments.PurchaseOrder(ctx, customer, before.OrderId)
		if err != nil || after.PaymentMethod != "yoomoney" || after.Quote.AmountMinor != before.Quote.AmountMinor || after.ManualPayment != nil {
			t.Fatal("YooMoney data changed", err)
		}
	})
}
