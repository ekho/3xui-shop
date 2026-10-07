package payments

import (
	"strings"
	"testing"
)

func TestClientTelegramLegacyDecoder(t *testing.T) {
	states := []string{"subscription", "change", "extend", "process", "devices", "duration", "promocode", "get_trial", "back_to_duration", "back_to_payment", "pay_yookassa", "pay_telegram_stars", "pay_cryptomus", "pay_heleket", "pay_yoomoney", "pay_manual"}
	for _, state := range states {
		packed := "subscription:" + state + ":0:0:0:0:0:0:0"
		if got, err := LegacyTelegramButton(packed, 701); err != nil || got != state {
			t.Fatalf("incomplete legacy navigation %s: %s %v", state, got, err)
		}
	}
	valid := "subscription:pay_yoomoney:1:0:701:2:30:15:123.45"
	if got, err := LegacyTelegramButton(valid, 701); err != nil || got != "pay_yoomoney" {
		t.Fatal("valid invoice decoder", err)
	}
	for _, packed := range []string{
		strings.Replace(valid, ":701:", ":702:", 1), strings.Replace(valid, ":701:", ":-1:", 1),
		strings.Replace(valid, "pay_yoomoney", "pay_unknown", 1), strings.Replace(valid, ":1:0:", ":2:0:", 1),
		strings.Replace(valid, ":2:30:", ":-1:30:", 1), strings.Replace(valid, ":2:30:", ":2:-1:", 1),
		strings.Replace(valid, ":15:", ":-1:", 1), strings.Replace(valid, ":123.45", ":-1", 1),
		strings.Replace(valid, ":123.45", ":NaN", 1), strings.Replace(valid, ":123.45", ":92233720368547758.08", 1),
		strings.Replace(valid, ":701:", ":9223372036854775808:", 1), strings.Replace(valid, ":2:30:", ":9223372036854775808:30:", 1),
		strings.Replace(valid, "pay_yoomoney", "pay_telegram_stars", 1), valid + ":extra", strings.Repeat("x", 65),
	} {
		if _, err := LegacyTelegramButton(packed, 701); err == nil {
			t.Fatal("unsafe legacy callback accepted", packed)
		}
	}
	if _, err := LegacyTelegramButton(valid, 0); err == nil {
		t.Fatal("missing actor accepted")
	}
	method, quote := legacyPaymentQuote(valid, 701)
	if method == nil || *method != "yoomoney" || quote == nil || quote.AmountMinor != "12345" || quote.Action != "renew" {
		t.Fatal("old completed quote changed")
	}
	if method, quote := legacyPaymentQuote("subscription:pay_yoomoney:0:0:701:0:0:0:0", 701); method != nil || quote != nil {
		t.Fatal("incomplete callback invented invoice terms")
	}
	archived := "subscription:pay_yoomoney:1:0:701:9223372036854775807:30:15:123.45"
	if _, err := LegacyTelegramButton(archived, 701); err == nil {
		t.Fatal("Telegram payload limit ignored")
	}
	if method, quote := legacyPaymentQuote(archived, 701); method == nil || quote == nil || quote.Devices != 9223372036854775807 {
		t.Fatal("historical archive decoder lost previously readable large quantities")
	}
}
