package platform

import (
	"example.com/cabinet/backend/internal/modules/vpn"
	"time"
)

type ProvisionTarget = vpn.ProvisionTarget
type accessTarget = vpn.AccessTarget
type PanelClientView = vpn.PanelClientView
type PanelClient = vpn.PanelClient

var errPanel = vpn.ErrPanel
var errPanelIdentity = vpn.ErrIdentity
var errPanelMembership = vpn.ErrMembership
var errPanelTraffic = vpn.ErrTraffic

func NewPanelClient(c Config) *PanelClient {
	return vpn.NewPanelClient(vpn.Config{PanelURL: c.PanelURL, PanelToken: c.PanelToken, PanelUsername: c.PanelUsername, PanelPassword: c.PanelPassword, PanelRootCAs: c.PanelRootCAs})
}

func panelMatches(v *PanelClientView, t ProvisionTarget, now time.Time) bool {
	return vpn.Matches(v, t, now)
}
func missingInbounds(v *PanelClientView, t ProvisionTarget) []int64 {
	return vpn.MissingInbounds(v, t)
}
