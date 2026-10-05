package platform

import (
	"context"

	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func (s *Service) CreateTrialRequest(ctx context.Context, accountID, key uuid.UUID, in wire.TrialRequestInput) (wire.TrialRequest, bool, error) {
	v, created, err := s.subscriptions.CreateTrialRequest(ctx, accountID, key, fromSubscriptionTrialRequestInput(in))
	return toSubscriptionTrialRequest(v), created, subscriptionError(err)
}
func (s *Service) CurrentTrialRequest(ctx context.Context, accountID uuid.UUID) (wire.CurrentTrialRequest, error) {
	v, err := s.subscriptions.CurrentTrialRequest(ctx, accountID)
	return toSubscriptionCurrentTrialRequest(v), subscriptionError(err)
}
func (s *Service) DecideTrialRequest(ctx context.Context, id uuid.UUID, in wire.DecisionInput) (wire.DecisionResult, error) {
	v, err := s.subscriptions.DecideTrialRequest(ctx, id, fromSubscriptionDecisionInput(in))
	return toSubscriptionDecisionResult(v), subscriptionError(err)
}
func (s *Service) ReconsiderTrialRequest(ctx context.Context, id, key uuid.UUID, in wire.ReconsiderInput) (wire.TrialRequest, error) {
	v, err := s.subscriptions.ReconsiderTrialRequest(ctx, id, key, fromSubscriptionReconsiderInput(in))
	return toSubscriptionTrialRequest(v), subscriptionError(err)
}
