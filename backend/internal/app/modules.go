package app

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"time"
)

// Modules is the assembled application; business operations stay with their owners.
type Modules struct {
	MiniApp       *telegram.MiniApp
	Accounts      *accounts.Service
	Catalogue     *catalogue.Service
	Subscriptions *subscriptions.Service
	VPN           *vpn.Service
	Payments      *payments.Service
	Support       *support.Service
	Notifications *notifications.Service
	MailDelivery  *notifications.MailService
	AuditReports  *auditreports.Service
}

func NewModules(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg *Config) *Modules {
	now := func() time.Time {
		if cfg.Accounts.Now != nil {
			return cfg.Accounts.Now()
		}
		return time.Now()
	}
	var owner *accounts.Service
	mailOwner := notifications.NewMail(pool, queue, func() notifications.MailConfig {
		c := cfg.Mail
		c.CabinetOrigin, c.Now = cfg.HTTP.CabinetOrigin, now
		return c
	}, func(ctx context.Context, email string, work func(*pgxpool.Conn) error) error {
		return owner.WithMailGuard(ctx, email, work)
	}, func(ctx context.Context, tx pgx.Tx, r, c *uuid.UUID) (bool, error) {
		return owner.MailProofValidTx(ctx, tx, r, c)
	}, nil)
	accountConfig := cfg.Accounts
	accountConfig.Now = now
	owner = accounts.New(pool, limiter, mailOwner, accountConfig)
	catalogueOwner := catalogue.New(pool, owner, now)
	var subscriptionOwner *subscriptions.Service
	var paymentsOwner *payments.Service
	notificationsOwner := notifications.New(pool, func() []int64 { return cfg.Accounts.Operators }, owner.OperatorAllowed, func(ctx context.Context, tx pgx.Tx, request uuid.UUID, chat int64) (json.RawMessage, error) {
		payload, err := subscriptionOwner.CardTx(ctx, tx, request, chat)
		if err != nil {
			return nil, err
		}
		return json.Marshal(payload)
	}, owner.WithTelegramDelivery)
	vpnOwner := vpn.New(pool, owner, func() *river.Client[pgx.Tx] { return queue }, func() vpn.Settings {
		c := cfg.VPN
		c.PanelID = cfg.Subscriptions.PanelID
		return c
	}, now, func(ctx context.Context, tx pgx.Tx, r, o uuid.UUID, status string) error {
		return subscriptionOwner.RecordTrialOutcomeTx(ctx, tx, r, o, status)
	}, vpn.PurchaseHooks{Check: func(ctx context.Context, tx pgx.Tx, order, account, operation uuid.UUID) (string, error) {
		return paymentsOwner.CheckPurchaseAccess(ctx, tx, order, account, operation)
	}, Outcome: func(ctx context.Context, tx pgx.Tx, operation uuid.UUID, status, reason string) error {
		return paymentsOwner.RecordPurchaseAccessTx(ctx, tx, operation, status, reason)
	}})
	subscriptionOwner = subscriptions.New(pool, owner, catalogueOwner, vpnOwner, notificationsOwner, func() subscriptions.Config {
		c := cfg.Subscriptions
		c.Operators = cfg.Accounts.Operators
		return c
	}, now)
	paymentsOwner = payments.New(pool, owner, catalogueOwner, subscriptionOwner, vpnOwner, func() *river.Client[pgx.Tx] { return queue }, func() payments.Config {
		c := cfg.Payments
		c.CabinetOrigin, c.PanelID = cfg.HTTP.CabinetOrigin, cfg.Subscriptions.PanelID
		return c
	}, now, notificationsOwner)
	supportOwner := support.New(pool, limiter, owner, cfg.Accounts.RateNamespace, now, notificationsOwner)
	return &Modules{Accounts: owner, Catalogue: catalogueOwner, Subscriptions: subscriptionOwner, VPN: vpnOwner, Payments: paymentsOwner, Support: supportOwner, Notifications: notificationsOwner, MailDelivery: mailOwner, AuditReports: auditreports.New(pool)}
}
