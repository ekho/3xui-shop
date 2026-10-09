package app

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/campaigns"
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
	Campaigns     *campaigns.Service
	Subscriptions *subscriptions.Service
	VPN           *vpn.Service
	Payments      *payments.Service
	Support       *support.Service
	Notifications *notifications.Service
	MailDelivery  *notifications.MailService
	Reminders     *notifications.ReminderService
	Notices       *notifications.NoticeService
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
	var paymentsOwner *payments.Service
	var campaignOwner *campaigns.Service
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
	accountConfig.RequireStarsCancellation = func(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string) error {
		return paymentsOwner.RequireStarsCancellationTx(ctx, tx, id, reason)
	}
	accountConfig.CanUnlinkTelegram = func(ctx context.Context, tx pgx.Tx, id uuid.UUID) (bool, error) {
		return paymentsOwner.CanUnlinkTelegramTx(ctx, tx, id)
	}
	accountConfig.CaptureRegistration = func(ctx context.Context, tx pgx.Tx, id uuid.UUID, channel, code string) error {
		return campaignOwner.CaptureRegistrationTx(ctx, tx, id, channel, code)
	}
	owner = accounts.New(pool, limiter, mailOwner, accountConfig)
	catalogueOwner := catalogue.New(pool, owner, now)
	var subscriptionOwner *subscriptions.Service
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
		c.SubscriptionBaseURL = cfg.Subscriptions.SubscriptionBaseURL
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
		c.RequireStarsCancellation = accountConfig.RequireStarsCancellation
		return c
	}, now)
	paymentsOwner = payments.New(pool, owner, catalogueOwner, subscriptionOwner, vpnOwner, func() *river.Client[pgx.Tx] { return queue }, func() payments.Config {
		c := cfg.Payments
		c.CabinetOrigin, c.PanelID = cfg.HTTP.CabinetOrigin, cfg.Subscriptions.PanelID
		return c
	}, now, notificationsOwner)
	campaignOwner = campaigns.New(pool, limiter, owner, subscriptionOwner, paymentsOwner, cfg.Accounts.RateNamespace, now)
	supportOwner := support.New(pool, limiter, owner, cfg.Accounts.RateNamespace, now, notificationsOwner)
	reportsOwner := auditreports.New(pool, auditreports.StatisticsPorts{RequireOperator: owner.RequireOperator, AccountsTx: owner.StatisticsTx, ReportCohortTx: campaignOwner.ReportCohortTx, PaymentsTx: paymentsOwner.StatisticsTx, TrialsTx: subscriptionOwner.StatisticsTx, PlansTx: catalogueOwner.StatisticsTx, VPNTx: vpnOwner.StatisticsTx}, auditreports.HistoryPorts{LockOperatorTx: owner.LockNoticeOperatorTx, AccountExistsTx: func(ctx context.Context, tx pgx.Tx, id uuid.UUID) (bool, error) {
		_, err := owner.LookupTx(ctx, tx, id)
		if errors.Is(err, accounts.ErrNotFound) {
			return false, nil
		}
		return err == nil, err
	}, LegacyTargetTx: owner.LegacyAuditTargetTx, LegacyLinksTx: owner.LegacyAuditLinksTx}, cfg.Audit)
	remindersOwner := notifications.NewReminders(pool, notifications.ReminderPorts{AudienceTx: owner.ReminderAudienceTx, RecipientTx: owner.ReminderRecipientTx, AccessTx: vpnOwner.ReminderAccessTx, PeriodTx: vpnOwner.ReminderPeriodTx, StarsTx: paymentsOwner.ReminderPolicyTx, MailGuard: owner.WithMailGuard}, mailOwner, notificationsOwner, now)
	noticesOwner := notifications.NewNotices(pool, notifications.NoticePorts{AudienceTx: owner.ReminderAudienceTx, RecipientTx: owner.NoticeRecipientTx, LockOperatorTx: owner.LockNoticeOperatorTx, LockPairTx: owner.LockNoticePairTx, DeliveryGuard: owner.WithNoticeDelivery, RequireOperator: owner.RequireOperator}, mailOwner, notificationsOwner, now)
	return &Modules{Accounts: owner, Catalogue: catalogueOwner, Campaigns: campaignOwner, Subscriptions: subscriptionOwner, VPN: vpnOwner, Payments: paymentsOwner, Support: supportOwner, Notifications: notificationsOwner, MailDelivery: mailOwner, Reminders: remindersOwner, Notices: noticesOwner, AuditReports: reportsOwner}
}
