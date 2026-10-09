package httpapi

import (
	"errors"
	"strconv"

	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func (a *API) ReadOperatorAuditHistory(c *echo.Context) error {
	actor, err := a.operatorAuth(c, true)
	if err != nil {
		return err
	}
	in, err := decode[struct {
		wire.AuditHistoryInput
		BeforeCreatedAt *string `json:"before_created_at"`
	}](a, c, "AuditHistoryInput")
	if err != nil {
		return err
	}
	input := auditreports.HistoryInput{Kind: string(in.Kind), AccountID: in.AccountId}
	if in.BeforeCreatedAt != nil {
		stamp, err := auditreports.ParseTimestamp(*in.BeforeCreatedAt)
		if err != nil {
			return invalid()
		}
		input.BeforeCreatedAt = &stamp
	}
	if in.BeforeId != nil {
		input.BeforeID = *in.BeforeId
	}
	if in.AccountId != nil && *in.AccountId == uuid.Nil || in.BeforeId != nil && *in.BeforeId == uuid.Nil {
		return invalid()
	}
	if in.LegacyTargetTgId != nil {
		id, err := parseTelegramID(*in.LegacyTargetTgId)
		if err != nil {
			return err
		}
		input.LegacyTargetTgID = &id
	}
	if in.BeforeSourceId != nil {
		input.BeforeSourceID, err = parseTelegramID(*in.BeforeSourceId)
		if err != nil {
			return err
		}
	}
	page, err := a.auditReports.History(c.Request().Context(), actor.Account.AccountId, input)
	if err != nil {
		var fault *auditreports.Error
		if errors.As(err, &fault) {
			return failure(fault.Status, fault.Code)
		}
		return accountError(err)
	}
	out := wire.AuditHistory{Version: wire.AuditHistoryVersion(page.Version), Kind: wire.AuditHistoryKind(page.Kind), AccountId: page.AccountID,
		LegacyTargetTgId: auditDecimal(page.LegacyTargetTgID), NativeEvents: []wire.NativeAuditHistoryEvent{}, LegacyEvents: []wire.LegacyAuditHistoryEvent{}, SystemEvents: []wire.SystemAuditHistoryEvent{}, HasMore: page.HasMore}
	for _, row := range page.Native {
		out.NativeEvents = append(out.NativeEvents, wire.NativeAuditHistoryEvent{AccountId: row.AccountID, Event: operatorAudit(row)})
	}
	for _, row := range page.Legacy {
		out.LegacyEvents = append(out.LegacyEvents, wire.LegacyAuditHistoryEvent{SourceId: strconv.FormatInt(row.SourceID, 10), CreatedAt: row.CreatedAt, Action: row.Action, TargetTgId: auditDecimal(row.TargetTgID), ActorId: auditDecimal(row.ActorID), ActorType: row.ActorType, ActorName: row.ActorName, Source: row.Source, AccountId: row.AccountID})
	}
	for _, row := range page.System {
		event := wire.SystemAuditHistoryEvent{Id: row.ID, CreatedAt: row.CreatedAt, Action: wire.SystemAuditHistoryEventAction(row.Action), PeriodDay: row.PeriodDay, Cutoff: row.Cutoff, RetentionDays: row.RetentionDays, NativeCount: row.NativeCount, LegacyCount: row.LegacyCount, SystemCount: row.SystemCount}
		if row.SupportTelegram != nil {
			source := row.SupportTelegram
			event.SupportTelegram = &wire.SupportTelegramAudit{BotId: strconv.FormatInt(source.BotID, 10), GroupId: strconv.FormatInt(source.GroupID, 10), ChatId: strconv.FormatInt(source.ChatID, 10), ThreadId: auditDecimal(source.ThreadID),
				ActorTgId: auditDecimal(source.ActorTgID), ActorAccountId: source.ActorAccountID, TargetAccountId: source.TargetAccountID, TopicId: source.TopicID, ReceiptId: source.ReceiptID,
				Kind: wire.SupportTelegramAuditKind(source.Kind), Outcome: wire.SupportTelegramAuditOutcome(source.Outcome), Reason: source.Reason, SourceId: auditDecimal(source.SourceID)}
			if source.SourceID == nil {
				event.SupportTelegram.MessageId = auditDecimal(&source.MessageID)
				event.SupportTelegram.UpdateId = auditDecimal(&source.UpdateID)
			}
		}
		out.SystemEvents = append(out.SystemEvents, event)
	}
	return c.JSON(200, out)
}

func auditDecimal(value *int64) *string {
	if value == nil {
		return nil
	}
	text := strconv.FormatInt(*value, 10)
	return &text
}
