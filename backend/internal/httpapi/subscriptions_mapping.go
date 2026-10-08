package httpapi

import (
	"context"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func toSubscriptionTrialRequest(v subscriptions.TrialRequest) wire.TrialRequest {
	out := wire.TrialRequest{}
	out.CreatedAt = v.CreatedAt
	out.DecidedAt = v.DecidedAt
	out.OperationId = v.OperationId
	out.PreviousRequestId = v.PreviousRequestId
	out.RequestId = v.RequestId
	out.Status = wire.TrialRequestStatus(v.Status)
	return out
}

func fromSubscriptionTrialRequestInput(v wire.TrialRequestInput) subscriptions.TrialRequestInput {
	out := subscriptions.TrialRequestInput{}
	out.Comment = v.Comment
	return out
}

func toSubscriptionCurrentTrialRequest(v subscriptions.CurrentTrialRequest) wire.CurrentTrialRequest {
	out := wire.CurrentTrialRequest{}
	if v.Request != nil {
		value := toSubscriptionTrialRequest(*v.Request)
		out.Request = &value
	}
	return out
}

func toSubscriptionTelegramPayload(v subscriptions.TelegramPayload) wire.TelegramPayload {
	out := wire.TelegramPayload{}
	out.Comment = v.Comment
	out.CreatedAt = v.CreatedAt
	out.DisplayName = v.DisplayName
	if v.Email != nil {
		value := openapi_types.Email(*v.Email)
		out.Email = &value
	}
	out.OperationId = v.OperationId
	out.RequestId = v.RequestId
	out.Status = wire.TelegramPayloadStatus(v.Status)
	out.TargetMessageId = v.TargetMessageId
	out.TelegramId = v.TelegramId
	return out
}

func toSubscriptionDecisionResult(v subscriptions.DecisionResult) wire.DecisionResult {
	out := wire.DecisionResult{}
	out.Card = toSubscriptionTelegramPayload(v.Card)
	out.DeliveryState = wire.DecisionResultDeliveryState(v.DeliveryState)
	out.OperationId = v.OperationId
	out.Request = toSubscriptionTrialRequest(v.Request)
	return out
}

func fromSubscriptionDecisionInput(v wire.DecisionInput) subscriptions.DecisionInput {
	out := subscriptions.DecisionInput{}
	out.CallbackQueryId = v.CallbackQueryId
	out.Decision = string(v.Decision)
	out.OperatorTgId = v.OperatorTgId
	out.Reason = v.Reason
	return out
}

func fromSubscriptionReconsiderInput(v wire.ReconsiderInput) subscriptions.ReconsiderInput {
	out := subscriptions.ReconsiderInput{}
	out.OperatorTgId = v.OperatorTgId
	out.Reason = v.Reason
	return out
}

func toSubscriptionReconcileResult(v subscriptions.ReconcileResult) wire.ReconcileResult {
	out := wire.ReconcileResult{}
	out.OperationId = v.OperationId
	out.Status = wire.ReconcileResultStatus(v.Status)
	return out
}

func fromSubscriptionReconcileInput(v wire.ReconcileInput) subscriptions.ReconcileInput {
	out := subscriptions.ReconcileInput{}
	out.OperatorTgId = v.OperatorTgId
	out.Reason = v.Reason
	return out
}

func toSubscriptionAccessDesired(v subscriptions.AccessDesired) wire.AccessDesired {
	out := wire.AccessDesired{}
	out.Devices = v.Devices
	out.ExpiresAt = v.ExpiresAt
	out.PeriodDays = v.PeriodDays
	out.PlanId = v.PlanId
	out.Profile = wire.AccessDesiredProfile(v.Profile)
	out.ResetTraffic = v.ResetTraffic
	out.Revision = v.Revision
	out.TrafficLimitBytes = v.TrafficLimitBytes
	out.VpnBanned = v.VpnBanned
	return out
}

func toSubscriptionAccessOperation(v subscriptions.AccessOperation) wire.AccessOperation {
	out := wire.AccessOperation{}
	out.AccountId = v.AccountId
	if v.CompletedSteps != nil {
		out.CompletedSteps = make([]wire.AccessOperationCompletedSteps, 0, len(v.CompletedSteps))
		for _, value := range v.CompletedSteps {
			out.CompletedSteps = append(out.CompletedSteps, wire.AccessOperationCompletedSteps(value))
		}
	}
	out.CreatedAt = v.CreatedAt
	out.Desired = toSubscriptionAccessDesired(v.Desired)
	out.Kind = wire.AccessOperationKind(v.Kind)
	out.OperationId = v.OperationId
	out.OperatorAccountId = v.OperatorAccountId
	out.Reason = v.Reason
	out.ReviewReason = v.ReviewReason
	out.Status = wire.AccessOperationStatus(v.Status)
	out.UpdatedAt = v.UpdatedAt
	return out
}

func fromSubscriptionAccessOperationInput(v wire.AccessOperationInput) subscriptions.AccessOperationInput {
	out := subscriptions.AccessOperationInput{}
	out.Days = v.Days
	out.Kind = string(v.Kind)
	out.PeriodDays = v.PeriodDays
	out.PlanId = v.PlanId
	if v.Profile != nil {
		value := string(*v.Profile)
		out.Profile = &value
	}
	out.Reason = v.Reason
	out.Revision = v.Revision
	out.VpnBanned = v.VpnBanned
	return out
}

func fromSubscriptionAccessReconcileInput(v wire.AccessReconcileInput) subscriptions.AccessReconcileInput {
	out := subscriptions.AccessReconcileInput{}
	out.AcknowledgeResetCost = v.AcknowledgeResetCost
	out.Reason = v.Reason
	return out
}

func toSubscriptionSubscription(v subscriptions.Subscription) wire.Subscription {
	out := wire.Subscription{}
	out.AccessOperationId = v.AccessOperationId
	if v.AccessOperationStatus != nil {
		value := wire.SubscriptionAccessOperationStatus(*v.AccessOperationStatus)
		out.AccessOperationStatus = &value
	}
	out.AccessProfile = wire.SubscriptionAccessProfile(v.AccessProfile)
	out.ConnectionAvailable = v.ConnectionAvailable
	out.DataStale = v.DataStale
	out.Devices = v.Devices
	out.ExpiresAt = v.ExpiresAt
	out.ObservedAt = v.ObservedAt
	if v.PanelError != nil {
		value := wire.SubscriptionPanelError(*v.PanelError)
		out.PanelError = &value
	}
	out.Status = wire.SubscriptionStatus(v.Status)
	out.TrafficDownloadBytes = v.TrafficDownloadBytes
	out.TrafficLimitBytes = v.TrafficLimitBytes
	out.TrafficRemainingBytes = v.TrafficRemainingBytes
	out.TrafficUploadBytes = v.TrafficUploadBytes
	out.TrafficUsedBytes = v.TrafficUsedBytes
	out.UnlimitedDevices = v.UnlimitedDevices
	out.UnlimitedTraffic = v.UnlimitedTraffic
	out.VpnBanned = v.VpnBanned
	return out
}

func toSubscriptionSubscriptionKey(v subscriptions.SubscriptionKey) wire.SubscriptionKey {
	out := wire.SubscriptionKey{}
	out.SubscriptionUrl = v.SubscriptionUrl
	return out
}

func toSubscriptionOperatorDecisionResult(v subscriptions.OperatorDecisionResult) wire.OperatorDecisionResult {
	out := wire.OperatorDecisionResult{}
	out.OperationId = v.OperationId
	out.Request = toSubscriptionTrialRequest(v.Request)
	return out
}

func fromSubscriptionOperatorDecisionInput(v wire.OperatorDecisionInput) subscriptions.OperatorDecisionInput {
	out := subscriptions.OperatorDecisionInput{}
	out.Decision = string(v.Decision)
	out.Reason = v.Reason
	return out
}

func toSubscriptionOperatorClient(v subscriptions.OperatorClient) wire.OperatorClient {
	out := wire.OperatorClient{}
	out.AccountId = v.AccountId
	out.CreatedAt = v.CreatedAt
	out.DisplayName = v.DisplayName
	if v.Email != nil {
		value := openapi_types.Email(*v.Email)
		out.Email = &value
	}
	out.HadSubscription = v.HadSubscription
	out.Kind = wire.OperatorClientKind(v.Kind)
	out.Locale = wire.OperatorClientLocale(v.Locale)
	out.Restricted = v.Restricted
	out.TelegramId = v.TelegramId
	out.VpnBanned = v.VpnBanned
	return out
}

func toSubscriptionOperatorTelegramTrialResult(v subscriptions.OperatorTelegramTrialResult) wire.OperatorTelegramTrialResult {
	out := wire.OperatorTelegramTrialResult{}
	out.Client = toSubscriptionOperatorClient(v.Client)
	out.OperationId = v.OperationId
	out.Request = toSubscriptionTrialRequest(v.Request)
	return out
}

func fromSubscriptionOperatorTelegramTrialInput(v wire.OperatorTelegramTrialInput) subscriptions.OperatorTelegramTrialInput {
	out := subscriptions.OperatorTelegramTrialInput{}
	out.DisplayName = v.DisplayName
	out.Locale = string(v.Locale)
	out.TelegramId = v.TelegramId
	return out
}

func toSubscriptionOperatorOperation(v subscriptions.OperatorOperation) wire.OperatorOperation {
	out := wire.OperatorOperation{}
	out.CreatedAt = v.CreatedAt
	out.OperationId = v.OperationId
	out.Status = wire.OperatorOperationStatus(v.Status)
	return out
}

func toSubscriptionOperatorTrialRequest(v subscriptions.OperatorTrialRequest) wire.OperatorTrialRequest {
	out := wire.OperatorTrialRequest{}
	out.Comment = v.Comment
	out.CreatedAt = v.CreatedAt
	out.DecidedAt = v.DecidedAt
	if v.Operation != nil {
		value := toSubscriptionOperatorOperation(*v.Operation)
		out.Operation = &value
	}
	out.OperationId = v.OperationId
	out.OperatorAccountId = v.OperatorAccountId
	out.OperatorTgId = v.OperatorTgId
	out.PreviousRequestId = v.PreviousRequestId
	out.Reason = v.Reason
	out.RequestId = v.RequestId
	out.Status = wire.OperatorTrialRequestStatus(v.Status)
	return out
}

func wireTrialHistory(rows []subscriptions.OperatorTrialRequest) []wire.OperatorTrialRequest {
	out := make([]wire.OperatorTrialRequest, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSubscriptionOperatorTrialRequest(r))
	}
	return out
}

func (a *API) createTrialRequest(ctx context.Context, accountID, key uuid.UUID, in wire.TrialRequestInput) (wire.TrialRequest, bool, error) {
	v, created, err := a.subscriptions.CreateTrialRequest(ctx, accountID, key, fromSubscriptionTrialRequestInput(in))
	return toSubscriptionTrialRequest(v), created, subscriptionError(err)
}

func (a *API) activateTelegramTrial(ctx context.Context, accountID, key uuid.UUID) (wire.TrialRequest, bool, error) {
	v, created, err := a.subscriptions.ActivateTelegramTrial(ctx, accountID, key)
	return toSubscriptionTrialRequest(v), created, subscriptionError(err)
}

func (a *API) currentTrialRequest(ctx context.Context, accountID uuid.UUID) (wire.CurrentTrialRequest, error) {
	v, err := a.subscriptions.CurrentTrialRequest(ctx, accountID)
	return toSubscriptionCurrentTrialRequest(v), subscriptionError(err)
}

func (a *API) decideTrialRequest(ctx context.Context, id uuid.UUID, in wire.DecisionInput) (wire.DecisionResult, error) {
	v, err := a.subscriptions.DecideTrialRequest(ctx, id, fromSubscriptionDecisionInput(in))
	return toSubscriptionDecisionResult(v), subscriptionError(err)
}

func (a *API) reconsiderTrialRequest(ctx context.Context, id, key uuid.UUID, in wire.ReconsiderInput) (wire.TrialRequest, error) {
	v, err := a.subscriptions.ReconsiderTrialRequest(ctx, id, key, fromSubscriptionReconsiderInput(in))
	return toSubscriptionTrialRequest(v), subscriptionError(err)
}

func (a *API) subscription(ctx context.Context, account uuid.UUID) (out wire.Subscription, retErr error) {
	v, err := a.subscriptions.Subscription(ctx, account)
	return toSubscriptionSubscription(v), subscriptionError(err)
}

func (a *API) subscriptionKey(ctx context.Context, account uuid.UUID) (wire.SubscriptionKey, error) {
	v, err := a.subscriptions.SubscriptionKey(ctx, account)
	return toSubscriptionSubscriptionKey(v), subscriptionError(err)
}

func (a *API) createAccessOperation(ctx context.Context, actor, target, key uuid.UUID, in wire.AccessOperationInput) (wire.AccessOperation, error) {
	v, err := a.subscriptions.CreateAccessOperation(ctx, actor, target, key, fromSubscriptionAccessOperationInput(in))
	return toSubscriptionAccessOperation(v), subscriptionError(err)
}

func (a *API) getAccessOperation(ctx context.Context, actor, target, id uuid.UUID) (wire.AccessOperation, error) {
	v, err := a.subscriptions.GetAccessOperation(ctx, actor, target, id)
	return toSubscriptionAccessOperation(v), subscriptionError(err)
}

func (a *API) reconcileAccessOperation(ctx context.Context, actor, target, id, key uuid.UUID, in wire.AccessReconcileInput) (wire.AccessOperation, error) {
	v, err := a.subscriptions.ReconcileAccessOperation(ctx, actor, target, id, key, fromSubscriptionAccessReconcileInput(in))
	return toSubscriptionAccessOperation(v), subscriptionError(err)
}

func (a *API) reconcileTrialOperation(ctx context.Context, id, key uuid.UUID, in wire.ReconcileInput) (wire.ReconcileResult, error) {
	v, err := a.subscriptions.ReconcileTrialOperation(ctx, id, key, fromSubscriptionReconcileInput(in))
	return toSubscriptionReconcileResult(v), subscriptionError(err)
}
