package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/modules/payments"
)

func setCryptoConfig(t *testing.T, config *payments.Config, merchant, key string) {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"CryptomusMerchantID": merchant, "CryptomusAPIKey": key})
	if err != nil || json.Unmarshal(raw, config) != nil {
		t.Fatal("cannot prepare crypto config")
	}
}

func TestCryptomusConfig(t *testing.T) {
	for _, tc := range []struct{ name, flag, inline, field string }{
		{"invalid-flag", "invalid", "", "SHOP_PAYMENT_CRYPTOMUS_ENABLED"},
		{"missing-file", "true", "", "CRYPTOMUS_API_KEY"},
		{"inline-only", "true", "test-only-crypto-key", "CRYPTOMUS_API_KEY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHOP_PAYMENT_CRYPTOMUS_ENABLED", tc.flag)
			t.Setenv("CRYPTOMUS_API_KEY_FILE", "")
			t.Setenv("CRYPTOMUS_API_KEY", tc.inline)
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), tc.field) || strings.Contains(err.Error(), "test-only-") {
				t.Fatal("crypto configuration accepted or unrelated/unsafe error", err)
			}
		})
	}
	for _, name := range []string{"conflict", "empty", "unreadable"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SHOP_PAYMENT_CRYPTOMUS_ENABLED", "false")
			t.Setenv("CRYPTOMUS_API_KEY", "")
			t.Setenv("CRYPTOMUS_MERCHANT_ID", "00000000-0000-4000-8000-000000000014")
			path := filepath.Join(t.TempDir(), "key")
			if name != "unreadable" {
				value := "test-only-crypto-key"
				if name == "empty" {
					value = " \n "
				}
				if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("CRYPTOMUS_API_KEY_FILE", path)
			if name == "conflict" {
				t.Setenv("CRYPTOMUS_API_KEY", "test-only-inline-key")
			}
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "CRYPTOMUS_API_KEY") || strings.Contains(err.Error(), "test-only-") {
				t.Fatal("retained file boundary ignored or unsafe error", err)
			}
		})
	}
}

func TestCryptomusConfigValidation(t *testing.T) {
	c := Config{}
	c.HTTP.CabinetOrigin = "https://cabinet.example.test"
	c.Mail.MailKey = bytes.Repeat([]byte{1}, 32)
	c.Accounts.CodeKey = bytes.Repeat([]byte{2}, 32)
	c.Accounts.TermsVersion, c.Accounts.PrivacyVersion = "1", "1"
	if err := c.Validate(); err != nil {
		t.Fatal("disabled unconfigured provider changed startup", err)
	}
	// JSON makes validation RED runnable before new configuration fields exist.
	for _, tc := range []struct{ name, merchant, key, field string }{
		{"missing-key", "00000000-0000-4000-8000-000000000014", "", "CRYPTOMUS_API_KEY"},
		{"missing-merchant", "", "test-only-crypto-key", "CRYPTOMUS_MERCHANT_ID"},
		{"invalid-merchant", "not-a-uuid", "test-only-crypto-key", "CRYPTOMUS_MERCHANT_ID"},
		{"nil-merchant", "00000000-0000-0000-0000-000000000000", "test-only-crypto-key", "CRYPTOMUS_MERCHANT_ID"},
		{"control-key", "00000000-0000-4000-8000-000000000014", "bad\x00key", "CRYPTOMUS_API_KEY"},
		{"long-key", "00000000-0000-4000-8000-000000000014", strings.Repeat("x", 513), "CRYPTOMUS_API_KEY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := c
			setCryptoConfig(t, &copy.Payments, tc.merchant, tc.key)
			if err := copy.Validate(); err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatal("invalid retained crypto credentials accepted", err)
			}
		})
	}
	setCryptoConfig(t, &c.Payments, "00000000-0000-4000-8000-000000000014", "test-only-crypto-key")
	if err := c.Validate(); err != nil {
		t.Fatal("valid retained credentials rejected", err)
	}
}
