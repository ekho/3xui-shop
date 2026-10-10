package bonuses

import (
	"github.com/google/uuid"
	"time"
)

type Promocode struct {
	PromocodeID        uuid.UUID  `json:"promocode_id"`
	Code               string     `json:"code"`
	DurationDays       int        `json:"duration_days"`
	Revision           int64      `json:"revision"`
	State              string     `json:"state"`
	CreatedAt          *time.Time `json:"created_at"`
	ActivatedAt        *time.Time `json:"activated_at"`
	ActivatedAccountID *uuid.UUID `json:"activated_account_id"`
	ActivatedByTgID    *string    `json:"activated_by_tg_id"`
	LegacySource       *string    `json:"legacy_source"`
	LegacyPromocodeID  *string    `json:"legacy_promocode_id"`
}
type CreatePromocodeInput struct {
	DurationDays int    `json:"duration_days"`
	Reason       string `json:"reason"`
}
type EditPromocodeInput struct {
	DurationDays     int    `json:"duration_days"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type DeletePromocodeInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type PromocodeList struct {
	Promocodes []Promocode `json:"promocodes"`
	Page       int         `json:"page"`
	PerPage    int         `json:"per_page"`
	Total      int64       `json:"total"`
}
type PromocodeMetadata struct {
	PromocodeID  uuid.UUID `json:"promocode_id"`
	DurationDays int       `json:"duration_days"`
	Revision     int64     `json:"revision"`
	State        string    `json:"state"`
}
type PromocodeEvent struct {
	EventID        uuid.UUID          `json:"event_id"`
	ActorAccountID *uuid.UUID         `json:"actor_account_id"`
	Action         string             `json:"action"`
	CreatedAt      time.Time          `json:"created_at"`
	Reason         *string            `json:"reason"`
	Before         *PromocodeMetadata `json:"before"`
	After          PromocodeMetadata  `json:"after"`
}
type PromocodeDetail struct {
	Promocode     Promocode        `json:"promocode"`
	Events        []PromocodeEvent `json:"events"`
	EventsHasMore bool             `json:"events_has_more"`
}
