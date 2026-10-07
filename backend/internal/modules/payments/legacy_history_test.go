package payments

import (
	"strings"
	"testing"
	"time"
)

// Rounding a source quote or inferring it from an unknown payload loses history.
func TestLegacyPaymentPayload(t *testing.T) {
	for _, tc := range []struct{ packed, method, currency, amount, action string }{
		{"subscription:pay_yoomoney:0:0:701:2:30:15:90071992547409.93", "yoomoney", "RUB", "9007199254740993", "purchase"},
		{"subscription:pay_yookassa:1:1:0:2:30:0:199.0", "yookassa", "RUB", "19900", "renew"},
		{"subscription:pay_manual:0:1:701:2:30:15:199.50", "manual", "RUB", "19950", "change_plan"},
		{"subscription:pay_cryptomus:0:0:701:2:30:15:9.93", "cryptomus", "USD", "993", "purchase"},
		{"subscription:pay_heleket:0:0:701:2:30:15:0.0", "heleket", "USD", "0", "purchase"},
		{"subscription:pay_telegram_stars:1:0:701:2:30:15:400.0", "telegram_stars", "XTR", "400", "renew"},
		{"subscription:pay_yoomoney:0:0:702:2:30:15:199.0", "", "", "", ""},
		{"subscription:pay_yoomoney:0:0:701:2:30:199.0", "", "", "", ""},
		{"subscription:pay_unknown:0:0:701:2:30:15:199.0", "", "", "", ""},
		{"subscription:pay_yoomoney:2:0:701:2:30:15:199.0", "", "", "", ""},
		{"subscription:pay_yoomoney:0:0:701:0:30:15:199.0", "", "", "", ""},
		{"subscription:pay_yoomoney:0:0:701:2:30:-1:199.0", "", "", "", ""},
		{"subscription:pay_yoomoney:0:0:701:2:30:15:NaN", "", "", "", ""},
		{"subscription:pay_yoomoney:0:0:701:2:30:15:1.999", "", "", "", ""},
		{"subscription:pay_telegram_stars:0:0:701:2:30:15:1.5", "", "", "", ""},
		{"subscription:pay_yoomoney:0:0:701:2:30:15:92233720368547758.08", "", "", "", ""},
	} {
		t.Run(tc.packed, func(t *testing.T) {
			method, quote := legacyPaymentQuote(tc.packed, 701)
			if tc.method == "" {
				if method != nil || quote != nil {
					t.Fatal("unknown source was assigned a quote/method")
				}
				return
			}
			if method == nil || *method != tc.method || quote == nil || quote.AmountMinor != tc.amount || quote.Currency != tc.currency || quote.Action != tc.action || quote.Devices != 2 || quote.PeriodDays != 30 {
				t.Fatal("known source quote was lost or rounded")
			}
		})
	}
}

func TestLegacyPaymentValidation(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	valid := LegacyPaymentPackage{Version: 1, Users: []LegacyPaymentUser{{SourceLegacyUserID: 5, SourceTgID: 701}}, Transactions: []LegacyPaymentTransaction{{SourceID: 1, SourceTgID: 701, PaymentID: "original", Subscription: "unknown:preserve", Status: "completed", CreatedAt: now, UpdatedAt: now}}}
	for _, change := range []func(*LegacyPaymentPackage){
		func(p *LegacyPaymentPackage) { p.Version = 2 }, func(p *LegacyPaymentPackage) { p.Users = nil }, func(p *LegacyPaymentPackage) { p.Transactions = nil },
		func(p *LegacyPaymentPackage) { p.Users[0].SourceLegacyUserID = 0 }, func(p *LegacyPaymentPackage) { p.Users = append(p.Users, p.Users[0]) },
		func(p *LegacyPaymentPackage) { p.Transactions[0].SourceID = 0 }, func(p *LegacyPaymentPackage) { p.Transactions[0].SourceTgID = 702 },
		func(p *LegacyPaymentPackage) { p.Transactions[0].Status = "paid" }, func(p *LegacyPaymentPackage) { p.Transactions[0].CreatedAt = time.Time{} },
		func(p *LegacyPaymentPackage) { p.Transactions[0].UpdatedAt = now.Add(time.Nanosecond) },
		func(p *LegacyPaymentPackage) { p.Transactions[0].Subscription = "raw\x00value" }, func(p *LegacyPaymentPackage) { p.Transactions[0].PaymentID = string([]byte{0xff}) },
		func(p *LegacyPaymentPackage) { p.Transactions[0].PaymentID = strings.Repeat("x", 16385) }, func(p *LegacyPaymentPackage) { p.Transactions[0].Subscription = strings.Repeat("x", 16385) },
		func(p *LegacyPaymentPackage) { p.Transactions = append(p.Transactions, p.Transactions[0]) },
	} {
		p := valid
		p.Users = append([]LegacyPaymentUser{}, valid.Users...)
		p.Transactions = append([]LegacyPaymentTransaction{}, valid.Transactions...)
		change(&p)
		if err := validateLegacyPayments(p); err == nil {
			t.Fatal("invalid raw import package accepted")
		}
	}
	valid.Transactions[0].PaymentID = ""
	valid.Transactions[0].Subscription = ""
	valid.Transactions[0].UpdatedAt = now.Add(-time.Second)
	if err := validateLegacyPayments(valid); err != nil {
		t.Fatal("legal original empty raw fields/drifting clock rejected", err)
	}
}
