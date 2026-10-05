package platform

import (
	"bytes"
	"context"
	"os"
	"testing"

	"example.com/cabinet/backend/internal/wire"
)

func TestAccountSecurityRestore(t *testing.T) {
	s, e := fixture(t)
	panelFixture(t, s)
	ctx := context.Background()
	account, operation := approved(t, s, e, "restore@example.test")
	if err := s.Provision(ctx, operation); err != nil {
		t.Fatal("grant fixture")
	}
	login := wire.LoginInput{Email: "restore@example.test", Password: "my long safe password ✨"}
	_, raw, err := s.Login(ctx, login, "127.0.0.1")
	if err != nil {
		t.Fatal("owner login")
	}
	_, other, err := s.Login(ctx, login, "127.0.0.2")
	if err != nil {
		t.Fatal("other login")
	}
	_, pair := emailPair(t, s, e, raw, "restore-target@example.test")
	if result, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{Token: &pair[0].Token}, "127.0.0.1"); err != nil || result.Completed {
		t.Fatal("first mailbox fixture")
	}
	_, _, reset, _ := resetProof(t, s, e, "restore@example.test")
	resetProof(t, s, e, "restore-unknown@example.test")
	if _, err := s.Register(ctx, signup("restore-registration@example.test")); err != nil {
		t.Fatal("registration mail fixture")
	}
	seedSecurityNotice(t, s, e, string(login.Email))
	// An old backup can reintroduce a cookie that was revoked after that backup.
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE fixture_saved_sessions AS TABLE sessions`); err != nil {
		t.Fatal("backup sessions")
	}
	if s.Logout(ctx, other) != nil {
		t.Fatal("pre-restore revocation")
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO sessions SELECT * FROM fixture_saved_sessions ON CONFLICT DO NOTHING; DROP TABLE fixture_saved_sessions`); err != nil {
		t.Fatal("restore sessions")
	}
	unchanged := func() []byte {
		t.Helper()
		var state []byte
		err := e.Pool.QueryRow(ctx, `SELECT jsonb_build_object(
		 'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a),
		 'grants',(SELECT jsonb_agg(to_jsonb(g) ORDER BY account_id) FROM trial_grants g),
		 'operations',(SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM trial_operations o),
		 'jobs',(SELECT jsonb_agg(id ORDER BY id) FROM river_job),
		 'other_mail',(SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM mail_deliveries m WHERE kind<>'credential'))`).Scan(&state)
		if err != nil {
			t.Fatal("ownership/job snapshot")
		}
		return state
	}
	before := unchanged()
	maintenance, err := os.ReadFile("../../db/maintenance/post_restore_auth.sql")
	if err != nil {
		t.Fatal("maintenance source")
	}
	for range 2 {
		if _, err := e.Pool.Exec(ctx, string(maintenance)); err != nil {
			t.Fatal("maintenance failed")
		}
		if count(t, e, "sessions") != 0 {
			t.Fatal("restored sessions survived maintenance")
		}
		var live, ciphertext int
		if e.Pool.QueryRow(ctx, `SELECT count(*) FROM credential_challenges WHERE NOT revoked AND used_at IS NULL`).Scan(&live) != nil ||
			e.Pool.QueryRow(ctx, `SELECT count(*) FROM mail_deliveries WHERE kind='credential' AND ciphertext IS NOT NULL`).Scan(&ciphertext) != nil || live != 0 || ciphertext != 0 {
			t.Fatal("restored credential proofs or encrypted payload survived")
		}
		if !bytes.Equal(before, unchanged()) {
			t.Fatal("maintenance changed account/VPN, notice or job identity")
		}
	}
	for _, old := range []string{raw, other} {
		if _, err := s.Authenticate(ctx, old); status(err) != 401 {
			t.Fatal("restored cookie authenticated")
		}
	}
	if err := s.CompletePasswordReset(ctx, wire.PasswordResetCompleteInput{Token: &reset, NewPassword: resetPassword}, "127.0.0.1"); status(err) != 400 {
		t.Fatal("restored reset proof accepted")
	}
	for _, proof := range pair {
		if _, err := s.ConfirmEmailChange(ctx, wire.EmailChangeConfirmInput{ChallengeId: &proof.ID, Code: &proof.Code}, "127.0.0.1"); status(err) != 400 {
			t.Fatal("restored email proof accepted")
		}
	}
	result, fresh, err := s.Login(ctx, login, "127.0.0.3")
	if err != nil || result.Account.AccountId != account || fresh == raw || fresh == other || count(t, e, "sessions") != 1 {
		t.Fatal("backup credentials did not issue a new owner session")
	}
}
