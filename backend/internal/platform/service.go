package platform

import (
	"context"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
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
	pool          *pgxpool.Pool
	limiter       *redis.Client
	queue         *river.Client[pgx.Tx]
	cfg           Config
	now           func() time.Time
	accounts      *accounts.Service
	catalogue     *catalogue.Service
	subscriptions *subscriptions.Service
	vpn           *vpn.Service
	payments      *payments.Service
	support       *support.Service
	notifications *notifications.Service
}

func NewService(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg Config) *Service {
	s := NewServiceWithModules(pool, limiter, queue, cfg, nil, nil, nil, nil, nil, nil, nil)
	s.accounts = accounts.New(pool, limiter, queue, accounts.Config{CabinetOrigin: cfg.CabinetOrigin, TermsVersion: cfg.TermsVersion, PrivacyVersion: cfg.PrivacyVersion, RateNamespace: cfg.RateNamespace, MailKey: cfg.MailKey, CodeKey: cfg.CodeKey, Operators: cfg.Operators, Now: func() time.Time { return s.now() }}, s.smtpSend)
	s.catalogue = catalogue.New(pool, s.accounts, func() time.Time { return s.now() })
	s.connectSubscriptions()
	s.payments = payments.New(pool, s.accounts, s.catalogue, s.vpn, func() *river.Client[pgx.Tx] { return s.queue }, func() payments.Config { return s.cfg.PaymentSettings() }, func() time.Time { return s.now() })
	s.support = support.New(pool, limiter, s.accounts, cfg.RateNamespace, func() time.Time { return s.now() })
	return s
}

func NewServiceWithModules(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg Config, owner *accounts.Service, catalogueOwner *catalogue.Service, subscriptionOwner *subscriptions.Service, vpnOwner *vpn.Service, paymentsOwner *payments.Service, supportOwner *support.Service, notificationsOwner *notifications.Service) *Service {
	return &Service{pool: pool, limiter: limiter, queue: queue, cfg: cfg, now: time.Now, accounts: owner, catalogue: catalogueOwner, subscriptions: subscriptionOwner, vpn: vpnOwner, payments: paymentsOwner, support: supportOwner, notifications: notificationsOwner}
}

type MailArgs = accounts.MailArgs

func (s *Service) Health(ctx context.Context) bool { return s.pool.Ping(ctx) == nil }
