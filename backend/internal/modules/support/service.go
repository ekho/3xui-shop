package support

import (
	"context"
	"errors"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Error struct {
	Status        int
	Code, Message string
	RetryAfter    int
}

func (e *Error) Error() string              { return e.Code }
func failure(status int, code string) error { return &Error{Status: status, Code: code, Message: code} }
func unavailable() error                    { return failure(503, "SERVICE_UNAVAILABLE") }

type Service struct {
	pool                           *pgxpool.Pool
	notifications                  *notifications.Service
	limiter                        *redis.Client
	authority                      *accounts.Service
	rateNamespace                  string
	now                            func() time.Time
	telegramBotID, telegramGroupID int64
}

func New(pool *pgxpool.Pool, limiter *redis.Client, authority *accounts.Service, rateNamespace string, now func() time.Time, notices *notifications.Service) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, notifications: notices, limiter: limiter, authority: authority, rateNamespace: rateNamespace, now: now}
}

func accountResult(a accounts.Snapshot, err error) (accounts.Snapshot, error) {
	if errors.Is(err, accounts.ErrNotFound) {
		return a, pgx.ErrNoRows
	}
	return a, err
}
func (s *Service) accountByID(ctx context.Context, id uuid.UUID) (accounts.Snapshot, error) {
	return accountResult(s.authority.Lookup(ctx, id))
}
func (s *Service) lockAccount(ctx context.Context, tx pgx.Tx, id uuid.UUID) (accounts.Snapshot, error) {
	return accountResult(s.authority.Lock(ctx, tx, id))
}
