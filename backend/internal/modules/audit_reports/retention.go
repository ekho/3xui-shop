package auditreports

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/audit_reports/internal/store"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

type Config struct {
	RetentionDays int
	Timezone      *time.Location
}

func (s *Service) Prune(ctx context.Context) (SystemEvent, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SystemEvent{}, false, unavailable()
	}
	defer tx.Rollback(ctx)
	out, changed, err := s.pruneTx(ctx, tx)
	if err != nil {
		return out, false, err
	}
	if tx.Commit(ctx) != nil {
		return SystemEvent{}, false, unavailable()
	}
	return out, changed, nil
}

func (s *Service) pruneTx(ctx context.Context, tx pgx.Tx) (SystemEvent, bool, error) {
	if s.config.RetentionDays < 1 || s.config.RetentionDays > 3650 {
		return SystemEvent{}, false, &Error{400, "INVALID_INPUT"}
	}
	q := store.New(tx)
	if q.LockAuditPrune(ctx) != nil {
		return SystemEvent{}, false, unavailable()
	}
	now, err := q.AuditDatabaseTime(ctx)
	if err != nil || !now.Valid {
		return SystemEvent{}, false, unavailable()
	}
	local := now.Time.In(s.config.Timezone)
	if local.Hour() < 3 || local.Hour() == 3 && local.Minute() < 30 {
		return SystemEvent{}, false, nil
	}
	day := pgtype.Date{Time: time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
	done, err := q.AuditDayPruned(ctx, store.AuditDayPrunedParams{PeriodDay: day, Timezone: s.config.Timezone.String()})
	if err != nil {
		return SystemEvent{}, false, unavailable()
	}
	if done {
		return SystemEvent{}, false, nil
	}
	cutoff := pgtype.Timestamptz{Time: now.Time.UTC().Add(-time.Duration(s.config.RetentionDays) * 24 * time.Hour), Valid: true}
	native, err := q.PruneNativeAudit(ctx, cutoff)
	if err != nil {
		return SystemEvent{}, false, unavailable()
	}
	legacy, err := q.PruneLegacyAudit(ctx, cutoff)
	if err != nil {
		return SystemEvent{}, false, unavailable()
	}
	system, err := q.PruneSystemAudit(ctx, cutoff)
	if err != nil {
		return SystemEvent{}, false, unavailable()
	}
	row := store.AuditSystemEvent{ID: uuid.New(), CreatedAt: now, Action: "audit.pruned", PeriodDay: day, Cutoff: cutoff, RetentionDays: pgtype.Int4{Int32: int32(s.config.RetentionDays), Valid: true}, NativeCount: native, LegacyCount: legacy, SystemCount: system}
	if q.InsertSystemAudit(ctx, store.InsertSystemAuditParams{ID: row.ID, CreatedAt: row.CreatedAt, Action: row.Action, PeriodDay: row.PeriodDay, Cutoff: row.Cutoff, RetentionDays: row.RetentionDays, NativeCount: native, LegacyCount: legacy, SystemCount: system}) != nil {
		return SystemEvent{}, false, unavailable()
	}
	return systemEvent(row), true, nil
}

func (s *Service) muteMirror(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if q.MuteNativeAuditMirror(ctx) != nil || q.MuteSystemAuditMirror(ctx) != nil || tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}

func (s *Service) claimMirror(ctx context.Context) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	row, err := q.ClaimNativeAuditMirror(ctx)
	text := ""
	if errors.Is(err, pgx.ErrNoRows) {
		system, err := q.ClaimSystemAuditMirror(ctx)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", unavailable()
		}
		if err == nil {
			text = fmt.Sprintf("action=%s\nid=%s\ncreated_at=%s\nnative_count=%d\nlegacy_count=%d\nsystem_count=%d", system.Action, system.ID, system.CreatedAt.Time.UTC().Format(time.RFC3339Nano), system.NativeCount, system.LegacyCount, system.SystemCount)
			if system.PeriodDay.Valid {
				text += "\nperiod_day=" + system.PeriodDay.Time.Format("2006-01-02")
			}
			if system.Cutoff.Valid {
				text += "\ncutoff=" + system.Cutoff.Time.UTC().Format(time.RFC3339Nano)
			}
			if system.RetentionDays.Valid {
				text += fmt.Sprintf("\nretention_days=%d", system.RetentionDays.Int32)
			}
		}
	} else if err != nil {
		return "", unavailable()
	} else if mirrorAction.MatchString(row.Action) {
		text = fmt.Sprintf("action=%s\nid=%s\naccount_id=%s\ncreated_at=%s", row.Action, row.ID, row.AccountID, row.CreatedAt.Time.UTC().Format(time.RFC3339Nano))
		for _, ref := range []struct {
			name string
			id   *uuid.UUID
		}{{"request_id", row.RequestID}, {"operation_id", row.OperationID}, {"operator_account_id", row.OperatorAccountID}, {"support_message_id", row.SupportMessageID}, {"access_operation_id", row.AccessOperationID}} {
			if ref.id != nil {
				text += "\n" + ref.name + "=" + ref.id.String()
			}
		}
		if row.OperatorTgID.Valid {
			text += fmt.Sprintf("\noperator_tg_id=%d", row.OperatorTgID.Int64)
		}
		if row.SystemActor.Valid {
			text += fmt.Sprintf("\nsystem_actor=%t", row.SystemActor.Bool)
		}
		if row.MonthlyPeriod.Valid && mirrorPeriod.MatchString(row.MonthlyPeriod.String) {
			text += "\nmonthly_period=" + row.MonthlyPeriod.String
		}
	}
	if tx.Commit(ctx) != nil {
		return "", unavailable()
	}
	return text, nil
}

var mirrorAction = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
var mirrorPeriod = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}$`)

func (s *Service) RunScheduler(ctx context.Context, mirror func(context.Context, string) error) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	ready := false
	nextMaintenance := time.Time{}
	for {
		if ctx.Err() != nil {
			return nil
		}
		if !time.Now().Before(nextMaintenance) {
			var err error
			if !ready {
				err = s.muteMirror(ctx)
				ready = err == nil
			}
			if ready {
				_, _, err = s.Prune(ctx)
			}
			if err != nil && ctx.Err() == nil {
				slog.Warn("audit maintenance unavailable", "code", "SERVICE_UNAVAILABLE")
			}
			nextMaintenance = time.Now().Add(time.Minute)
		}
		// ponytail: one best-effort attempt/sec; batch only for an observed backlog.
		if ready && mirror != nil {
			text, err := s.claimMirror(ctx)
			if err == nil && strings.TrimSpace(text) != "" {
				_ = mirror(ctx, text)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
