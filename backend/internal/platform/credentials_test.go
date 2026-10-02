package platform

import (
	"bytes"
	"context"
	"errors"
	"example.com/cabinet/backend/db"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

const resetPassword = "a different safe password ✨"

func credentialSecrets(t *testing.T, s *Service, e *testkit.Env, id uuid.UUID) (uuid.UUID, string, string) {
	t.Helper()
	return testkit.CredentialMailSecrets(t, e.Pool, s.cfg.MailKey, id)
}
func resetProof(t *testing.T, s *Service, e *testkit.Env, email string) (uuid.UUID, uuid.UUID, string, string) {
	t.Helper()
	e.Advance(time.Minute)
	out, err := s.RequestPasswordReset(context.Background(), wire.PasswordResetInput{Email: signup(email).Email, Locale: "en"}, "127.0.0.1")
	if err != nil {
		t.Fatal("reset request", status(err))
	}
	if out.ChallengeId == uuid.Nil || out.ResendAfter != 60 {
		t.Fatal("reset accepted shape")
	}
	mail, token, code := credentialSecrets(t, s, e, out.ChallengeId)
	return out.ChallengeId, mail, token, code
}

// A reset that only updates the password, keeps sessions, or replaces a foreign account fails this test.
func TestPasswordReset(t *testing.T) {
	for _, method := range []string{"token", "code"} {
		t.Run(method, func(t *testing.T) {
			s, e := fixture(t)
			ctx := context.Background()
			id := verified(t, s, e, "reset@example.test")
			login := wire.LoginInput{Email: "reset@example.test", Password: "my long safe password ✨"}
			_, raw, err := s.Login(ctx, login, "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			_, other, err := s.Login(ctx, login, "127.0.0.2")
			if err != nil {
				t.Fatal(err)
			}
			challenge, mail, token, code := resetProof(t, s, e, string(login.Email))
			if _, err = s.Authenticate(ctx, raw); err != nil {
				t.Fatal("request revoked session")
			}
			input := wire.PasswordResetCompleteInput{NewPassword: resetPassword}
			if method == "token" {
				input.Token = &token
			} else {
				input.ChallengeId = &challenge
				input.Code = &code
			}
			if err = s.CompletePasswordReset(ctx, input, "127.0.0.1"); err != nil {
				t.Fatal("reset completion", status(err))
			}
			for _, session := range []string{raw, other} {
				if _, err = s.Authenticate(ctx, session); status(err) != 401 {
					t.Fatal("session survived reset")
				}
			}
			if _, _, err = s.Login(ctx, login, "127.0.0.1"); status(err) != 401 {
				t.Fatal("old password accepted")
			}
			login.Password = resetPassword
			out, _, err := s.Login(ctx, login, "127.0.0.1")
			if err != nil || out.Account.AccountId != id {
				t.Fatal("new login ownership")
			}
			if err = s.CompletePasswordReset(ctx, input, "127.0.0.1"); status(err) != 400 {
				t.Fatal("used proof accepted")
			}
			var version int64
			var cipher []byte
			e.Pool.QueryRow(ctx, `SELECT credential_version FROM accounts WHERE id=$1`, id).Scan(&version)
			e.Pool.QueryRow(ctx, `SELECT ciphertext FROM mail_deliveries WHERE id=$1`, mail).Scan(&cipher)
			if version != 1 || len(cipher) != 0 {
				t.Fatal("version/proof secret cleanup")
			}
		})
	}
	t.Run("unknown and restricted", func(t *testing.T) {
		s, e := fixture(t)
		ctx := context.Background()
		id := verified(t, s, e, "restricted-reset@example.test")
		_, mail, token, _ := resetProof(t, s, e, "absent@example.test")
		smtp := testkit.MailServer(t)
		s.cfg.SMTPAddress = smtp.Address
		s.cfg.SMTPRootCAs = smtp.Roots
		s.cfg.SMTPFrom = "sender@example.test"
		if err := s.SendMail(ctx, mail); err != nil || len(smtp.Letters()) != 0 || count(t, e, "accounts") != 1 {
			t.Fatal("dummy reset sent SMTP or account")
		}
		if err := s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &token, NewPassword: resetPassword}, "127.0.0.1"); status(err) != 400 {
			t.Fatal("dummy proof accepted")
		}
		e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, id)
		_, _, token, _ = resetProof(t, s, e, "restricted-reset@example.test")
		if err := s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &token, NewPassword: resetPassword}, "127.0.0.1"); err != nil {
			t.Fatal("restricted reset rejected")
		}
		if _, _, err := s.Login(ctx, wire.LoginInput{Email: "restricted-reset@example.test", Password: resetPassword}, "127.0.0.1"); status(err) != 403 {
			t.Fatal("reset removed restriction")
		}
	})
}

// A wrong purpose, ignored TTL, reset resend that keeps an old link, or unlimited code guesses fails here.
func TestCredentialProofBoundary(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	verified(t, s, e, "proof@example.test")
	reg, err := s.Register(ctx, signup("foreign-purpose@example.test"))
	if err != nil {
		t.Fatal(err)
	}
	_, regToken, _ := secrets(t, s, e, reg.ChallengeId)
	if err = s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &regToken, NewPassword: resetPassword}, "127.0.0.1"); status(err) != 400 {
		t.Fatal("registration token accepted")
	}
	id, _, oldToken, _ := resetProof(t, s, e, "proof@example.test")
	id, _, token, code := resetProof(t, s, e, "proof@example.test")
	if err = s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &oldToken, NewPassword: resetPassword}, "127.0.0.1"); status(err) != 400 {
		t.Fatal("replaced reset accepted")
	}
	wrong := "00000000"
	if wrong == code {
		wrong = "99999999"
	}
	for range 5 {
		if err = s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{ChallengeId: &id, Code: &wrong, NewPassword: resetPassword}, "127.0.0.1"); status(err) != 400 {
			t.Fatal("wrong code accepted")
		}
	}
	if err = s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{ChallengeId: &id, Code: &code, NewPassword: resetPassword}, "127.0.0.1"); status(err) != 400 {
		t.Fatal("sixth code attempt accepted")
	}
	if err = s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &token, NewPassword: resetPassword}, "127.0.0.1"); err != nil {
		t.Fatal("code budget disabled token")
	}
	for _, ttl := range []time.Duration{10 * time.Minute, 30 * time.Minute} {
		id, _, token, code = resetProof(t, s, e, "proof@example.test")
		e.Advance(ttl)
		in := wire.PasswordResetCompleteInput{ChallengeId: &id, Code: &code, NewPassword: resetPassword}
		if ttl == 30*time.Minute {
			in.Token = &token
			in.Code = nil
			in.ChallengeId = nil
		}
		if err = s.CompletePasswordReset(ctx, in, "127.0.0.1"); status(err) != 400 {
			t.Fatal("expiry boundary accepted")
		}
	}
	e.Advance(time.Hour) // The proof-shape case gets a fresh legitimate hourly mail budget.
	id, _, token, code = resetProof(t, s, e, "proof@example.test")
	if err = s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &token, ChallengeId: &id, Code: &code, NewPassword: resetPassword}, "127.0.0.1"); status(err) != 400 {
		t.Fatal("both proof forms accepted")
	}
	e.Redis.Close()
	if _, err = s.RequestPasswordReset(ctx, wire.PasswordResetInput{Email: "proof@example.test", Locale: "en"}, "127.0.0.1"); status(err) != 503 {
		t.Fatal("Redis outage accepted known request")
	}
	if _, err = s.RequestPasswordReset(ctx, wire.PasswordResetInput{Email: "unknown@example.test", Locale: "en"}, "127.0.0.1"); status(err) != 503 {
		t.Fatal("Redis outage differs for unknown")
	}
}

// Registration cancelling all mail or credential cancellation cancelling notices fails here.
func TestCredentialMailIsolation(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	verified(t, s, e, "isolation@example.test")
	_, mail, token, _ := resetProof(t, s, e, "isolation@example.test")
	e.Advance(time.Minute)
	if _, err := s.Register(ctx, signup("isolation@example.test")); err != nil {
		t.Fatal(err)
	}
	var cipher []byte
	e.Pool.QueryRow(ctx, `SELECT ciphertext FROM mail_deliveries WHERE id=$1`, mail).Scan(&cipher)
	if len(cipher) == 0 {
		t.Fatal("registration cancelled credential mail")
	}
	if err := s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &token, NewPassword: resetPassword}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	e.Advance(time.Minute)
	if _, err := s.Register(ctx, signup("isolation@example.test")); err != nil {
		t.Fatal(err)
	}
	var notices int
	e.Pool.QueryRow(ctx, `SELECT count(*) FROM mail_deliveries WHERE kind='security_notice' AND ciphertext IS NOT NULL`).Scan(&notices)
	if notices != 1 {
		t.Fatal("registration cancelled security notice")
	}
	// A failed SMTP attempt keeps the proof payload; a successful retry clears it and does not resend.
	_, delivery, _, _ := resetProof(t, s, e, "isolation@example.test")
	smtp := testkit.MailServer(t)
	s.cfg.SMTPAddress = smtp.Address
	s.cfg.SMTPFrom = "sender@example.test"
	if err := s.SendMail(ctx, delivery); status(err) != 503 {
		t.Fatal("untrusted SMTP certificate accepted")
	}
	e.Pool.QueryRow(ctx, `SELECT ciphertext FROM mail_deliveries WHERE id=$1`, delivery).Scan(&cipher)
	if len(cipher) == 0 {
		t.Fatal("SMTP failure lost pending proof")
	}
	s.cfg.SMTPRootCAs = smtp.Roots
	for range 2 {
		if err := s.SendMail(ctx, delivery); err != nil {
			t.Fatal("SMTP retry failed")
		}
	}
	if len(smtp.Letters()) != 1 {
		t.Fatal("delivered proof was resent")
	}
}

func TestPasswordResetQueueFailure(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	verified(t, s, e, "queue-reset@example.test")
	id, _, _, _ := resetProof(t, s, e, "queue-reset@example.test")
	e.Advance(time.Minute)
	if _, err := e.Pool.Exec(ctx, `CREATE FUNCTION fail_reset_job() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test queue unavailable'; END $$; CREATE TRIGGER fail_reset_job BEFORE INSERT ON river_job FOR EACH ROW EXECUTE FUNCTION fail_reset_job();`); err != nil {
		t.Fatal("queue failure fixture")
	}
	for _, email := range []string{"queue-reset@example.test", "queue-absent@example.test"} {
		if _, err := s.RequestPasswordReset(ctx, wire.PasswordResetInput{Email: signup(email).Email, Locale: "en"}, "127.0.0.1"); status(err) != 503 {
			t.Fatal("queue failure not uniform")
		}
	}
	var revoked bool
	e.Pool.QueryRow(ctx, `SELECT revoked FROM credential_challenges WHERE id=$1`, id).Scan(&revoked)
	if revoked || count(t, e, "credential_challenges") != 1 {
		t.Fatal("queue error committed reset replacement")
	}
}

// A login that writes a session after its verified password/version became stale fails here.
func TestCredentialConcurrency(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	verified(t, s, e, "race@example.test")
	_, _, token, _ := resetProof(t, s, e, "race@example.test")
	loginService := NewService(s.pool, s.limiter, s.queue, s.cfg)
	hashed, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	defer close(release)
	var calls atomic.Int32
	loginService.now = func() time.Time {
		if calls.Add(1) == 2 {
			close(hashed)
			<-release
		}
		return e.Clock()
	}
	go func() {
		_, _, err := loginService.Login(ctx, wire.LoginInput{Email: "race@example.test", Password: "my long safe password ✨"}, "127.0.0.2")
		finished <- err
	}()
	select {
	case <-hashed:
	case <-time.After(10 * time.Second):
		t.Fatal("post-hash login barrier")
	}
	if err := s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &token, NewPassword: resetPassword}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	select {
	case err := <-finished:
		if status(err) != 401 {
			t.Fatal("stale login created session")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("login transaction did not finish")
	}
	if count(t, e, "sessions") != 0 {
		t.Fatal("stale session exists")
	}
	for range 4 {
		if _, _, err := s.Login(ctx, wire.LoginInput{Email: "race@example.test", Password: "wrong but long password"}, "127.0.0.2"); status(err) != 401 {
			t.Fatal("wrong password attempt")
		}
	}
	if _, _, err := s.Login(ctx, wire.LoginInput{Email: "race@example.test", Password: resetPassword}, "127.0.0.2"); status(err) != 429 {
		t.Fatal("stale credential failure escaped shared login budget")
	}
}

// An additive migration that destroys old sessions, encrypted letters or job identity fails here.
func TestAccountSecurityMigration(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("../../db/migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 5); err != nil {
		t.Fatal("empty isolated pre-security schema")
	}
	q := store.New(e.Pool)
	id, proof := uuid.New(), uuid.New()
	now := e.Clock()
	raw, token := opaque(), opaque()
	hash, err := s.hashPassword(ctx, "my long safe password ✨")
	if err != nil {
		t.Fatal(err)
	}
	// The fixture is intentionally at migration 5; current generated inserts use later columns.
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, "migration@example.test", "en", hash, now, uuid.New(), "0123456789abcdef", "acct_"+id.String(), "1", "1"); err != nil {
		t.Fatal(err)
	}
	if err = q.AddSession(ctx, store.AddSessionParams{IDHash: digest(raw), AccountID: id, CsrfToken: opaque(), CreatedAt: stamp(now), LastSeen: stamp(now), AbsoluteExpiresAt: stamp(now.Add(30 * 24 * time.Hour))}); err != nil {
		t.Fatal(err)
	}
	if err = q.AddChallenge(ctx, store.AddChallengeParams{ID: proof, EmailKey: "pending@example.test", Locale: "en", TermsVersion: "1", PrivacyVersion: "1", TokenHash: digest(token), CodeHash: s.codeDigest(proof, "12345678"), CreatedAt: stamp(now), TokenExpiresAt: stamp(now.Add(24 * time.Hour)), CodeExpiresAt: stamp(now.Add(10 * time.Minute))}); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = s.enqueueMail(ctx, tx, "pending@example.test", &proof, mailPayload{Type: "verify", Locale: "en", Token: token, Code: "12345678"}); err != nil || tx.Commit(ctx) != nil {
		t.Fatal("old mail/job seed")
	}
	var delivery uuid.UUID
	var before []byte
	var jobID int64
	e.Pool.QueryRow(ctx, `SELECT id,ciphertext FROM mail_deliveries`).Scan(&delivery, &before)
	e.Pool.QueryRow(ctx, `SELECT id FROM river_job WHERE kind='mail_delivery'`).Scan(&jobID)
	for range 2 {
		if err = db.Migrate(ctx, e.Pool); err != nil {
			t.Fatal(err)
		}
	}
	var after []byte
	var kind string
	var version int64
	var afterJob int64
	e.Pool.QueryRow(ctx, `SELECT ciphertext,kind FROM mail_deliveries WHERE id=$1`, delivery).Scan(&after, &kind)
	e.Pool.QueryRow(ctx, `SELECT credential_version FROM accounts WHERE id=$1`, id).Scan(&version)
	e.Pool.QueryRow(ctx, `SELECT id FROM river_job WHERE kind='mail_delivery'`).Scan(&afterJob)
	if !bytes.Equal(before, after) || kind != "registration" || version != 0 || afterJob != jobID || count(t, e, "sessions") != 1 {
		t.Fatal("migration changed registration data/jobs")
	}
	smtp := testkit.MailServer(t)
	s.cfg.SMTPAddress = smtp.Address
	s.cfg.SMTPRootCAs = smtp.Roots
	s.cfg.SMTPFrom = "sender@example.test"
	if err = s.SendMail(ctx, delivery); err != nil || len(smtp.Letters()) != 1 {
		t.Fatal("old job mail compatibility")
	}
}

func TestPasswordChange(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	id := verified(t, s, e, "change@example.test")
	login := wire.LoginInput{Email: "change@example.test", Password: "my long safe password ✨"}
	before, raw, err := s.Login(ctx, login, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := s.Login(ctx, login, "127.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	_, _, resetToken, _ := resetProof(t, s, e, "change@example.test")
	var expiry time.Time
	e.Pool.QueryRow(ctx, `SELECT absolute_expires_at FROM sessions WHERE id_hash=$1`, digest(raw)).Scan(&expiry)
	for _, in := range []wire.PasswordChangeInput{{CurrentPassword: "wrong long password", NewPassword: resetPassword}, {CurrentPassword: login.Password, NewPassword: login.Password}, {CurrentPassword: login.Password, NewPassword: "films+pic+galeries"}, {CurrentPassword: login.Password, NewPassword: "short"}} {
		if _, err = s.ChangePassword(ctx, raw, in, "127.0.0.1"); status(err) != 400 {
			t.Fatal("invalid change", status(err))
		}
	}
	var domain *Error
	_, err = s.ChangePassword(ctx, raw, wire.PasswordChangeInput{CurrentPassword: login.Password, NewPassword: "films+pic+galeries"}, "127.0.0.1")
	if !errors.As(err, &domain) || domain.Code != "COMMON_PASSWORD" {
		t.Fatal("common password code")
	}
	_, err = s.ChangePassword(ctx, raw, wire.PasswordChangeInput{CurrentPassword: "wrong long password", NewPassword: resetPassword}, "127.0.0.1")
	if !errors.As(err, &domain) || domain.Code != "CURRENT_PASSWORD_INVALID" {
		t.Fatal("current password code")
	}
	if _, err = s.ChangePassword(ctx, "", wire.PasswordChangeInput{CurrentPassword: login.Password, NewPassword: resetPassword}, "127.0.0.1"); status(err) != 401 {
		t.Fatal("missing session")
	}
	e.Advance(time.Hour)
	rotation, err := s.ChangePassword(ctx, raw, wire.PasswordChangeInput{CurrentPassword: login.Password, NewPassword: resetPassword}, "127.0.0.1")
	if err != nil || rotation.Raw == raw || !rotation.AbsoluteExpiresAt.Equal(expiry) {
		t.Fatal("rotation/absolute TTL", err)
	}
	for _, old := range []string{raw, other} {
		if _, err = s.Authenticate(ctx, old); status(err) != 401 {
			t.Fatal("copied old session alive")
		}
	}
	after, err := s.Authenticate(ctx, rotation.Raw)
	if err != nil || after.CsrfToken == before.CsrfToken || after.Account.AccountId != id {
		t.Fatal("new session/CSRF")
	}
	if err = s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &resetToken, NewPassword: login.Password}, "127.0.0.1"); status(err) != 400 {
		t.Fatal("old reset proof alive")
	}
	// Lost Set-Cookie: ordinary login with the new password recovers access.
	login.Password = resetPassword
	if _, _, err = s.Login(ctx, login, "127.0.0.3"); err != nil {
		t.Fatal("new login", err)
	}
	var version int64
	e.Pool.QueryRow(ctx, `SELECT credential_version FROM accounts WHERE id=$1`, id).Scan(&version)
	if version != 1 {
		t.Fatal("version")
	}
}
func TestRevokeOtherSessions(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	id := verified(t, s, e, "sessions@example.test")
	login := wire.LoginInput{Email: "sessions@example.test", Password: "my long safe password ✨"}
	_, raw, _ := s.Login(ctx, login, "127.0.0.1")
	_, other, _ := s.Login(ctx, login, "127.0.0.2")
	_, _, token, _ := resetProof(t, s, e, "sessions@example.test")
	var expiry time.Time
	e.Pool.QueryRow(ctx, `SELECT absolute_expires_at FROM sessions WHERE id_hash=$1`, digest(raw)).Scan(&expiry)
	info, err := s.GetAccountSecurity(ctx, raw)
	if err != nil || !info.HasOtherSessions || info.PendingEmailChange != nil {
		t.Fatal("session summary")
	}
	rotation, err := s.RevokeOtherSessions(ctx, raw, wire.CurrentPasswordInput{CurrentPassword: login.Password}, "127.0.0.1")
	if err != nil || !rotation.AbsoluteExpiresAt.Equal(expiry) {
		t.Fatal("rotation", err)
	}
	for _, old := range []string{raw, other} {
		if _, err = s.Authenticate(ctx, old); status(err) != 401 {
			t.Fatal("old session alive")
		}
	}
	info, err = s.GetAccountSecurity(ctx, rotation.Raw)
	if err != nil || info.HasOtherSessions {
		t.Fatal("other session count")
	}
	var version int64
	e.Pool.QueryRow(ctx, `SELECT credential_version FROM accounts WHERE id=$1`, id).Scan(&version)
	if version != 0 {
		t.Fatal("revocation changed credentials")
	}
	if err = s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &token, NewPassword: resetPassword}, "127.0.0.1"); err != nil {
		t.Fatal("revocation invalidated reset", err)
	}
}
func TestPasswordChangeSharedBudget(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	verified(t, s, e, "budget@example.test")
	login := wire.LoginInput{Email: "budget@example.test", Password: "my long safe password ✨"}
	_, raw, _ := s.Login(ctx, login, "127.0.0.1")
	for range 3 {
		if _, err := s.RevokeOtherSessions(ctx, raw, wire.CurrentPasswordInput{CurrentPassword: "wrong long password"}, "127.0.0.1"); status(err) != 400 {
			t.Fatal("wrong current password")
		}
	}
	for range 2 {
		if _, _, err := s.Login(ctx, wire.LoginInput{Email: login.Email, Password: "wrong long password"}, "127.0.0.2"); status(err) != 401 {
			t.Fatal("wrong login")
		}
	}
	if _, err := s.RevokeOtherSessions(ctx, raw, wire.CurrentPasswordInput{CurrentPassword: login.Password}, "127.0.0.3"); status(err) != 429 {
		t.Fatal("separate login/reauth budget")
	}
	e.Advance(15 * time.Minute)
	e.Redis.Close()
	if _, err := s.RevokeOtherSessions(ctx, raw, wire.CurrentPasswordInput{CurrentPassword: login.Password}, "127.0.0.1"); status(err) != 503 {
		t.Fatal("Redis down permitted check")
	}
}
