package app

import (
	"context"
	"encoding/json"
	"errors"

	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/telegram"
)

// TrialBridge adapts owned trial actions and notification delivery to the channel.
type TrialBridge struct {
	trials   *subscriptions.Service
	delivery *notifications.Service
}

var _ telegram.TrialActions = (*TrialBridge)(nil)
var _ telegram.Outbox = (*TrialBridge)(nil)

func NewTrialBridge(trials *subscriptions.Service, delivery *notifications.Service) *TrialBridge {
	return &TrialBridge{trials: trials, delivery: delivery}
}

func bridgeError(err error) error {
	if err == nil {
		return nil
	}
	out := &telegram.ActionError{Code: "SERVICE_UNAVAILABLE"}
	var trialError *subscriptions.Error
	if errors.As(err, &trialError) {
		out.Code = trialError.Code
		out.CurrentRequestStatus, _ = trialError.Details["current_request_status"].(string)
	}
	var deliveryError *notifications.Error
	if errors.As(err, &deliveryError) {
		out.Code = deliveryError.Code
	}
	return out
}
func trial(r subscriptions.TrialRequest) telegram.Trial {
	return telegram.Trial{ID: r.RequestId, PreviousID: r.PreviousRequestId, OperationID: r.OperationId, Status: string(r.Status)}
}
func card(p subscriptions.TelegramPayload) telegram.TrialCard {
	out := telegram.TrialCard{RequestID: p.RequestId, OperationID: p.OperationId, TargetMessageID: p.TargetMessageId, Comment: p.Comment, CreatedAt: p.CreatedAt, Status: string(p.Status)}
	if p.Email != nil {
		out.Email = string(*p.Email)
	}
	if p.DisplayName != nil {
		out.DisplayName = *p.DisplayName
	}
	if p.TelegramId != nil {
		out.TelegramID = *p.TelegramId
	}
	return out
}
func (b *TrialBridge) Decide(ctx context.Context, in telegram.TrialDecision) (telegram.Decision, error) {
	out, err := b.trials.DecideTrialRequest(ctx, in.RequestID, subscriptions.DecisionInput{OperatorTgId: in.ActorID, Decision: string(in.Action), CallbackQueryId: in.CallbackID})
	return telegram.Decision{Trial: trial(out.Request), Card: card(out.Card)}, bridgeError(err)
}
func (b *TrialBridge) Reconsider(ctx context.Context, in telegram.SupportAction) (telegram.Trial, error) {
	out, err := b.trials.ReconsiderTrialRequest(ctx, in.TargetID, in.Key, subscriptions.ReconsiderInput{OperatorTgId: in.ActorID, Reason: in.Reason})
	return trial(out), bridgeError(err)
}
func (b *TrialBridge) Reconcile(ctx context.Context, in telegram.SupportAction) (telegram.Operation, error) {
	out, err := b.trials.ReconcileTrialOperation(ctx, in.TargetID, in.Key, subscriptions.ReconcileInput{OperatorTgId: in.ActorID, Reason: in.Reason})
	return telegram.Operation{ID: out.OperationId, Status: string(out.Status)}, bridgeError(err)
}
func (b *TrialBridge) Claim(ctx context.Context) (*telegram.Delivery, error) {
	out, err := b.delivery.ClaimTelegramJobs(ctx, 1)
	if err != nil {
		return nil, bridgeError(err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	j := out[0]
	var payload subscriptions.TelegramPayload
	if json.Unmarshal(j.Payload, &payload) != nil {
		return nil, &telegram.ActionError{Code: "SERVICE_UNAVAILABLE"}
	}
	return &telegram.Delivery{ID: j.JobID, ChatID: j.ChatID, Kind: j.Kind, Card: card(payload), LeaseToken: j.LeaseToken, LeaseExpiresAt: j.LeaseExpiresAt}, nil
}
func (b *TrialBridge) Complete(ctx context.Context, d telegram.Delivery, out telegram.DeliveryOutcome) error {
	var result json.RawMessage
	var err error
	switch out.Kind {
	case "sent":
		result, err = json.Marshal(notifications.TelegramSent{Kind: "sent", ChatId: out.ChatID, MessageId: out.MessageID})
	case "delivery_failed":
		result, err = json.Marshal(notifications.TelegramFailed{Kind: "delivery_failed", Code: out.Code})
	default:
		return &telegram.ActionError{Code: "INVALID_INPUT"}
	}
	if err != nil {
		return &telegram.ActionError{Code: "INVALID_INPUT"}
	}
	return bridgeError(b.delivery.CompleteTelegramJob(ctx, d.ID, d.LeaseToken, result))
}
