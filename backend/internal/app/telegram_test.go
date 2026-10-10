package app

import (
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/modules/vpn"
)

func TestNewTelegramTrialOnlyWithoutCabinetOrigin(t *testing.T) {
	runtime, err := NewTelegram(telegram.Config{
		Enabled:   true,
		Token:     "123456789:abcdefghijklmnopqrstuvwxyz012345678",
		Operators: []int64{101},
	}, &Modules{}, "", nil)
	if err != nil || runtime == nil || !runtime.State().Enabled {
		t.Fatalf("enabled trial-only Telegram constructor: runtime=%v err=%v", runtime != nil, err)
	}
}

func TestNewTelegramFullModeValidatesOrigin(t *testing.T) {
	cfg := telegram.Config{Enabled: true, Token: "123456789:abcdefghijklmnopqrstuvwxyz012345678", Operators: []int64{101}}
	// Construction only: no domain action is invoked against these ports.
	modules := &Modules{Accounts: &accounts.Service{}, Payments: &payments.Service{}, Notifications: &notifications.Service{}, VPN: &vpn.Service{}}
	if runtime, err := NewTelegram(cfg, modules, "https://cabinet.example.test", nil); err != nil || runtime == nil {
		t.Fatalf("full mode valid origin: runtime=%v err=%v", runtime != nil, err)
	}
	for _, origin := range []string{"http://cabinet.example.test", "https://cabinet.example.test/other"} {
		if _, err := NewTelegram(cfg, modules, origin, nil); err == nil {
			t.Fatalf("accepted invalid full-mode origin %q", origin)
		}
	}
}
