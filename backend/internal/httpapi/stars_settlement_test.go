package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func starsPayment(order uuid.UUID, at time.Time) payments.StarsPaymentInput {
	return payments.StarsPaymentInput{StarsPreCheckoutInput: payments.StarsPreCheckoutInput{BotID: 123, PayerID: 701, Amount: 100, Currency: "XTR", Payload: "stars:v1:" + order.String()}, ChargeID: "owned-stars-charge", At: at.Add(time.Second)}
}

// The native endpoint cannot replace current operator/target authority with a
// saved request or accept client-provided payout coordinates.
func TestStarsRefundHTTP(t *testing.T) {
	h, s, e, auth, plan := starsHTTPFixture(t)
	ctx := context.Background()
	order, err := s.payments.CreatePurchaseOrder(ctx, auth.Account.AccountId, uuid.New(), payments.PurchaseOrderInput{Action: "purchase", PaymentMethod: "telegram_stars", PaymentType: "STARS", PlanId: plan, Revision: 1, PeriodDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.payments.RecordStarsPayment(ctx, starsPayment(order.OrderId, e.Clock())); err != nil {
		t.Fatal(err)
	}
	key := starsReceipt(t, s, order.OrderId)
	operator := supportLogin(t, h, e, *s.cfg, "stars-http-operator@example.test")
	client := supportLogin(t, h, e, *s.cfg, "stars-http-client@example.test")
	if err = s.changeOperatorRole(ctx, operator.id, true); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) { return "", nil }, Refund: func(context.Context, int64, string) error { calls++; return nil }})
	base := "/api/v1/operator/clients/" + auth.Account.AccountId.String() + "/orders/" + order.OrderId.String()
	input := map[string]any{"receipt_operation_id": key, "reason": "Owned native HTTP refund", "confirm_full": true, "keep_access": true}
	write := func(session *supportSession, body any, k uuid.UUID, origin string, want int) wire.StarsRefund {
		t.Helper()
		r := supportRequest(h, session, "POST", base+"/stars-refund", "application/json", mustJSON(t, body), origin, k)
		if r.Code != want {
			t.Fatalf("native refund status %d, want %d", r.Code, want)
		}
		var out wire.StarsRefund
		if want == 200 && json.Unmarshal(r.Body.Bytes(), &out) != nil {
			t.Fatal("invalid native refund response")
		}
		return out
	}
	write(nil, input, uuid.New(), s.cfg.HTTP.CabinetOrigin, 401)
	write(&client, input, uuid.New(), s.cfg.HTTP.CabinetOrigin, 403)
	noCSRF := operator
	noCSRF.csrf = ""
	write(&noCSRF, input, uuid.New(), s.cfg.HTTP.CabinetOrigin, 403)
	write(&operator, input, uuid.New(), "https://attacker.example", 403)
	write(&operator, input, uuid.Nil, s.cfg.HTTP.CabinetOrigin, 400)
	bad := map[string]any{}
	for k, v := range input {
		bad[k] = v
	}
	bad["payer_id"] = 702
	write(&operator, bad, uuid.New(), s.cfg.HTTP.CabinetOrigin, 400)
	if calls != 0 {
		t.Fatal("unsafe HTTP reached payout")
	}
	if _, err = e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, auth.Account.AccountId, e.Clock()); err != nil {
		t.Fatal(err)
	}
	caseData, err := s.payments.OperatorPaymentCase(ctx, operator.id, auth.Account.AccountId, order.OrderId, &key)
	if err != nil || caseData.CanRefundStars {
		t.Fatal("protected target advertised native payout", err)
	}
	write(&operator, input, uuid.New(), s.cfg.HTTP.CabinetOrigin, 403)
	if calls != 0 {
		t.Fatal("protected target reached payout")
	}
	if _, err = e.Pool.Exec(ctx, `DELETE FROM operator_accounts WHERE account_id=$1`, auth.Account.AccountId); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	out := write(&operator, input, id, s.cfg.HTTP.CabinetOrigin, 200)
	if out.State != "confirmed" || out.Refund == nil || out.Refund.Source != "telegram" || out.Refund.OperatorAccountId == nil || *out.Refund.OperatorAccountId != operator.id || calls != 1 {
		t.Fatal("native HTTP forged source/actor or lost proof")
	}
	write(&operator, input, id, s.cfg.HTTP.CabinetOrigin, 200)
	if calls != 1 {
		t.Fatal("HTTP replay paid twice")
	}
	if err = s.changeOperatorRole(ctx, operator.id, false); err != nil {
		t.Fatal(err)
	}
	write(&operator, input, id, s.cfg.HTTP.CabinetOrigin, 403)
}

func starsReceipt(t *testing.T, s *regressionFixture, order uuid.UUID) string {
	t.Helper()
	var key string
	if err := s.pool.QueryRow(context.Background(), `SELECT operation_id FROM purchase_receipts WHERE order_id=$1 ORDER BY created_at,operation_id LIMIT 1`, order).Scan(&key); err != nil {
		t.Fatal(err)
	}
	return key
}

// The actual shared funding guard must accept a genuine charge and preserve one
// access identity after replay, not merely acknowledge its Telegram update.
func TestStarsSettlementAndFunding(t *testing.T) {
	s, e, auth, order := starsTestOrder(t)
	p := panelFixture(t, s)
	ctx := context.Background()
	in := starsPayment(order.OrderId, e.Clock())
	for range 2 {
		if err := s.payments.RecordStarsPayment(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.payments.PaymentHistory(ctx, auth.Account.AccountId, payments.PaymentHistoryInput{Kind: "receipts"})
	if err != nil || len(page.Receipts) != 1 {
		t.Fatal("receipt replay/history", err)
	}
	r := page.Receipts[0]
	if r.GrossMinor != "100" || r.NetMinor != nil || r.Currency == nil || *r.Currency != "XTR" || r.Source != "provider" || !r.FundsOrder || !strings.HasPrefix(r.OperationId, "stars:") {
		t.Fatal("integer Stars proof/history lost")
	}
	for range 2 {
		if err := s.payments.FulfillPurchase(ctx, order.OrderId); err != nil {
			t.Fatal("Stars funding guard rejected saved proof", err)
		}
	}
	got, err := s.payments.PurchaseOrder(ctx, auth.Account.AccountId, order.OrderId)
	if err != nil || got.AccessOperationId == nil {
		t.Fatal("paid charge did not prepare access", err)
	}
	for range 2 {
		if err := s.vpn.ApplyAccess(ctx, *got.AccessOperationId); err != nil {
			t.Fatal(err)
		}
	}
	got, err = s.payments.PurchaseOrder(ctx, auth.Account.AccountId, order.OrderId)
	if err != nil || got.FulfillmentStatus != "applied" || p.adds != 1 {
		t.Fatal("charge replay duplicated or lost access", err)
	}
	var receipts, jobs, accesses, audits int
	err = e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM purchase_receipts WHERE order_id=$1),(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=$1::uuid::text),(SELECT count(*) FROM access_operations WHERE purchase_order_id=$1),(SELECT count(*) FROM audit_events WHERE account_id=$2 AND action='stars_payment_received')`, order.OrderId, auth.Account.AccountId).Scan(&receipts, &jobs, &accesses, &audits)
	if err != nil || receipts != 1 || jobs != 1 || accesses != 1 || audits != 1 {
		t.Fatal("receipt/job/access/audit duplicated", err)
	}
}

// Received money survives present-day sales/identity rejection, without funding
// an order. A duplicate cannot promote a rejected proof to an eligible one.
func TestStarsSettlementRetainsRejectedCharges(t *testing.T) {
	for _, kind := range []string{"amount", "currency", "payer", "bot", "late", "before-order", "canceled", "disabled", "restricted", "binding"} {
		t.Run(kind, func(t *testing.T) {
			s, e, auth, order := starsTestOrder(t)
			ctx := context.Background()
			in := starsPayment(order.OrderId, e.Clock())
			switch kind {
			case "amount":
				in.Amount = 101
			case "currency":
				in.Currency = "RUB"
			case "payer":
				in.PayerID = 702
			case "bot":
				in.BotID = 124
			case "late":
				in.At = e.Clock().Add(31 * time.Minute)
			case "before-order":
				in.At = e.Clock().Add(-6 * time.Minute)
			case "disabled":
				s.cfg.Payments.StarsEnabled = false
			case "canceled":
				if _, err := s.payments.CancelPurchaseOrder(ctx, auth.Account.AccountId, order.OrderId, uuid.New()); err != nil {
					t.Fatal(err)
				}
			case "restricted":
				if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, auth.Account.AccountId); err != nil {
					t.Fatal(err)
				}
			case "binding":
				if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=702 WHERE id=$1`, auth.Account.AccountId); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := s.payments.RecordStarsPayment(ctx, in); err != nil {
					t.Fatal("received money discarded", err)
				}
			}
			var receipts, jobs int
			var safe bool
			err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM purchase_receipts WHERE order_id=$1),(SELECT count(*) FROM river_job WHERE kind='purchase_fulfillment' AND args->>'order_id'=$1::uuid::text),EXISTS(SELECT 1 FROM purchase_orders WHERE id=$1 AND payment_status='paid' AND review_required AND fulfillment_status='needs_review' AND funding_operation_id IS NULL AND access_operation_id IS NULL)`, order.OrderId).Scan(&receipts, &jobs, &safe)
			if err != nil || receipts != 1 || jobs != 0 || !safe {
				t.Fatal("rejected charge lost or issued access", err)
			}
		})
	}
}

func TestStarsSettlementConflictAndExtraCharge(t *testing.T) {
	for _, kind := range []string{"conflict", "extra"} {
		t.Run(kind, func(t *testing.T) {
			s, e, auth, order := starsTestOrder(t)
			ctx := context.Background()
			in := starsPayment(order.OrderId, e.Clock())
			if err := s.payments.RecordStarsPayment(ctx, in); err != nil {
				t.Fatal(err)
			}
			if kind == "conflict" {
				in.Amount = 200
			} else {
				in.ChargeID = "owned-second-charge"
			}
			for range 2 {
				if err := s.payments.RecordStarsPayment(ctx, in); err != nil {
					t.Fatal(err)
				}
			}
			var receipts int
			var original int64
			if err := e.Pool.QueryRow(ctx, `SELECT count(*),min(gross_minor) FROM purchase_receipts WHERE order_id=$1`, order.OrderId).Scan(&receipts, &original); err != nil || original != 100 || (kind == "conflict" && receipts != 1) || (kind == "extra" && receipts != 2) {
				t.Fatal("original or extra money rewritten", err)
			}
			if err := s.payments.FulfillPurchase(ctx, order.OrderId); err != nil {
				t.Fatal(err)
			}
			got, err := s.payments.PurchaseOrder(ctx, auth.Account.AccountId, order.OrderId)
			if err != nil || !got.ReviewRequired || got.AccessOperationId != nil {
				t.Fatal("conflicting/extra charge issued access", err)
			}
		})
	}
}

func TestStarsSettlementRollbackAndUnsupported(t *testing.T) {
	s, e, _, order := starsTestOrder(t)
	ctx := context.Background()
	in := starsPayment(order.OrderId, e.Clock())
	noQueue := app.NewModules(e.Pool, e.Redis, nil, s.cfg)
	if err := noQueue.Payments.RecordStarsPayment(ctx, in); err == nil {
		t.Fatal("receipt acknowledged without queue")
	}
	var receipts, audits int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM purchase_receipts WHERE order_id=$1),(SELECT count(*) FROM audit_events WHERE action='stars_payment_received')`, order.OrderId).Scan(&receipts, &audits); err != nil || receipts != 0 || audits != 0 {
		t.Fatal("failed enqueue partially committed proof", err)
	}
	for _, change := range []func(*payments.StarsPaymentInput){func(p *payments.StarsPaymentInput) { p.Payload = "legacy:701:100" }, func(p *payments.StarsPaymentInput) { p.Recurring = true }, func(p *payments.StarsPaymentInput) { p.FirstRecurring = true }, func(p *payments.StarsPaymentInput) { p.SubscriptionExpiresAt = 1 }, func(p *payments.StarsPaymentInput) { p.ChargeID = strings.Repeat("x", 4097) }, func(p *payments.StarsPaymentInput) { p.ChargeID = "x\x00" }} {
		bad := in
		change(&bad)
		var domain *payments.Error
		if err := s.payments.RecordStarsPayment(ctx, bad); !errors.As(err, &domain) || domain.Code != "STARS_UNSUPPORTED_PAYMENT" {
			t.Fatal("unknown money update acknowledged")
		}
	}
}

// Provider refunds can arrive first. The negative proof/audit must be durable
// before acknowledgement even though the common ledger awaits a paid receipt.
func TestStarsEarlyRefundAudit(t *testing.T) {
	s, e, auth, order := starsTestOrder(t)
	ctx := context.Background()
	in := starsPayment(order.OrderId, e.Clock())
	for range 2 {
		if err := s.payments.RecordStarsRefund(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	var negative, audits int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM stars_refunds WHERE order_id=$1),(SELECT count(*) FROM audit_events WHERE account_id=$2 AND action='stars_refund_observed')`, order.OrderId, auth.Account.AccountId).Scan(&negative, &audits); err != nil || negative != 1 || audits != 1 {
		t.Fatal("early negative proof/audit not atomic", err)
	}
}

func TestStarsNativeRefund(t *testing.T) {
	for _, kind := range []string{"confirmed", "uncertain", "before-write", "applied", "binding"} {
		t.Run(kind, func(t *testing.T) {
			s, e, auth, order := starsTestOrder(t)
			p := panelFixture(t, s)
			ctx := context.Background()
			in := starsPayment(order.OrderId, e.Clock())
			if err := s.payments.RecordStarsPayment(ctx, in); err != nil {
				t.Fatal(err)
			}
			key := starsReceipt(t, s, order.OrderId)
			actor := verified(t, s, e, "stars-refund-operator@example.test")
			if err := s.changeOperatorRole(ctx, actor, true); err != nil {
				t.Fatal(err)
			}
			if kind == "before-write" || kind == "applied" {
				if err := s.payments.FulfillPurchase(ctx, order.OrderId); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.payments.PurchaseOrder(ctx, auth.Account.AccountId, order.OrderId)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "applied" {
				if err := s.vpn.ApplyAccess(ctx, *got.AccessOperationId); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "binding" {
				if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=702 WHERE id=$1`, auth.Account.AccountId); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) { return "", nil }, Refund: func(_ context.Context, payer int64, charge string) error {
				calls++
				if payer != 701 || charge != in.ChargeID {
					t.Fatal("refund redirected from original payer")
				}
				if kind == "uncertain" {
					return errors.New("owned response loss")
				}
				return nil
			}})
			input := payments.StarsRefundInput{ReceiptOperationId: key, Reason: "Owned full Stars refund", ConfirmFull: true, KeepAccess: true}
			id := uuid.New()
			refund, err := s.payments.RefundStarsPurchase(ctx, actor, auth.Account.AccountId, order.OrderId, id, input)
			if err != nil || calls != 1 {
				t.Fatal("native refund failed", err)
			}
			if kind == "uncertain" {
				if refund.State != "uncertain" || refund.Refund != nil {
					t.Fatal("lost provider response forged refund")
				}
			} else if refund.State != "confirmed" || refund.Refund == nil || refund.Refund.Source != "telegram" || refund.Refund.OperatorAccountId == nil || *refund.Refund.OperatorAccountId != actor || refund.Refund.ReturnedAmount != "100" || refund.Refund.ReturnedCurrency != "XTR" {
				t.Fatal("native proof/attribution lost")
			}
			for _, replayKey := range []uuid.UUID{id, uuid.New()} {
				again, err := s.payments.RefundStarsPurchase(ctx, actor, auth.Account.AccountId, order.OrderId, replayKey, input)
				if err != nil || again.State != refund.State || calls != 1 {
					t.Fatal("uncertain/replayed payout repeated", err)
				}
			}
			if kind == "uncertain" {
				restarted := app.NewModules(e.Pool, e.Redis, s.queue, s.cfg)
				restarted.Payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) { return "", nil }, Refund: func(context.Context, int64, string) error { calls++; return nil }})
				out, err := restarted.Payments.RefundStarsPurchase(ctx, actor, auth.Account.AccountId, order.OrderId, uuid.New(), input)
				if err != nil || out.State != "uncertain" || out.Refund != nil || calls != 1 {
					t.Fatal("restart forgot ambiguous payout", err)
				}
				if err := s.payments.RecordStarsRefund(ctx, in); err != nil {
					t.Fatal(err)
				}
				c, err := s.payments.OperatorPaymentCase(ctx, actor, auth.Account.AccountId, order.OrderId, &key)
				if err != nil || c.Refund == nil || c.StarsRefundState == nil || *c.StarsRefundState != "confirmed" || c.CanRefundStars {
					t.Fatal("provider completion lost", err)
				}
			}
			if kind == "before-write" {
				if err := s.vpn.ApplyAccess(ctx, *got.AccessOperationId); err != nil {
					t.Fatal(err)
				}
				if p.adds != 0 {
					t.Fatal("refunded funding wrote panel")
				}
			}
			if kind == "applied" {
				after, err := s.payments.PurchaseOrder(ctx, auth.Account.AccountId, order.OrderId)
				if err != nil || after.FulfillmentStatus != "applied" || after.AccessOperationId == nil || *after.AccessOperationId != *got.AccessOperationId || p.adds != 1 || p.disables != 0 {
					t.Fatal("refund blindly revoked applied access", err)
				}
			}
			if err := s.changeOperatorRole(ctx, actor, false); err != nil {
				t.Fatal(err)
			}
			if _, err := s.payments.RefundStarsPurchase(ctx, actor, auth.Account.AccountId, order.OrderId, id, input); err == nil || calls != 1 {
				t.Fatal("revoked operator replay accepted")
			}
		})
	}
}

func TestStarsPendingRefundBlocksFunding(t *testing.T) {
	s, e, auth, order := starsTestOrder(t)
	ctx := context.Background()
	if err := s.payments.RecordStarsPayment(ctx, starsPayment(order.OrderId, e.Clock())); err != nil {
		t.Fatal(err)
	}
	key := starsReceipt(t, s, order.OrderId)
	actor := verified(t, s, e, "stars-pending-operator@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	started, finish := make(chan struct{}), make(chan struct{})
	defer close(finish)
	s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) { return "", nil }, Refund: func(c context.Context, _ int64, _ string) error {
		close(started)
		select {
		case <-finish:
			return nil
		case <-c.Done():
			return c.Err()
		}
	}})
	input := payments.StarsRefundInput{ReceiptOperationId: key, Reason: "Owned pending payout", ConfirmFull: true, KeepAccess: true}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.payments.RefundStarsPurchase(requestCtx, actor, auth.Account.AccountId, order.OrderId, uuid.New(), input)
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatal("refund did not reach provider", err)
	case <-requestCtx.Done():
		t.Fatal("refund startup timed out")
	}
	var state string
	if err := e.Pool.QueryRow(ctx, `SELECT state FROM stars_refunds WHERE receipt_operation_id=$1`, key).Scan(&state); err != nil || state != "pending" {
		t.Fatal("payout preceded durable intent", err)
	}
	if err := s.payments.FulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	got, err := s.payments.PurchaseOrder(ctx, auth.Account.AccountId, order.OrderId)
	if err != nil || got.AccessOperationId != nil || got.FulfillmentStatus != "needs_review" {
		t.Fatal("pending payout allowed funding", err)
	}
	if _, err := s.payments.RefundStarsPurchase(ctx, actor, auth.Account.AccountId, order.OrderId, uuid.New(), input); err == nil {
		t.Fatal("concurrent payout bypassed account owner")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal("canceled payout did not retain uncertainty", err)
	}
}

// Matching proof after a restart closes pending/partial access, without losing
// its frozen target/steps or paying a second refund after an uncertain result.
func TestStarsProviderRefundRetiresAccess(t *testing.T) {
	for _, kind := range []string{"pending", "partial", "uncertain"} {
		t.Run(kind, func(t *testing.T) {
			s, e, auth, order := starsTestOrder(t)
			p := panelFixture(t, s)
			ctx := context.Background()
			in := starsPayment(order.OrderId, e.Clock())
			if err := s.payments.RecordStarsPayment(ctx, in); err != nil {
				t.Fatal(err)
			}
			if err := s.payments.FulfillPurchase(ctx, order.OrderId); err != nil {
				t.Fatal(err)
			}
			got, err := s.payments.PurchaseOrder(ctx, auth.Account.AccountId, order.OrderId)
			if err != nil || got.AccessOperationId == nil {
				t.Fatal("access not prepared", err)
			}
			if kind == "partial" {
				if _, err := e.Pool.Exec(ctx, `CREATE FUNCTION fail_stars_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='purchase' AND NEW.status='applied' THEN RAISE EXCEPTION 'owned refund partial write'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_stars_finish BEFORE UPDATE ON access_operations FOR EACH ROW EXECUTE FUNCTION fail_stars_finish()`); err != nil {
					t.Fatal(err)
				}
				if err := s.vpn.ApplyAccess(ctx, *got.AccessOperationId); err != nil || p.adds != 1 {
					t.Fatal("controlled partial write did not reach the panel", err)
				}
			}
			var before, after string
			if err := e.Pool.QueryRow(ctx, `SELECT target::text||':'||completed_steps::text FROM access_operations WHERE id=$1`, *got.AccessOperationId).Scan(&before); err != nil {
				t.Fatal(err)
			}
			actor := verified(t, s, e, "refund-retirement-operator@example.test")
			if err := s.changeOperatorRole(ctx, actor, true); err != nil {
				t.Fatal(err)
			}
			calls := 0
			input := payments.StarsRefundInput{ReceiptOperationId: starsReceipt(t, s, order.OrderId), Reason: "Owned refund completion", ConfirmFull: true, KeepAccess: true}
			if kind == "uncertain" {
				s.payments.ConfigureStars(payments.StarsGateway{BotID: 123, Invoice: func(context.Context, payments.StarsInvoice) (string, error) { return "", nil }, Refund: func(context.Context, int64, string) error { calls++; return errors.New("owned response loss") }})
				out, err := s.payments.RefundStarsPurchase(ctx, actor, auth.Account.AccountId, order.OrderId, uuid.New(), input)
				if err != nil || out.State != "uncertain" || calls != 1 {
					t.Fatal("ambiguous refund setup", err)
				}
			}
			restarted := app.NewModules(e.Pool, e.Redis, s.queue, s.cfg)
			for range 2 {
				if err := restarted.Payments.RecordStarsRefund(ctx, in); err != nil {
					t.Fatal(err)
				}
			}
			var status string
			if err := e.Pool.QueryRow(ctx, `SELECT status,target::text||':'||completed_steps::text FROM access_operations WHERE id=$1`, *got.AccessOperationId).Scan(&status, &after); err != nil || status != "skipped" || before != after {
				t.Fatal("provider refund left access unresolved or lost partial evidence", err, status)
			}
			blocked, err := restarted.VPN.UnresolvedTx(ctx, nil, auth.Account.AccountId)
			if err != nil || blocked {
				t.Fatal("confirmed refund kept an unresolved account blocker", err)
			}
			if err := restarted.VPN.ApplyAccess(ctx, *got.AccessOperationId); err != nil || p.disables != 0 || (kind != "partial" && p.adds != 0) {
				t.Fatal("retired operation made a panel write", err)
			}
			if out, err := restarted.Payments.RefundStarsPurchase(ctx, actor, auth.Account.AccountId, order.OrderId, uuid.New(), input); err != nil || out.State != "confirmed" || out.Refund == nil || calls > 1 {
				t.Fatal("matching proof retried payout or lost confirmed result", err)
			}
		})
	}
}
