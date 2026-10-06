package platform

import (
	"context"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
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
	mailDelivery  *notifications.MailService
	auditReports  *auditreports.Service
}

func NewService(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg Config) *Service {
	s := NewServiceWithModules(pool, limiter, queue, cfg, nil, nil, nil, nil, nil, nil, nil, nil, auditreports.New(pool))
	s.mailDelivery = notifications.NewMail(pool, queue, func() notifications.MailConfig {
		c := s.cfg.MailSettings()
		c.Now = func() time.Time { return s.now() }
		return c
	}, func(ctx context.Context, email string, work func(*pgxpool.Conn) error) error {
		return s.accounts.WithMailGuard(ctx, email, work)
	}, func(ctx context.Context, tx pgx.Tx, r, c *uuid.UUID) (bool, error) {
		return s.accounts.MailProofValidTx(ctx, tx, r, c)
	}, nil)
	s.accounts = accounts.New(pool, limiter, s.mailDelivery, accounts.Config{TermsVersion: cfg.TermsVersion, PrivacyVersion: cfg.PrivacyVersion, RateNamespace: cfg.RateNamespace, CodeKey: cfg.CodeKey, Operators: cfg.Operators, Now: func() time.Time { return s.now() }})
	s.catalogue = catalogue.New(pool, s.accounts, func() time.Time { return s.now() })
	s.connectSubscriptions()
	s.payments = payments.New(pool, s.accounts, s.catalogue, s.vpn, func() *river.Client[pgx.Tx] { return s.queue }, func() payments.Config { return s.cfg.PaymentSettings() }, func() time.Time { return s.now() })
	s.support = support.New(pool, limiter, s.accounts, cfg.RateNamespace, func() time.Time { return s.now() })
	return s
}

func NewServiceWithModules(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg Config, owner *accounts.Service, catalogueOwner *catalogue.Service, subscriptionOwner *subscriptions.Service, vpnOwner *vpn.Service, paymentsOwner *payments.Service, supportOwner *support.Service, notificationsOwner *notifications.Service, mailOwner *notifications.MailService, auditOwner *auditreports.Service) *Service {
	return &Service{pool: pool, limiter: limiter, queue: queue, cfg: cfg, now: time.Now, accounts: owner, catalogue: catalogueOwner, subscriptions: subscriptionOwner, vpn: vpnOwner, payments: paymentsOwner, support: supportOwner, notifications: notificationsOwner, mailDelivery: mailOwner, auditReports: auditOwner}
}

type MailArgs = notifications.MailArgs

func (s *Service) Health(ctx context.Context) bool { return s.pool.Ping(ctx) == nil }
