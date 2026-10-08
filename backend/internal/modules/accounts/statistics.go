package accounts

import (
	"context"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"github.com/jackc/pgx/v5"
)

func (s *Service) StatisticsTx(ctx context.Context, tx pgx.Tx) ([]auditreports.StatisticsAccount, error) {
	rows, err := tx.Query(ctx, `SELECT id,COALESCE(access_profile,''),vpn_banned FROM accounts ORDER BY id`)
	if err != nil {
		return nil, unavailable()
	}
	defer rows.Close()
	out := []auditreports.StatisticsAccount{}
	for rows.Next() {
		var a auditreports.StatisticsAccount
		if rows.Scan(&a.ID, &a.AccessProfile, &a.VpnBanned) != nil {
			return nil, unavailable()
		}
		out = append(out, a)
	}
	if rows.Err() != nil {
		return nil, unavailable()
	}
	return out, nil
}
