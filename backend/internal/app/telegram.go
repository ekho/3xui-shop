package app

import (
	"crypto/ed25519"
	"encoding/hex"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/telegram"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func NewTelegram(cfg telegram.Config, modules *Modules, origin string, client *http.Client) (*telegram.Runtime, error) {
	bridge := NewTrialBridge(modules.Subscriptions, modules.Notifications)
	var channel *telegram.Client
	if cfg.Enabled && origin != "" {
		var err error
		channel, err = telegram.NewClient(origin, modules.Accounts, modules.Payments, modules.Notifications, modules.Maintenance)
		if err != nil {
			return nil, err
		}
	}
	runtime, err := telegram.New(cfg, client, bridge, bridge, channel)
	if err != nil {
		return nil, err
	}
	if cfg.Enabled && channel != nil {
		modules.Payments.ConfigureStars(runtime.StarsGateway())
	}
	return runtime, nil
}

func NewSupportTelegram(cfg telegram.SupportConfig, modules *Modules, origin string, client *http.Client) (*telegram.Runtime, error) {
	return telegram.NewSupport(cfg, client, origin, modules.Accounts, modules.Support, modules.Subscriptions)
}

func NewTelegramMiniApp(cfg telegram.Config, owner *accounts.Service, now func() time.Time) *telegram.MiniApp {
	if !cfg.Enabled {
		return nil
	}
	botID, _ := strconv.ParseInt(strings.SplitN(cfg.Token, ":", 2)[0], 10, 64)
	key, _ := hex.DecodeString(telegram.MiniAppProductionKey)
	return telegram.NewMiniApp(botID, ed25519.PublicKey(key), owner, now)
}
