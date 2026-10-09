package accounts

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Legacy audit targets follow the saved import proof, never a current binding.
func (s *Service) LegacyAuditTargetTx(ctx context.Context, tx pgx.Tx, account uuid.UUID) (*int64, error) {
	row, err := store.New(tx).LegacyApprovalByAccount(ctx, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row.SourceTgID, nil
}

func (s *Service) LegacyAuditLinksTx(ctx context.Context, tx pgx.Tx, targets []int64) (map[int64]uuid.UUID, error) {
	rows, err := store.New(tx).LegacyAuditLinks(ctx, targets)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]uuid.UUID, len(rows))
	for _, row := range rows {
		out[row.SourceTgID] = row.AccountID
	}
	return out, nil
}
