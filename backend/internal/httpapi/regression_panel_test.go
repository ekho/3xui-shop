package httpapi

import (
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/vpn"
	"testing"
)

func TestRegressionPanelConfig(t *testing.T) {
	cfg := app.Config{HTTP: app.HTTPConfig{CabinetOrigin: "https://cabinet.example.test"}, Accounts: accounts.Config{TermsVersion: "1", PrivacyVersion: "1", CodeKey: make([]byte, 32), Operators: []int64{101}}, Mail: notifications.MailConfig{MailKey: make([]byte, 32)}, Subscriptions: subscriptions.Config{TrialEnabled: true, PanelID: "test", SubscriptionBaseURL: "https://subs.example.test/sub/"}, VPN: vpn.Settings{Panel: vpn.Config{PanelURL: "https://panel.example.test/path", PanelToken: "fixture-token"}}}
	if cfg.Validate() != nil {
		t.Fatal("valid panel config rejected")
	}
	for _, u := range []string{"http://panel.example.test", "https://username@panel.example.test", "https://panel.example.test?token=bad", "https://panel.example.test#secret", ""} {
		bad := cfg
		bad.VPN.Panel.PanelURL = u
		if bad.Validate() == nil {
			t.Fatal("unsafe panel URL accepted")
		}
	}
	bad := cfg
	bad.VPN.Panel.PanelToken = ""
	if bad.Validate() == nil {
		t.Fatal("missing credential accepted")
	}
	bad = cfg
	bad.Subscriptions.SubscriptionBaseURL = "http://subs.example.test/"
	if bad.Validate() == nil {
		t.Fatal("unsafe key base URL accepted")
	}
}
