package app

import (
	"strings"
	"testing"
)

// Catches ignored legacy switches, accepting unsafe durations, or leaking a
// caller's invalid setting before reaching secret/dependency startup.
func TestReferredTrialConfigDefaultsFlagsAndBounds(t *testing.T) {
	t.Setenv("SHOP_REFERRED_TRIAL_ENABLED", "")
	t.Setenv("SHOP_REFERRED_TRIAL_PERIOD", "")
	c, err := readReferredTrialConfig()
	if err != nil || c.Enabled || c.PeriodDays != 7 {
		t.Fatal("legacy referred trial defaults changed", err)
	}
	for _, tc := range []struct {
		flag, days string
		enabled    bool
		period     int64
	}{
		{"true", "1", true, 1},
		{"false", "12", false, 12},
		{"true", "106751", true, 106751},
	} {
		t.Setenv("SHOP_REFERRED_TRIAL_ENABLED", tc.flag)
		t.Setenv("SHOP_REFERRED_TRIAL_PERIOD", tc.days)
		c, err = readReferredTrialConfig()
		if err != nil || c.Enabled != tc.enabled || c.PeriodDays != tc.period {
			t.Fatal("configured referred trial ignored", err)
		}
	}
	for _, tc := range []struct{ name, value string }{
		{"SHOP_REFERRED_TRIAL_ENABLED", "owned-private-invalid-flag"},
		{"SHOP_REFERRED_TRIAL_PERIOD", "0"},
		{"SHOP_REFERRED_TRIAL_PERIOD", "-1"},
		{"SHOP_REFERRED_TRIAL_PERIOD", "1.5"},
		{"SHOP_REFERRED_TRIAL_PERIOD", "106752"},
		{"SHOP_REFERRED_TRIAL_PERIOD", "9223372036854775808"},
		{"SHOP_REFERRED_TRIAL_PERIOD", "owned-private-invalid-period"},
	} {
		t.Setenv("SHOP_REFERRED_TRIAL_ENABLED", "")
		t.Setenv("SHOP_REFERRED_TRIAL_PERIOD", "")
		t.Setenv(tc.name, tc.value)
		if _, err = LoadConfig(); err == nil || !strings.Contains(err.Error(), tc.name) || strings.Contains(err.Error(), tc.value) {
			t.Fatal("invalid config reached dependencies or echoed input", err)
		}
	}
}
