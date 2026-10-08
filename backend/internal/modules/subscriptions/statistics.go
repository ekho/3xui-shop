package subscriptions

import (
	"context"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type TrialStatistics = auditreports.TrialStatistics

// StatisticsTx reads delivered grants in the caller's consistent snapshot.
func (s *Service) StatisticsTx(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (TrialStatistics, error) {
	var out TrialStatistics
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM trial_grants WHERE account_id=ANY($1::uuid[]) AND status='granted'`, ids).Scan(&out.TrialUsers); err != nil {
		return out, unavailable()
	}
	return out, nil
}
