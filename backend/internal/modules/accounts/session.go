package accounts

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"fmt"
	"github.com/jackc/pgx/v5"
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

func (s *Service) loginKeys(identity, ip string) []string {
	hash := func(value string) string {
		h := hmac.New(sha256.New, s.cfg.CodeKey)
		h.Write([]byte(value))
		return fmt.Sprintf("%x", h.Sum(nil))
	}
	prefix := "{" + s.cfg.RateNamespace + "}:login:"
	return []string{prefix + "account:" + hash(identity), prefix + "ip:" + hash(ip)}
}
func (s *Service) Login(ctx context.Context, in LoginInput, ip string) (LoginResult, string, error) {
	out := LoginResult{}
	email, err := normalizeEmail(string(in.Email))
	if err != nil {
		return out, "", err
	}
	length := utf8.RuneCountInString(in.Password)
	if !utf8.ValidString(in.Password) || length < 15 || length > 128 {
		return out, "", failure(400, "INVALID_INPUT")
	}
	account, err := store.New(s.pool).AccountByEmail(ctx, email)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, "", unavailable()
	}
	found := err == nil
	identity := "email:" + email
	if found {
		identity = "id:" + account.ID.String()
	}
	keys, attempt, err := s.reservePasswordAttempt(ctx, identity, ip)
	if err != nil {
		return out, "", err
	}
	encoded := dummyHash
	if found && account.Kind == "web" && account.PasswordHash.Valid {
		encoded = account.PasswordHash.String
	}
	matched, err := s.checkPassword(ctx, in.Password, encoded)
	if err != nil {
		return out, "", err
	}
	if !found || account.Kind != "web" || !account.EmailKey.Valid || !account.PasswordHash.Valid || !account.VerifiedAt.Valid || !matched {
		return out, "", failure(401, "INVALID_CREDENTIALS")
	}
	if account.Restricted {
		return out, "", failure(403, "ACCOUNT_RESTRICTED")
	}
	raw := opaque()
	csrf := opaque()
	now := s.now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, "", unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	current, err := q.LockAccount(ctx, account.ID)
	if err != nil {
		return out, "", unavailable()
	}
	if current.Kind != "web" || current.EmailKey.String != email || current.PasswordHash != account.PasswordHash || current.CredentialVersion != account.CredentialVersion {
		return out, "", failure(401, "INVALID_CREDENTIALS")
	}
	if current.Restricted {
		return out, "", failure(403, "ACCOUNT_RESTRICTED")
	}
	if err = s.finishPasswordAttempt(ctx, keys, attempt); err != nil {
		return out, "", err
	}
	err = q.AddSession(ctx, store.AddSessionParams{IDHash: digest(raw), AccountID: account.ID, CsrfToken: csrf, CreatedAt: stamp(now), LastSeen: stamp(now), AbsoluteExpiresAt: stamp(now.Add(30 * 24 * time.Hour))})
	if err != nil {
		return out, "", unavailable()
	}
	if err = tx.Commit(ctx); err != nil {
		return out, "", unavailable()
	}
	return LoginResult{Account: snapshot(account), CsrfToken: csrf}, raw, nil
}
func (s *Service) Authenticate(ctx context.Context, raw string) (Authentication, error) {
	out := Authentication{}
	q := store.New(s.pool)
	session, err := s.sessionByRaw(ctx, q, raw)
	if err != nil {
		return out, err
	}
	account, err := q.AccountByID(ctx, session.AccountID)
	if err != nil {
		return out, unavailable()
	}
	if account.Kind != "web" || !account.EmailKey.Valid || !account.PasswordHash.Valid || !account.VerifiedAt.Valid {
		return out, failure(401, "INVALID_CREDENTIALS")
	}
	if account.Restricted {
		return out, failure(403, "ACCOUNT_RESTRICTED")
	}
	return Authentication{Account: snapshot(account), CsrfToken: session.CsrfToken}, nil
}
func (s *Service) sessionByRaw(ctx context.Context, q *store.Queries, raw string) (store.Session, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return store.Session{}, failure(401, "INVALID_CREDENTIALS")
	}
	session, err := q.AuthenticateSession(ctx, store.AuthenticateSessionParams{IDHash: digest(raw), Now: stamp(s.now())})
	if errors.Is(err, pgx.ErrNoRows) {
		return session, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return session, unavailable()
	}
	if session.AuthSource != "web" {
		return session, failure(401, "INVALID_CREDENTIALS")
	}
	account, err := q.AccountByID(ctx, session.AccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return session, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return session, unavailable()
	}
	if account.Kind != "web" || !account.EmailKey.Valid || !account.PasswordHash.Valid || !account.VerifiedAt.Valid {
		return session, failure(401, "INVALID_CREDENTIALS")
	}
	return session, nil
}
func (s *Service) Logout(ctx context.Context, raw string) error {
	session, err := s.sessionByRaw(ctx, store.New(s.pool), raw)
	var domain *Error
	if errors.As(err, &domain) && domain.Status == 401 {
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if _, err = q.LockAccount(ctx, session.AccountID); err != nil {
		return unavailable()
	}
	if _, err = q.LookupLiveSession(ctx, store.LookupLiveSessionParams{IDHash: digest(raw), Now: stamp(s.now())}); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return unavailable()
	}
	if q.DeleteSession(ctx, digest(raw)) != nil || s.credentialAudit(ctx, tx, session.AccountID, "logout") != nil {
		return unavailable()
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}
