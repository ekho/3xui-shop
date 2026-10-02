package s01

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/redis/go-redis/v9"
	"math/big"
	"slices"
	"time"
	"unicode/utf8"
)

var credentialIPLimit = redis.NewScript(`
local now=tonumber(ARGV[1]);local key=KEYS[1]
redis.call('ZREMRANGEBYSCORE',key,'-inf',now-900000)
if redis.call('ZCARD',key)>=30 then local first=redis.call('ZRANGE',key,0,0,'WITHSCORES');return math.max(1,math.ceil((900000-now+tonumber(first[2]))/1000)) end
redis.call('ZADD',key,now,ARGV[2]);redis.call('PEXPIRE',key,900000);return 0`)

func (s *Service) limitCredentialIP(ctx context.Context, ip string) error {
	ctx, end := context.WithTimeout(ctx, 2*time.Second)
	defer end()
	key := fmt.Sprintf("{%s}:credential-ip:%x", s.cfg.RateNamespace, s.codeDigest(uuid.Nil, ip))
	retry, err := credentialIPLimit.Run(ctx, s.limiter, []string{key}, s.now().UnixMilli(), uuid.NewString()).Int()
	if err != nil {
		return unavailable()
	}
	if retry > 0 {
		return &Error{Status: 429, Code: "RATE_LIMITED", Message: "RATE_LIMITED", RetryAfter: retry}
	}
	return nil
}

func (s *Service) lockCredentialEmails(ctx context.Context, tx pgx.Tx, emails []string) error {
	slices.Sort(emails)
	for _, email := range slices.Compact(emails) {
		if err := store.New(tx).LockRegistrationEmail(ctx, email); err != nil {
			return unavailable()
		}
	}
	return nil
}
func (s *Service) credentialEmails(ctx context.Context, tx pgx.Tx, account store.Account) ([]string, error) {
	emails, err := store.New(tx).CredentialRecipients(ctx, &account.ID)
	if err != nil {
		return nil, unavailable()
	}
	return append(emails, account.EmailKey.String), nil
}
func (s *Service) revokeCredentialProofs(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) error {
	q := store.New(tx)
	if q.RevokeCredentialProofs(ctx, &accountID) != nil || q.ClearRevokedCredentialMail(ctx, &accountID) != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) credentialAudit(ctx context.Context, tx pgx.Tx, id uuid.UUID, action string) error {
	if err := store.New(tx).AddAudit(ctx, store.AddAuditParams{ID: uuid.New(), CreatedAt: stamp(s.now()), Action: action, AccountID: id}); err != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) addCredentialProof(ctx context.Context, tx pgx.Tx, id uuid.UUID, purpose string, accountID, changeID *uuid.UUID, original, target string, version int64, locale string, now time.Time) error {
	token := opaque()
	n, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		return unavailable()
	}
	code := fmt.Sprintf("%08d", n)
	err = store.New(tx).AddCredentialProof(ctx, store.AddCredentialProofParams{ID: id, Purpose: purpose, AccountID: accountID, ChangeID: changeID, OriginalEmail: original, TargetEmail: target, CredentialVersion: version, TokenHash: digest(token), CodeHash: s.codeDigest(id, code), CreatedAt: stamp(now), TokenExpiresAt: stamp(now.Add(30 * time.Minute)), CodeExpiresAt: stamp(now.Add(10 * time.Minute))})
	if err != nil {
		return unavailable()
	}
	email := target
	if purpose == "email_change_old" {
		email = original
	}
	return s.enqueueCredentialMail(ctx, tx, email, id, mailPayload{Type: purpose, Locale: locale, Token: token, Code: code})
}

func (s *Service) RequestPasswordReset(ctx context.Context, in wire.PasswordResetInput, ip string) (wire.PasswordResetAccepted, error) {
	out := wire.PasswordResetAccepted{ChallengeId: uuid.New(), ResendAfter: 60}
	email, err := normalizeEmail(string(in.Email))
	if err != nil {
		return out, err
	}
	if in.Locale != "ru" && in.Locale != "en" {
		return out, failure(400, "INVALID_INPUT")
	}
	if err = s.limitCredentialIP(ctx, ip); err != nil {
		return out, err
	}
	if err = s.limitMail(ctx, email); err != nil {
		return out, err
	}
	account, err := store.New(s.pool).AccountByEmail(ctx, email)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, unavailable()
	}
	found := err == nil
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	var accountID *uuid.UUID
	var version int64
	emails := []string{email}
	if found {
		account, err = q.LockAccount(ctx, account.ID)
		if err != nil {
			return out, unavailable()
		}
		if account.EmailKey.String == email {
			accountID = &account.ID
			version = account.CredentialVersion
			emails, err = s.credentialEmails(ctx, tx, account)
			if err != nil {
				return out, err
			}
		}
	}
	if err = s.lockCredentialEmails(ctx, tx, emails); err != nil {
		return out, err
	}
	if q.RevokeResetProofs(ctx, email) != nil || q.ClearResetMail(ctx, email) != nil {
		return out, unavailable()
	}
	// Unknown recipients use the same proof/job transaction; the worker stops before SMTP.
	if err = s.addCredentialProof(ctx, tx, out.ChallengeId, "password_reset", accountID, nil, email, email, version, string(in.Locale), s.now()); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, unavailable()
	}
	return out, nil
}

func (s *Service) lookupCredential(ctx context.Context, token *string, id *uuid.UUID, code *string) (store.CredentialChallenge, bool, error) {
	bad := failure(400, "INVALID_INPUT")
	byToken := token != nil
	if byToken && (id != nil || code != nil) || !byToken && (id == nil || code == nil) {
		return store.CredentialChallenge{}, false, bad
	}
	q := store.New(s.pool)
	var proof store.CredentialChallenge
	var err error
	if byToken {
		if len(*token) != 43 {
			return proof, false, bad
		}
		proof, err = q.LookupCredentialByToken(ctx, digest(*token))
	} else {
		if *id == uuid.Nil || len(*code) != 8 {
			return proof, false, bad
		}
		for _, r := range *code {
			if r < '0' || r > '9' {
				return proof, false, bad
			}
		}
		proof, err = q.LookupCredentialByID(ctx, *id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return proof, false, failure(400, "INVALID_VERIFICATION")
	}
	if err != nil {
		return proof, false, unavailable()
	}
	return proof, byToken, nil
}
func (s *Service) checkCredentialProof(ctx context.Context, tx pgx.Tx, proof store.CredentialChallenge, account store.Account, byToken bool, code *string) error {
	bad := failure(400, "INVALID_VERIFICATION")
	if proof.AccountID == nil || *proof.AccountID != account.ID || proof.OriginalEmail != account.EmailKey.String || proof.CredentialVersion != account.CredentialVersion || proof.Revoked || proof.UsedAt.Valid || !s.now().Before(proof.TokenExpiresAt.Time) {
		return bad
	}
	if !byToken {
		if proof.FailedGuesses >= 5 || !s.now().Before(proof.CodeExpiresAt.Time) {
			return bad
		}
		if subtle.ConstantTimeCompare(proof.CodeHash, s.codeDigest(proof.ID, *code)) != 1 {
			if store.New(tx).FailCredentialCode(ctx, proof.ID) != nil || tx.Commit(ctx) != nil {
				return unavailable()
			}
			return bad
		}
	}
	return nil
}
func (s *Service) CompletePasswordReset(ctx context.Context, in wire.PasswordResetCompleteInput, ip string) error {
	if err := s.limitCredentialIP(ctx, ip); err != nil {
		return err
	}
	proof, byToken, err := s.lookupCredential(ctx, in.Token, in.ChallengeId, in.Code)
	if err != nil {
		return err
	}
	if proof.Purpose != "password_reset" || proof.AccountID == nil {
		return failure(400, "INVALID_VERIFICATION")
	}
	if err = validatePassword(in.NewPassword); err != nil {
		return err
	}
	hash, err := s.hashPassword(ctx, in.NewPassword)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	account, err := q.LockAccount(ctx, *proof.AccountID)
	if err != nil {
		return unavailable()
	}
	emails, err := s.credentialEmails(ctx, tx, account)
	if err != nil {
		return err
	}
	if err = s.lockCredentialEmails(ctx, tx, emails); err != nil {
		return err
	}
	proof, err = q.LockCredentialProof(ctx, proof.ID)
	if err != nil {
		return unavailable()
	}
	if err = s.checkCredentialProof(ctx, tx, proof, account, byToken, in.Code); err != nil {
		return err
	}
	if q.SetAccountPassword(ctx, store.SetAccountPasswordParams{ID: account.ID, PasswordHash: hash}) != nil || q.ConsumeCredentialProof(ctx, store.ConsumeCredentialProofParams{ID: proof.ID, UsedAt: stamp(s.now())}) != nil || q.DeleteAccountSessions(ctx, account.ID) != nil {
		return unavailable()
	}
	if err = s.revokeCredentialProofs(ctx, tx, account.ID); err != nil {
		return err
	}
	if err = s.credentialAudit(ctx, tx, account.ID, "password_reset"); err != nil {
		return err
	}
	if err = s.enqueueSecurityNotice(ctx, tx, account.EmailKey.String, mailPayload{Type: "password_changed", Locale: account.Locale}); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}

type SessionRotation struct {
	Raw               string
	AbsoluteExpiresAt time.Time
}

func (s *Service) reservePasswordAttempt(ctx context.Context, identity, ip string) ([]string, string, error) {
	keys, attempt := s.loginKeys(identity, ip), uuid.NewString()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	retry, err := loginLimit.Run(ctx, s.limiter, keys, s.now().UnixMilli(), attempt).Int()
	if err != nil {
		return nil, "", unavailable()
	}
	if retry > 0 {
		return nil, "", &Error{Status: 429, Code: "RATE_LIMITED", RetryAfter: retry}
	}
	return keys, attempt, nil
}
func (s *Service) finishPasswordAttempt(ctx context.Context, keys []string, attempt string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if successfulLogin.Run(ctx, s.limiter, keys, attempt).Err() != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) authenticateCurrentPassword(ctx context.Context, raw, password, ip string) (store.Account, store.Session, error) {
	q := store.New(s.pool)
	session, err := s.sessionByRaw(ctx, q, raw)
	if err != nil {
		return store.Account{}, session, err
	}
	account, err := q.AccountByID(ctx, session.AccountID)
	if err != nil {
		return account, session, unavailable()
	}
	if account.Restricted {
		return account, session, failure(403, "ACCOUNT_RESTRICTED")
	}
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 15 || utf8.RuneCountInString(password) > 128 {
		return account, session, failure(400, "INVALID_INPUT")
	}
	keys, attempt, err := s.reservePasswordAttempt(ctx, "id:"+account.ID.String(), ip)
	if err != nil {
		return account, session, err
	}
	matched, err := s.checkPassword(ctx, password, account.PasswordHash.String)
	if err != nil {
		return account, session, err
	}
	if !matched {
		return account, session, failure(400, "CURRENT_PASSWORD_INVALID")
	}
	if err = s.finishPasswordAttempt(ctx, keys, attempt); err != nil {
		return account, session, err
	}
	return account, session, nil
}

// Account lock must precede the session row lock for every credential mutation.
func (s *Service) revalidateCredentialSession(ctx context.Context, q *store.Queries, raw string, snapshot store.Account) (store.Account, store.Session, error) {
	account, err := q.LockAccount(ctx, snapshot.ID)
	if err != nil {
		return account, store.Session{}, unavailable()
	}
	session, err := s.sessionByRaw(ctx, q, raw)
	if err != nil {
		return account, session, err
	}
	if session.AccountID != snapshot.ID || account.CredentialVersion != snapshot.CredentialVersion || account.PasswordHash != snapshot.PasswordHash || account.EmailKey != snapshot.EmailKey {
		return account, session, failure(401, "INVALID_CREDENTIALS")
	}
	if account.Restricted {
		return account, session, failure(403, "ACCOUNT_RESTRICTED")
	}
	return account, session, nil
}
func (s *Service) ChangePassword(ctx context.Context, raw string, in wire.PasswordChangeInput, ip string) (SessionRotation, error) {
	account, _, err := s.authenticateCurrentPassword(ctx, raw, in.CurrentPassword, ip)
	if err != nil {
		return SessionRotation{}, err
	}
	if in.NewPassword == in.CurrentPassword {
		return SessionRotation{}, failure(400, "INVALID_INPUT")
	}
	if err = validatePassword(in.NewPassword); err != nil {
		return SessionRotation{}, err
	}
	hash, err := s.hashPassword(ctx, in.NewPassword)
	if err != nil {
		return SessionRotation{}, err
	}
	return s.rotateCredentialSession(ctx, raw, account, hash, "password_change")
}
func (s *Service) RevokeOtherSessions(ctx context.Context, raw string, in wire.CurrentPasswordInput, ip string) (SessionRotation, error) {
	account, _, err := s.authenticateCurrentPassword(ctx, raw, in.CurrentPassword, ip)
	if err != nil {
		return SessionRotation{}, err
	}
	return s.rotateCredentialSession(ctx, raw, account, "", "revoke_other_sessions")
}
func (s *Service) rotateCredentialSession(ctx context.Context, raw string, snapshot store.Account, passwordHash, action string) (SessionRotation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SessionRotation{}, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	account, session, err := s.revalidateCredentialSession(ctx, q, raw, snapshot)
	if err != nil {
		return SessionRotation{}, err
	}
	if passwordHash != "" {
		emails, err := s.credentialEmails(ctx, tx, account)
		if err != nil {
			return SessionRotation{}, err
		}
		if err = s.lockCredentialEmails(ctx, tx, emails); err != nil {
			return SessionRotation{}, err
		}
		if q.SetAccountPassword(ctx, store.SetAccountPasswordParams{ID: account.ID, PasswordHash: passwordHash}) != nil || s.revokeCredentialProofs(ctx, tx, account.ID) != nil {
			return SessionRotation{}, unavailable()
		}
		if s.enqueueSecurityNotice(ctx, tx, account.EmailKey.String, mailPayload{Type: "security_notice", Locale: account.Locale}) != nil {
			return SessionRotation{}, unavailable()
		}
	}
	rotation := SessionRotation{Raw: opaque(), AbsoluteExpiresAt: session.AbsoluteExpiresAt.Time}
	now := s.now()
	if q.DeleteAccountSessions(ctx, account.ID) != nil || q.AddSession(ctx, store.AddSessionParams{IDHash: digest(rotation.Raw), AccountID: account.ID, CsrfToken: opaque(), CreatedAt: stamp(now), LastSeen: stamp(now), AbsoluteExpiresAt: session.AbsoluteExpiresAt}) != nil || s.credentialAudit(ctx, tx, account.ID, action) != nil {
		return SessionRotation{}, unavailable()
	}
	if tx.Commit(ctx) != nil {
		return SessionRotation{}, unavailable()
	}
	return rotation, nil
}
func (s *Service) GetSessionContext(ctx context.Context, raw string) (wire.SessionContext, error) {
	session, err := s.sessionByRaw(ctx, store.New(s.pool), raw)
	if err != nil {
		return wire.SessionContext{}, err
	}
	return wire.SessionContext{CsrfToken: session.CsrfToken}, nil
}
func (s *Service) GetAccountSecurity(ctx context.Context, raw string) (wire.AccountSecurity, error) {
	q := store.New(s.pool)
	session, err := s.sessionByRaw(ctx, q, raw)
	if err != nil {
		return wire.AccountSecurity{}, err
	}
	account, err := q.AccountByID(ctx, session.AccountID)
	if err != nil {
		return wire.AccountSecurity{}, unavailable()
	}
	if account.Restricted {
		return wire.AccountSecurity{}, failure(403, "ACCOUNT_RESTRICTED")
	}
	other, err := q.HasOtherSessions(ctx, store.HasOtherSessionsParams{AccountID: account.ID, IDHash: digest(raw), Now: stamp(s.now())})
	if err != nil {
		return wire.AccountSecurity{}, unavailable()
	}
	pending, err := s.pendingEmailChange(ctx, q, account)
	if err != nil {
		return wire.AccountSecurity{}, err
	}
	return wire.AccountSecurity{Email: openapi_types.Email(account.EmailKey.String), HasOtherSessions: other, PendingEmailChange: pending}, nil
}
