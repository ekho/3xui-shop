package platform

import (
	"context"

	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func (s *Service) CreateAccessOperation(ctx context.Context, actor, target, key uuid.UUID, in wire.AccessOperationInput) (wire.AccessOperation, error) {
	v, err := s.subscriptions.CreateAccessOperation(ctx, actor, target, key, fromSubscriptionAccessOperationInput(in))
	return toSubscriptionAccessOperation(v), subscriptionError(err)
}
func (s *Service) GetAccessOperation(ctx context.Context, actor, target, id uuid.UUID) (wire.AccessOperation, error) {
	v, err := s.subscriptions.GetAccessOperation(ctx, actor, target, id)
	return toSubscriptionAccessOperation(v), subscriptionError(err)
}
func (s *Service) ReconcileAccessOperation(ctx context.Context, actor, target, id, key uuid.UUID, in wire.AccessReconcileInput) (wire.AccessOperation, error) {
	v, err := s.subscriptions.ReconcileAccessOperation(ctx, actor, target, id, key, fromSubscriptionAccessReconcileInput(in))
	return toSubscriptionAccessOperation(v), subscriptionError(err)
}
