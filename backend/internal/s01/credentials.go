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
	"github.com/redis/go-redis/v9"
	"math/big"
	"slices"
	"time"
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
	return append(emails, account.EmailKey), nil
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
func (s *Service) addCredentialProof(ctx context.Context, tx pgx.Tx, id uuid.UUID, purpose string, accountID, changeID *uuid.UUID, original, target string, version int64, locale string) error {
	token := opaque()
	n, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		return unavailable()
	}
	code := fmt.Sprintf("%08d", n)
	now := s.now()
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
		if account.EmailKey == email {
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
	if err = s.addCredentialProof(ctx, tx, out.ChallengeId, "password_reset", accountID, nil, email, email, version, string(in.Locale)); err != nil {
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
	if proof.AccountID == nil || *proof.AccountID != account.ID || proof.OriginalEmail != account.EmailKey || proof.CredentialVersion != account.CredentialVersion || proof.Revoked || proof.UsedAt.Valid || !s.now().Before(proof.TokenExpiresAt.Time) {
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
	if err = s.enqueueSecurityNotice(ctx, tx, account.EmailKey, mailPayload{Type: "password_changed", Locale: account.Locale}); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}
