package auditreports

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/audit_reports/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type SupportTelegramEvent struct {
	ID              uuid.UUID  `json:"-"`
	BotID           int64      `json:"bot_id"`
	GroupID         int64      `json:"group_id"`
	ChatID          int64      `json:"chat_id"`
	MessageID       int64      `json:"message_id"`
	UpdateID        int64      `json:"update_id"`
	ThreadID        *int64     `json:"thread_id"`
	ActorTgID       *int64     `json:"actor_tg_id"`
	ActorAccountID  *uuid.UUID `json:"actor_account_id"`
	TargetAccountID *uuid.UUID `json:"target_account_id"`
	TopicID         *uuid.UUID `json:"topic_id"`
	ReceiptID       *uuid.UUID `json:"receipt_id"`
	Kind            string     `json:"kind"`
	Outcome         string     `json:"outcome"`
}

func RecordSupportTelegramTx(ctx context.Context, tx pgx.Tx, event SupportTelegramEvent) error {
	if event.ID == uuid.Nil || event.BotID <= 0 || event.GroupID >= 0 || event.ChatID == 0 || event.MessageID <= 0 || event.UpdateID < 0 || event.ThreadID != nil && *event.ThreadID <= 1 || event.ActorTgID != nil && *event.ActorTgID <= 0 || event.ActorAccountID != nil && event.ActorTgID == nil {
		return &Error{400, "INVALID_INPUT"}
	}
	for _, id := range []*uuid.UUID{event.ActorAccountID, event.TargetAccountID, event.TopicID, event.ReceiptID} {
		if id != nil && *id == uuid.Nil {
			return &Error{400, "INVALID_INPUT"}
		}
	}
	switch event.Kind {
	case "message", "topic_closed", "topic_reopened", "command", "delivery", "topic_bound", "legacy_imported", "guest_banned", "guest_unbanned":
	default:
		return &Error{400, "INVALID_INPUT"}
	}
	switch event.Outcome {
	case "observed", "requested", "completed", "queued", "sent", "failed", "unknown", "skipped":
	default:
		return &Error{400, "INVALID_INPUT"}
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return &Error{400, "INVALID_INPUT"}
	}
	return store.New(tx).InsertSupportTelegramAudit(ctx, store.InsertSupportTelegramAuditParams{ID: event.ID, SupportTelegram: raw})
}

type telegramActorKey struct{}
type telegramActor struct {
	account  uuid.UUID
	telegram int64
}

// Metadata only. The business owner must authorize the actor in its caller Tx.
func WithTelegramActor(ctx context.Context, account uuid.UUID, tg int64) context.Context {
	if account == uuid.Nil || tg <= 0 || tg > 1<<52-1 {
		return ctx
	}
	return context.WithValue(ctx, telegramActorKey{}, telegramActor{account, tg})
}
