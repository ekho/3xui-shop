package telegram

import (
	"context"
	"github.com/google/uuid"
	"time"
)

// TrialActions is the domain boundary consumed by the Telegram channel.
type TrialActions interface {
	Decide(context.Context, TrialDecision) (Decision, error)
	Reconsider(context.Context, SupportAction) (Trial, error)
	Reconcile(context.Context, SupportAction) (Operation, error)
}

type Outbox interface {
	Claim(context.Context) (*Delivery, error)
	Complete(context.Context, Delivery, DeliveryOutcome) error
}

type TrialDecision struct {
	RequestID          uuid.UUID
	ActorID            int64
	Action, CallbackID string
}
type SupportAction struct {
	TargetID, Key uuid.UUID
	ActorID       int64
	Reason        string
}
type Trial struct {
	ID                      uuid.UUID
	PreviousID, OperationID *uuid.UUID
	Status                  string
}
type Decision struct {
	Trial Trial
	Card  TrialCard
}
type Operation struct {
	ID     uuid.UUID
	Status string
}
type TrialCard struct {
	RequestID                                       uuid.UUID
	OperationID                                     *uuid.UUID
	TargetMessageID                                 *int64
	Email, DisplayName, TelegramID, Comment, Status string
	CreatedAt                                       time.Time
}
type Delivery struct {
	ID             uuid.UUID
	ChatID         int64
	Kind           string
	Card           TrialCard
	LeaseToken     string
	LeaseExpiresAt time.Time
}
type DeliveryOutcome struct {
	Kind, Code        string
	ChatID, MessageID int64
}
type ActionError struct{ Code, CurrentRequestStatus string }

func (e *ActionError) Error() string { return e.Code }
