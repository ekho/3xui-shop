package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

// Synthetic saved rows test aggregate consumers; Stars children use the real
// owner with the existing owned provider/panel fixture. No real money is sent.
func TestCampaignStatisticsProofs(t *testing.T) {
	h, s, e, stars, m, root, _ := starsCycleFixture(t)
	ctx := context.Background()
	for range 2 {
		e.Advance(30 * 24 * time.Hour)
		in := starsCyclePayment(root.OrderId, e.Clock())
		in.ChargeID = uuid.NewString()
		if err := m.Payments.RecordStarsPayment(ctx, in); err != nil {
			t.Fatal(err)
		}
		child := starsCycleOrder(t, s, stars.Account.AccountId, root.OrderId, in.ChargeID)
		if err := m.Payments.FulfillPurchase(ctx, child.OrderId); err != nil {
			t.Fatal(err)
		}
		child, err := m.Payments.PurchaseOrder(ctx, stars.Account.AccountId, child.OrderId)
		if err != nil || child.AccessOperationId == nil {
			t.Fatal("owned child funding", err)
		}
		if err = m.VPN.ApplyAccess(ctx, *child.AccessOperationId); err != nil {
			t.Fatal(err)
		}
	}
	actor := campaignOperator(t, h, e, *s.cfg)
	c := campaignCreate(t, h, actor, *s.cfg, "Proofs")
	rub := supportLogin(t, h, e, *s.cfg, "stats-rub@example.test")
	usd := supportLogin(t, h, e, *s.cfg, "stats-usd@example.test")
	legacy := supportLogin(t, h, e, *s.cfg, "stats-legacy@example.test")
	for _, u := range []struct {
		id      uuid.UUID
		channel string
	}{{stars.Account.AccountId, "telegram"}, {rub.id, "web"}, {usd.id, "web"}} {
		if _, err := e.Pool.Exec(ctx, `INSERT INTO campaign_acquisitions(account_id,campaign_id,channel,created_at,source_code) VALUES($1,$2,$3,$4,$5)`, u.id, c.ID, u.channel, e.Clock(), *c.Code); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO campaign_acquisitions(account_id,campaign_id,channel,legacy_source,legacy_trial_used,legacy_payload) VALUES($1,$2,'legacy_name','owned-copy',true,'{}')`, legacy.id, c.ID); err != nil {
		t.Fatal(err)
	}
	var usdOrder uuid.UUID
	for i, u := range []uuid.UUID{rub.id, rub.id, usd.id} {
		id := uuid.New()
		op := uuid.NewString()
		currency, method, paymentType, gross := "RUB", "yoomoney", "AC", int64(9223372036854775807)
		receiptCurrency, notification := "643", "p2p-incoming"
		net := ptr(gross)
		var proof []byte
		if i == 2 {
			usdOrder = id
			currency, method, paymentType, gross = "USD", "cryptomus", "CRYPTOMUS", 12345
			receiptCurrency, notification, net = "USD", "cryptomus.paid", nil
			proof = mustJSON(t, map[string]any{"provider": method, "invoice_id": uuid.NewString(), "merchant_id": "owned-merchant", "order_id": id.String(), "status": "paid", "payment_status": "paid", "is_final": true, "amount_minor": "12345", "currency": "USD", "payment_amount": "2.000000000000000000001", "payer_amount": "2", "merchant_amount": "1.9", "payer_currency": "BTC", "created_at": e.Clock().Format(time.RFC3339)})
		}
		quote := fmt.Sprintf(`{"amount_minor":"%d","currency":"%s"}`, gross, currency)
		if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_method,payment_type,active,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,false,$9,$10)`, id, u, uuid.New(), []byte{1}, quote, gross, method, paymentType, e.Clock(), e.Clock().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,created_at,provider_data) VALUES($1,$2,$3,$4,$5,$6,$7,false,false,$3,$8)`, op, id, e.Clock(), gross, net, receiptCurrency, notification, proof); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Pool.Exec(ctx, `UPDATE purchase_orders SET payment_status='paid',paid_at=$2,funding_operation_id=$3 WHERE id=$1`, id, e.Clock(), op); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			// Same order's unmatched/extra receipt must not add money or purchases.
			if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,review_reason,created_at) VALUES($1,$2,$3,999,999,'643','p2p-incoming',false,false,'extra',$3)`, uuid.NewString(), id, e.Clock()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_refunds(id,order_id,receipt_operation_id,payment_method,reference,returned_amount,returned_currency,reason,operator_account_id,created_at) SELECT $1,id,funding_operation_id,'cryptomus','owned-return','0.000000000000000000000001','USDT','owned return',$3,$4 FROM purchase_orders WHERE id=$2`, uuid.New(), usdOrder, actor.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	// Approved reservations alone are not delivered trials.
	for i, u := range []uuid.UUID{rub.id, usd.id} {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		request, operation := uuid.New(), uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO trial_requests(id,account_id,status,comment,created_at,decided_at,operator_tg_id,operation_id) VALUES($1,$2,'approved','',$3,$3,101,$4)`, request, u, e.Clock(), operation); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO trial_operations(id,account_id,request_id,status,trial_enabled,period_days,traffic_gb,devices,panel_id,created_at) VALUES($1,$2,$3,'pending',true,3,15,1,'owned',$4)`, operation, u, request, e.Clock()); err != nil {
			t.Fatal(err)
		}
		grant := "reserved"
		var at *time.Time
		if i == 0 {
			grant = "granted"
			at = ptr(e.Clock())
		}
		if _, err = tx.Exec(ctx, `INSERT INTO trial_grants(account_id,request_id,operation_id,status,created_at,granted_at) VALUES($1,$2,$3,$4,$5,$6)`, u, request, operation, grant, e.Clock(), at); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=702,legacy_user_id=8 WHERE id=$1`, legacy.id); err != nil {
		t.Fatal(err)
	}
	p := payments.LegacyPaymentPackage{Version: 1, Users: []payments.LegacyPaymentUser{{SourceLegacyUserID: 8, SourceTgID: 702}}, Transactions: []payments.LegacyPaymentTransaction{}}
	for i, packed := range []string{"subscription:pay_yoomoney:0:0:702:2:30:15:10.25", "unknown:preserve", "subscription:subscription:0:0:702:2:30:15:500.0"} {
		p.Transactions = append(p.Transactions, payments.LegacyPaymentTransaction{SourceID: int64(i + 1), SourceTgID: 702, PaymentID: fmt.Sprintf("owned-legacy-%d", i), Subscription: packed, Status: "completed", CreatedAt: e.Clock(), UpdatedAt: e.Clock()})
	}
	if _, err := m.Payments.ImportLegacyPayments(ctx, p, false); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var raw string
		if err := e.Pool.QueryRow(ctx, `SELECT md5(jsonb_build_array((SELECT jsonb_agg(x ORDER BY id) FROM purchase_orders x),(SELECT jsonb_agg(x ORDER BY operation_id) FROM purchase_receipts x),(SELECT jsonb_agg(x ORDER BY id) FROM purchase_refunds x),(SELECT jsonb_agg(x ORDER BY account_id) FROM trial_grants x),(SELECT jsonb_agg(x ORDER BY id) FROM river_job x))::text)`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot()
	r := supportRequest(h, &actor, "GET", "/api/v1/operator/campaigns/"+c.ID.String(), "", nil, s.cfg.HTTP.CabinetOrigin, uuid.Nil)
	var out wire.CampaignDetail
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil {
		t.Fatalf("statistics card want200 got%d", r.Code)
	}
	var value any
	if err := json.Unmarshal(r.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	contract, err := wire.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	if err = contract.Components.Schemas["CampaignDetail"].Value.VisitJSON(value); err != nil {
		t.Fatal("public DTO schema", err)
	}
	stat := out.Statistics
	if stat.Users != 4 || stat.WebRegistrations != 2 || stat.TelegramRegistrations != 1 || stat.LegacyNameUsers != 1 || stat.LegacyTrialUsed != 1 || stat.Trials.TrialUsers != 1 || stat.Payments.PaidOrders != 6 || stat.Payments.PaidUsers != 3 || stat.Payments.RepeatUsers != 2 {
		t.Fatal("proof counts", stat)
	}
	money := map[string]wire.StatisticsMoney{}
	for _, v := range stat.Payments.Money {
		money[string(v.Currency)] = v
	}
	if len(money) != 3 || money["RUB"].GrossMinor != "18446744073709551614" || money["RUB"].KnownNetMinor != "18446744073709551614" || money["USD"].GrossMinor != "12345" || money["USD"].KnownNetMinor != "0" || money["USD"].UnknownNetReceipts != 1 || money["XTR"].GrossMinor != "300" || money["XTR"].UnknownNetReceipts != 3 {
		t.Fatal("currency/precision/net", money)
	}
	if len(stat.Payments.Refunds) != 1 || stat.Payments.Refunds[0].Currency != "USDT" || stat.Payments.Refunds[0].ReturnedAmount != "0.000000000000000000000001" {
		t.Fatal("actual return currency/precision", stat.Payments.Refunds)
	}
	old := stat.Payments.Legacy
	if old.CompletedTransactions != 3 || old.PaidUsers != 1 || old.RepeatUsers != 1 || old.UnknownQuoteCount != 2 || len(old.Money) != 1 || old.Money[0].QuotedMinor != "1025" || snapshot() != before {
		t.Fatal("archive truth or reader wrote", old)
	}
	for _, tc := range []struct {
		actor *supportSession
		path  string
		want  int
	}{{nil, "/api/v1/operator/campaigns/" + c.ID.String(), 401}, {&rub, "/api/v1/operator/campaigns/" + c.ID.String(), 403}, {&actor, "/api/v1/operator/campaigns/" + uuid.NewString(), 404}, {&actor, "/api/v1/operator/campaigns/" + c.ID.String() + "?unexpected=1", 400}} {
		if got := supportRequest(h, tc.actor, "GET", tc.path, "", nil, s.cfg.HTTP.CabinetOrigin, uuid.Nil); got.Code != tc.want {
			t.Fatal("card access/strict query", got.Code, tc.want)
		}
	}
}

func TestCampaignStatisticsEmptyAndEvents(t *testing.T) {
	h, e, cfg, _ := miniAppHTTPFixture(t)
	actor := campaignOperator(t, h, e, cfg)
	c := campaignCreate(t, h, actor, cfg, "Empty")
	for i := range 21 {
		state := "paused"
		if i%2 == 1 {
			state = "active"
		}
		r := supportRequest(h, &actor, "POST", "/api/v1/operator/campaigns/"+c.ID.String()+"/state", "application/json", mustJSON(t, map[string]any{"state": state, "expected_revision": i + 1, "reason": "owned state"}), cfg.HTTP.CabinetOrigin, uuid.New())
		if r.Code != 200 {
			t.Fatal(r.Code)
		}
	}
	r := supportRequest(h, &actor, "GET", "/api/v1/operator/campaigns/"+c.ID.String(), "", nil, cfg.HTTP.CabinetOrigin, uuid.Nil)
	var out wire.CampaignDetail
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.Statistics.Users != 0 || out.Statistics.Payments.PaidOrders != 0 || out.Statistics.Payments.Money == nil || out.Statistics.Payments.Refunds == nil || out.Statistics.Payments.Legacy.Money == nil || len(out.Events) != 20 || !out.EventsHasMore {
		t.Fatal("empty arrays/events bound", r.Code)
	}
}
