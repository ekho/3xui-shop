package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

// A partial import, identity guess or repeated audit corrupts financial history.
func TestLegacyPaymentImport(t *testing.T) {
	h, e, cfg := httpFixture(t)
	client := supportLogin(t, h, e, cfg, "legacy-payment@example.test")
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=701,legacy_user_id=5 WHERE id=$1`, client.id); err != nil {
		t.Fatal(err)
	}
	owner := app.NewModules(e.Pool, e.Redis, nil, &cfg).Payments
	created := time.Date(2026, 10, 1, 1, 2, 3, 123456000, time.FixedZone("source", 3*3600))
	p := payments.LegacyPaymentPackage{Version: 1, Users: []payments.LegacyPaymentUser{{SourceLegacyUserID: 5, SourceTgID: 701}}, Transactions: []payments.LegacyPaymentTransaction{}}
	for i, status := range []string{"pending", "completed", "canceled", "refunded"} {
		p.Transactions = append(p.Transactions, payments.LegacyPaymentTransaction{SourceID: int64(i + 1), SourceTgID: 701, PaymentID: strings.Repeat("long-test-charge-", 512) + status, Subscription: "subscription:pay_telegram_stars:1:0:701:2:30:15:400.0", Status: status, CreatedAt: created, UpdatedAt: created.Add(-time.Second)})
	}
	p.Transactions[3].Subscription = "unknown-v0:preserve-completely"
	counts := func() [6]int {
		t.Helper()
		var value string
		if err := e.Pool.QueryRow(ctx, `SELECT json_build_array((SELECT count(*) FROM legacy_payment_transactions),(SELECT count(*) FROM audit_events WHERE action='legacy_payment_history_imported'),(SELECT count(*) FROM purchase_orders),(SELECT count(*) FROM purchase_receipts),(SELECT count(*) FROM river_job),(SELECT count(*) FROM access_operations))::text`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		var counts [6]int
		if json.Unmarshal([]byte(value), &counts) != nil {
			t.Fatal("invalid count snapshot")
		}
		return counts
	}
	before := counts()
	dry, err := owner.ImportLegacyPayments(ctx, p, true)
	if err != nil || dry.Inserted != 4 || dry.Users != 1 || dry.Transactions != 4 || counts() != before {
		t.Fatal("dry-run failed or wrote data", err)
	}
	result, err := owner.ImportLegacyPayments(ctx, p, false)
	if err != nil || result.Inserted != 4 {
		t.Fatal("archive apply", err)
	}
	after := counts()
	want := before
	want[0] += 4
	want[1]++
	if after != want {
		t.Fatal("archive generated payment/access/jobs or repeated audit", after)
	}
	for _, raw := range p.Transactions {
		var paymentID, packed, status string
		var gotCreated, gotUpdated time.Time
		var account uuid.UUID
		if err = e.Pool.QueryRow(ctx, `SELECT account_id,source_payment_id,subscription,status,created_at,updated_at FROM legacy_payment_transactions WHERE source_id=$1`, raw.SourceID).Scan(&account, &paymentID, &packed, &status, &gotCreated, &gotUpdated); err != nil || account != client.id || paymentID != raw.PaymentID || packed != raw.Subscription || status != raw.Status || !gotCreated.Equal(raw.CreatedAt) || !gotUpdated.Equal(raw.UpdatedAt) {
			t.Fatal("raw ID/status/time/payload changed", err)
		}
	}
	for _, dryRun := range []bool{true, false} {
		again, err := owner.ImportLegacyPayments(ctx, p, dryRun)
		if err != nil || again.Inserted != 0 || counts() != after {
			t.Fatal("replay was not an exact no-op", err)
		}
	}
	for _, tc := range []struct {
		change func(*payments.LegacyPaymentPackage)
		code   string
	}{
		{func(p *payments.LegacyPaymentPackage) { p.Transactions[0].Status = "completed" }, "IMPORT_SOURCE_CONFLICT"},
		{func(p *payments.LegacyPaymentPackage) { p.Transactions[0].PaymentID = "different-source" }, "IMPORT_SOURCE_CONFLICT"},
		{func(p *payments.LegacyPaymentPackage) { p.Transactions[0].SourceID = 99 }, "IMPORT_SOURCE_CONFLICT"},
		{func(p *payments.LegacyPaymentPackage) { p.Users[0].SourceLegacyUserID = 9 }, "IMPORT_IDENTITY_CONFLICT"},
		{func(p *payments.LegacyPaymentPackage) {
			p.Users[0].SourceTgID = 702
			p.Transactions[0].SourceTgID = 702
			p.Transactions = p.Transactions[:1]
		}, "IMPORT_IDENTITY_CONFLICT"},
	} {
		bad := p
		bad.Users = append([]payments.LegacyPaymentUser{}, p.Users...)
		bad.Transactions = append([]payments.LegacyPaymentTransaction{}, p.Transactions...)
		tc.change(&bad)
		fresh := p.Transactions[1]
		fresh.SourceID = 100
		fresh.PaymentID = "fresh-before-conflict"
		fresh.SourceTgID = bad.Users[0].SourceTgID
		bad.Transactions = append([]payments.LegacyPaymentTransaction{fresh}, bad.Transactions...)
		var domain *payments.Error
		if _, err = owner.ImportLegacyPayments(ctx, bad, false); !errors.As(err, &domain) || domain.Code != tc.code || counts() != after {
			t.Fatal("conflict retained a partial import/audit")
		}
	}
	r := supportRequest(h, &client, "POST", "/api/v1/payment-history", "application/json", []byte(`{"kind":"legacy"}`), cfg.HTTP.CabinetOrigin, uuid.Nil)
	var page wire.PaymentHistoryPage
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.LegacyTransactions) != 4 || page.LegacyTransactions[0].SourceId != "4" || page.LegacyTransactions[0].Quote != nil || page.LegacyTransactions[0].FulfillmentStatus != "unknown" || page.LegacyTransactions[1].Quote == nil || page.LegacyTransactions[1].Quote.AmountMinor != "400" || page.LegacyTransactions[1].PaymentStatus != "canceled" {
		t.Fatal("legacy archive reader inferred modern finance/access", r.Code)
	}
	if strings.Contains(r.Body.String(), "long-test-charge") || strings.Contains(r.Body.String(), "unknown-v0") {
		t.Fatal("raw legacy data leaked")
	}
	more := payments.LegacyPaymentPackage{Version: 1, Users: p.Users, Transactions: []payments.LegacyPaymentTransaction{}}
	for i := range 50 {
		row := p.Transactions[3]
		row.SourceID = 9007199254740993 + int64(i)
		row.PaymentID = fmt.Sprintf("extra-raw-%d", i)
		more.Transactions = append(more.Transactions, row)
	}
	if added, err := owner.ImportLegacyPayments(ctx, more, false); err != nil || added.Inserted != 50 {
		t.Fatal("large source ID import", err)
	}
	after = counts()
	r = supportRequest(h, &client, "POST", "/api/v1/payment-history", "application/json", []byte(`{"kind":"legacy"}`), cfg.HTTP.CabinetOrigin, uuid.Nil)
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || !page.HasMore || len(page.LegacyTransactions) != 50 || page.LegacyTransactions[0].SourceId != "9007199254741042" || page.LegacyTransactions[49].SourceId != "9007199254740993" {
		t.Fatal("legacy integer ID ordering/precision/page size lost")
	}
	last := page.LegacyTransactions[49]
	r = supportRequest(h, &client, "POST", "/api/v1/payment-history", "application/json", mustJSON(t, map[string]any{"kind": "legacy", "before_created_at": last.CreatedAt, "before_id": last.SourceId}), cfg.HTTP.CabinetOrigin, uuid.Nil)
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || page.HasMore || len(page.LegacyTransactions) != 4 || page.LegacyTransactions[0].SourceId != "4" || counts() != after {
		t.Fatal("legacy cursor skipped/duplicated rows or reader wrote data")
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE legacy_payment_transactions SET status='refunded' WHERE source_id=1`); err == nil {
		t.Fatal("archive update allowed")
	}
	if _, err = e.Pool.Exec(ctx, `DELETE FROM legacy_payment_transactions WHERE source_id=1`); err == nil || counts() != after {
		t.Fatal("archive deletion allowed")
	}
}

// Legacy int64 quantities cannot become rounded JavaScript numbers in the DTO.
func TestLegacyPaymentQuantityPrecision(t *testing.T) {
	page := payments.PaymentHistoryPage{Kind: "legacy", LegacyTransactions: []payments.LegacyPayment{{SourceId: "1", PaymentStatus: "completed", FulfillmentStatus: "unknown", Quote: &payments.LegacyQuote{Action: "purchase", AmountMinor: "100", Currency: "RUB", Devices: 9223372036854775807, PeriodDays: 9007199254740993, TrafficGb: 9007199254740995}}}}
	body := string(mustJSON(t, paymentHistoryResult(page)))
	for _, literal := range []string{`"devices":"9223372036854775807"`, `"period_days":"9007199254740993"`, `"traffic_gb":"9007199254740995"`} {
		if !strings.Contains(body, literal) {
			t.Fatal("legacy quantity would lose JavaScript precision")
		}
	}
}
