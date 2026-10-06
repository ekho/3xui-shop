package platform

import (
	"context"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"sync/atomic"
	"testing"
	"time"
)

type emailProof struct {
	ID, Mail    uuid.UUID
	Token, Code string
}

func emailPair(t *testing.T, s *Service, e *testkit.Env, raw, target string) (wire.EmailChangeAccepted, []emailProof) {
	t.Helper()
	e.Advance(time.Minute)
	out, err := s.RequestEmailChange(context.Background(), raw, wire.EmailChangeInput{NewEmail: signup(target).Email, CurrentPassword: "my long safe password ✨"}, "127.0.0.1")
	if err != nil {
		t.Fatal("pair request", status(err))
	}
	if out.ChangeId == uuid.Nil || out.ResendAfter != 60 {
		t.Fatal("pair accepted")
	}
	proofs := make([]emailProof, 2)
	var expires []time.Time
	rows, err := e.Pool.Query(context.Background(), `SELECT id,token_expires_at FROM credential_challenges WHERE change_id=$1 ORDER BY purpose`, out.ChangeId)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		var expiry time.Time
		if rows.Scan(&id, &expiry) != nil {
			t.Fatal("pair fixture")
		}
		proofs[len(expires)].ID = id
		expires = append(expires, expiry)
	}
	rows.Close()
	if len(expires) != 2 || !expires[0].Equal(expires[1]) || !out.ExpiresAt.Truncate(time.Microsecond).Equal(expires[0]) {
		t.Fatal("pair common expiry")
	}
	for i := range proofs {
		proofs[i].Mail, proofs[i].Token, proofs[i].Code = credentialSecrets(t, s, e, proofs[i].ID)
	}
	return out, proofs
}
func TestEmailChange(t *testing.T) {
	for _, order := range []int{0, 1} {
		t.Run([]string{"new-first", "old-first"}[order], func(t *testing.T) {
			s, e := fixture(t)
			ctx := context.Background()
			id := verified(t, s, e, "old@example.test")
			login := wire.LoginInput{Email: "old@example.test", Password: "my long safe password ✨"}
			_, raw, _ := s.Login(ctx, login, "127.0.0.1")
			_, other, _ := s.Login(ctx, login, "127.0.0.2")
			_, proofs := emailPair(t, s, e, raw, "New.Address+test@EXAMPLE.test")
			// Alphabetical purpose order is new then old; the other mailbox uses a code.
			first, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[order].Token}, "127.0.0.1")
			if err != nil || first.Completed {
				t.Fatal("one mailbox applied", err)
			}
			if _, _, err = s.Login(ctx, login, "127.0.0.3"); err != nil {
				t.Fatal("old login after first proof", err)
			}
			if _, err = s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[order].Token}, "127.0.0.2"); status(err) != 400 {
				t.Fatal("first proof reused")
			}
			last := proofs[1-order]
			second, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{ChallengeId: &last.ID, Code: &last.Code}, "127.0.0.2")
			if err != nil || !second.Completed {
				t.Fatal("pair not applied", err)
			}
			for _, old := range []string{raw, other} {
				if _, err = s.Authenticate(ctx, old); status(err) != 401 {
					t.Fatal("session survived email change")
				}
			}
			if _, _, err = s.Login(ctx, login, "127.0.0.3"); status(err) != 401 {
				t.Fatal("old email login")
			}
			login.Email = "new.address+test@example.test"
			out, _, err := s.Login(ctx, login, "127.0.0.3")
			if err != nil || out.Account.AccountId != id {
				t.Fatal("new email owner")
			}
			var version int64
			e.Pool.QueryRow(ctx, `SELECT credential_version FROM accounts WHERE id=$1`, id).Scan(&version)
			if version != 1 {
				t.Fatal("email version")
			}
			// A new signup at the freed old address gets a different identity, never the previous VPN.
			e.Advance(time.Hour)
			newID := verified(t, s, e, "old@example.test")
			if newID == id {
				t.Fatal("freed email inherited owner")
			}
			var same bool
			e.Pool.QueryRow(ctx, `SELECT a.vpn_id=b.vpn_id OR a.sub_id=b.sub_id OR a.panel_key=b.panel_key FROM accounts a,accounts b WHERE a.id=$1 AND b.id=$2`, id, newID).Scan(&same)
			if same {
				t.Fatal("freed email inherited VPN")
			}
		})
	}
	t.Run("cancel replace expiry", func(t *testing.T) {
		s, e := fixture(t)
		ctx := context.Background()
		verified(t, s, e, "old@example.test")
		_, raw, _ := s.Login(ctx, wire.LoginInput{Email: "old@example.test", Password: "my long safe password ✨"}, "127.0.0.1")
		_, first := emailPair(t, s, e, raw, "target@example.test")
		if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &first[0].Token}, "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
		_, replacement := emailPair(t, s, e, raw, "target@example.test")
		info, err := s.GetAccountSecurity(ctx, raw)
		if err != nil || info.PendingEmailChange == nil || info.PendingEmailChange.NewEmailConfirmed || info.PendingEmailChange.CurrentEmailConfirmed {
			t.Fatal("resend carried confirmation")
		}
		for _, proof := range first {
			if _, err = s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proof.Token}, "127.0.0.1"); status(err) != 400 {
				t.Fatal("old pair alive")
			}
		}
		for range 2 {
			if err = s.CancelEmailChange(ctx, raw); err != nil {
				t.Fatal("cancel", err)
			}
		}
		var audits int
		e.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='email_change_cancel'`).Scan(&audits)
		if audits != 1 {
			t.Fatal("non-idempotent cancel")
		}
		for _, proof := range replacement {
			if _, err = s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proof.Token}, "127.0.0.1"); status(err) != 400 {
				t.Fatal("canceled proof alive")
			}
		}
		_, last := emailPair(t, s, e, raw, "target@example.test")
		e.Advance(30 * time.Minute)
		if _, err = s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &last[0].Token}, "127.0.0.1"); status(err) != 400 {
			t.Fatal("expired pair")
		}
		info, err = s.GetAccountSecurity(ctx, raw)
		if err != nil || info.PendingEmailChange != nil {
			t.Fatal("expired pending visible")
		}
	})
	t.Run("code bounds and wrong purpose", func(t *testing.T) {
		s, e := fixture(t)
		ctx := context.Background()
		verified(t, s, e, "old@example.test")
		_, raw, _ := s.Login(ctx, wire.LoginInput{Email: "old@example.test", Password: "my long safe password ✨"}, "127.0.0.1")
		_, proofs := emailPair(t, s, e, raw, "target@example.test")
		wrong := "00000000"
		if wrong == proofs[0].Code {
			wrong = "11111111"
		}
		for range 5 {
			if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{ChallengeId: &proofs[0].ID, Code: &wrong}, "127.0.0.1"); status(err) != 400 {
				t.Fatal("wrong code")
			}
		}
		if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{ChallengeId: &proofs[0].ID, Code: &proofs[0].Code}, "127.0.0.1"); status(err) != 400 {
			t.Fatal("sixth code allowed")
		}
		if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[0].Token}, "127.0.0.1"); err != nil {
			t.Fatal("token lost to code guesses")
		}
		e.Advance(10 * time.Minute)
		if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{ChallengeId: &proofs[1].ID, Code: &proofs[1].Code}, "127.0.0.1"); status(err) != 400 {
			t.Fatal("expired code")
		}
		if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[1].Token}, "127.0.0.1"); err != nil {
			t.Fatal("valid token after code expiry", err)
		}
	})
	t.Run("unavailable and IDNA", func(t *testing.T) {
		s, e := fixture(t)
		ctx := context.Background()
		verified(t, s, e, "old@example.test")
		verified(t, s, e, "busy@example.test")
		_, raw, _ := s.Login(ctx, wire.LoginInput{Email: "old@example.test", Password: "my long safe password ✨"}, "127.0.0.1")
		for _, target := range []string{"old@example.test", "BUSY@example.test"} {
			if _, err := s.RequestEmailChange(ctx, raw, wire.EmailChangeInput{NewEmail: signup(target).Email, CurrentPassword: "my long safe password ✨"}, "127.0.0.1"); status(err) != 409 {
				t.Fatal("unavailable", status(err))
			}
		}
		_, proofs := emailPair(t, s, e, raw, "Dots.Plus+keep@пример.рф")
		info, err := s.GetAccountSecurity(ctx, raw)
		if err != nil || info.PendingEmailChange == nil || info.PendingEmailChange.NewEmail != "dots.plus+keep@xn--e1afmkfd.xn--p1ai" {
			t.Fatal("normalization")
		}
		if err = s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &proofs[0].Token, NewPassword: resetPassword}, "127.0.0.1"); status(err) != 400 {
			t.Fatal("email proof used as reset")
		}
	})
}
func TestEmailChangeOwnershipRace(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	id := verified(t, s, e, "old@example.test")
	login := wire.LoginInput{Email: "old@example.test", Password: "my long safe password ✨"}
	_, raw, _ := s.Login(ctx, login, "127.0.0.1")
	_, proofs := emailPair(t, s, e, raw, "target@example.test")
	if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[1].Token}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	e.Advance(time.Minute)
	foreign := verified(t, s, e, "target@example.test")
	if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[0].Token}, "127.0.0.1"); status(err) != 409 {
		t.Fatal("target conflict", status(err))
	}
	info, err := s.GetAccountSecurity(ctx, raw)
	if err != nil || info.PendingEmailChange != nil || info.Email != login.Email {
		t.Fatal("conflict not committed as canceled")
	}
	if out, _, err := s.Login(ctx, login, "127.0.0.1"); err != nil || out.Account.AccountId != id {
		t.Fatal("old owner lost")
	}
	var address string
	e.Pool.QueryRow(ctx, `SELECT email_key FROM accounts WHERE id=$1`, foreign).Scan(&address)
	if address != "target@example.test" {
		t.Fatal("foreign owner changed")
	}
}
func TestEmailChangeMailBudget(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	verified(t, s, e, "old@example.test")
	_, raw, _ := s.Login(ctx, wire.LoginInput{Email: "old@example.test", Password: "my long safe password ✨"}, "127.0.0.1")
	e.Advance(time.Minute)
	if _, err := s.Register(ctx, signup("target@example.test")); err != nil {
		t.Fatal(err)
	}
	mails, jobs := count(t, e, "mail_deliveries"), count(t, e, "river_job")
	if _, err := s.RequestEmailChange(ctx, raw, wire.EmailChangeInput{NewEmail: "target@example.test", CurrentPassword: "my long safe password ✨"}, "127.0.0.1"); status(err) != 429 {
		t.Fatal("target mail cooldown", status(err))
	}
	if count(t, e, "credential_challenges") != 0 || count(t, e, "mail_deliveries") != mails || count(t, e, "river_job") != jobs {
		t.Fatal("half pair/jobs")
	}
	if _, err := s.Register(ctx, signup("old@example.test")); err != nil {
		t.Fatal("limited pair consumed old budget", err)
	}
	for range 3 {
		emailPair(t, s, e, raw, "other@example.test")
	}
	e.Advance(time.Minute)
	if _, err := s.RequestEmailChange(ctx, raw, wire.EmailChangeInput{NewEmail: "third@example.test", CurrentPassword: "my long safe password ✨"}, "127.0.0.1"); status(err) != 429 {
		t.Fatal("shared hourly mail budget")
	}
}

func TestEmailChangeConcurrency(t *testing.T) {
	for _, mutation := range []string{"password", "reset", "cancel", "restriction"} {
		t.Run(mutation, func(t *testing.T) {
			s, e := fixture(t)
			ctx := context.Background()
			id := verified(t, s, e, "old@example.test")
			_, raw, _ := s.Login(ctx, wire.LoginInput{Email: "old@example.test", Password: "my long safe password ✨"}, "127.0.0.1")
			var resetToken string
			if mutation == "reset" {
				_, _, resetToken, _ = resetProof(t, s, e, "old@example.test")
			}
			var operator uuid.UUID
			if mutation == "restriction" {
				operator = verified(t, s, e, "mail-operator@example.test")
				if err := s.ChangeOperatorRole(ctx, operator, true); err != nil {
					t.Fatal(err)
				}
			}
			_, proofs := emailPair(t, s, e, raw, "aaa-target@example.test")
			smtp := testkit.MailServer(t)
			s.cfg.SMTPAddress, s.cfg.SMTPRootCAs, s.cfg.SMTPFrom = smtp.Address, smtp.Roots, "sender@example.test"
			entered, release := smtp.HoldNextData()
			defer release()
			sent := make(chan error, 1)
			go func() { sent <- s.SendMail(ctx, proofs[0].Mail) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("SMTP did not reach DATA")
			}
			// The worker serializes the recipient, but must not hold account during SMTP.
			probe, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			_, err = s.accounts.Lock(short, probe, id)
			cancel()
			probe.Rollback(ctx)
			if err != nil {
				t.Fatal("SMTP worker holds account lock")
			}
			finished := make(chan error, 1)
			go func() {
				var err error
				switch mutation {
				case "password":
					_, err = s.ChangePassword(ctx, raw, wire.PasswordChangeInput{CurrentPassword: "my long safe password ✨", NewPassword: resetPassword}, "127.0.0.1")
				case "reset":
					err = s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &resetToken, NewPassword: resetPassword}, "127.0.0.1")
				case "cancel":
					err = s.CancelEmailChange(ctx, raw)
				case "restriction":
					_, err = s.SetOperatorRestriction(ctx, operator, id, uuid.New(), wire.OperatorRestrictionInput{Restricted: true, Reason: "mail guard ordering"})
				}
				finished <- err
			}()
			select {
			case <-finished:
				t.Fatal("mutation skipped pending recipient SMTP lock")
			case <-time.After(100 * time.Millisecond):
			}
			release()
			for _, done := range []<-chan error{sent, finished} {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal("SMTP/mutation", status(err))
					}
				case <-time.After(5 * time.Second):
					t.Fatal("credential lock deadlock")
				}
			}
			if len(smtp.Letters()) != 1 {
				t.Fatal("unexpected SMTP retries")
			}
			wantStatus := 400
			if mutation == "restriction" {
				wantStatus = 403
			}
			for _, proof := range proofs {
				if _, err = s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proof.Token}, "127.0.0.1"); status(err) != wantStatus {
					t.Fatal("canceled email proof survived")
				}
			}
		})
	}
	t.Run("stale login and stable budget", func(t *testing.T) {
		s, e := fixture(t)
		ctx := context.Background()
		verified(t, s, e, "old@example.test")
		login := wire.LoginInput{Email: "old@example.test", Password: "my long safe password ✨"}
		_, raw, _ := s.Login(ctx, login, "127.0.0.1")
		_, proofs := emailPair(t, s, e, raw, "target@example.test")
		if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[0].Token}, "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
		stalled := NewService(e.Pool, e.Redis, s.queue, s.cfg)
		entered, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		stalled.now = func() time.Time {
			if calls.Add(1) == 2 {
				close(entered)
				<-release
			}
			return e.Clock()
		}
		finished := make(chan error, 1)
		go func() { _, _, err := stalled.Login(ctx, login, "127.0.0.2"); finished <- err }()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("login barrier")
		}
		if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[1].Token}, "127.0.0.1"); err != nil {
			close(release)
			t.Fatal(err)
		}
		close(release)
		select {
		case err := <-finished:
			if status(err) != 401 {
				t.Fatal("stale email login")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("login blocked")
		}
		for range 4 {
			if _, _, err := s.Login(ctx, wire.LoginInput{Email: "target@example.test", Password: "wrong long password"}, "127.0.0.2"); status(err) != 401 {
				t.Fatal("wrong login")
			}
		}
		if _, _, err := s.Login(ctx, wire.LoginInput{Email: "target@example.test", Password: login.Password}, "127.0.0.2"); status(err) != 429 {
			t.Fatal("email rename reset account failure budget")
		}
	})
}

func TestEmailChangeRevokeOthersPreservesPair(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	verified(t, s, e, "old@example.test")
	_, raw, _ := s.Login(ctx, wire.LoginInput{Email: "old@example.test", Password: "my long safe password ✨"}, "127.0.0.1")
	_, proofs := emailPair(t, s, e, raw, "target@example.test")
	if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[0].Token}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	rotation, err := s.RevokeOtherSessions(ctx, raw, wire.CurrentPasswordInput{CurrentPassword: "my long safe password ✨"}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.GetAccountSecurity(ctx, rotation.Raw)
	if err != nil || info.PendingEmailChange == nil || !info.PendingEmailChange.NewEmailConfirmed {
		t.Fatal("revocation lost pending pair")
	}
	final, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[1].Token}, "127.0.0.1")
	if err != nil || !final.Completed {
		t.Fatal("pending pair not applicable")
	}
}
func TestEmailChangeQueueRollback(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	verified(t, s, e, "old@example.test")
	_, raw, _ := s.Login(ctx, wire.LoginInput{Email: "old@example.test", Password: "my long safe password ✨"}, "127.0.0.1")
	_, proofs := emailPair(t, s, e, raw, "first@example.test")
	if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[0].Token}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	mails, jobs := count(t, e, "mail_deliveries"), count(t, e, "river_job")
	_, err := e.Pool.Exec(ctx, `CREATE FUNCTION fail_second_email_job() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF EXISTS(SELECT 1 FROM mail_deliveries m JOIN credential_challenges c ON m.credential_challenge_id=c.id WHERE m.id=(NEW.args->>'delivery_id')::uuid AND c.purpose='email_change_new') THEN RAISE EXCEPTION 'controlled second job failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_second_email_job BEFORE INSERT ON river_job FOR EACH ROW EXECUTE FUNCTION fail_second_email_job();`)
	if err != nil {
		t.Fatal("queue fixture")
	}
	e.Advance(time.Minute)
	_, err = s.RequestEmailChange(ctx, raw, wire.EmailChangeInput{NewEmail: "second@example.test", CurrentPassword: "my long safe password ✨"}, "127.0.0.1")
	if status(err) != 503 {
		t.Fatal("second job failure accepted", status(err))
	}
	if count(t, e, "credential_challenges") != 2 || count(t, e, "mail_deliveries") != mails || count(t, e, "river_job") != jobs {
		t.Fatal("half pair committed")
	}
	info, err := s.GetAccountSecurity(ctx, raw)
	if err != nil || info.PendingEmailChange == nil || info.PendingEmailChange.NewEmail != "first@example.test" || !info.PendingEmailChange.NewEmailConfirmed {
		t.Fatal("failed resend replaced prior pair")
	}
}
func TestEmailChangeRestriction(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	id := verified(t, s, e, "old@example.test")
	_, raw, _ := s.Login(ctx, wire.LoginInput{Email: "old@example.test", Password: "my long safe password ✨"}, "127.0.0.1")
	_, proofs := emailPair(t, s, e, raw, "target@example.test")
	e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, id)
	if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[0].Token}, "127.0.0.1"); status(err) != 403 {
		t.Fatal("restriction bypass")
	}
}

func TestEmailChangeConstraintConflict(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	verified(t, s, e, "old@example.test")
	_, raw, _ := s.Login(ctx, wire.LoginInput{Email: "old@example.test", Password: "my long safe password ✨"}, "127.0.0.1")
	_, proofs := emailPair(t, s, e, raw, "target@example.test")
	if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[0].Token}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	// Same PostgreSQL error as a unique constraint raised at UPDATE, after the availability read.
	if _, err := e.Pool.Exec(ctx, `CREATE FUNCTION late_email_unique() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.email_key<>OLD.email_key THEN RAISE EXCEPTION 'controlled unique conflict' USING ERRCODE='23505'; END IF; RETURN NEW; END $$; CREATE TRIGGER late_email_unique BEFORE UPDATE ON accounts FOR EACH ROW EXECUTE FUNCTION late_email_unique();`); err != nil {
		t.Fatal("late conflict fixture")
	}
	if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &proofs[1].Token}, "127.0.0.1"); status(err) != 409 {
		t.Fatal("late conflict", status(err))
	}
	info, err := s.GetAccountSecurity(ctx, raw)
	if err != nil || info.Email != "old@example.test" || info.PendingEmailChange != nil {
		t.Fatal("aborted transaction did not commit cancellation")
	}
	var live int
	e.Pool.QueryRow(ctx, `SELECT count(*) FROM credential_challenges WHERE NOT revoked AND used_at IS NULL`).Scan(&live)
	if live != 0 {
		t.Fatal("pair survived late conflict")
	}
}
