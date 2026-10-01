package s01

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	CabinetOrigin, DatabaseURL, RedisURL, TermsVersion, PrivacyVersion, SMTPAddress, SMTPUser, SMTPPassword, SMTPFrom string
	MailKey, CodeKey                                                                                                  []byte
	RateNamespace                                                                                                     string
	SMTPRootCAs                                                                                                       *x509.CertPool
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
	var err error
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
	u, e := url.Parse(c.CabinetOrigin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("CABINET_ORIGIN must be HTTPS origin")
	}
	if len(c.MailKey) != 32 || len(c.CodeKey) != 32 || c.TermsVersion == "" || c.PrivacyVersion == "" {
		return errors.New("invalid registration config")
	}
	return nil
}
