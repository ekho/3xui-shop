package subscriptions

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type TrialStatistics struct {
	TrialUsers int64 `json:"trial_users"`
}

// StatisticsTx reads delivered grants in the caller's consistent snapshot.
func (s *Service) StatisticsTx(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (TrialStatistics, error) {
	var out TrialStatistics
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM trial_grants WHERE account_id=ANY($1::uuid[]) AND status='granted'`, ids).Scan(&out.TrialUsers); err != nil {
		return out, unavailable()
	}
	return out, nil
}
