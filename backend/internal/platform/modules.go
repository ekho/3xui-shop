package platform

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"time"

	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

func subscriptionError(err error) error {
	if err == nil {
		return nil
	}
	var a *subscriptions.Error
	if errors.As(err, &a) {
		return &Error{Status: a.Status, Code: a.Code, Message: a.Message, Details: a.Details}
	}
	var v *vpn.Error
	if errors.As(err, &v) {
		return &Error{Status: v.Status, Code: v.Code, Message: v.Message}
	}
	return accountError(err)
}
func (c Config) VPNSettings() vpn.Settings {
	return vpn.Settings{Panel: vpn.Config{PanelURL: c.PanelURL, PanelToken: c.PanelToken, PanelUsername: c.PanelUsername, PanelPassword: c.PanelPassword, PanelRootCAs: c.PanelRootCAs}, PanelID: c.PanelID, AccessResetTimezone: c.AccessResetTimezone, PanelDuplicateGuardVerified: c.PanelDuplicateGuardVerified}
}
func (c Config) SubscriptionSettings() subscriptions.Config {
	return subscriptions.Config{Operators: c.Operators, PanelID: c.PanelID, TrialEnabled: c.TrialEnabled, TrialPeriodDays: c.TrialPeriodDays, TrialTrafficGB: c.TrialTrafficGB, TrialDevices: c.TrialDevices, SubscriptionBaseURL: c.SubscriptionBaseURL}
}
func (s *Service) Subscriptions() *subscriptions.Service { return s.subscriptions }
func (s *Service) VPN() *vpn.Service                     { return s.vpn }
func (s *Service) Notifications() *notifications.Service { return s.notifications }
func (s *Service) connectSubscriptions() {
	s.notifications = notifications.New(s.pool, func() []int64 { return s.cfg.Operators }, s.accounts.OperatorAllowed, func(ctx context.Context, tx pgx.Tx, request uuid.UUID, chat int64) (json.RawMessage, error) {
		payload, err := s.subscriptions.CardTx(ctx, tx, request, chat)
		if err != nil {
			return nil, err
		}
		return json.Marshal(payload)
	})
	s.vpn = vpn.New(s.pool, s.accounts, func() *river.Client[pgx.Tx] { return s.queue }, func() vpn.Settings { return s.cfg.VPNSettings() }, func() time.Time { return s.now() }, func(ctx context.Context, tx pgx.Tx, r, o uuid.UUID, status string) error {
		return s.subscriptions.RecordTrialOutcomeTx(ctx, tx, r, o, status)
	}, vpn.PurchaseHooks{Check: s.CheckPurchaseAccess, Outcome: s.RecordPurchaseAccessTx})
	s.subscriptions = subscriptions.New(s.pool, s.accounts, s.catalogue, s.vpn, s.notifications, func() subscriptions.Config { return s.cfg.SubscriptionSettings() }, func() time.Time { return s.now() })
}

func (s *Service) operatorAllowed(actor int64) bool { return s.accounts.OperatorAllowed(actor) }

func (c Config) PaymentSettings() payments.Config {
	return payments.Config{CabinetOrigin: c.CabinetOrigin, PanelID: c.PanelID, YooMoneyWalletID: c.YooMoneyWalletID, YooMoneyEnabled: c.YooMoneyEnabled, YooMoneyNotificationSecret: c.YooMoneyNotificationSecret}
}
func (s *Service) Payments() *payments.Service { return s.payments }

func (c Config) MailSettings() notifications.MailConfig {
	return notifications.MailConfig{CabinetOrigin: c.CabinetOrigin, MailKey: c.MailKey, SMTPAddress: c.SMTPAddress, SMTPFrom: c.SMTPFrom, SMTPUser: c.SMTPUser, SMTPPassword: c.SMTPPassword, SMTPRootCAs: c.SMTPRootCAs}
}
func (s *Service) MailDelivery() *notifications.MailService { return s.mailDelivery }
func (s *Service) AuditReports() *auditreports.Service      { return s.auditReports }
