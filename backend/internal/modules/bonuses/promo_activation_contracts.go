package bonuses

import "github.com/google/uuid"

type ActivatePromocodeInput struct {
	Code string `json:"code"`
}

type PromocodeActivation struct {
	PromocodeID  uuid.UUID `json:"promocode_id"`
	DurationDays int       `json:"duration_days"`
	OperationID  uuid.UUID `json:"operation_id"`
	Status       string    `json:"status"`
}
