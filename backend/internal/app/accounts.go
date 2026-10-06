package app

import (
	"context"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/platform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
)

// NewService is the production composition root during the remaining extractions.
func NewService(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg platform.Config) *platform.Service {
	owner := accounts.New(pool, limiter, queue, accounts.Config{CabinetOrigin: cfg.CabinetOrigin, TermsVersion: cfg.TermsVersion, PrivacyVersion: cfg.PrivacyVersion, RateNamespace: cfg.RateNamespace, MailKey: cfg.MailKey, CodeKey: cfg.CodeKey, Operators: cfg.Operators}, func(ctx context.Context, to, subject, body string) error {
		return platform.SendSMTP(ctx, cfg, to, subject, body)
	})
	catalogueOwner := catalogue.New(pool, owner, nil)
	var subscriptionOwner *subscriptions.Service
	var service *platform.Service
	vpnOwner := vpn.New(pool, owner, func() *river.Client[pgx.Tx] { return queue }, func() vpn.Settings { return cfg.VPNSettings() }, nil, func(ctx context.Context, tx pgx.Tx, r, o uuid.UUID, status string) error {
		return subscriptionOwner.RecordTrialOutcomeTx(ctx, tx, r, o, status)
	}, vpn.PurchaseHooks{Check: func(ctx context.Context, tx pgx.Tx, order, account, operation uuid.UUID) (string, error) {
		return service.CheckPurchaseAccess(ctx, tx, order, account, operation)
	}, Outcome: func(ctx context.Context, tx pgx.Tx, operation uuid.UUID, status, reason string) error {
		return service.RecordPurchaseAccessTx(ctx, tx, operation, status, reason)
	}})
	subscriptionOwner = subscriptions.New(pool, owner, catalogueOwner, vpnOwner, func() subscriptions.Config { return cfg.SubscriptionSettings() }, nil)
	service = platform.NewServiceWithModules(pool, limiter, queue, cfg, owner, catalogueOwner, subscriptionOwner, vpnOwner)
	return service
}
