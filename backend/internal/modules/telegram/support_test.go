package telegram

import (
	"os"
	"path/filepath"
	"testing"
)

// Catches starting a second poller for one bot ID, or accepting plaintext and
// noncanonical destinations that cannot identify the configured forum.
func TestSupportRuntimeConfiguration(t *testing.T) {
	const token = "973:abcdefghijklmnopqrstuvwx"
	file := filepath.Join(t.TempDir(), "support-token")
	if err := os.WriteFile(file, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"valid", "disabled", "plaintext", "positive_group", "zero_group", "noncanonical", "overflow", "same_bot", "other_bot"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("SUPPORT_TELEGRAM_ENABLED", "true")
			t.Setenv("SUPPORT_BOT_TOKEN_FILE", file)
			t.Setenv("SUPPORT_BOT_TOKEN", "")
			t.Setenv("SUPPORT_GROUP_ID", "-10074001")
			switch kind {
			case "disabled":
				t.Setenv("SUPPORT_TELEGRAM_ENABLED", "false")
				t.Setenv("SUPPORT_BOT_TOKEN_FILE", "")
			case "plaintext":
				t.Setenv("SUPPORT_BOT_TOKEN", token)
			case "positive_group":
				t.Setenv("SUPPORT_GROUP_ID", "10074001")
			case "zero_group":
				t.Setenv("SUPPORT_GROUP_ID", "0")
			case "noncanonical":
				t.Setenv("SUPPORT_GROUP_ID", "-010074001")
			case "overflow":
				t.Setenv("SUPPORT_GROUP_ID", "-4503599627370496")
			}
			cfg, err := LoadSupportConfig()
			valid := kind == "valid" || kind == "disabled" || kind == "same_bot" || kind == "other_bot"
			if valid {
				if err != nil {
					t.Fatal("valid config rejected", err)
				}
			} else {
				if err == nil {
					t.Fatal("unsafe support config accepted", kind)
				}
				return
			}
			if kind == "disabled" {
				if cfg.Enabled || cfg.Token != "" {
					t.Fatal("disabled channel reads secrets")
				}
				return
			}
			if kind == "same_bot" || kind == "other_bot" {
				cfg = SupportConfig{Enabled: true, Token: token, GroupID: -10074001}
			}
			if !cfg.Enabled || cfg.Token != token || cfg.GroupID != -10074001 {
				t.Fatal("support configuration lost")
			}
			main := Config{Enabled: true, Token: "974:abcdefghijklmnopqrstuvwx", Operators: []int64{732}}
			if kind == "same_bot" {
				main.Token = "973:zyxwvutsrqponmlkjihgfedcba"
			}
			err = ValidatePollingBots(main, cfg)
			if kind == "same_bot" {
				if err == nil {
					t.Fatal("same bot ID admitted two pollers")
				}
			} else if err != nil {
				t.Fatal("independent channels rejected", err)
			}
		})
	}
}
