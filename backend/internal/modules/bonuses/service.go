package bonuses

import (
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool      *pgxpool.Pool
	authority *accounts.Service
	now       func() time.Time
}

func New(pool *pgxpool.Pool, authority *accounts.Service, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, authority: authority, now: now}
}
