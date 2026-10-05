package accounts

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"time"
)

type Config struct {
	CabinetOrigin, TermsVersion, PrivacyVersion, RateNamespace string
	MailKey, CodeKey                                           []byte
	Operators                                                  []int64
	Now                                                        func() time.Time
}

// Error carries safe domain failure data; transports translate the status hint.
type Error struct {
	Status        int
	Code, Message string
	Details       map[string]any
	RetryAfter    int
}

func (e *Error) Error() string              { return e.Code }
func failure(status int, code string) error { return &Error{Status: status, Code: code, Message: code} }
func unavailable() error                    { return failure(503, "SERVICE_UNAVAILABLE") }

var ErrNotFound = errors.New("account not found")

type Service struct {
	pool      *pgxpool.Pool
	limiter   *redis.Client
	queue     *river.Client[pgx.Tx]
	cfg       Config
	now       func() time.Time
	hashSlots chan struct{}
	sender    func(context.Context, string, string, string) error
}

func New(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg Config, sender func(context.Context, string, string, string) error) *Service {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, limiter: limiter, queue: queue, cfg: cfg, now: now, hashSlots: make(chan struct{}, 2), sender: sender}
}

type MailArgs struct {
	DeliveryID uuid.UUID `json:"delivery_id"`
}

func (MailArgs) Kind() string { return "mail_delivery" }
