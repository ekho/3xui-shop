package s01

import (
	"context"
	"github.com/google/uuid"
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
	pool      *pgxpool.Pool
	limiter   *redis.Client
	queue     *river.Client[pgx.Tx]
	cfg       Config
	now       func() time.Time
	hashSlots chan struct{}
}

func NewService(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg Config) *Service {
	return &Service{pool: pool, limiter: limiter, queue: queue, cfg: cfg, now: time.Now, hashSlots: make(chan struct{}, 2)}
}

type MailArgs struct {
	DeliveryID uuid.UUID `json:"delivery_id"`
}

func (MailArgs) Kind() string { return "s01_mail" }

func (s *Service) Health(ctx context.Context) bool { return s.pool.Ping(ctx) == nil }
