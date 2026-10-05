package subscriptions

import (
	"time"

	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
)

type AccessDesired = vpn.AccessDesired
type AccessDesiredProfile = string
type AccessOperation struct {
	AccountId         uuid.UUID                       `json:"account_id"`
	CompletedSteps    []AccessOperationCompletedSteps `json:"completed_steps"`
	CreatedAt         time.Time                       `json:"created_at"`
	Desired           AccessDesired                   `json:"desired"`
	Kind              AccessOperationKind             `json:"kind"`
	OperationId       uuid.UUID                       `json:"operation_id"`
	OperatorAccountId *uuid.UUID                      `json:"operator_account_id"`
	Reason            string                          `json:"reason"`
	ReviewReason      *string                         `json:"review_reason"`
	Status            AccessOperationStatus           `json:"status"`
	UpdatedAt         time.Time                       `json:"updated_at"`
}
type AccessOperationCompletedSteps = string
type AccessOperationInput struct {
	Days       *int                         `json:"days,omitempty"`
	Kind       AccessOperationInputKind     `json:"kind"`
	PeriodDays *int64                       `json:"period_days,omitempty"`
	PlanId     *uuid.UUID                   `json:"plan_id,omitempty"`
	Profile    *AccessOperationInputProfile `json:"profile,omitempty"`
	Reason     string                       `json:"reason"`
	Revision   *int64                       `json:"revision,omitempty"`
	VpnBanned  *bool                        `json:"vpn_banned,omitempty"`
}
type AccessOperationInputKind = string
type AccessOperationInputProfile = string
type AccessOperationKind = string
type AccessOperationStatus = string
type AccessReconcileInput struct {
	// AcknowledgeResetCost Explicit true only when choosing to repeat a destructive reset after ambiguous effect.
	AcknowledgeResetCost bool   `json:"acknowledge_reset_cost"`
	Reason               string `json:"reason"`
}
type CurrentTrialRequest struct {
	Request *TrialRequest `json:"request"`
}
type DecisionInput struct {
	CallbackQueryId string                `json:"callback_query_id"`
	Decision        DecisionInputDecision `json:"decision"`
	OperatorTgId    int64                 `json:"operator_tg_id"`
	Reason          *string               `json:"reason,omitempty"`
}
type DecisionInputDecision = string
type DecisionResult struct {
	Card          TelegramPayload             `json:"card"`
	DeliveryState DecisionResultDeliveryState `json:"delivery_state"`
	OperationId   *uuid.UUID                  `json:"operation_id"`
	Request       TrialRequest                `json:"request"`
}
type DecisionResultDeliveryState = string
type OperatorClient struct {
	AccountId uuid.UUID `json:"account_id"`

	// CreatedAt Null when historical account creation time was not recorded. Never an invented timestamp.
	CreatedAt       *time.Time           `json:"created_at"`
	DisplayName     string               `json:"display_name"`
	Email           *string              `json:"email"`
	HadSubscription bool                 `json:"had_subscription"`
	Kind            OperatorClientKind   `json:"kind"`
	Locale          OperatorClientLocale `json:"locale"`
	Restricted      bool                 `json:"restricted"`

	// TelegramId Positive signed int64 as decimal text. Server rejects values above 9223372036854775807; text preserves exact identity in JavaScript.
	TelegramId *string `json:"telegram_id"`
	VpnBanned  bool    `json:"vpn_banned"`
}
type OperatorClientKind = string
type OperatorClientLocale = string
type OperatorDecisionInput struct {
	Decision OperatorDecisionInputDecision `json:"decision"`
	Reason   string                        `json:"reason"`
}
type OperatorDecisionInputDecision = string
type OperatorDecisionResult struct {
	OperationId *uuid.UUID   `json:"operation_id"`
	Request     TrialRequest `json:"request"`
}
type OperatorOperation struct {
	CreatedAt   time.Time               `json:"created_at"`
	OperationId uuid.UUID               `json:"operation_id"`
	Status      OperatorOperationStatus `json:"status"`
}
type OperatorOperationStatus = string
type OperatorTelegramTrialInput struct {
	DisplayName string                           `json:"display_name"`
	Locale      OperatorTelegramTrialInputLocale `json:"locale"`

	// TelegramId Positive signed int64 as decimal text. Server rejects values above 9223372036854775807; text preserves exact identity in JavaScript.
	TelegramId string `json:"telegram_id"`
}
type OperatorTelegramTrialInputLocale = string
type OperatorTelegramTrialResult struct {
	Client      OperatorClient `json:"client"`
	OperationId uuid.UUID      `json:"operation_id"`
	Request     TrialRequest   `json:"request"`
}
type OperatorTrialRequest struct {
	Comment           string             `json:"comment"`
	CreatedAt         time.Time          `json:"created_at"`
	DecidedAt         *time.Time         `json:"decided_at"`
	Operation         *OperatorOperation `json:"operation"`
	OperationId       *uuid.UUID         `json:"operation_id"`
	OperatorAccountId *uuid.UUID         `json:"operator_account_id"`

	// OperatorTgId Positive signed int64 as decimal text. Server rejects values above 9223372036854775807; text preserves exact identity in JavaScript.
	OperatorTgId      *string                    `json:"operator_tg_id"`
	PreviousRequestId *uuid.UUID                 `json:"previous_request_id"`
	Reason            *string                    `json:"reason"`
	RequestId         uuid.UUID                  `json:"request_id"`
	Status            OperatorTrialRequestStatus `json:"status"`
}
type OperatorTrialRequestStatus = string
type ReconcileInput struct {
	OperatorTgId int64  `json:"operator_tg_id"`
	Reason       string `json:"reason"`
}
type ReconcileResult struct {
	OperationId uuid.UUID             `json:"operation_id"`
	Status      ReconcileResultStatus `json:"status"`
}
type ReconcileResultStatus = string
type ReconsiderInput struct {
	OperatorTgId int64  `json:"operator_tg_id"`
	Reason       string `json:"reason"`
}
type Subscription struct {
	AccessOperationId     *uuid.UUID                         `json:"access_operation_id"`
	AccessOperationStatus *SubscriptionAccessOperationStatus `json:"access_operation_status"`
	AccessProfile         SubscriptionAccessProfile          `json:"access_profile"`
	ConnectionAvailable   *bool                              `json:"connection_available,omitempty"`
	DataStale             bool                               `json:"data_stale"`
	Devices               int64                              `json:"devices"`
	ExpiresAt             *time.Time                         `json:"expires_at"`
	ObservedAt            *time.Time                         `json:"observed_at"`
	PanelError            *SubscriptionPanelError            `json:"panel_error,omitempty"`
	Status                SubscriptionStatus                 `json:"status"`
	TrafficDownloadBytes  *int64                             `json:"traffic_download_bytes,omitempty"`
	TrafficLimitBytes     int64                              `json:"traffic_limit_bytes"`
	TrafficRemainingBytes *int64                             `json:"traffic_remaining_bytes,omitempty"`
	TrafficUploadBytes    *int64                             `json:"traffic_upload_bytes,omitempty"`
	TrafficUsedBytes      *int64                             `json:"traffic_used_bytes"`
	UnlimitedDevices      *bool                              `json:"unlimited_devices,omitempty"`
	UnlimitedTraffic      *bool                              `json:"unlimited_traffic,omitempty"`

	// VpnBanned VPN ban is independent of the access profile and account restrictions.
	VpnBanned bool `json:"vpn_banned"`
}
type SubscriptionAccessOperationStatus = string
type SubscriptionAccessProfile = string
type SubscriptionKey struct {
	SubscriptionUrl string `json:"subscription_url"`
}
type SubscriptionPanelError = string
type SubscriptionStatus = string
type TelegramPayload struct {
	Comment         string                `json:"comment"`
	CreatedAt       time.Time             `json:"created_at"`
	DisplayName     *string               `json:"display_name,omitempty"`
	Email           *string               `json:"email"`
	OperationId     *uuid.UUID            `json:"operation_id"`
	RequestId       uuid.UUID             `json:"request_id"`
	Status          TelegramPayloadStatus `json:"status"`
	TargetMessageId *int64                `json:"target_message_id"`

	// TelegramId Positive signed int64 as decimal text. Server rejects values above 9223372036854775807; text preserves exact identity in JavaScript.
	TelegramId *string `json:"telegram_id,omitempty"`
}
type TelegramPayloadStatus = string
type TrialRequest struct {
	CreatedAt         time.Time          `json:"created_at"`
	DecidedAt         *time.Time         `json:"decided_at"`
	OperationId       *uuid.UUID         `json:"operation_id"`
	PreviousRequestId *uuid.UUID         `json:"previous_request_id"`
	RequestId         uuid.UUID          `json:"request_id"`
	Status            TrialRequestStatus `json:"status"`
}
type TrialRequestInput struct {
	Comment *string `json:"comment,omitempty"`
}
type TrialRequestStatus = string

const SubscriptionAccessProfileUnknown = "unknown"
const SubscriptionPanelErrorIdentityMismatch = "identity_mismatch"
const SubscriptionPanelErrorInvalidTraffic = "invalid_traffic"
const SubscriptionPanelErrorUnavailable = "unavailable"
const SubscriptionPanelErrorUnknownMembership = "unknown_membership"
