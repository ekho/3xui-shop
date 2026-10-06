package httpapi

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"strconv"
)

func operatorClient(a accounts.Snapshot) wire.OperatorClient {
	out := wire.OperatorClient{
		AccountId: a.ID, DisplayName: stringValue(a.DisplayName),
		HadSubscription: a.HadSubscription, Kind: wire.OperatorClientKind(a.Kind),
		Locale: wire.OperatorClientLocale(a.Locale), Restricted: a.Restricted,
		VpnBanned: a.VpnBanned,
	}
	out.CreatedAt = a.CreatedAt
	if a.EmailKey != nil {
		email := openapi_types.Email(*a.EmailKey)
		out.Email = &email
	}
	if a.TelegramID != nil {
		id := strconv.FormatInt(*a.TelegramID, 10)
		out.TelegramId = &id
	}
	return out
}

func operatorAudit(e auditreports.Event) wire.OperatorAuditEvent {
	out := wire.OperatorAuditEvent{Id: e.ID, CreatedAt: e.CreatedAt, Action: e.Action,
		RequestId: e.RequestID, OperationId: e.OperationID,
		OperatorAccountId: e.OperatorAccountID, SupportMessageId: e.SupportMessageID, AccessOperationId: e.AccessOperationID,
		Reason: e.Reason, SystemActor: e.SystemActor, MonthlyPeriod: e.MonthlyPeriod}
	if e.OperatorTgID != nil {
		id := strconv.FormatInt(*e.OperatorTgID, 10)
		out.OperatorTgId = &id
	}
	return out
}

func operatorAuditRows(rows []auditreports.Event) []wire.OperatorAuditEvent {
	out := make([]wire.OperatorAuditEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, operatorAudit(row))
	}
	return out
}

func legacySnapshot(r accounts.LegacyApprovalUser) *wire.OperatorLegacyApproval {
	out := &wire.OperatorLegacyApproval{SourceLegacyUserId: strconv.FormatInt(r.SourceLegacyUserID, 10), SourceTgId: strconv.FormatInt(r.SourceTgID, 10), Status: wire.OperatorLegacyApprovalStatus(r.Status)}
	if r.RequestedAt != nil {
		out.RequestedAt = r.RequestedAt
	}
	if r.DecidedAt != nil {
		out.DecidedAt = r.DecidedAt
	}
	if r.DecidedBy != nil {
		id := strconv.FormatInt(*r.DecidedBy, 10)
		out.DecidedBy = &id
	}
	return out
}

func legacyEvent(r accounts.LegacyApprovalSourceEvent) wire.OperatorLegacyApprovalEvent {
	out := wire.OperatorLegacyApprovalEvent{SourceId: strconv.FormatInt(r.SourceID, 10), TargetTgId: strconv.FormatInt(r.TargetTgID, 10), CreatedAt: r.CreatedAt, Action: wire.OperatorLegacyApprovalEventAction(r.Action)}
	if r.Source != nil {
		out.Source = r.Source
	}
	if r.ActorType != nil {
		out.ActorType = r.ActorType
	}
	if r.ActorID != nil {
		id := strconv.FormatInt(*r.ActorID, 10)
		out.ActorId = &id
	}
	if r.ActorName != nil {
		out.ActorName = r.ActorName
	}
	return out
}

func legacyEventRows(rows []accounts.LegacyApprovalSourceEvent) ([]wire.OperatorLegacyApprovalEvent, bool) {
	more := len(rows) > 50
	if more {
		rows = rows[:50]
	}
	out := make([]wire.OperatorLegacyApprovalEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, legacyEvent(r))
	}
	return out, more
}

func (a *API) searchOperatorClients(ctx context.Context, actor uuid.UUID, in wire.OperatorSearchInput) (wire.OperatorSearchResult, error) {
	result, err := a.accounts.SearchClients(ctx, actor, accounts.OperatorSearchInput(in))
	out := wire.OperatorSearchResult{Clients: []wire.OperatorClient{}, Page: result.Page, PerPage: result.PerPage, Total: result.Total}
	for _, a := range result.Clients {
		out.Clients = append(out.Clients, operatorClient(a))
	}
	return out, accountError(err)
}

func (a *API) operatorClientAccount(ctx context.Context, actor, target uuid.UUID) (accounts.Snapshot, error) {
	var empty accounts.Snapshot
	if err := a.requireSupportOperator(ctx, actor); err != nil {
		return empty, err
	}
	if target == uuid.Nil {
		return empty, failure(400, "INVALID_INPUT")
	}
	targetAccount, err := a.accounts.Lookup(ctx, target)
	err = accountError(err)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	return targetAccount, nil
}

func (a *API) operatorClientHistory(ctx context.Context, actor, target uuid.UUID, in wire.OperatorHistoryInput) (wire.OperatorHistoryResult, error) {
	out := wire.OperatorHistoryResult{Kind: wire.OperatorHistoryResultKind(in.Kind), TrialRequests: []wire.OperatorTrialRequest{}, AuditEvents: []wire.OperatorAuditEvent{}, LegacyEvents: []wire.OperatorLegacyApprovalEvent{}}
	if _, err := a.operatorClientAccount(ctx, actor, target); err != nil {
		return out, err
	}
	if in.Kind != "trials" && in.Kind != "audit" && in.Kind != "legacy" {
		return out, failure(400, "INVALID_INPUT")
	}
	if in.Kind == "legacy" {
		if in.BeforeId != nil || (in.BeforeCreatedAt == nil) != (in.BeforeSourceId == nil) {
			return out, failure(400, "INVALID_INPUT")
		}
		var sourceID int64
		if in.BeforeCreatedAt != nil {
			var err error
			sourceID, err = parseTelegramID(*in.BeforeSourceId)
			if err != nil {
				return out, err
			}
		}
		rows, err := a.accounts.LegacyEvents(ctx, target, in.BeforeCreatedAt, sourceID)
		if err != nil {
			return out, unavailable()
		}
		out.LegacyEvents, out.HasMore = legacyEventRows(rows)
		return out, nil
	}
	if in.BeforeSourceId != nil || (in.BeforeCreatedAt == nil) != (in.BeforeId == nil) {
		return out, failure(400, "INVALID_INPUT")
	}
	var id uuid.UUID
	if in.BeforeCreatedAt != nil {
		id = *in.BeforeId
		if id == uuid.Nil {
			return out, failure(400, "INVALID_INPUT")
		}
	}
	if in.Kind == "trials" {
		rows, more, err := a.subscriptions.TrialHistory(ctx, actor, target, in.BeforeCreatedAt, in.BeforeId)
		if err != nil {
			return out, unavailable()
		}
		out.TrialRequests, out.HasMore = wireTrialHistory(rows), more
	} else {
		rows, more, err := a.auditReports.Page(ctx, target, in.BeforeCreatedAt, id)
		if err != nil {
			return out, unavailable()
		}
		out.AuditEvents, out.HasMore = operatorAuditRows(rows), more
	}
	return out, nil
}

func (a *API) operatorClient(ctx context.Context, actor, target uuid.UUID) (wire.OperatorClientCard, error) {
	var out wire.OperatorClientCard
	client, err := a.operatorClientAccount(ctx, actor, target)
	if err != nil {
		return out, err
	}
	legacy, err := a.accounts.LegacyApproval(ctx, target)
	if err == nil {
		out.LegacyApproval = legacySnapshot(legacy)
	} else if !errors.Is(err, accounts.ErrNotFound) {
		return out, unavailable()
	}
	legacyRows, err := a.accounts.LegacyEvents(ctx, target, nil, 0)
	if err != nil {
		return out, unavailable()
	}
	out.LegacyEvents, out.LegacyHasMore = legacyEventRows(legacyRows)
	trials, more, err := a.subscriptions.TrialHistory(ctx, actor, target, nil, nil)
	if err != nil {
		return out, unavailable()
	}
	audit, auditMore, err := a.auditReports.Page(ctx, target, nil, uuid.Nil)
	if err != nil {
		return out, unavailable()
	}
	out.Client = operatorClient(client)
	out.TrialRequests, out.TrialHasMore = wireTrialHistory(trials), more
	out.AuditEvents, out.AuditHasMore = operatorAuditRows(audit), auditMore
	if panelID, enabled := a.subscriptions.TrialServer(); panelID != "" {
		out.Server = &wire.OperatorServer{PanelId: panelID, Enabled: enabled}
	}
	conversation, supportErr := a.supportOwner.Conversation(ctx, actor, target, true)
	if supportErr != nil {
		return out, supportError(supportErr)
	}
	out.Support = wireSupportConversation(conversation)
	subscription, subscriptionErr := a.subscriptions.OperatorSubscription(ctx, actor, target)
	out.Subscription, err = toSubscriptionSubscription(subscription), subscriptionError(subscriptionErr)
	if err != nil {
		return out, err
	}
	out.Subscription.VpnBanned = client.VpnBanned
	if out.Subscription.AccessProfile == "" {
		if client.AccessProfile != nil {
			out.Subscription.AccessProfile = wire.SubscriptionAccessProfile(*client.AccessProfile)
		} else {
			out.Subscription.AccessProfile = "unknown"
		}
	}
	return out, nil
}

func (a *API) operatorClientKey(ctx context.Context, actor, target uuid.UUID) (wire.SubscriptionKey, error) {
	var out wire.SubscriptionKey
	if _, err := a.operatorClientAccount(ctx, actor, target); err != nil {
		return out, err
	}
	return a.subscriptionKey(ctx, target)
}

func parseTelegramID(value string) (int64, error) {
	if value == "" || len(value) > 19 || value[0] < '1' || value[0] > '9' {
		return 0, failure(400, "INVALID_INPUT")
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, failure(400, "INVALID_INPUT")
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, failure(400, "INVALID_INPUT")
	}
	return id, nil
}

func (a *API) decideOperatorTrial(ctx context.Context, actor, id, key uuid.UUID, in wire.OperatorDecisionInput) (wire.OperatorDecisionResult, bool, error) {
	v, created, err := a.subscriptions.DecideOperatorTrial(ctx, actor, id, key, fromSubscriptionOperatorDecisionInput(in))
	return toSubscriptionOperatorDecisionResult(v), created, subscriptionError(err)
}

func (a *API) reconsiderOperatorTrial(ctx context.Context, actor, id, key uuid.UUID, reason string) (wire.TrialRequest, bool, error) {
	v, created, err := a.subscriptions.ReconsiderOperatorTrial(ctx, actor, id, key, reason)
	return toSubscriptionTrialRequest(v), created, subscriptionError(err)
}

func (a *API) reconcileOperatorTrial(ctx context.Context, actor, id, key uuid.UUID, reason string) (wire.ReconcileResult, bool, error) {
	v, created, err := a.subscriptions.ReconcileOperatorTrial(ctx, actor, id, key, reason)
	return toSubscriptionReconcileResult(v), created, subscriptionError(err)
}

func (a *API) createTelegramTrial(ctx context.Context, actor, key uuid.UUID, in wire.OperatorTelegramTrialInput) (wire.OperatorTelegramTrialResult, bool, error) {
	v, created, err := a.subscriptions.CreateTelegramTrial(ctx, actor, key, fromSubscriptionOperatorTelegramTrialInput(in))
	return toSubscriptionOperatorTelegramTrialResult(v), created, subscriptionError(err)
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
