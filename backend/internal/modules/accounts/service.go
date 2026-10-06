package accounts

import (
	"errors"
	"example.com/cabinet/backend/internal/modules/notifications"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"time"
)

type Config struct {
	TermsVersion, PrivacyVersion, RateNamespace string
	CodeKey                                     []byte
	Operators                                   []int64
	Now                                         func() time.Time
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
	mail      *notifications.MailService
	cfg       Config
	now       func() time.Time
	hashSlots chan struct{}
}

func New(pool *pgxpool.Pool, limiter *redis.Client, mail *notifications.MailService, cfg Config) *Service {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, limiter: limiter, mail: mail, cfg: cfg, now: now, hashSlots: make(chan struct{}, 2)}
}
