package app

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/audit_reports"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/support"
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
	var owner *accounts.Service
	mailOwner := notifications.NewMail(pool, queue, cfg.MailSettings, func(ctx context.Context, email string, work func(*pgxpool.Conn) error) error {
		return owner.WithMailGuard(ctx, email, work)
	}, func(ctx context.Context, tx pgx.Tx, r, c *uuid.UUID) (bool, error) {
		return owner.MailProofValidTx(ctx, tx, r, c)
	}, nil)
	owner = accounts.New(pool, limiter, mailOwner, accounts.Config{TermsVersion: cfg.TermsVersion, PrivacyVersion: cfg.PrivacyVersion, RateNamespace: cfg.RateNamespace, CodeKey: cfg.CodeKey, Operators: cfg.Operators})
	catalogueOwner := catalogue.New(pool, owner, nil)
	var subscriptionOwner *subscriptions.Service
	var paymentsOwner *payments.Service
	notificationsOwner := notifications.New(pool, func() []int64 { return cfg.Operators }, owner.OperatorAllowed, func(ctx context.Context, tx pgx.Tx, request uuid.UUID, chat int64) (json.RawMessage, error) {
		payload, err := subscriptionOwner.CardTx(ctx, tx, request, chat)
		if err != nil {
			return nil, err
		}
		return json.Marshal(payload)
	})
	vpnOwner := vpn.New(pool, owner, func() *river.Client[pgx.Tx] { return queue }, func() vpn.Settings { return cfg.VPNSettings() }, nil, func(ctx context.Context, tx pgx.Tx, r, o uuid.UUID, status string) error {
		return subscriptionOwner.RecordTrialOutcomeTx(ctx, tx, r, o, status)
	}, vpn.PurchaseHooks{Check: func(ctx context.Context, tx pgx.Tx, order, account, operation uuid.UUID) (string, error) {
		return paymentsOwner.CheckPurchaseAccess(ctx, tx, order, account, operation)
	}, Outcome: func(ctx context.Context, tx pgx.Tx, operation uuid.UUID, status, reason string) error {
		return paymentsOwner.RecordPurchaseAccessTx(ctx, tx, operation, status, reason)
	}})
	subscriptionOwner = subscriptions.New(pool, owner, catalogueOwner, vpnOwner, notificationsOwner, func() subscriptions.Config { return cfg.SubscriptionSettings() }, nil)
	paymentsOwner = payments.New(pool, owner, catalogueOwner, vpnOwner, func() *river.Client[pgx.Tx] { return queue }, func() payments.Config { return cfg.PaymentSettings() }, nil)
	supportOwner := support.New(pool, limiter, owner, cfg.RateNamespace, nil)
	auditOwner := auditreports.New(pool)
	return platform.NewServiceWithModules(pool, limiter, queue, cfg, owner, catalogueOwner, subscriptionOwner, vpnOwner, paymentsOwner, supportOwner, notificationsOwner, mailOwner, auditOwner)
}
