package support

import (
	"time"

	"github.com/google/uuid"
)

// Field order and tags preserve persisted message replay from the old owner.
type SupportAttachment struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
}

type SupportConversation struct {
	CreatedAt                time.Time `json:"created_at"`
	CustomerReceivedSequence int64     `json:"customer_received_sequence"`
	Id                       uuid.UUID `json:"id"`
	OperatorReceivedSequence int64     `json:"operator_received_sequence"`
	Status                   string    `json:"status"`
	SupportBanned            bool      `json:"support_banned"`
	UpdatedAt                time.Time `json:"updated_at"`
}

type SupportMessage struct {
	Attachment       *SupportAttachment      `json:"attachment"`
	CreatedAt        time.Time               `json:"created_at"`
	Delivery         string                  `json:"delivery"`
	Id               uuid.UUID               `json:"id"`
	Sender           string                  `json:"sender"`
	Sequence         int64                   `json:"sequence"`
	Text             string                  `json:"text"`
	TelegramDelivery *TelegramDeliveryStatus `json:"telegram_delivery,omitempty"`
}

type TelegramDeliveryStatus struct {
	Id                uuid.UUID `json:"id"`
	Status            string    `json:"status"`
	Code              string    `json:"code"`
	RetryCapability   bool      `json:"retry_capability"`
	MediaAvailability string    `json:"media_availability"`
}

type SupportResult struct {
	Conversation   *SupportConversation `json:"conversation"`
	HasMore        bool                 `json:"has_more"`
	Messages       []SupportMessage     `json:"messages"`
	OldestSequence *int64               `json:"oldest_sequence"`
}
