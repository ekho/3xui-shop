package app

import (
	"bytes"
	"example.com/cabinet/backend/internal/modules/payments"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestYooKassaConfig(t *testing.T) {
	for _, tc := range []struct{ name, flag, inline, field string }{
		{"invalid-flag", "invalid", "", "SHOP_PAYMENT_YOOKASSA_ENABLED"},
		{"missing-file", "true", "", "YOOKASSA_TOKEN"},
		{"inline-only", "true", "test-only-api-token", "YOOKASSA_TOKEN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHOP_PAYMENT_YOOKASSA_ENABLED", tc.flag)
			t.Setenv("YOOKASSA_TOKEN_FILE", "")
			t.Setenv("YOOKASSA_TOKEN", tc.inline)
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), tc.field) || strings.Contains(err.Error(), "test-only-api-token") {
				t.Fatal("provider config accepted or unsafe/unrelated error", err)
			}
		})
	}
}

func TestYooKassaConfigFilesAndValidation(t *testing.T) {
	for _, name := range []string{"conflict", "empty", "unreadable", "invalid-mode"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SHOP_PAYMENT_YOOKASSA_ENABLED", "true")
			t.Setenv("YOOKASSA_TOKEN", "")
			t.Setenv("YOOKASSA_TEST_MODE", "")
			path := filepath.Join(t.TempDir(), "token")
			if name != "unreadable" {
				value := "test-only-api-token"
				if name == "empty" {
					value = " \n "
				}
				if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("YOOKASSA_TOKEN_FILE", path)
			field := "YOOKASSA_TOKEN"
			if name == "conflict" {
				t.Setenv("YOOKASSA_TOKEN", "test-only-inline-token")
			}
			if name == "invalid-mode" {
				t.Setenv("YOOKASSA_TEST_MODE", "invalid")
				field = "YOOKASSA_TEST_MODE"
			}
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), field) || strings.Contains(err.Error(), "test-only-") {
				t.Fatal("file/mode boundary accepted or unsafe error", err)
			}
		})
	}
	c := Config{}
	c.HTTP.CabinetOrigin = "https://cabinet.example.test"
	c.Mail.MailKey = bytes.Repeat([]byte{1}, 32)
	c.Accounts.CodeKey = bytes.Repeat([]byte{2}, 32)
	c.Accounts.TermsVersion, c.Accounts.PrivacyVersion = "1", "1"
	if err := c.Validate(); err != nil {
		t.Fatal("disabled unconfigured provider changed default startup", err)
	}
	c.Payments = payments.Config{YooKassaEnabled: true, YooKassaShopID: "100001", YooKassaToken: "test-only-api-token", ShopEmail: "receipts@example.test"}
	if err := c.Validate(); err != nil {
		t.Fatal("valid deployed provider config rejected", err)
	}
	for _, tc := range []struct{ name, field, value string }{
		{"zero-shop", "YOOKASSA_SHOP_ID", "0"}, {"invalid-shop", "YOOKASSA_SHOP_ID", "bad"},
		{"empty-token", "YOOKASSA_TOKEN", ""}, {"control-token", "YOOKASSA_TOKEN", "bad\x00value"},
		{"long-token", "YOOKASSA_TOKEN", strings.Repeat("x", 513)},
		{"empty-email", "SHOP_EMAIL", ""}, {"name-email", "SHOP_EMAIL", "Name <receipts@example.test>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := c
			copy.Payments.YooKassaEnabled = false // Retained credentials are validated after disabling sales, too.
			switch tc.field {
			case "YOOKASSA_SHOP_ID":
				copy.Payments.YooKassaShopID = tc.value
			case "YOOKASSA_TOKEN":
				copy.Payments.YooKassaToken = tc.value
				copy.Payments.YooKassaEnabled = true
			case "SHOP_EMAIL":
				copy.Payments.ShopEmail = tc.value
			}
			if err := copy.Validate(); err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatal("invalid retained provider config accepted", err)
			}
		})
	}
}
