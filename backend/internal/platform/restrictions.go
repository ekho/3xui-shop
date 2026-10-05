package platform

import (
	"context"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func (s *Service) SetOperatorRestriction(ctx context.Context, actor, target, key uuid.UUID, in wire.OperatorRestrictionInput) (wire.OperatorRestrictionResult, error) {
	out, err := s.accounts.SetOperatorRestriction(ctx, actor, target, key, accounts.OperatorRestrictionInput(in))
	return wire.OperatorRestrictionResult(out), accountError(err)
}
