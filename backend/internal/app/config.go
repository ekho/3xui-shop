package app

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/vpn"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type Config struct {
	DatabaseURL, RedisURL string
	HTTP                  HTTPConfig
	Accounts              accounts.Config
	Subscriptions         subscriptions.Config
	VPN                   vpn.Settings
	Payments              payments.Config
	Mail                  notifications.MailConfig
}
type HTTPConfig struct {
	CabinetOrigin, AdapterToken string
	TrustedProxyCIDRs           []string
}

func SecretFile(name string) (string, error) {
	path := os.Getenv(name + "_FILE")
	if path == "" {
		return "", errors.New("missing secret file: " + name)
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return "", errors.New("unreadable secret file: " + name)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", errors.New("empty secret file: " + name)
	}
	if os.Getenv(name) != "" {
		return "", errors.New("conflicting secret inputs: " + name)
	}
	return v, nil
}
func LoadConfig() (Config, error) {
	c := Config{
		HTTP:     HTTPConfig{CabinetOrigin: os.Getenv("CABINET_ORIGIN")},
		Accounts: accounts.Config{TermsVersion: os.Getenv("TERMS_VERSION"), PrivacyVersion: os.Getenv("PRIVACY_VERSION"), RateNamespace: "platform"},
		Mail:     notifications.MailConfig{SMTPAddress: os.Getenv("SMTP_ADDRESS"), SMTPUser: os.Getenv("SMTP_USER"), SMTPFrom: os.Getenv("SMTP_FROM")},
	}
	var err error
	if value := os.Getenv("TRUSTED_PROXY_CIDRS"); value != "" {
		c.HTTP.TrustedProxyCIDRs = strings.Split(value, ",")
	}
	c.Subscriptions.PanelID = os.Getenv("PANEL_ID")
	c.Payments.YooMoneyWalletID = os.Getenv("YOOMONEY_WALLET_ID")
	if value := os.Getenv("SHOP_PAYMENT_YOOMONEY_ENABLED"); value != "" {
		c.Payments.YooMoneyEnabled, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid SHOP_PAYMENT_YOOMONEY_ENABLED")
		}
	}
	if c.Payments.YooMoneyEnabled || os.Getenv("YOOMONEY_NOTIFICATION_SECRET_FILE") != "" || os.Getenv("YOOMONEY_NOTIFICATION_SECRET") != "" {
		secret, e := SecretFile("YOOMONEY_NOTIFICATION_SECRET")
		if e != nil {
			return c, e
		}
		c.Payments.YooMoneyNotificationSecret = []byte(secret)
	}
	c.VPN.AccessResetTimezone = os.Getenv("ACCESS_RESET_TIMEZONE")
	if c.VPN.AccessResetTimezone == "" {
		c.VPN.AccessResetTimezone = "UTC"
	}
	c.VPN.Panel.PanelURL = os.Getenv("PANEL_URL")
	c.Subscriptions.SubscriptionBaseURL = os.Getenv("SUBSCRIPTION_BASE_URL")
	c.VPN.Panel.PanelUsername = os.Getenv("PANEL_USERNAME")
	if value := os.Getenv("TRIAL_ENABLED"); value != "" {
		c.Subscriptions.TrialEnabled, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid TRIAL_ENABLED")
		}
	}
	if value := os.Getenv("PANEL_DUPLICATE_GUARD_VERIFIED"); value != "" {
		c.VPN.PanelDuplicateGuardVerified, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid PANEL_DUPLICATE_GUARD_VERIFIED")
		}
	}
	if os.Getenv("PANEL_TOKEN_FILE") != "" {
		c.VPN.Panel.PanelToken, err = SecretFile("PANEL_TOKEN")
		if err != nil {
			return c, err
		}
	}
	if c.VPN.Panel.PanelUsername != "" {
		c.VPN.Panel.PanelPassword, err = SecretFile("PANEL_PASSWORD")
		if err != nil {
			return c, err
		}
	}
	if path := os.Getenv("PANEL_CA_FILE"); path != "" {
		pem, e := os.ReadFile(path)
		if e != nil {
			return c, errors.New("unreadable PANEL_CA_FILE")
		}
		c.VPN.Panel.PanelRootCAs = x509.NewCertPool()
		if !c.VPN.Panel.PanelRootCAs.AppendCertsFromPEM(pem) {
			return c, errors.New("invalid PANEL_CA_FILE")
		}
	}
	for name, dest := range map[string]*int64{"TRIAL_PERIOD": &c.Subscriptions.TrialPeriodDays, "TRIAL_TRAFFIC_GB": &c.Subscriptions.TrialTrafficGB, "BONUS_DEVICES_COUNT": &c.Subscriptions.TrialDevices} {
		value := os.Getenv(name)
		if value == "" {
			switch name {
			case "TRIAL_PERIOD":
				value = "3"
			case "TRIAL_TRAFFIC_GB":
				value = "15"
			case "BONUS_DEVICES_COUNT":
				value = "1"
			}
		}
		*dest, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return c, errors.New("invalid trial setting: " + name)
		}
	}
	if value := os.Getenv("BOT_OPERATOR_IDS"); value != "" {
		for _, text := range strings.Split(value, ",") {
			id, e := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
			if e != nil || id <= 0 {
				return c, errors.New("invalid BOT_OPERATOR_IDS")
			}
			c.Accounts.Operators = append(c.Accounts.Operators, id)
		}
	}
	legacyBotAPIEnabled := true
	if value := os.Getenv("LEGACY_BOT_API_ENABLED"); value != "" {
		legacyBotAPIEnabled, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid LEGACY_BOT_API_ENABLED")
		}
	}
	if len(c.Accounts.Operators) > 0 && legacyBotAPIEnabled {
		c.HTTP.AdapterToken, err = SecretFile("BOT_ADAPTER_TOKEN")
		if err != nil || len(c.HTTP.AdapterToken) < 32 {
			return c, errors.New("invalid BOT_ADAPTER_TOKEN_FILE")
		}
	}
	if c.Subscriptions.TrialEnabled && c.Subscriptions.PanelID == "" {
		return c, errors.New("enabled trial requires panel")
	}

	for name, dest := range map[string]*string{"DATABASE_URL": &c.DatabaseURL, "REDIS_URL": &c.RedisURL} {
		if *dest, err = SecretFile(name); err != nil {
			return c, err
		}
	}
	if c.Mail.SMTPUser != "" {
		if c.Mail.SMTPPassword, err = SecretFile("SMTP_PASSWORD"); err != nil {
			return c, err
		}
	}
	if path := os.Getenv("SMTP_CA_FILE"); path != "" {
		pem, e := os.ReadFile(path)
		if e != nil {
			return c, errors.New("cannot read SMTP CA file")
		}
		c.Mail.SMTPRootCAs = x509.NewCertPool()
		if !c.Mail.SMTPRootCAs.AppendCertsFromPEM(pem) {
			return c, errors.New("invalid SMTP CA file")
		}
	}

	for name, dest := range map[string]*[]byte{"MAIL_KEY": &c.Mail.MailKey, "CODE_KEY": &c.Accounts.CodeKey} {
		v, e := SecretFile(name)
		if e != nil {
			return c, e
		}
		*dest, e = base64.StdEncoding.DecodeString(v)
		if e != nil || len(*dest) != 32 {
			return c, errors.New("key must be base64 encoded 32 bytes: " + name)
		}
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.Payments.YooMoneyEnabled && (c.Payments.YooMoneyWalletID == "" || len(c.Payments.YooMoneyNotificationSecret) == 0) {
		return errors.New("enabled YooMoney requires wallet and notification secret file")
	}
	if c.Payments.YooMoneyWalletID != "" {
		if len(c.Payments.YooMoneyWalletID) < 11 || len(c.Payments.YooMoneyWalletID) > 20 {
			return errors.New("invalid YOOMONEY_WALLET_ID")
		}
		for _, digit := range c.Payments.YooMoneyWalletID {
			if digit < '0' || digit > '9' {
				return errors.New("invalid YOOMONEY_WALLET_ID")
			}
		}
	}
	zone := c.VPN.AccessResetTimezone
	if zone == "" {
		zone = "UTC"
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return errors.New("invalid ACCESS_RESET_TIMEZONE")
	}
	if c.Subscriptions.TrialEnabled || c.VPN.Panel.PanelURL != "" {
		for _, raw := range []string{c.VPN.Panel.PanelURL, c.Subscriptions.SubscriptionBaseURL} {
			u, e := url.Parse(raw)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return errors.New("panel and subscription URLs must be HTTPS")
			}
		}
		if c.VPN.Panel.PanelToken == "" && (c.VPN.Panel.PanelUsername == "" || c.VPN.Panel.PanelPassword == "") {
			return errors.New("panel credentials required")
		}
		if c.VPN.Panel.PanelToken != "" && (c.VPN.Panel.PanelUsername != "" || c.VPN.Panel.PanelPassword != "") {
			return errors.New("choose one panel credential mode")
		}
	}

	for _, cidr := range c.HTTP.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return errors.New("invalid trusted proxy CIDR")
		}
	}
	u, e := url.Parse(c.HTTP.CabinetOrigin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("CABINET_ORIGIN must be HTTPS origin")
	}
	if len(c.Mail.MailKey) != 32 || len(c.Accounts.CodeKey) != 32 || c.Accounts.TermsVersion == "" || c.Accounts.PrivacyVersion == "" {
		return errors.New("invalid registration config")
	}
	return nil
}
