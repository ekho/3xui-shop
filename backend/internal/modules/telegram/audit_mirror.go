package telegram

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"net/http"
	"os"
	"strconv"
)

type AuditMirrorConfig struct {
	Enabled bool
	Token   string
	GroupID int64
}

func LoadAuditMirrorConfig() (AuditMirrorConfig, error) {
	c := AuditMirrorConfig{}
	if value := os.Getenv("AUDIT_MIRROR_ENABLED"); value != "" {
		var err error
		c.Enabled, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid AUDIT_MIRROR_ENABLED")
		}
	}
	if !c.Enabled {
		return c, nil
	}
	var err error
	c.Token, err = loadTokenFile("SUPPORT_BOT_TOKEN")
	if err != nil {
		return AuditMirrorConfig{}, err
	}
	raw := os.Getenv("SUPPORT_GROUP_ID")
	c.GroupID, err = strconv.ParseInt(raw, 10, 64)
	if err != nil || strconv.FormatInt(c.GroupID, 10) != raw || c.GroupID >= 0 || c.GroupID < -(1<<52-1) {
		return AuditMirrorConfig{}, errors.New("invalid SUPPORT_GROUP_ID")
	}
	return c, nil
}
func NewAuditMirror(cfg AuditMirrorConfig, client *http.Client) (func(context.Context, string) error, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if !tokenPattern.MatchString(cfg.Token) || cfg.GroupID >= 0 || cfg.GroupID < -(1<<52-1) {
		return nil, errors.New("invalid audit mirror configuration")
	}
	transport := botapi.New(cfg.Token, client)
	return func(ctx context.Context, text string) error { return transport.SendGeneral(ctx, cfg.GroupID, text) }, nil
}
