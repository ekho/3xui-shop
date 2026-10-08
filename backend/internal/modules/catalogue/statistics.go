package catalogue

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// Current stored references include archived and hidden plans.
func (s *Service) StatisticsTx(ctx context.Context, tx pgx.Tx) (map[string]int64, error) {
	rows, err := tx.Query(ctx, `SELECT current_profile,count(*) FROM catalogue_plans GROUP BY current_profile`)
	if err != nil {
		return nil, unavailable()
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var profile string
		var count int64
		if rows.Scan(&profile, &count) != nil {
			return nil, unavailable()
		}
		out[profile] = count
	}
	if rows.Err() != nil {
		return nil, unavailable()
	}
	return out, nil
}
