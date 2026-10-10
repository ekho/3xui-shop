package vpn

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// BonusHooks keeps source proof and applied facts with their owner, in the same access transaction.
type BonusHooks struct {
	Check   func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (string, error)
	Applied func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error
}

func (s *Service) ConfigureBonusHooks(hooks BonusHooks) { s.bonus = hooks }

func (s *Service) checkBonus(ctx context.Context, tx pgx.Tx, account, operation uuid.UUID) string {
	if s.bonus.Check == nil {
		return ""
	}
	reason, err := s.bonus.Check(ctx, tx, account, operation)
	if err != nil {
		return "bonus_source_unavailable"
	}
	return reason
}
