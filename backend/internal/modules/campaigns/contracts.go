package campaigns

import (
	"github.com/google/uuid"
	"time"
)

type Campaign struct {
	CampaignID     uuid.UUID  `json:"campaign_id"`
	Name           string     `json:"name"`
	Code           *string    `json:"code"`
	State          string     `json:"state"`
	Revision       int64      `json:"revision"`
	WebVisits      int64      `json:"web_visits"`
	LegacyClicks   *int64     `json:"legacy_clicks"`
	LegacyInviteID *string    `json:"legacy_invite_id"`
	CreatedAt      *time.Time `json:"created_at"`
	Source         string     `json:"source"`
}
type CreateInput struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}
type StateInput struct {
	State            string `json:"state"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type ListResult struct {
	Campaigns []Campaign `json:"campaigns"`
	Page      int        `json:"page"`
	PerPage   int        `json:"per_page"`
	Total     int64      `json:"total"`
}
type Event struct {
	EventID        uuid.UUID  `json:"event_id"`
	ActorAccountID *uuid.UUID `json:"actor_account_id"`
	Action         string     `json:"action"`
	CreatedAt      time.Time  `json:"created_at"`
	Reason         *string    `json:"reason"`
	Before         *Campaign  `json:"before"`
	After          Campaign   `json:"after"`
}
