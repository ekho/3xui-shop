package auditreports

import (
	"context"
	"strings"
	"time"

	"example.com/cabinet/backend/internal/modules/audit_reports/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type HistoryPorts struct {
	LockOperatorTx  func(context.Context, pgx.Tx, uuid.UUID) (bool, error)
	AccountExistsTx func(context.Context, pgx.Tx, uuid.UUID) (bool, error)
	LegacyTargetTx  func(context.Context, pgx.Tx, uuid.UUID) (*int64, error)
	LegacyLinksTx   func(context.Context, pgx.Tx, []int64) (map[int64]uuid.UUID, error)
}
type HistoryInput struct {
	Kind             string
	AccountID        *uuid.UUID
	LegacyTargetTgID *int64
	BeforeCreatedAt  *time.Time
	BeforeID         uuid.UUID
	BeforeSourceID   int64
}
type LegacyEvent struct {
	SourceID                     int64
	CreatedAt                    time.Time
	Action                       string
	TargetTgID, ActorID          *int64
	ActorType, ActorName, Source *string
	AccountID                    *uuid.UUID
}
type SystemEvent struct {
	ID                                    uuid.UUID
	CreatedAt                             time.Time
	Action                                string
	PeriodDay                             *string
	Cutoff                                *time.Time
	RetentionDays                         *int32
	NativeCount, LegacyCount, SystemCount int64
}
type HistoryPage struct {
	Version, Kind    string
	AccountID        *uuid.UUID
	LegacyTargetTgID *int64
	Native           []Event
	Legacy           []LegacyEvent
	System           []SystemEvent
	HasMore          bool
}

func (s *Service) History(ctx context.Context, actor uuid.UUID, in HistoryInput) (HistoryPage, error) {
	out := HistoryPage{Version: "audit-history-v1", Kind: in.Kind, AccountID: in.AccountID, LegacyTargetTgID: in.LegacyTargetTgID, Native: []Event{}, Legacy: []LegacyEvent{}, System: []SystemEvent{}}
	if !validHistoryInput(in) {
		return out, &Error{400, "INVALID_INPUT"}
	}
	if s.history.LockOperatorTx == nil {
		return out, unavailable()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	allowed, err := s.history.LockOperatorTx(ctx, tx, actor)
	if err != nil {
		return out, unavailable()
	}
	if !allowed {
		return out, &Error{403, "INVALID_CREDENTIALS"}
	}
	if in.AccountID != nil {
		if s.history.AccountExistsTx == nil {
			return out, unavailable()
		}
		exists, err := s.history.AccountExistsTx(ctx, tx, *in.AccountID)
		if err != nil {
			return out, unavailable()
		}
		if !exists {
			return out, &Error{404, "INVALID_INPUT"}
		}
	}
	before := pgtype.Timestamptz{}
	if in.BeforeCreatedAt != nil {
		before = pgtype.Timestamptz{Time: *in.BeforeCreatedAt, Valid: true}
	}
	q := store.New(tx)
	switch in.Kind {
	case "native":
		rows, err := q.OperatorAuditPage(ctx, store.OperatorAuditPageParams{AccountID: in.AccountID, BeforeCreatedAt: before, BeforeID: in.BeforeID})
		if err != nil {
			return out, unavailable()
		}
		out.HasMore = len(rows) > 50
		if out.HasMore {
			rows = rows[:50]
		}
		for _, row := range rows {
			out.Native = append(out.Native, auditEvent(row))
		}
	case "legacy":
		target := in.LegacyTargetTgID
		if in.AccountID != nil {
			if s.history.LegacyTargetTx == nil {
				return out, unavailable()
			}
			target, err = s.history.LegacyTargetTx(ctx, tx, *in.AccountID)
			if err != nil {
				return out, unavailable()
			}
			// Absence of immutable proof is an empty client history, never a global read.
			if target == nil {
				break
			}
		}
		rows, err := q.LegacyAuditPage(ctx, store.LegacyAuditPageParams{TargetTgID: intValue(target), BeforeCreatedAt: before, BeforeSourceID: in.BeforeSourceID})
		if err != nil {
			return out, unavailable()
		}
		out.HasMore = len(rows) > 50
		if out.HasMore {
			rows = rows[:50]
		}
		targets := make([]int64, 0, len(rows))
		for _, row := range rows {
			if row.TargetTgID.Valid {
				targets = append(targets, row.TargetTgID.Int64)
			}
		}
		links := map[int64]uuid.UUID{}
		if len(targets) > 0 {
			if s.history.LegacyLinksTx == nil {
				return out, unavailable()
			}
			links, err = s.history.LegacyLinksTx(ctx, tx, targets)
			if err != nil {
				return out, unavailable()
			}
		}
		for _, row := range rows {
			event := LegacyEvent{SourceID: row.SourceID, CreatedAt: row.CreatedAt.Time, Action: row.Action, TargetTgID: intPointer(row.TargetTgID), ActorID: intPointer(row.ActorID), ActorType: textPointer(row.ActorType), ActorName: textPointer(row.ActorName), Source: textPointer(row.Source)}
			if id, ok := links[row.TargetTgID.Int64]; row.TargetTgID.Valid && ok {
				event.AccountID = &id
			}
			out.Legacy = append(out.Legacy, event)
		}
	case "system":
		rows, err := q.SystemAuditPage(ctx, store.SystemAuditPageParams{BeforeCreatedAt: before, BeforeID: in.BeforeID})
		if err != nil {
			return out, unavailable()
		}
		out.HasMore = len(rows) > 50
		if out.HasMore {
			rows = rows[:50]
		}
		for _, row := range rows {
			out.System = append(out.System, systemEvent(row))
		}
	}
	if tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	return out, nil
}

func validHistoryInput(in HistoryInput) bool {
	if in.AccountID != nil && *in.AccountID == uuid.Nil || in.BeforeCreatedAt != nil && !validAuditTime(*in.BeforeCreatedAt) {
		return false
	}
	switch in.Kind {
	case "native", "system":
		return in.LegacyTargetTgID == nil && in.BeforeSourceID == 0 && (in.BeforeCreatedAt == nil) == (in.BeforeID == uuid.Nil) && (in.Kind != "system" || in.AccountID == nil)
	case "legacy":
		return in.BeforeID == uuid.Nil && in.BeforeSourceID >= 0 && (in.BeforeCreatedAt == nil) == (in.BeforeSourceID == 0) && (in.LegacyTargetTgID == nil || *in.LegacyTargetTgID > 0 && in.AccountID == nil)
	}
	return false
}

// ParseTimestamp checks the original fraction before Go can truncate it.
func ParseTimestamp(raw string) (time.Time, error) {
	if dot := strings.IndexAny(raw, ".,"); dot >= 0 {
		for i := dot + 1; i < len(raw) && raw[i] >= '0' && raw[i] <= '9'; i++ {
			if i-dot > 6 && raw[i] != '0' {
				return time.Time{}, &Error{400, "INVALID_INPUT"}
			}
		}
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || !validAuditTime(t) {
		return time.Time{}, &Error{400, "INVALID_INPUT"}
	}
	return t, nil
}

func validAuditTime(t time.Time) bool {
	return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 && t.UTC().Year() >= 1 && t.UTC().Year() <= 9999 && t.Nanosecond()%1000 == 0
}
func intPointer(v pgtype.Int8) *int64 {
	if v.Valid {
		return &v.Int64
	}
	return nil
}
func textPointer(v pgtype.Text) *string {
	if v.Valid {
		return &v.String
	}
	return nil
}
func intValue(v *int64) pgtype.Int8 {
	if v != nil {
		return pgtype.Int8{Int64: *v, Valid: true}
	}
	return pgtype.Int8{}
}
func textValue(v *string) pgtype.Text {
	if v != nil {
		return pgtype.Text{String: *v, Valid: true}
	}
	return pgtype.Text{}
}
func systemEvent(row store.AuditSystemEvent) SystemEvent {
	out := SystemEvent{ID: row.ID, CreatedAt: row.CreatedAt.Time, Action: row.Action, NativeCount: row.NativeCount, LegacyCount: row.LegacyCount, SystemCount: row.SystemCount}
	if row.PeriodDay.Valid {
		day := row.PeriodDay.Time.Format("2006-01-02")
		out.PeriodDay = &day
	}
	if row.Cutoff.Valid {
		out.Cutoff = &row.Cutoff.Time
	}
	if row.RetentionDays.Valid {
		out.RetentionDays = &row.RetentionDays.Int32
	}
	return out
}
