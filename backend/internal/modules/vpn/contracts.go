package vpn

import (
	"crypto/x509"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Config struct {
	PanelURL, PanelToken, PanelUsername, PanelPassword string
	PanelRootCAs                                       *x509.CertPool
}

// Targets are persisted before the first panel write and never regenerated.
type ProvisionTarget struct {
	OperationID       uuid.UUID `json:"operation_id"`
	PanelID           string    `json:"panel_id"`
	PanelKey          string    `json:"panel_key"`
	VPNID             uuid.UUID `json:"vpn_id"`
	SubID             string    `json:"sub_id"`
	InboundIDs        []int64   `json:"inbound_ids"`
	ExpiryTimeMS      int64     `json:"expiry_time_ms"`
	DeviceCount       int64     `json:"device_count"`
	TrafficLimitBytes int64     `json:"traffic_limit_bytes"`
	Profile           string    `json:"profile,omitempty"`
	Banned            bool      `json:"banned,omitempty"`
}
type PanelClientView struct {
	Raw                                      map[string]json.RawMessage
	PanelKey, SubID                          string
	VPNID                                    uuid.UUID
	ExpiryTimeMS, LimitIP, TrafficLimitBytes int64
	Enabled                                  bool
	InboundIDs                               []int64
	UsedTraffic                              *int64
}

// AccessTarget is persisted before any panel write and never returned by HTTP.
type AccessTarget struct {
	OperationID               uuid.UUID `json:"operation_id"`
	PanelID                   string    `json:"panel_id"`
	PanelKey                  string    `json:"panel_key"`
	VPNID                     uuid.UUID `json:"vpn_id"`
	SubID                     string    `json:"sub_id"`
	ExpiryTimeMS              int64     `json:"expiry_time_ms"`
	DeviceCount               int64     `json:"device_count"`
	TrafficLimitBytes         int64     `json:"traffic_limit_bytes"`
	Profile                   string    `json:"profile"`
	InboundIDs                []int64   `json:"inbound_ids"`
	Missing                   bool      `json:"missing"`
	Reset                     bool      `json:"reset"`
	Banned                    bool      `json:"banned"`
	PreviousBanned            bool      `json:"previous_banned"`
	Enable                    bool      `json:"enable"`
	NoClientIntent            bool      `json:"no_client_intent"`
	RestoreEnabled            bool      `json:"restore_enabled"`
	PreviousExpiryMS          int64     `json:"previous_expiry_ms"`
	PreviousLimitIP           int64     `json:"previous_limit_ip"`
	PreviousTrafficLimitBytes int64     `json:"previous_traffic_limit_bytes"`
	PreviousInboundIDs        []int64   `json:"previous_inbound_ids"`
}

// Desired access is part of the persisted operation contract.
type AccessDesired struct {
	Devices           int64      `json:"devices"`
	ExpiresAt         *time.Time `json:"expires_at"`
	PeriodDays        *int64     `json:"period_days"`
	PlanId            *uuid.UUID `json:"plan_id"`
	Profile           string     `json:"profile"`
	ResetTraffic      bool       `json:"reset_traffic"`
	Revision          *int64     `json:"revision"`
	TrafficLimitBytes int64      `json:"traffic_limit_bytes"`
	VpnBanned         bool       `json:"vpn_banned"`
}
