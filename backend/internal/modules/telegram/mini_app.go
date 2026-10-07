package telegram

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MiniAppProductionKey = "e7bf03a2fa4602af4580703d88dda5bb59f32ed8b02a56c187fe7d34caed242d"

var ErrMiniAppData = errors.New("invalid Telegram Mini App data")

type VerifiedMiniAppData struct {
	accounts.TelegramInput
	StartParam string
}

// VerifyMiniAppData implements Telegram's third-party Ed25519 format.
func VerifyMiniAppData(raw string, botID int64, key ed25519.PublicKey, now time.Time) (VerifiedMiniAppData, error) {
	empty := VerifiedMiniAppData{}
	if botID <= 0 || len(key) != ed25519.PublicKeySize || len(raw) > 12288 || !utf8.ValidString(raw) {
		return empty, ErrMiniAppData
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return empty, ErrMiniAppData
	}
	keys := []string{}
	for k, v := range values {
		if k == "" || len(v) != 1 || !utf8.ValidString(v[0]) {
			return empty, ErrMiniAppData
		}
		for _, r := range k {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
				return empty, ErrMiniAppData
			}
		}
		if k != "hash" && k != "signature" {
			keys = append(keys, k)
		}
	}
	signature, err := base64.RawURLEncoding.DecodeString(strings.TrimSuffix(values.Get("signature"), "=="))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return empty, ErrMiniAppData
	}
	sort.Strings(keys)
	lines := []string{strconv.FormatInt(botID, 10) + ":WebAppData"}
	for _, k := range keys {
		lines = append(lines, k+"="+values.Get(k))
	}
	if !ed25519.Verify(key, []byte(strings.Join(lines, "\n")), signature) {
		return empty, ErrMiniAppData
	}
	at, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || at < now.Unix()-300 || at > now.Unix()+60 {
		return empty, ErrMiniAppData
	}
	var user struct {
		ID           int64  `json:"id"`
		IsBot        bool   `json:"is_bot"`
		FirstName    string `json:"first_name"`
		LastName     string `json:"last_name"`
		LanguageCode string `json:"language_code"`
	}
	if json.Unmarshal([]byte(values.Get("user")), &user) != nil || user.ID <= 0 || user.ID > 1<<52-1 || user.IsBot || strings.TrimSpace(user.FirstName) == "" {
		return empty, ErrMiniAppData
	}
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	for _, r := range name {
		if unicode.IsControl(r) {
			return empty, ErrMiniAppData
		}
	}
	runes := []rune(name)
	if len(runes) > 128 {
		name = string(runes[:128])
	}
	locale := "en"
	if user.LanguageCode == "" || strings.HasPrefix(user.LanguageCode, "ru") {
		locale = "ru"
	}
	start := values.Get("start_param")
	if len(start) > 512 {
		return empty, ErrMiniAppData
	}
	for _, r := range start {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return empty, ErrMiniAppData
		}
	}
	return VerifiedMiniAppData{TelegramInput: accounts.TelegramInput{TelegramID: user.ID, DisplayName: name, Locale: locale}, StartParam: start}, nil
}

type MiniApp struct {
	botID    int64
	key      ed25519.PublicKey
	accounts *accounts.Service
	now      func() time.Time
}

// The key is a trusted assembly dependency, never a deployment environment override.
func NewMiniApp(botID int64, key ed25519.PublicKey, owner *accounts.Service, now func() time.Time) *MiniApp {
	if now == nil {
		now = time.Now
	}
	return &MiniApp{botID: botID, key: append(ed25519.PublicKey(nil), key...), accounts: owner, now: now}
}
func (s *MiniApp) Login(ctx context.Context, raw, terms, privacy, ip string) (accounts.Authentication, string, error) {
	if err := s.accounts.LimitMiniAppLogin(ctx, ip); err != nil {
		return accounts.Authentication{}, "", err
	}
	user, err := VerifyMiniAppData(raw, s.botID, s.key, s.now())
	if err != nil {
		return accounts.Authentication{}, "", err
	}
	return s.accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: user.TelegramInput, StartParam: user.StartParam, AcceptedTermsVersion: terms, AcceptedPrivacyVersion: privacy})
}
