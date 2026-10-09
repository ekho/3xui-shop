package auditreports

import (
	"context"
	"time"

	"example.com/cabinet/backend/internal/modules/audit_reports/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Event struct {
	ID, AccountID                             uuid.UUID
	CreatedAt                                 time.Time
	Action                                    string
	RequestID, OperationID, OperatorAccountID *uuid.UUID
	SupportMessageID, AccessOperationID       *uuid.UUID
	OperatorTgID                              *int64
	Reason, MonthlyPeriod                     *string
	SystemActor                               *bool
}

// RecordTx keeps the event in the caller's transaction, including its errors.
func RecordTx(ctx context.Context, tx pgx.Tx, event Event) error {
	row := store.RecordAuditParams{ID: event.ID, AccountID: event.AccountID,
		CreatedAt: pgtype.Timestamptz{Time: event.CreatedAt, Valid: true}, Action: event.Action,
		RequestID: event.RequestID, OperationID: event.OperationID, OperatorAccountID: event.OperatorAccountID,
		SupportMessageID: event.SupportMessageID, AccessOperationID: event.AccessOperationID}
	if source, ok := ctx.Value(telegramActorKey{}).(telegramActor); ok && event.OperatorAccountID != nil && *event.OperatorAccountID == source.account && event.OperatorTgID == nil {
		row.OperatorTgID = pgtype.Int8{Int64: source.telegram, Valid: true}
		row.OperatorSource = pgtype.Text{String: "telegram_support", Valid: true}
	}
	if event.OperatorTgID != nil {
		row.OperatorTgID = pgtype.Int8{Int64: *event.OperatorTgID, Valid: true}
	}
	if event.Reason != nil {
		row.Reason = pgtype.Text{String: *event.Reason, Valid: true}
	}
	if event.SystemActor != nil {
		row.SystemActor = pgtype.Bool{Bool: *event.SystemActor, Valid: true}
	}
	if event.MonthlyPeriod != nil {
		row.MonthlyPeriod = pgtype.Text{String: *event.MonthlyPeriod, Valid: true}
	}
	return store.New(tx).RecordAudit(ctx, row)
}

type Service struct {
	pool       *pgxpool.Pool
	statistics StatisticsPorts
	history    HistoryPorts
	config     Config
}

func New(pool *pgxpool.Pool, statistics StatisticsPorts, history HistoryPorts, config Config) *Service {
	if config.RetentionDays == 0 {
		config.RetentionDays = 365
	}
	if config.Timezone == nil {
		config.Timezone = time.UTC
	}
	return &Service{pool: pool, statistics: statistics, history: history, config: config}
}

// Page is an internal read port; the operator consumer authorizes the request.
func (s *Service) Page(ctx context.Context, account uuid.UUID, before *time.Time, beforeID uuid.UUID) ([]Event, bool, error) {
	var cursor pgtype.Timestamptz
	if before != nil {
		cursor = pgtype.Timestamptz{Time: *before, Valid: true}
	}
	rows, err := store.New(s.pool).AuditPage(ctx, store.AuditPageParams{AccountID: account, BeforeCreatedAt: cursor, BeforeID: beforeID})
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > 50
	if more {
		rows = rows[:50]
	}
	out := make([]Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, auditEvent(row))
	}
	return out, more, nil
}

func auditEvent(row store.AuditEvent) Event {
	event := Event{ID: row.ID, AccountID: row.AccountID, CreatedAt: row.CreatedAt.Time, Action: row.Action,
		RequestID: row.RequestID, OperationID: row.OperationID, OperatorAccountID: row.OperatorAccountID,
		SupportMessageID: row.SupportMessageID, AccessOperationID: row.AccessOperationID}
	if row.OperatorTgID.Valid {
		event.OperatorTgID = &row.OperatorTgID.Int64
	}
	if row.Reason.Valid {
		event.Reason = &row.Reason.String
	}
	if row.SystemActor.Valid {
		event.SystemActor = &row.SystemActor.Bool
	}
	if row.MonthlyPeriod.Valid {
		event.MonthlyPeriod = &row.MonthlyPeriod.String
	}
	return event
}
