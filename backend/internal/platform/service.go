package platform

import (
	"context"
	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"time"
)

type Error struct {
	Status        int
	Code, Message string
	Details       map[string]any
	RetryAfter    int
}

func (e *Error) Error() string              { return e.Code }
func failure(status int, code string) error { return &Error{Status: status, Code: code, Message: code} }
func unavailable() error                    { return failure(503, "SERVICE_UNAVAILABLE") }

type Service struct {
	pool     *pgxpool.Pool
	limiter  *redis.Client
	queue    *river.Client[pgx.Tx]
	cfg      Config
	now      func() time.Time
	accounts *accounts.Service
}

func NewService(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg Config) *Service {
	s := NewServiceWithAccounts(pool, limiter, queue, cfg, nil)
	s.accounts = accounts.New(pool, limiter, queue, accounts.Config{CabinetOrigin: cfg.CabinetOrigin, TermsVersion: cfg.TermsVersion, PrivacyVersion: cfg.PrivacyVersion, RateNamespace: cfg.RateNamespace, MailKey: cfg.MailKey, CodeKey: cfg.CodeKey, Operators: cfg.Operators, Now: func() time.Time { return s.now() }}, s.smtpSend)
	return s
}

func NewServiceWithAccounts(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg Config, owner *accounts.Service) *Service {
	return &Service{pool: pool, limiter: limiter, queue: queue, cfg: cfg, now: time.Now, accounts: owner}
}

type MailArgs = accounts.MailArgs

func (s *Service) Health(ctx context.Context) bool { return s.pool.Ping(ctx) == nil }
