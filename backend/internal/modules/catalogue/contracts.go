package catalogue

import (
	"github.com/google/uuid"
	"time"
)

// Field order and JSON tags preserve the persisted pre-extraction hashes/results.
type Price struct {
	AmountMinor string `json:"amount_minor"`
	Currency    string `json:"currency"`
	PeriodDays  int64  `json:"period_days"`
}

type Terms struct {
	Devices   int     `json:"devices"`
	Hidden    bool    `json:"hidden"`
	Periods   []int64 `json:"periods"`
	Prices    []Price `json:"prices"`
	Profile   string  `json:"profile"`
	TrafficGb int     `json:"traffic_gb"`
}

type CreateInput struct {
	Reason string `json:"reason"`
	Terms  Terms  `json:"terms"`
}

type RevisionInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
	Terms            Terms  `json:"terms"`
}

type ArchiveInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

type PlanSnapshot struct {
	Devices   int       `json:"devices"`
	Hidden    bool      `json:"hidden"`
	Periods   []int64   `json:"periods"`
	PlanId    uuid.UUID `json:"plan_id"`
	Prices    []Price   `json:"prices"`
	Profile   string    `json:"profile"`
	Revision  int64     `json:"revision"`
	TrafficGb int       `json:"traffic_gb"`
}

type Result struct {
	Plans []PlanSnapshot `json:"plans"`
}

type OperatorPlan struct {
	ActorAccountId *uuid.UUID `json:"actor_account_id"`
	Archived       bool       `json:"archived"`
	ChangedAt      time.Time  `json:"changed_at"`
	Devices        int        `json:"devices"`
	Hidden         bool       `json:"hidden"`
	LegacyPlanId   *string    `json:"legacy_plan_id"`
	Periods        []int64    `json:"periods"`
	PlanId         uuid.UUID  `json:"plan_id"`
	Prices         []Price    `json:"prices"`
	Profile        string     `json:"profile"`
	Reason         *string    `json:"reason"`
	Revision       int64      `json:"revision"`
	Source         string     `json:"source"`
	TrafficGb      int        `json:"traffic_gb"`
}

type OperatorResult struct {
	Page    int            `json:"page"`
	PerPage int            `json:"per_page"`
	Plans   []OperatorPlan `json:"plans"`
	Total   int64          `json:"total"`
}
