package app

import (
	"crypto/ed25519"
	"encoding/hex"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/telegram"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func NewTelegram(cfg telegram.Config, trials *subscriptions.Service, delivery *notifications.Service, client *http.Client) (*telegram.Runtime, error) {
	bridge := NewTrialBridge(trials, delivery)
	return telegram.New(cfg, client, bridge, bridge)
}

func NewTelegramMiniApp(cfg telegram.Config, owner *accounts.Service, now func() time.Time) *telegram.MiniApp {
	if !cfg.Enabled {
		return nil
	}
	botID, _ := strconv.ParseInt(strings.SplitN(cfg.Token, ":", 2)[0], 10, 64)
	key, _ := hex.DecodeString(telegram.MiniAppProductionKey)
	return telegram.NewMiniApp(botID, ed25519.PublicKey(key), owner, now)
}
