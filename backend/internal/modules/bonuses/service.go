package bonuses

import (
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"time"
)

type Service struct {
	pool          *pgxpool.Pool
	authority     *accounts.Service
	now           func() time.Time
	subscriptions *subscriptions.Service
	payments      *payments.Service
	queue         func() *river.Client[pgx.Tx]
	rewardConfig  func() RewardConfig
}

func (s *Service) ConfigureSubscriptions(owner *subscriptions.Service) { s.subscriptions = owner }

func New(pool *pgxpool.Pool, authority *accounts.Service, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, authority: authority, now: now}
}
