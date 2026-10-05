package app

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/platform"
	"example.com/cabinet/backend/internal/wire"
)

// TrialBridge is transitional wiring; domain transactions remain in platform
// until their owners are extracted. It performs no SQL or eligibility checks.
type TrialBridge struct{ svc *platform.Service }

var _ telegram.TrialActions = (*TrialBridge)(nil)
var _ telegram.Outbox = (*TrialBridge)(nil)

func NewTrialBridge(svc *platform.Service) *TrialBridge { return &TrialBridge{svc: svc} }

func bridgeError(err error) error {
	if err == nil {
		return nil
	}
	out := &telegram.ActionError{Code: "SERVICE_UNAVAILABLE"}
	var domain *platform.Error
	if errors.As(err, &domain) {
		out.Code = domain.Code
		out.CurrentRequestStatus, _ = domain.Details["current_request_status"].(string)
	}
	return out
}
func trial(r wire.TrialRequest) telegram.Trial {
	return telegram.Trial{ID: r.RequestId, PreviousID: r.PreviousRequestId, OperationID: r.OperationId, Status: string(r.Status)}
}
func card(p wire.TelegramPayload) telegram.TrialCard {
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
	out, err := b.svc.DecideTrialRequest(ctx, in.RequestID, wire.DecisionInput{OperatorTgId: in.ActorID, Decision: wire.DecisionInputDecision(in.Action), CallbackQueryId: in.CallbackID})
	return telegram.Decision{Trial: trial(out.Request), Card: card(out.Card)}, bridgeError(err)
}
func (b *TrialBridge) Reconsider(ctx context.Context, in telegram.SupportAction) (telegram.Trial, error) {
	out, err := b.svc.ReconsiderTrialRequest(ctx, in.TargetID, in.Key, wire.ReconsiderInput{OperatorTgId: in.ActorID, Reason: in.Reason})
	return trial(out), bridgeError(err)
}
func (b *TrialBridge) Reconcile(ctx context.Context, in telegram.SupportAction) (telegram.Operation, error) {
	out, err := b.svc.ReconcileTrialOperation(ctx, in.TargetID, in.Key, wire.ReconcileInput{OperatorTgId: in.ActorID, Reason: in.Reason})
	return telegram.Operation{ID: out.OperationId, Status: string(out.Status)}, bridgeError(err)
}
func (b *TrialBridge) Claim(ctx context.Context) (*telegram.Delivery, error) {
	out, err := b.svc.ClaimTelegramJobs(ctx, wire.ClaimInput{Limit: 1})
	if err != nil {
		return nil, bridgeError(err)
	}
	if len(out.Jobs) == 0 {
		return nil, nil
	}
	j := out.Jobs[0]
	return &telegram.Delivery{ID: j.JobId, ChatID: j.ChatId, Kind: string(j.Kind), Card: card(j.Payload), LeaseToken: j.LeaseToken, LeaseExpiresAt: j.LeaseExpiresAt}, nil
}
func (b *TrialBridge) Complete(ctx context.Context, d telegram.Delivery, out telegram.DeliveryOutcome) error {
	var result wire.TelegramResult
	var err error
	switch out.Kind {
	case "sent":
		err = result.FromTelegramSent(wire.TelegramSent{Kind: "sent", ChatId: out.ChatID, MessageId: out.MessageID})
	case "delivery_failed":
		err = result.FromTelegramFailed(wire.TelegramFailed{Kind: "delivery_failed", Code: wire.TelegramFailedCode(out.Code)})
	default:
		return &telegram.ActionError{Code: "INVALID_INPUT"}
	}
	if err != nil {
		return &telegram.ActionError{Code: "INVALID_INPUT"}
	}
	return bridgeError(b.svc.CompleteTelegramJob(ctx, d.ID, wire.TelegramResultInput{LeaseToken: d.LeaseToken, Result: result}))
}
