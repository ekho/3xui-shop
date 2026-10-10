package payments

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func TestLegacyLateYooMoneyAndStarsStayInReview(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	account := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id,legacy_user_id)
	 VALUES($1,$2,'en','fixture',now(),$3,$4,$5,'1','1',701,5)`, account, account.String()+"@example.test", uuid.New(), uuid.NewString(), "acct_"+account.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO legacy_stars_imports(source_legacy_user_id,account_id,source_tg_id,source_snapshot) VALUES(5,$1,701,'{}')`, account); err != nil {
		t.Fatal(err)
	}
	label := uuid.NewString()
	p := LegacyPaymentPackage{Version: 1, Users: []LegacyPaymentUser{{SourceLegacyUserID: 5, SourceTgID: 701}}, Transactions: []LegacyPaymentTransaction{{SourceID: 9, SourceTgID: 701, PaymentID: label, Subscription: "subscription:pay_yoomoney:0:0:701:2:30:0:10.0", Status: "pending", CreatedAt: e.Clock(), UpdatedAt: e.Clock()}}}
	s := New(e.Pool, accounts.New(e.Pool, e.Redis, nil, accounts.Config{Now: e.Clock}), nil, nil, nil, nil, func() Config { return Config{YooMoneyNotificationSecret: []byte("test-secret")} }, e.Clock, nil)
	s.ConfigureStars(StarsGateway{BotID: 123, Invoice: func(context.Context, StarsInvoice) (string, error) { return "", nil }, Refund: func(context.Context, int64, string) error { return nil }})
	if _, err := s.ImportLegacyPayments(ctx, p, false); err != nil {
		t.Fatal(err)
	}
	fields := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {"old-operation"}, "amount": {"9.90"}, "currency": {"643"}, "datetime": {e.Clock().Format(time.RFC3339)}, "sender": {"old-sender"}, "codepro": {"false"}, "label": {label}}
	signed := []string{fields.Get("notification_type"), fields.Get("operation_id"), fields.Get("amount"), fields.Get("currency"), fields.Get("datetime"), fields.Get("sender"), fields.Get("codepro"), "test-secret", fields.Get("label")}
	hash := sha1.Sum([]byte(strings.Join(signed, "&")))
	fields.Set("sha1_hash", hex.EncodeToString(hash[:]))
	withBadHMAC := url.Values{}
	for key, values := range fields {
		withBadHMAC[key] = append([]string{}, values...)
	}
	withBadHMAC.Set("sign", strings.Repeat("0", 64))
	if err := s.ReceiveYooMoney(ctx, withBadHMAC); err == nil {
		t.Fatal("invalid HMAC fell back to old SHA-1")
	}
	for i := 0; i < 2; i++ {
		if err := s.ReceiveYooMoney(ctx, fields); err != nil {
			t.Fatal("signed late YooMoney", err)
		}
	}
	var linked int64
	var savedAmount int64
	if err := e.Pool.QueryRow(ctx, `SELECT source_transaction_id,amount_minor FROM legacy_payment_receipts WHERE provider='yoomoney'`).Scan(&linked, &savedAmount); err != nil || linked != 9 || savedAmount != 990 {
		t.Fatal("legacy source or signed amount lost", err)
	}
	conflict := url.Values{}
	for key, values := range fields {
		conflict[key] = append([]string{}, values...)
	}
	conflict.Set("amount", "8.90")
	signed[2] = "8.90"
	hash = sha1.Sum([]byte(strings.Join(signed, "&")))
	conflict.Set("sha1_hash", hex.EncodeToString(hash[:]))
	if err := s.ReceiveYooMoney(ctx, conflict); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := e.Pool.QueryRow(ctx, `SELECT state,amount_minor FROM legacy_payment_receipts WHERE provider='yoomoney'`).Scan(&state, &savedAmount); err != nil || state != "conflict" || savedAmount != 990 {
		t.Fatal("conflict overwrote first proof", err)
	}
	stars := StarsPaymentInput{StarsPreCheckoutInput: StarsPreCheckoutInput{BotID: 123, PayerID: 701, Amount: 4, Currency: "XTR", Payload: "subscription:pay_telegram_stars:0:0:701:2:30:0:4.0"}, ChargeID: "late-charge", At: e.Clock(), Recurring: true}
	if err := s.RecordStarsPayment(ctx, stars); err != nil {
		t.Fatal("recurring Stars", err)
	}
	if err := s.RecordStarsPayment(ctx, stars); err != nil {
		t.Fatal("recurring replay", err)
	}
	if err := s.RecordStarsRefund(ctx, stars); err != nil {
		t.Fatal("Stars refund", err)
	}
	var receipts int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM legacy_payment_receipts`).Scan(&receipts); err != nil || receipts != 3 {
		t.Fatal("late events missing", err)
	}
	for _, table := range []string{"purchase_orders", "purchase_receipts", "river_job", "access_operations", "purchase_refunds"} {
		var count int
		if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s changed: %d %v", table, count, err)
		}
	}
	if _, err := s.ListLegacyReceipts(ctx, account); err == nil {
		t.Fatal("nonoperator read review journal")
	}
	operator := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
	 VALUES($1,'web',$2,'en','fixture',now(),$3,$4,$5,'1','1')`, operator, operator.String()+"@example.test", uuid.New(), strings.ReplaceAll(uuid.NewString(), "-", "")[:16], "acct_"+operator.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, operator, e.Clock()); err != nil {
		t.Fatal(err)
	}
	report, err := s.ListLegacyReceipts(ctx, operator)
	if err != nil || len(report) != 3 || report[0].ID == uuid.Nil {
		t.Fatal("operator review port unavailable", err)
	}
}
