package platform

import "testing"

func TestPanelConfig(t *testing.T) {
	cfg := Config{CabinetOrigin: "https://cabinet.example.test", TermsVersion: "1", PrivacyVersion: "1", MailKey: make([]byte, 32), CodeKey: make([]byte, 32), TrialEnabled: true, PanelID: "test", Operators: []int64{101}, PanelURL: "https://panel.example.test/path", PanelToken: "fixture-token", SubscriptionBaseURL: "https://subs.example.test/sub/"}
	if cfg.Validate() != nil {
		t.Fatal("valid panel config rejected")
	}
	for _, u := range []string{"http://panel.example.test", "https://username@panel.example.test", "https://panel.example.test?token=bad", "https://panel.example.test#secret", ""} {
		bad := cfg
		bad.PanelURL = u
		if bad.Validate() == nil {
			t.Fatal("unsafe panel URL accepted")
		}
	}
	bad := cfg
	bad.PanelToken = ""
	if bad.Validate() == nil {
		t.Fatal("missing credential accepted")
	}
	bad = cfg
	bad.SubscriptionBaseURL = "http://subs.example.test/"
	if bad.Validate() == nil {
		t.Fatal("unsafe key base URL accepted")
	}
}
