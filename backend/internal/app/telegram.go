package app

import (
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/telegram"
	"net/http"
)

func NewTelegram(cfg telegram.Config, trials *subscriptions.Service, delivery *notifications.Service, client *http.Client) (*telegram.Runtime, error) {
	bridge := NewTrialBridge(trials, delivery)
	return telegram.New(cfg, client, bridge, bridge)
}
