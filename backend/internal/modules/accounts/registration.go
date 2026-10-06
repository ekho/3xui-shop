package accounts

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
	"golang.org/x/net/idna"
	"math/big"
	"net/mail"
	"slices"
	"strings"
	"time"
)

func normalizeEmail(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if len(raw) > 254 || strings.ContainsAny(raw, "\r\n") {
		return "", failure(400, "INVALID_INPUT")
	}
	at := strings.LastIndexByte(raw, '@')
	if at < 1 {
		return "", failure(400, "INVALID_INPUT")
	}
	domain, e := idna.Lookup.ToASCII(raw[at+1:])
	if e != nil || domain == "" {
		return "", failure(400, "INVALID_INPUT")
	}
	raw = raw[:at+1] + domain
	address, e := mail.ParseAddress(raw)
	if e != nil || address.Address != raw || address.Name != "" {
		return "", failure(400, "INVALID_INPUT")
	}
	return raw, nil
}
func opaque() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func digest(v string) []byte { h := sha256.Sum256([]byte(v)); return h[:] }
func (s *Service) codeDigest(id uuid.UUID, code string) []byte {
	h := hmac.New(sha256.New, s.cfg.CodeKey)
	h.Write([]byte(id.String() + ":" + code))
	return h.Sum(nil)
}
func stamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

var mailLimit = redis.NewScript(`
local now=tonumber(ARGV[1]);local retry=0
for _,key in ipairs(KEYS) do
 redis.call('ZREMRANGEBYSCORE',key,'-inf',now-3600000)
 local last=redis.call('ZREVRANGE',key,0,0,'WITHSCORES')
 if #last>0 and now-tonumber(last[2])<60000 then retry=math.max(retry,math.ceil((60000-now+tonumber(last[2]))/1000)) end
 if redis.call('ZCARD',key)>=5 then local first=redis.call('ZRANGE',key,0,0,'WITHSCORES');retry=math.max(retry,math.ceil((3600000-now+tonumber(first[2]))/1000)) end
end
if retry>0 then return retry end
for _,key in ipairs(KEYS) do redis.call('ZADD',key,now,ARGV[2]);redis.call('PEXPIRE',key,3600000) end
return 0`)

func (s *Service) limitMail(ctx context.Context, email string) error {
	return s.limitMails(ctx, []string{email})
}
func (s *Service) limitMails(ctx context.Context, emails []string) error {
	slices.Sort(emails)
	emails = slices.Compact(emails)
	keys := make([]string, 0, len(emails))
	for _, email := range emails {
		h := hmac.New(sha256.New, s.cfg.CodeKey)
		h.Write([]byte(email))
		keys = append(keys, fmt.Sprintf("%s:mail:%x", s.cfg.RateNamespace, h.Sum(nil)))
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	retry, err := mailLimit.Run(ctx, s.limiter, keys, s.now().UnixMilli(), uuid.NewString()).Int()
	if err != nil {
		return unavailable()
	}
	if retry > 0 {
		return &Error{Status: 429, Code: "RATE_LIMITED", Message: "RATE_LIMITED", RetryAfter: retry}
	}
	return nil
}
func (s *Service) Register(ctx context.Context, in RegisterInput) (RegistrationAccepted, error) {
	out := RegistrationAccepted{ChallengeId: uuid.New(), ResendAfter: 60}
	email, err := normalizeEmail(string(in.Email))
	if err != nil {
		return out, err
	}
	if (in.Locale != "ru" && in.Locale != "en") || in.AcceptedTermsVersion != s.cfg.TermsVersion || in.AcceptedPrivacyVersion != s.cfg.PrivacyVersion {
		return out, failure(400, "INVALID_INPUT")
	}
	if err = s.limitMail(ctx, email); err != nil {
		return out, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if err = q.LockRegistrationEmail(ctx, email); err != nil {
		return out, unavailable()
	}
	account, err := q.AccountByEmail(ctx, email)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, unavailable()
	}
	if err = q.RevokeChallenges(ctx, email); err != nil {
		return out, unavailable()
	}
	if err = s.mail.ClearRegistrationMailTx(ctx, tx, email); err != nil {
		return out, unavailable()
	}
	payload := mailPayload{Locale: string(in.Locale), Type: "login"}
	var challengeID *uuid.UUID
	if exists {
		payload.Locale = account.Locale
	} else {
		token := opaque()
		n, _ := rand.Int(rand.Reader, big.NewInt(100000000))
		code := fmt.Sprintf("%08d", n)
		id := out.ChallengeId
		challengeID = &id
		payload.Type = "verify"
		payload.Token = token
		payload.Code = code
		now := s.now()
		err = q.AddChallenge(ctx, store.AddChallengeParams{ID: id, EmailKey: email, Locale: string(in.Locale), TermsVersion: in.AcceptedTermsVersion, PrivacyVersion: in.AcceptedPrivacyVersion, TokenHash: digest(token), CodeHash: s.codeDigest(id, code), CreatedAt: stamp(now), TokenExpiresAt: stamp(now.Add(24 * time.Hour)), CodeExpiresAt: stamp(now.Add(10 * time.Minute))})
		if err != nil {
			return out, unavailable()
		}
	}
	if err = s.enqueueMail(ctx, tx, email, challengeID, payload); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, unavailable()
	}
	return out, nil
}
func (s *Service) ResendVerification(ctx context.Context, in ResendInput) (ResendAccepted, error) {
	email, err := normalizeEmail(string(in.Email))
	if err != nil {
		return ResendAccepted{}, err
	}
	var locale, terms, privacy string
	err = s.pool.QueryRow(ctx, `SELECT locale,terms_version,privacy_version FROM registration_challenges WHERE email_key=$1 ORDER BY created_at DESC LIMIT 1`, email).Scan(&locale, &terms, &privacy)
	if errors.Is(err, pgx.ErrNoRows) {
		locale = "ru"
	} else if err != nil {
		return ResendAccepted{}, unavailable()
	}
	// Versions must match the currently published consent. A new registration collects it if changed.
	if terms != "" && (terms != s.cfg.TermsVersion || privacy != s.cfg.PrivacyVersion) {
		return ResendAccepted{}, failure(400, "INVALID_INPUT")
	}
	// Unknown email is indistinguishable but cannot bypass consent by creating a challenge.
	if terms == "" {
		if err = s.limitMail(ctx, email); err != nil {
			return ResendAccepted{}, err
		}
		tx, e := s.pool.Begin(ctx)
		if e != nil {
			return ResendAccepted{}, unavailable()
		}
		defer tx.Rollback(ctx)
		q := store.New(tx)
		if e = q.LockRegistrationEmail(ctx, email); e != nil {
			return ResendAccepted{}, unavailable()
		}
		a, e := q.AccountByEmail(ctx, email)
		if e == nil {
			if e = s.enqueueMail(ctx, tx, email, nil, mailPayload{Type: "login", Locale: a.Locale}); e != nil {
				return ResendAccepted{}, e
			}
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return ResendAccepted{}, unavailable()
		}
		if e = tx.Commit(ctx); e != nil {
			return ResendAccepted{}, unavailable()
		}
		return ResendAccepted{ResendAfter: 60}, nil
	}
	_, err = s.Register(ctx, RegisterInput{Email: in.Email, Locale: string(locale), AcceptedTermsVersion: terms, AcceptedPrivacyVersion: privacy})
	return ResendAccepted{ResendAfter: 60}, err
}
func (s *Service) VerifyEmail(ctx context.Context, in VerifyInput) (VerifyResult, error) {
	bad := failure(400, "INVALID_INPUT")
	out := VerifyResult{}
	byToken := in.Token != nil
	if byToken && (in.Code != nil || in.ChallengeId != nil) || !byToken && (in.Code == nil || in.ChallengeId == nil) {
		return out, bad
	}
	if byToken && len(*in.Token) != 43 {
		return out, bad
	}
	if !byToken {
		if len(*in.Code) != 8 {
			return out, bad
		}
		for _, r := range *in.Code {
			if r < '0' || r > '9' {
				return out, bad
			}
		}
	}
	if err := validatePassword(in.NewPassword); err != nil {
		return out, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	var email string
	if byToken {
		email, err = q.ChallengeEmailByToken(ctx, digest(*in.Token))
	} else {
		email, err = q.ChallengeEmailByID(ctx, *in.ChallengeId)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return out, bad
	}
	if err != nil {
		return out, unavailable()
	}
	if err = q.LockRegistrationEmail(ctx, email); err != nil {
		return out, unavailable()
	}
	var c store.RegistrationChallenge
	if byToken {
		c, err = q.ChallengeByToken(ctx, digest(*in.Token))
	} else {
		c, err = q.ChallengeByID(ctx, *in.ChallengeId)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return out, bad
	}
	if err != nil {
		return out, unavailable()
	}
	if c.Revoked || !s.now().Before(c.TokenExpiresAt.Time) {
		return out, bad
	}
	if !byToken {
		if c.FailedGuesses >= 5 || !s.now().Before(c.CodeExpiresAt.Time) {
			return out, bad
		}
		if subtle.ConstantTimeCompare(c.CodeHash, s.codeDigest(c.ID, *in.Code)) != 1 {
			if q.FailChallenge(ctx, c.ID) != nil || tx.Commit(ctx) != nil {
				return out, unavailable()
			}
			return out, bad
		}
	}
	hash, err := s.hashPassword(ctx, in.NewPassword)
	if err != nil {
		return out, err
	}
	id := uuid.New()
	sub := make([]byte, 16)
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	for i := range sub {
		n, _ := rand.Int(rand.Reader, big.NewInt(36))
		sub[i] = alphabet[n.Int64()]
	}
	err = q.AddAccount(ctx, store.AddAccountParams{ID: id, EmailKey: c.EmailKey, Locale: c.Locale, PasswordHash: hash, VerifiedAt: stamp(s.now()), VpnID: uuid.New(), SubID: string(sub), PanelKey: "acct_" + strings.ReplaceAll(id.String(), "-", ""), TermsVersion: c.TermsVersion, PrivacyVersion: c.PrivacyVersion})
	if err != nil {
		var existing bool
		s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE email_key=$1)`, email).Scan(&existing)
		if existing {
			return out, bad
		}
		return out, unavailable()
	}
	if q.ConsumeChallenge(ctx, c.ID) != nil || s.mail.ClearRegistrationMailTx(ctx, tx, email) != nil || tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	out.Verified = true
	return out, nil
}
