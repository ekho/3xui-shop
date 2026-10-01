package s01

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/argon2"
	"time"
	"unicode/utf8"
)

var dummyHash = func() string {
	salt := make([]byte, 16)
	hash := argon2.IDKey([]byte("dummy authentication work"), salt, 2, 19456, 1, 32)
	return "$argon2id$v=19$m=19456,t=2,p=1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash)
}()
var loginLimit = redis.NewScript(`
local now=tonumber(ARGV[1]);local retry=0
for i,key in ipairs(KEYS) do
 redis.call('ZREMRANGEBYSCORE',key,'-inf',now-900000)
 local limit=5;if i==2 then limit=30 end
 if redis.call('ZCARD',key)>=limit then local first=redis.call('ZRANGE',key,0,0,'WITHSCORES');retry=math.max(retry,math.ceil((900000-now+tonumber(first[2]))/1000)) end
end
if retry>0 then return retry end
for _,key in ipairs(KEYS) do redis.call('ZADD',key,now,ARGV[2]);redis.call('PEXPIRE',key,900000) end
return 0`)
var successfulLogin = redis.NewScript(`for _,key in ipairs(KEYS) do redis.call('ZREM',key,ARGV[1]) end;return 0`)

func (s *Service) loginKeys(email, ip string) []string {
	hash := func(value string) string {
		h := hmac.New(sha256.New, s.cfg.CodeKey)
		h.Write([]byte(value))
		return fmt.Sprintf("%x", h.Sum(nil))
	}
	prefix := "{" + s.cfg.RateNamespace + "}:login:"
	return []string{prefix + "email:" + hash(email), prefix + "ip:" + hash(ip)}
}
func (s *Service) Login(ctx context.Context, in wire.LoginInput, ip string) (wire.LoginResult, string, error) {
	out := wire.LoginResult{}
	email, err := normalizeEmail(string(in.Email))
	if err != nil {
		return out, "", err
	}
	length := utf8.RuneCountInString(in.Password)
	if !utf8.ValidString(in.Password) || length < 15 || length > 128 {
		return out, "", failure(400, "INVALID_INPUT")
	}
	attempt := uuid.NewString()
	keys := s.loginKeys(email, ip)
	limitCtx, done := context.WithTimeout(ctx, 2*time.Second)
	retry, err := loginLimit.Run(limitCtx, s.limiter, keys, s.now().UnixMilli(), attempt).Int()
	done()
	if err != nil {
		return out, "", unavailable()
	}
	if retry > 0 {
		return out, "", &Error{Status: 429, Code: "RATE_LIMITED", Message: "RATE_LIMITED", RetryAfter: retry}
	}
	account, err := store.New(s.pool).AccountByEmail(ctx, email)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, "", unavailable()
	}
	found := err == nil
	encoded := dummyHash
	if found {
		encoded = account.PasswordHash
	}
	matched, err := s.checkPassword(ctx, in.Password, encoded)
	if err != nil {
		return out, "", err
	}
	if !found || !matched {
		return out, "", failure(401, "INVALID_CREDENTIALS")
	}
	if account.Restricted {
		return out, "", failure(403, "ACCOUNT_RESTRICTED")
	}
	limitCtx, done = context.WithTimeout(ctx, 2*time.Second)
	err = successfulLogin.Run(limitCtx, s.limiter, keys, attempt).Err()
	done()
	if err != nil {
		return out, "", unavailable()
	}
	raw := opaque()
	csrf := opaque()
	now := s.now()
	err = store.New(s.pool).AddSession(ctx, store.AddSessionParams{IDHash: digest(raw), AccountID: account.ID, CsrfToken: csrf, CreatedAt: stamp(now), LastSeen: stamp(now), AbsoluteExpiresAt: stamp(now.Add(30 * 24 * time.Hour))})
	if err != nil {
		return out, "", unavailable()
	}
	return wire.LoginResult{Account: publicAccount(account), CsrfToken: csrf}, raw, nil
}
func publicAccount(a store.Account) wire.Account {
	return wire.Account{AccountId: a.ID, Email: openapi_types.Email(a.EmailKey), EmailVerified: true, Locale: wire.AccountLocale(a.Locale), TelegramLinked: false}
}
func (s *Service) Authenticate(ctx context.Context, raw string) (wire.AccountResult, error) {
	out := wire.AccountResult{}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return out, failure(401, "INVALID_CREDENTIALS")
	}
	q := store.New(s.pool)
	session, err := q.AuthenticateSession(ctx, store.AuthenticateSessionParams{IDHash: digest(raw), Now: stamp(s.now())})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return out, unavailable()
	}
	account, err := q.AccountByID(ctx, session.AccountID)
	if err != nil {
		return out, unavailable()
	}
	if account.Restricted {
		return out, failure(403, "ACCOUNT_RESTRICTED")
	}
	return wire.AccountResult{Account: publicAccount(account), CsrfToken: session.CsrfToken, Capabilities: wire.Capabilities{TrialAvailable: false}}, nil
}
func (s *Service) Logout(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	if err := store.New(s.pool).DeleteSession(ctx, digest(raw)); err != nil {
		return unavailable()
	}
	return nil
}
