package platform

import (
	"context"
	"time"

	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (s *Service) Subscription(ctx context.Context, account uuid.UUID) (out wire.Subscription, retErr error) {
	v, err := s.subscriptions.Subscription(ctx, account)
	return toSubscriptionSubscription(v), subscriptionError(err)
}
func (s *Service) SubscriptionKey(ctx context.Context, account uuid.UUID) (wire.SubscriptionKey, error) {
	v, err := s.subscriptions.SubscriptionKey(ctx, account)
	return toSubscriptionSubscriptionKey(v), subscriptionError(err)
}

type profileSnapshot = vpn.ProfileSnapshot

func observeProfileTraffic(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, up, down int64, at time.Time, snapshot profileSnapshot) error {
	return vpn.ObserveProfileTraffic(ctx, pool, id, up, down, at, snapshot)
}
