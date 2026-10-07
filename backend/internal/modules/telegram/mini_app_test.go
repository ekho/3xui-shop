package telegram

import (
	"crypto/ed25519"
	"crypto/rand"
	"example.com/cabinet/backend/internal/testkit"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Catches trusting user data without the official canonical signature and time bounds.
func TestMiniAppVerifiedIdentity(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	wrong, _, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	const user = `{"id":4503599627370495,"first_name":"Анна","last_name":"Тест","language_code":"ru"}`
	good := testkit.SignedMiniAppData(priv, 123, now, user, "legacy_campaign")
	cases := []struct {
		name, raw string
		bot       int64
		key       ed25519.PublicKey
		ok        bool
	}{
		{"valid", good, 123, pub, true},
		{"other bot", good, 124, pub, false}, {"other key", good, 123, wrong, false},
		{"tampered user", strings.Replace(good, "%D0%90%D0%BD%D0%BD%D0%B0", "Other", 1), 123, pub, false},
		{"duplicate", good + "&auth_date=1", 123, pub, false},
		{"bad escape", good + "&extra=%QQ", 123, pub, false},
		{"bad utf8", good + "&extra=%ff", 123, pub, false},
		{"bad field", good + "&extra%0Afield=value", 123, pub, false},
		{"missing user", testkit.SignedMiniAppData(priv, 123, now, `{}`, ""), 123, pub, false},
		{"negative id", testkit.SignedMiniAppData(priv, 123, now, `{"id":-1,"first_name":"A"}`, ""), 123, pub, false},
		{"fractional id", testkit.SignedMiniAppData(priv, 123, now, `{"id":1.5,"first_name":"A"}`, ""), 123, pub, false},
		{"too large id", testkit.SignedMiniAppData(priv, 123, now, `{"id":4503599627370496,"first_name":"A"}`, ""), 123, pub, false},
		{"control name", testkit.SignedMiniAppData(priv, 123, now, `{"id":1,"first_name":"A\nB"}`, ""), 123, pub, false},
		{"bot user", testkit.SignedMiniAppData(priv, 123, now, `{"id":1,"first_name":"A","is_bot":true}`, ""), 123, pub, false},
		{"idle window boundary", testkit.SignedMiniAppData(priv, 123, now.Add(-300*time.Second), user, ""), 123, pub, true},
		{"stale", testkit.SignedMiniAppData(priv, 123, now.Add(-301*time.Second), user, ""), 123, pub, false},
		{"future boundary", testkit.SignedMiniAppData(priv, 123, now.Add(60*time.Second), user, ""), 123, pub, true},
		{"future", testkit.SignedMiniAppData(priv, 123, now.Add(61*time.Second), user, ""), 123, pub, false},
		{"invalid payload", testkit.SignedMiniAppData(priv, 123, now, user, "not/a/link"), 123, pub, false},
		{"oversized", strings.Repeat("x", 12289), 123, pub, false},
	}
	v, _ := url.ParseQuery(good)
	v.Set("signature", v.Get("signature")+"==")
	cases = append(cases, struct {
		name, raw string
		bot       int64
		key       ed25519.PublicKey
		ok        bool
	}{"padded signature", v.Encode(), 123, pub, true})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := VerifyMiniAppData(tc.raw, tc.bot, tc.key, now)
			if (err == nil) != tc.ok {
				t.Fatalf("verification valid=%v want %v", err == nil, tc.ok)
			}
			if tc.ok && (got.TelegramID != 4503599627370495 || got.DisplayName != "Анна Тест" || got.Locale != "ru") {
				t.Fatal("signed identity changed")
			}
		})
	}
}
