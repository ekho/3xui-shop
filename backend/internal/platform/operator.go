package platform

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Every web-operator mutation takes account locks in UUID order, then the role.
// The CLI revoker takes its account lock before deleting that same role.
func (s *Service) lockOperatorPair(ctx context.Context, tx pgx.Tx, actor, target uuid.UUID) (store.Account, error) {
	out, err := s.accounts.LockOperatorPair(ctx, tx, actor, target)
	return legacyAccount(out), accountError(err)
}

func operatorClient(a store.Account) wire.OperatorClient {
	out := wire.OperatorClient{
		AccountId: a.ID, DisplayName: a.DisplayName.String,
		HadSubscription: a.HadSubscription, Kind: wire.OperatorClientKind(a.Kind),
		Locale: wire.OperatorClientLocale(a.Locale), Restricted: a.Restricted,
		VpnBanned: a.VpnBanned,
	}
	if a.CreatedAt.Valid {
		out.CreatedAt = &a.CreatedAt.Time
	}
	if a.EmailKey.Valid {
		email := openapi_types.Email(a.EmailKey.String)
		out.Email = &email
	}
	if a.TelegramID.Valid {
		id := strconv.FormatInt(a.TelegramID.Int64, 10)
		out.TelegramId = &id
	}
	return out
}

func operatorAudit(e store.AuditEvent) wire.OperatorAuditEvent {
	out := wire.OperatorAuditEvent{Id: e.ID, CreatedAt: e.CreatedAt.Time, Action: e.Action,
		RequestId: e.RequestID, OperationId: e.OperationID,
		OperatorAccountId: e.OperatorAccountID, SupportMessageId: e.SupportMessageID, AccessOperationId: e.AccessOperationID}
	if e.OperatorTgID.Valid {
		id := strconv.FormatInt(e.OperatorTgID.Int64, 10)
		out.OperatorTgId = &id
	}
	if e.Reason.Valid {
		out.Reason = &e.Reason.String
	}
	if e.SystemActor.Valid {
		out.SystemActor = &e.SystemActor.Bool
	}
	if e.MonthlyPeriod.Valid {
		out.MonthlyPeriod = &e.MonthlyPeriod.String
	}
	return out
}
func operatorAuditRows(rows []store.AuditEvent) ([]wire.OperatorAuditEvent, bool) {
	more := len(rows) > 50
	if more {
		rows = rows[:50]
	}
	out := make([]wire.OperatorAuditEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, operatorAudit(row))
	}
	return out, more
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

func (s *Service) SearchOperatorClients(ctx context.Context, actor uuid.UUID, in wire.OperatorSearchInput) (wire.OperatorSearchResult, error) {
	result, err := s.accounts.SearchClients(ctx, actor, accounts.OperatorSearchInput(in))
	out := wire.OperatorSearchResult{Clients: []wire.OperatorClient{}, Page: result.Page, PerPage: result.PerPage, Total: result.Total}
	for _, a := range result.Clients {
		out.Clients = append(out.Clients, operatorClient(legacyAccount(a)))
	}
	return out, accountError(err)
}

func (s *Service) operatorClientAccount(ctx context.Context, actor, target uuid.UUID) (store.Account, error) {
	var empty store.Account
	if err := s.RequireSupportOperator(ctx, actor); err != nil {
		return empty, err
	}
	if target == uuid.Nil {
		return empty, failure(400, "INVALID_INPUT")
	}
	a, err := s.accountByID(ctx, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	return a, nil
}

func (s *Service) OperatorClientHistory(ctx context.Context, actor, target uuid.UUID, in wire.OperatorHistoryInput) (wire.OperatorHistoryResult, error) {
	out := wire.OperatorHistoryResult{Kind: wire.OperatorHistoryResultKind(in.Kind), TrialRequests: []wire.OperatorTrialRequest{}, AuditEvents: []wire.OperatorAuditEvent{}, LegacyEvents: []wire.OperatorLegacyApprovalEvent{}}
	if _, err := s.operatorClientAccount(ctx, actor, target); err != nil {
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
		rows, err := s.accounts.LegacyEvents(ctx, target, in.BeforeCreatedAt, sourceID)
		if err != nil {
			return out, unavailable()
		}
		out.LegacyEvents, out.HasMore = legacyEventRows(rows)
		return out, nil
	}
	if in.BeforeSourceId != nil || (in.BeforeCreatedAt == nil) != (in.BeforeId == nil) {
		return out, failure(400, "INVALID_INPUT")
	}
	var before pgtype.Timestamptz
	var id uuid.UUID
	if in.BeforeCreatedAt != nil {
		before = stamp(*in.BeforeCreatedAt)
		id = *in.BeforeId
		if id == uuid.Nil {
			return out, failure(400, "INVALID_INPUT")
		}
	}
	q := store.New(s.pool)
	if in.Kind == "trials" {
		rows, more, err := s.subscriptions.TrialHistory(ctx, actor, target, in.BeforeCreatedAt, in.BeforeId)
		if err != nil {
			return out, unavailable()
		}
		out.TrialRequests, out.HasMore = wireTrialHistory(rows), more
	} else {
		rows, err := q.OperatorAuditPage(ctx, store.OperatorAuditPageParams{AccountID: target, BeforeCreatedAt: before, BeforeID: id})
		if err != nil {
			return out, unavailable()
		}
		out.AuditEvents, out.HasMore = operatorAuditRows(rows)
	}
	return out, nil
}

func (s *Service) OperatorClient(ctx context.Context, actor, target uuid.UUID) (wire.OperatorClientCard, error) {
	var out wire.OperatorClientCard
	a, err := s.operatorClientAccount(ctx, actor, target)
	if err != nil {
		return out, err
	}
	q := store.New(s.pool)
	legacy, err := s.accounts.LegacyApproval(ctx, target)
	if err == nil {
		out.LegacyApproval = legacySnapshot(legacy)
	} else if !errors.Is(err, accounts.ErrNotFound) {
		return out, unavailable()
	}
	legacyRows, err := s.accounts.LegacyEvents(ctx, target, nil, 0)
	if err != nil {
		return out, unavailable()
	}
	out.LegacyEvents, out.LegacyHasMore = legacyEventRows(legacyRows)
	trials, more, err := s.subscriptions.TrialHistory(ctx, actor, target, nil, nil)
	if err != nil {
		return out, unavailable()
	}
	audit, err := q.OperatorAuditPage(ctx, store.OperatorAuditPageParams{AccountID: target})
	if err != nil {
		return out, unavailable()
	}
	out.Client = operatorClient(a)
	out.TrialRequests, out.TrialHasMore = wireTrialHistory(trials), more
	out.AuditEvents, out.AuditHasMore = operatorAuditRows(audit)
	if s.cfg.PanelID != "" {
		out.Server = &wire.OperatorServer{PanelId: s.cfg.PanelID, Enabled: s.cfg.TrialEnabled}
	}
	conversation, supportErr := s.support.Conversation(ctx, actor, target, true)
	if supportErr != nil {
		return out, supportError(supportErr)
	}
	out.Support = wireSupportConversation(conversation)
	subscription, subscriptionErr := s.subscriptions.OperatorSubscription(ctx, actor, target)
	out.Subscription, err = toSubscriptionSubscription(subscription), subscriptionError(subscriptionErr)
	if err != nil {
		return out, err
	}
	out.Subscription.VpnBanned = a.VpnBanned
	if out.Subscription.AccessProfile == "" {
		if a.AccessProfile.Valid {
			out.Subscription.AccessProfile = wire.SubscriptionAccessProfile(a.AccessProfile.String)
		} else {
			out.Subscription.AccessProfile = "unknown"
		}
	}
	return out, nil
}

func (s *Service) OperatorClientKey(ctx context.Context, actor, target uuid.UUID) (wire.SubscriptionKey, error) {
	var out wire.SubscriptionKey
	if _, err := s.operatorClientAccount(ctx, actor, target); err != nil {
		return out, err
	}
	return s.SubscriptionKey(ctx, target)
}

func (s *Service) ChangeOperatorRole(ctx context.Context, target uuid.UUID, grant bool) error {
	return accountError(s.accounts.ChangeOperatorRole(ctx, target, grant))
}

func validOperatorName(name string) bool {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 128 || strings.TrimSpace(name) == "" || strings.ContainsRune(name, '\x00') {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
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

func (s *Service) DecideOperatorTrial(ctx context.Context, actor, id, key uuid.UUID, in wire.OperatorDecisionInput) (wire.OperatorDecisionResult, bool, error) {
	v, created, err := s.subscriptions.DecideOperatorTrial(ctx, actor, id, key, fromSubscriptionOperatorDecisionInput(in))
	return toSubscriptionOperatorDecisionResult(v), created, subscriptionError(err)
}
func (s *Service) ReconsiderOperatorTrial(ctx context.Context, actor, id, key uuid.UUID, reason string) (wire.TrialRequest, bool, error) {
	v, created, err := s.subscriptions.ReconsiderOperatorTrial(ctx, actor, id, key, reason)
	return toSubscriptionTrialRequest(v), created, subscriptionError(err)
}
func (s *Service) ReconcileOperatorTrial(ctx context.Context, actor, id, key uuid.UUID, reason string) (wire.ReconcileResult, bool, error) {
	v, created, err := s.subscriptions.ReconcileOperatorTrial(ctx, actor, id, key, reason)
	return toSubscriptionReconcileResult(v), created, subscriptionError(err)
}
func (s *Service) CreateTelegramTrial(ctx context.Context, actor, key uuid.UUID, in wire.OperatorTelegramTrialInput) (wire.OperatorTelegramTrialResult, bool, error) {
	v, created, err := s.subscriptions.CreateTelegramTrial(ctx, actor, key, fromSubscriptionOperatorTelegramTrialInput(in))
	return toSubscriptionOperatorTelegramTrialResult(v), created, subscriptionError(err)
}
