package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/payments"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func paidPurchaseHTTPFixture(t *testing.T, s *regressionFixture, after func(context.Context, pgx.Tx) error) (*payments.Service, http.Handler) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), `CREATE TABLE paid_purchase_fixture(order_id uuid PRIMARY KEY,account_id uuid NOT NULL,funding_id text NOT NULL,paid_at timestamptz NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	modules := app.NewModules(s.pool, s.limiter, s.queue, s.cfg)
	modules.Payments.ConfigurePaidPurchase(func(ctx context.Context, tx pgx.Tx, order uuid.UUID) error {
		paid, found, err := modules.Payments.ConfirmedPurchaseTx(ctx, tx, order)
		if err != nil || !found {
			return fmt.Errorf("callback has no confirmed purchase: %w", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO paid_purchase_fixture(order_id,account_id,funding_id,paid_at) VALUES($1,$2,$3,$4)`, paid.OrderID, paid.AccountID, paid.FundingID, paid.PaidAt); err != nil {
			return err
		}
		if after != nil {
			return after(ctx, tx)
		}
		return nil
	})
	return modules.Payments, New(modules, s.pool, s.cfg.HTTP)
}

// Replaying an authenticated callback must not prepare a second reward.
func TestPaidPurchaseHTTPHookStableFunding(t *testing.T) {
	s, _, account, plan := purchaseFixture(t)
	ctx := context.Background()
	order, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
	if err != nil {
		t.Fatal(err)
	}
	owner, h := paidPurchaseHTTPFixture(t, s, nil)
	for _, id := range []uuid.UUID{order.OrderId, uuid.New(), uuid.Nil} {
		if paid, found, err := owner.ConfirmedPurchaseTx(ctx, nil, id); err != nil || found || paid != (payments.PaidPurchase{}) {
			t.Fatal("pending or absent order manufactured purchase proof", err)
		}
	}
	fields := purchaseNotice(s, order.OrderId, "referral-original-payment", "90071992547409.00", "90071992547409.93")
	fields.Set("sign", strings.Repeat("0", 64))
	if r := supportRequest(h, nil, "POST", "/webhooks/yoomoney", "application/x-www-form-urlencoded", []byte(fields.Encode()), "", uuid.Nil); r.Code != 403 {
		t.Fatal("unsigned callback was accepted", r.Code)
	}
	fields.Set("sign", yooMoneySignature(fields, s.cfg.Payments.YooMoneyNotificationSecret))
	results := make(chan int, 3)
	for range 3 {
		go func() {
			results <- supportRequest(h, nil, "POST", "/webhooks/yoomoney", "application/x-www-form-urlencoded", []byte(fields.Encode()), "", uuid.Nil).Code
		}()
	}
	for range 3 {
		if code := <-results; code != 200 {
			t.Fatal("signed concurrent callback/replay rejected", code)
		}
	}
	manualCounts(t, s, order.OrderId, 1, 1)
	paid, found, err := owner.ConfirmedPurchaseTx(ctx, nil, order.OrderId)
	if err != nil || !found || paid.OrderID != order.OrderId || paid.AccountID != account || paid.FundingID != "referral-original-payment" || !paid.PaidAt.Equal(time.Date(2026, 10, 1, 0, 1, 0, 0, time.UTC)) {
		t.Fatal("confirmed purchase lost the original paid order/receipt identity", err)
	}
	fields.Set("operation_id", "referral-extra-payment")
	fields.Set("sign", yooMoneySignature(fields, s.cfg.Payments.YooMoneyNotificationSecret))
	for range 2 {
		if r := supportRequest(h, nil, "POST", "/webhooks/yoomoney", "application/x-www-form-urlencoded", []byte(fields.Encode()), "", uuid.Nil); r.Code != 200 {
			t.Fatal("extra callback/replay rejected", r.Code)
		}
	}
	manualCounts(t, s, order.OrderId, 2, 1)
	var saved payments.PaidPurchase
	if err = s.pool.QueryRow(ctx, `SELECT order_id,account_id,funding_id,paid_at FROM paid_purchase_fixture`).Scan(&saved.OrderID, &saved.AccountID, &saved.FundingID, &saved.PaidAt); err != nil || saved != paid {
		t.Fatal("extra/repeated callbacks changed the single reward preparation", err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM paid_purchase_fixture`).Scan(&count); err != nil || count != 1 {
		t.Fatal("callbacks prepared duplicate rewards", err)
	}
}

// Money retained for review is not verified funding for a reward.
func TestPaidPurchaseHTTPRejectsDisputedFunding(t *testing.T) {
	for _, tc := range []struct{ name, field, value string }{
		{"wrong-currency", "currency", "840"},
		{"wrong-gross", "withdraw_amount", "90071992547409.92"},
		{"protected", "codepro", "true"},
		{"unaccepted", "unaccepted", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, account, plan := purchaseFixture(t)
			ctx := context.Background()
			order, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
			if err != nil {
				t.Fatal(err)
			}
			owner, h := paidPurchaseHTTPFixture(t, s, nil)
			fields := purchaseNotice(s, order.OrderId, "referral-disputed-payment", "90071992547409.00", "90071992547409.93")
			fields.Set(tc.field, tc.value)
			fields.Set("sign", yooMoneySignature(fields, s.cfg.Payments.YooMoneyNotificationSecret))
			for range 2 {
				if r := supportRequest(h, nil, "POST", "/webhooks/yoomoney", "application/x-www-form-urlencoded", []byte(fields.Encode()), "", uuid.Nil); r.Code != 200 {
					t.Fatal("signed disputed callback was not retained", r.Code)
				}
			}
			manualCounts(t, s, order.OrderId, 1, 0)
			if _, found, err := owner.ConfirmedPurchaseTx(ctx, nil, order.OrderId); err != nil || found {
				t.Fatal("disputed receipt manufactured confirmed funding", err)
			}
			var count int
			if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM paid_purchase_fixture`).Scan(&count); err != nil || count != 0 {
				t.Fatal("disputed funding prepared a reward", err)
			}
		})
	}
}

// Reward preparation must share the payment commit, including its rollback.
func TestPaidPurchaseHTTPHookRollbackAndRetry(t *testing.T) {
	s, _, account, plan := purchaseFixture(t)
	ctx := context.Background()
	order, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
	if err != nil {
		t.Fatal(err)
	}
	fail := true
	owner, h := paidPurchaseHTTPFixture(t, s, func(context.Context, pgx.Tx) error {
		if fail {
			return errors.New("synthetic reward preparation failure")
		}
		return nil
	})
	fields := purchaseNotice(s, order.OrderId, "referral-retried-payment", "90071992547409.00", "90071992547409.93")
	if r := supportRequest(h, nil, "POST", "/webhooks/yoomoney", "application/x-www-form-urlencoded", []byte(fields.Encode()), "", uuid.Nil); r.Code != 503 || strings.Contains(r.Body.String(), "synthetic") {
		t.Fatal("hook failure did not return a safe retryable response", r.Code)
	}
	manualCounts(t, s, order.OrderId, 0, 0)
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM paid_purchase_fixture`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed callback committed reward preparation", err)
	}
	if _, found, err := owner.ConfirmedPurchaseTx(ctx, nil, order.OrderId); err != nil || found {
		t.Fatal("failed hook committed funding", err)
	}
	fail = false
	for range 2 {
		if r := supportRequest(h, nil, "POST", "/webhooks/yoomoney", "application/x-www-form-urlencoded", []byte(fields.Encode()), "", uuid.Nil); r.Code != 200 {
			t.Fatal("same callback could not recover after hook failure", r.Code)
		}
	}
	manualCounts(t, s, order.OrderId, 1, 1)
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM paid_purchase_fixture`).Scan(&count); err != nil || count != 1 {
		t.Fatal("recovered callback did not commit a single reward", err)
	}
}

func TestPaidPurchaseHTTPHookBeforeAccessReview(t *testing.T) {
	s, _, account, plan := purchaseFixture(t)
	ctx := context.Background()
	order, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
	if err != nil {
		t.Fatal(err)
	}
	_, h := paidPurchaseHTTPFixture(t, s, nil)
	if _, err = s.pool.Exec(ctx, `UPDATE accounts SET vpn_banned=true WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	fields := purchaseNotice(s, order.OrderId, "referral-access-review", "90071992547409.00", "90071992547409.93")
	if r := supportRequest(h, nil, "POST", "/webhooks/yoomoney", "application/x-www-form-urlencoded", []byte(fields.Encode()), "", uuid.Nil); r.Code != 200 {
		t.Fatal("valid funding lost to access eligibility", r.Code)
	}
	manualCounts(t, s, order.OrderId, 1, 0)
	var prepared int
	var state string
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM paid_purchase_fixture),fulfillment_status FROM purchase_orders WHERE id=$1`, order.OrderId).Scan(&prepared, &state); err != nil || prepared != 1 || state != "needs_review" {
		t.Fatal("access review prevented a confirmed purchase reward", err)
	}
}

func TestConfirmedPurchaseLocksOrderAndVetoesRefund(t *testing.T) {
	s, e, account, order := paidPurchase(t)
	ctx := context.Background()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	paid, found, err := s.payments.ConfirmedPurchaseTx(ctx, tx, order.OrderId)
	if err != nil || !found {
		t.Fatal("real paid order was not confirmed inside a transaction", err)
	}
	probe, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Rollback(ctx)
	_, err = probe.Exec(ctx, `SELECT id FROM purchase_orders WHERE id=$1 FOR UPDATE NOWAIT`, order.OrderId)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "55P03" {
		t.Fatal("purchase proof did not serialize against a concurrent order mutation", err)
	}
	if err = probe.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	actor := verified(t, s, e, "referral-refund-operator@example.test")
	if err = s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.payments.ConfirmPurchaseRefund(ctx, actor, account, order.OrderId, uuid.New(), payments.PurchaseRefundInput{ReceiptOperationId: paid.FundingID, Reference: "referral-funding-return", Reason: "Synthetic confirmed full return", ConfirmFull: true, KeepAccess: true}); err != nil {
		t.Fatal(err)
	}
	if proof, found, err := s.payments.ConfirmedPurchaseTx(ctx, nil, order.OrderId); err != nil || found || proof != (payments.PaidPurchase{}) {
		t.Fatal("refunded receipt remained confirmed reward funding", err)
	}
	if _, found, err := s.payments.ConfirmedPurchaseTx(ctx, tx, order.OrderId); err == nil || found {
		t.Fatal("closed transaction did not fail safely")
	} else {
		var safe *payments.Error
		if !errors.As(err, &safe) || safe.Status != 503 || safe.Code != "SERVICE_UNAVAILABLE" {
			t.Fatal("purchase proof exposed an unsafe database error", err)
		}
	}
}
