package app

import (
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/platform"
	"net/http"
)

func NewTelegram(cfg telegram.Config, svc *platform.Service, client *http.Client) (*telegram.Runtime, error) {
	bridge := NewTrialBridge(svc)
	return telegram.New(cfg, client, bridge, bridge)
}
