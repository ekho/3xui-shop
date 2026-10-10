package app

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"net"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"
)

type Config struct {
	DatabaseURL, RedisURL string
	OperationsEmail       string
	HTTP                  HTTPConfig
	Accounts              accounts.Config
	Bonuses               bonuses.RewardConfig
	ReferredTrial         bonuses.ReferredTrialConfig
	Subscriptions         subscriptions.Config
	VPN                   vpn.Settings
	Payments              payments.Config
	Mail                  notifications.MailConfig
	Audit                 auditreports.Config
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
	referredTrial, err := readReferredTrialConfig()
	if err != nil {
		return Config{}, err
	}
	audit, err := readAuditConfig()
	if err != nil {
		return Config{}, err
	}
	operationsEmail, err := readOperationsEmail()
	if err != nil {
		return Config{}, err
	}
	rewards, err := readRewardConfig()
	if err != nil {
		return Config{}, err
	}
	c := Config{
		OperationsEmail: operationsEmail,
		HTTP:            HTTPConfig{CabinetOrigin: os.Getenv("CABINET_ORIGIN")},
		Accounts:        accounts.Config{TermsVersion: os.Getenv("TERMS_VERSION"), PrivacyVersion: os.Getenv("PRIVACY_VERSION"), RateNamespace: "platform"},
		Mail:            notifications.MailConfig{SMTPAddress: os.Getenv("SMTP_ADDRESS"), SMTPUser: os.Getenv("SMTP_USER"), SMTPFrom: os.Getenv("SMTP_FROM")},
		Audit:           audit,
		Bonuses:         rewards,
		ReferredTrial:   referredTrial,
	}
	if value := os.Getenv("TRUSTED_PROXY_CIDRS"); value != "" {
		c.HTTP.TrustedProxyCIDRs = strings.Split(value, ",")
	}
	c.Subscriptions.PanelID = os.Getenv("PANEL_ID")
	c.Payments.YooMoneyWalletID = os.Getenv("YOOMONEY_WALLET_ID")
	c.Payments.YooKassaShopID, c.Payments.ShopEmail = os.Getenv("YOOKASSA_SHOP_ID"), os.Getenv("SHOP_EMAIL")
	c.Payments.CryptomusMerchantID = os.Getenv("CRYPTOMUS_MERCHANT_ID")
	c.Payments.HeleketMerchantID = os.Getenv("HELEKET_MERCHANT_ID")
	for name, dest := range map[string]*bool{"SHOP_PAYMENT_STARS_ENABLED": &c.Payments.StarsEnabled, "SHOP_PAYMENT_YOOKASSA_ENABLED": &c.Payments.YooKassaEnabled, "YOOKASSA_TEST_MODE": &c.Payments.YooKassaTestMode, "SHOP_PAYMENT_CRYPTOMUS_ENABLED": &c.Payments.CryptomusEnabled, "SHOP_PAYMENT_HELEKET_ENABLED": &c.Payments.HeleketEnabled} {
		if value := os.Getenv(name); value != "" {
			*dest, err = strconv.ParseBool(value)
			if err != nil {
				return c, errors.New("invalid " + name)
			}
		}
	}
	if c.Payments.YooKassaEnabled || os.Getenv("YOOKASSA_TOKEN_FILE") != "" || os.Getenv("YOOKASSA_TOKEN") != "" {
		c.Payments.YooKassaToken, err = SecretFile("YOOKASSA_TOKEN")
		if err != nil {
			return c, err
		}
	}
	if c.Payments.CryptomusEnabled || c.Payments.CryptomusMerchantID != "" || os.Getenv("CRYPTOMUS_API_KEY_FILE") != "" || os.Getenv("CRYPTOMUS_API_KEY") != "" {
		c.Payments.CryptomusAPIKey, err = SecretFile("CRYPTOMUS_API_KEY")
		if err != nil {
			return c, err
		}
	}
	if c.Payments.HeleketEnabled || c.Payments.HeleketMerchantID != "" || os.Getenv("HELEKET_API_KEY_FILE") != "" || os.Getenv("HELEKET_API_KEY") != "" {
		c.Payments.HeleketAPIKey, err = SecretFile("HELEKET_API_KEY")
		if err != nil {
			return c, err
		}
	}
	if value := os.Getenv("SHOP_PAYMENT_MANUAL_ENABLED"); value != "" {
		c.Payments.ManualEnabled, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid SHOP_PAYMENT_MANUAL_ENABLED")
		}
	}
	if c.Payments.ManualEnabled || os.Getenv("MANUAL_CARD_DETAILS_FILE") != "" || os.Getenv("MANUAL_CARD_DETAILS") != "" {
		c.Payments.ManualCardDetails, err = SecretFile("MANUAL_CARD_DETAILS")
		if err != nil {
			return c, err
		}
	}
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

func readOperationsEmail() (string, error) {
	if _, present := os.LookupEnv("OPERATIONS_EMAIL"); present {
		return "", errors.New("OPERATIONS_EMAIL plaintext is forbidden")
	}
	if os.Getenv("OPERATIONS_EMAIL_FILE") == "" {
		return "", nil
	}
	address, err := SecretFile("OPERATIONS_EMAIL")
	if err != nil {
		return "", err
	}
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Address != address || parsed.Name != "" || len(address) > 254 || strings.ContainsAny(address, "\r\n\x00") {
		return "", errors.New("invalid OPERATIONS_EMAIL_FILE")
	}
	return address, nil
}

func readAuditConfig() (auditreports.Config, error) {
	c := auditreports.Config{RetentionDays: 365, Timezone: time.UTC}
	if raw := os.Getenv("AUDIT_RETENTION_DAYS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 3650 {
			return c, errors.New("invalid AUDIT_RETENTION_DAYS")
		}
		c.RetentionDays = value
	}
	zone := os.Getenv("AUDIT_RETENTION_TIMEZONE")
	if zone == "" {
		zone = os.Getenv("BOT_TIMEZONE")
	}
	if zone == "" {
		zone = "UTC"
	}
	var err error
	c.Timezone, err = time.LoadLocation(zone)
	if err != nil || zone == "Local" {
		return c, errors.New("invalid AUDIT_RETENTION_TIMEZONE")
	}
	return c, nil
}
func (c Config) Validate() error {
	if c.Payments.CryptomusEnabled || c.Payments.CryptomusMerchantID != "" || c.Payments.CryptomusAPIKey != "" {
		merchant, err := uuid.Parse(c.Payments.CryptomusMerchantID)
		if err != nil || merchant == uuid.Nil || merchant.String() != c.Payments.CryptomusMerchantID {
			return errors.New("invalid CRYPTOMUS_MERCHANT_ID")
		}
		key := c.Payments.CryptomusAPIKey
		if key == "" || len(key) > 512 || !utf8.ValidString(key) || strings.ContainsAny(key, " \t\r\n\x00") {
			return errors.New("invalid CRYPTOMUS_API_KEY file")
		}
		if len(c.HTTP.CabinetOrigin)+len("/orders/")+36 > 255 {
			return errors.New("Cryptomus return URL exceeds provider limit")
		}
	}
	if c.Payments.HeleketEnabled || c.Payments.HeleketMerchantID != "" || c.Payments.HeleketAPIKey != "" {
		merchant, err := uuid.Parse(c.Payments.HeleketMerchantID)
		if err != nil || merchant == uuid.Nil || merchant.String() != c.Payments.HeleketMerchantID {
			return errors.New("invalid HELEKET_MERCHANT_ID")
		}
		key := c.Payments.HeleketAPIKey
		if key == "" || len(key) > 512 || !utf8.ValidString(key) || strings.ContainsAny(key, " \t\r\n\x00") {
			return errors.New("invalid HELEKET_API_KEY file")
		}
		if len(c.HTTP.CabinetOrigin)+len("/orders/")+36 > 255 {
			return errors.New("Heleket return URL exceeds provider limit")
		}
	}
	if c.Payments.YooKassaEnabled || c.Payments.YooKassaToken != "" {
		shop, err := strconv.ParseUint(c.Payments.YooKassaShopID, 10, 64)
		if err != nil || shop == 0 || len(c.Payments.YooKassaShopID) > 20 {
			return errors.New("invalid YOOKASSA_SHOP_ID")
		}
		if c.Payments.YooKassaToken == "" || len(c.Payments.YooKassaToken) > 512 || !utf8.ValidString(c.Payments.YooKassaToken) || strings.ContainsAny(c.Payments.YooKassaToken, " \t\r\n\x00") {
			return errors.New("invalid YOOKASSA_TOKEN file")
		}
		email, err := mail.ParseAddress(c.Payments.ShopEmail)
		if err != nil || email.Address != c.Payments.ShopEmail || len(c.Payments.ShopEmail) > 254 {
			return errors.New("invalid SHOP_EMAIL")
		}
	}
	if c.Payments.ManualEnabled && (!utf8.ValidString(c.Payments.ManualCardDetails) || strings.ContainsRune(c.Payments.ManualCardDetails, '\x00') || utf8.RuneCountInString(c.Payments.ManualCardDetails) > 2000 || strings.TrimSpace(c.Payments.ManualCardDetails) == "") {
		return errors.New("enabled manual payment requires valid MANUAL_CARD_DETAILS file")
	}
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
