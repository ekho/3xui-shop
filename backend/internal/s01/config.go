package s01

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type Config struct {
	PanelURL, PanelToken, PanelUsername, PanelPassword, SubscriptionBaseURL string
	PanelDuplicateGuardVerified                                             bool
	PanelRootCAs                                                            *x509.CertPool
	AccessResetTimezone                                                     string

	Operators         []int64
	AdapterToken      string
	PanelID           string
	TrialEnabled      bool
	TrialPeriodDays   int64
	TrialTrafficGB    int64
	TrialDevices      int64
	TrustedProxyCIDRs []string

	CabinetOrigin  string
	DatabaseURL    string
	RedisURL       string
	TermsVersion   string
	PrivacyVersion string
	SMTPAddress    string
	SMTPUser       string
	SMTPPassword   string
	SMTPFrom       string

	MailKey       []byte
	CodeKey       []byte
	RateNamespace string
	SMTPRootCAs   *x509.CertPool
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
	c := Config{CabinetOrigin: os.Getenv("CABINET_ORIGIN"), TermsVersion: os.Getenv("TERMS_VERSION"), PrivacyVersion: os.Getenv("PRIVACY_VERSION"), SMTPAddress: os.Getenv("SMTP_ADDRESS"), SMTPUser: os.Getenv("SMTP_USER"), SMTPFrom: os.Getenv("SMTP_FROM"), RateNamespace: "s01"}
	if value := os.Getenv("TRUSTED_PROXY_CIDRS"); value != "" {
		c.TrustedProxyCIDRs = strings.Split(value, ",")
	}
	c.PanelID = os.Getenv("PANEL_ID")
	c.AccessResetTimezone = os.Getenv("ACCESS_RESET_TIMEZONE")
	if c.AccessResetTimezone == "" {
		c.AccessResetTimezone = "UTC"
	}
	c.PanelURL = os.Getenv("PANEL_URL")
	c.SubscriptionBaseURL = os.Getenv("SUBSCRIPTION_BASE_URL")
	c.PanelUsername = os.Getenv("PANEL_USERNAME")
	var err error
	if value := os.Getenv("TRIAL_ENABLED"); value != "" {
		c.TrialEnabled, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid TRIAL_ENABLED")
		}
	}
	if value := os.Getenv("PANEL_DUPLICATE_GUARD_VERIFIED"); value != "" {
		c.PanelDuplicateGuardVerified, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid PANEL_DUPLICATE_GUARD_VERIFIED")
		}
	}
	if os.Getenv("PANEL_TOKEN_FILE") != "" {
		c.PanelToken, err = SecretFile("PANEL_TOKEN")
		if err != nil {
			return c, err
		}
	}
	if c.PanelUsername != "" {
		c.PanelPassword, err = SecretFile("PANEL_PASSWORD")
		if err != nil {
			return c, err
		}
	}
	if path := os.Getenv("PANEL_CA_FILE"); path != "" {
		pem, e := os.ReadFile(path)
		if e != nil {
			return c, errors.New("unreadable PANEL_CA_FILE")
		}
		c.PanelRootCAs = x509.NewCertPool()
		if !c.PanelRootCAs.AppendCertsFromPEM(pem) {
			return c, errors.New("invalid PANEL_CA_FILE")
		}
	}
	for name, dest := range map[string]*int64{"TRIAL_PERIOD": &c.TrialPeriodDays, "TRIAL_TRAFFIC_GB": &c.TrialTrafficGB, "BONUS_DEVICES_COUNT": &c.TrialDevices} {
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
			c.Operators = append(c.Operators, id)
		}
		c.AdapterToken, err = SecretFile("BOT_ADAPTER_TOKEN")
		if err != nil || len(c.AdapterToken) < 32 {
			return c, errors.New("invalid BOT_ADAPTER_TOKEN_FILE")
		}
	}
	if c.TrialEnabled && c.PanelID == "" {
		return c, errors.New("enabled trial requires panel")
	}

	for name, dest := range map[string]*string{"DATABASE_URL": &c.DatabaseURL, "REDIS_URL": &c.RedisURL} {
		if *dest, err = SecretFile(name); err != nil {
			return c, err
		}
	}
	if c.SMTPUser != "" {
		if c.SMTPPassword, err = SecretFile("SMTP_PASSWORD"); err != nil {
			return c, err
		}
	}
	if path := os.Getenv("SMTP_CA_FILE"); path != "" {
		pem, e := os.ReadFile(path)
		if e != nil {
			return c, errors.New("cannot read SMTP CA file")
		}
		c.SMTPRootCAs = x509.NewCertPool()
		if !c.SMTPRootCAs.AppendCertsFromPEM(pem) {
			return c, errors.New("invalid SMTP CA file")
		}
	}

	for name, dest := range map[string]*[]byte{"MAIL_KEY": &c.MailKey, "CODE_KEY": &c.CodeKey} {
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
	zone := c.AccessResetTimezone
	if zone == "" {
		zone = "UTC"
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return errors.New("invalid ACCESS_RESET_TIMEZONE")
	}
	if c.TrialEnabled || c.PanelURL != "" {
		for _, raw := range []string{c.PanelURL, c.SubscriptionBaseURL} {
			u, e := url.Parse(raw)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return errors.New("panel and subscription URLs must be HTTPS")
			}
		}
		if c.PanelToken == "" && (c.PanelUsername == "" || c.PanelPassword == "") {
			return errors.New("panel credentials required")
		}
		if c.PanelToken != "" && (c.PanelUsername != "" || c.PanelPassword != "") {
			return errors.New("choose one panel credential mode")
		}
	}

	for _, cidr := range c.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return errors.New("invalid trusted proxy CIDR")
		}
	}
	u, e := url.Parse(c.CabinetOrigin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("CABINET_ORIGIN must be HTTPS origin")
	}
	if len(c.MailKey) != 32 || len(c.CodeKey) != 32 || c.TermsVersion == "" || c.PrivacyVersion == "" {
		return errors.New("invalid registration config")
	}
	return nil
}
