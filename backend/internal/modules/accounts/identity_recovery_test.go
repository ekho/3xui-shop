package accounts

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func identityRecoveryFixture(t *testing.T, s *Service, e *testkit.Env, cfg fixtureConfig) (uuid.UUID, string, Authentication, string) {
	t.Helper()
	actor, raw := identityWebFixture(t, s, e, cfg, "recovery-operator@example.test")
	if err := s.ChangeOperatorRole(context.Background(), actor.Account.ID, true); err != nil {
		t.Fatal(err)
	}
	target, mini := identityMiniFixture(t, s, 9501)
	return actor.Account.ID, raw, target, mini
}
func identityRecoveryRequest(email string) OperatorRecoveryInput {
	return OperatorRecoveryInput{Email: email, CurrentPassword: identityWebPassword, Reason: " Support verified the owner ", Confirmed: true}
}
func identityRecoveryComplete(t *testing.T, e *testkit.Env, cfg fixtureConfig, proof IdentityRecoveryAccepted) IdentityRecoveryCompleteInput {
	t.Helper()
	_, _, code := testkit.CredentialMailSecrets(t, e.Pool, cfg.MailKey, proof.ChallengeId)
	return IdentityRecoveryCompleteInput{ChallengeId: &proof.ChallengeId, Code: &code, NewPassword: identityWebPassword, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"}
}
func TestIdentityRecoveryIdempotency(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	actor, raw, target, _ := identityRecoveryFixture(t, s, e, cfg)
	key := uuid.NewString()
	in := identityRecoveryRequest("recovered@example.test")
	first, err := s.RequestOperatorRecovery(ctx, raw, actor, target.Account.ID, key, in, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.RequestOperatorRecovery(ctx, raw, actor, target.Account.ID, key, in, "127.0.0.1")
	if err != nil || second.ChallengeId != first.ChallengeId || !second.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatal("replay did not keep one proof", err)
	}
	wrong := in
	wrong.CurrentPassword = "wrong current long password"
	if _, err = s.RequestOperatorRecovery(ctx, raw, actor, target.Account.ID, key, wrong, "127.0.0.1"); !hasStatus(err, 400) {
		t.Fatal("replay skipped current password")
	}
	other := in
	other.Reason = "different justification"
	if _, err = s.RequestOperatorRecovery(ctx, raw, actor, target.Account.ID, key, other, "127.0.0.1"); !hasStatus(err, 409) {
		t.Fatal("conflicting replay accepted")
	}
	var count int
	var reason string
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM credential_challenges WHERE account_id=$1 AND purpose='identity_recovery'`, target.Account.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate recovery proof", err)
	}
	if err = e.Pool.QueryRow(ctx, `SELECT reason FROM audit_events WHERE account_id=$1 AND action='identity_recovery_requested'`, target.Account.ID).Scan(&reason); err != nil || reason != strings.TrimSpace(in.Reason) || strings.Contains(reason, identityWebPassword) {
		t.Fatal("recovery audit reason/actor unsafe", err)
	}
	var response string
	if err = e.Pool.QueryRow(ctx, `SELECT result::text FROM idempotency_records WHERE principal=$1 AND operation='requestOperatorRecovery'`, "operator-account:"+actor.String()).Scan(&response); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response, identityWebPassword) || strings.Contains(response, "token") {
		t.Fatal("credentials entered durable replay result")
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(response), &decoded) != nil {
		t.Fatal("invalid replay result")
	}
}

func TestIdentityRecoveryOperatorRevoked(t *testing.T) {
	for _, scenario := range []string{"revoked", "revoked_then_regranted", "restricted", "expired", "replaced"} {
		t.Run(scenario, func(t *testing.T) {
			s, e, cfg := fixture(t)
			ctx := context.Background()
			actor, raw, target, mini := identityRecoveryFixture(t, s, e, cfg)
			proof, err := s.RequestOperatorRecovery(ctx, raw, actor, target.Account.ID, uuid.NewString(), identityRecoveryRequest("recover@example.test"), "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			complete := identityRecoveryComplete(t, e, cfg, proof)
			switch scenario {
			case "revoked", "revoked_then_regranted":
				if err = s.ChangeOperatorRole(ctx, actor, false); err != nil {
					t.Fatal(err)
				}
				if scenario == "revoked_then_regranted" {
					if err = s.ChangeOperatorRole(ctx, actor, true); err != nil {
						t.Fatal(err)
					}
				}
			case "restricted":
				if _, err = e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, actor); err != nil {
					t.Fatal(err)
				}
			case "expired":
				e.Advance(31 * time.Minute)
			case "replaced":
				e.Advance(61 * time.Second)
				if _, err = s.RequestOperatorRecovery(ctx, raw, actor, target.Account.ID, uuid.NewString(), identityRecoveryRequest("replacement@example.test"), "127.0.0.1"); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			valid, err := s.MailProofValidTx(ctx, tx, nil, &proof.ChallengeId)
			tx.Rollback(ctx)
			if err != nil || valid {
				t.Fatal("obsolete authorization allowed delivery", err)
			}
			if _, err = s.CompleteIdentityRecovery(ctx, complete, "127.0.0.2"); err == nil {
				t.Fatal("obsolete recovery granted")
			}
			if _, err = s.AuthenticateTelegram(ctx, mini, false); !hasStatus(err, 401) {
				t.Fatal("old Mini restored after recovery failure")
			}
			var disabled bool
			if err = e.Pool.QueryRow(ctx, `SELECT telegram_login_disabled FROM accounts WHERE id=$1`, target.Account.ID).Scan(&disabled); err != nil || !disabled {
				t.Fatal("quarantine lost", err)
			}
		})
	}
}
func TestIdentityRecoveryRequestGuards(t *testing.T) {
	for _, scenario := range []string{"occupied", "no_confirmation", "empty_reason", "wrong_password", "self", "independent_target", "wrong_actor", "protected_target"} {
		t.Run(scenario, func(t *testing.T) {
			s, e, cfg := fixture(t)
			ctx := context.Background()
			actor, raw, target, mini := identityRecoveryFixture(t, s, e, cfg)
			in := identityRecoveryRequest("recover@example.test")
			targetID := target.Account.ID
			want := 400
			switch scenario {
			case "occupied":
				identityWebFixture(t, s, e, cfg, in.Email)
				want = 409
			case "no_confirmation":
				in.Confirmed = false
			case "empty_reason":
				in.Reason = " "
			case "wrong_password":
				in.CurrentPassword = "wrong current long password"
			case "self":
				targetID = actor
				want = 403
			case "independent_target":
				w, _ := identityWebFixture(t, s, e, cfg, "other@example.test")
				targetID = w.Account.ID
				want = 409
			case "wrong_actor":
				actor = uuid.New()
				want = 401
			case "protected_target":
				if _, err := e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, targetID, e.Clock()); err != nil {
					t.Fatal(err)
				}
				want = 403
			}
			if _, err := s.RequestOperatorRecovery(ctx, raw, actor, targetID, uuid.NewString(), in, "127.0.0.1"); !hasStatus(err, want) {
				t.Fatalf("want%d got%v", want, err)
			}
			if _, err := s.AuthenticateTelegram(ctx, mini, false); err != nil {
				t.Fatal("denied request mutated target", err)
			}
		})
	}
}

func TestIdentityRecoveryQuarantineRace(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	actor, raw, target, mini := identityRecoveryFixture(t, s, e, cfg)
	key := uuid.NewString()
	in := identityRecoveryRequest("race-recovery@example.test")
	start := make(chan struct{})
	loginDone := make(chan error, 1)
	recoveryDone := make(chan error, 1)
	go func() {
		<-start
		_, _, err := s.StartTelegramSession(ctx, TelegramSessionInput{TelegramInput: TelegramInput{TelegramID: 9501, DisplayName: "Racing old Telegram", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
		loginDone <- err
	}()
	go func() {
		<-start
		_, err := s.RequestOperatorRecovery(ctx, raw, actor, target.Account.ID, key, in, "127.0.0.1")
		recoveryDone <- err
	}()
	close(start)
	loginErr, recoveryErr := <-loginDone, <-recoveryDone
	if loginErr != nil && !hasStatus(loginErr, 409) && !hasStatus(loginErr, 401) {
		t.Fatal("unexpected racing login", loginErr)
	}
	if hasStatus(recoveryErr, 409) {
		_, recoveryErr = s.RequestOperatorRecovery(ctx, raw, actor, target.Account.ID, key, in, "127.0.0.1")
	}
	if recoveryErr != nil {
		t.Fatal("recovery after known advisory conflict", recoveryErr)
	}
	if _, err := s.AuthenticateTelegram(ctx, mini, false); !hasStatus(err, 401) {
		t.Fatal("pre-quarantine session survived")
	}
	if _, _, err := s.StartTelegramSession(ctx, TelegramSessionInput{TelegramInput: TelegramInput{TelegramID: 9501, DisplayName: "Old Telegram", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"}); !hasStatus(err, 401) {
		t.Fatal("quarantine allowed a new login")
	}
	var count int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE telegram_id=9501`).Scan(&count); err != nil || count != 1 {
		t.Fatal("quarantine duplicated account", err)
	}
}

func TestIdentityRecoveryProofGuards(t *testing.T) {
	for _, scenario := range []string{"wrong_code", "expired_code", "stale_version", "occupied_after_request", "legal", "weak_password"} {
		t.Run(scenario, func(t *testing.T) {
			s, e, cfg := fixture(t)
			ctx := context.Background()
			actor, raw, target, _ := identityRecoveryFixture(t, s, e, cfg)
			proof, err := s.RequestOperatorRecovery(ctx, raw, actor, target.Account.ID, uuid.NewString(), identityRecoveryRequest("proof-recovery@example.test"), "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			in := identityRecoveryComplete(t, e, cfg, proof)
			want := 400
			switch scenario {
			case "wrong_code":
				wrong := "00000000"
				if wrong == *in.Code {
					wrong = "11111111"
				}
				in.Code = &wrong
				for i := 0; i < 5; i++ {
					if _, err = s.CompleteIdentityRecovery(ctx, in, "127.0.0.2"); !hasStatus(err, 400) {
						t.Fatal("incorrect code accepted", err)
					}
				}
				in = identityRecoveryComplete(t, e, cfg, proof)
			case "expired_code":
				e.Advance(10 * time.Minute)
			case "stale_version":
				if _, err = e.Pool.Exec(ctx, `UPDATE accounts SET credential_version=credential_version+1 WHERE id=$1`, target.Account.ID); err != nil {
					t.Fatal(err)
				}
			case "occupied_after_request":
				e.Advance(61 * time.Second)
				identityWebFixture(t, s, e, cfg, "proof-recovery@example.test")
				want = 409
			case "legal":
				in.AcceptedTermsVersion = "old"
			case "weak_password":
				in.NewPassword = "short"
			}
			if _, err = s.CompleteIdentityRecovery(ctx, in, "127.0.0.2"); !hasStatus(err, want) {
				t.Fatalf("want%d got%v", want, err)
			}
			var disabled bool
			if err = e.Pool.QueryRow(ctx, `SELECT telegram_login_disabled FROM accounts WHERE id=$1`, target.Account.ID).Scan(&disabled); err != nil || !disabled {
				t.Fatal("failed proof released quarantine", err)
			}
		})
	}
}

func TestIdentityRecoveryPreservesFacts(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	actor, raw, target, _ := identityRecoveryFixture(t, s, e, cfg)
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET legacy_user_id=999001,restricted=true WHERE id=$1`, target.Account.ID); err != nil {
		t.Fatal(err)
	}
	const facts = `SELECT (to_jsonb(a)-ARRAY['kind','email_key','password_hash','verified_at','terms_version','privacy_version','policy_accepted_at','credential_version','telegram_id','telegram_login_disabled','original_kind'])::text FROM accounts a WHERE id=$1`
	var before, after string
	if err := e.Pool.QueryRow(ctx, facts, target.Account.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	proof, err := s.RequestOperatorRecovery(ctx, raw, actor, target.Account.ID, uuid.NewString(), identityRecoveryRequest("legacy-restored@example.test"), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	in := identityRecoveryComplete(t, e, cfg, proof)
	out, err := s.CompleteIdentityRecovery(ctx, in, "127.0.0.2")
	if err != nil || !out.Verified {
		t.Fatal("restricted recovery failed", err)
	}
	if err = e.Pool.QueryRow(ctx, facts, target.Account.ID).Scan(&after); err != nil || before != after {
		t.Fatal("recovery changed unrelated source/legacy/access facts", err)
	}
	if _, _, err = s.Login(ctx, LoginInput{Email: "legacy-restored@example.test", Password: identityWebPassword}, "127.0.0.2"); !hasStatus(err, 403) {
		t.Fatal("recovery bypassed existing account restriction")
	}
	if _, _, err = s.StartTelegramSession(ctx, TelegramSessionInput{TelegramInput: TelegramInput{TelegramID: 9501, DisplayName: "Old Telegram", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"}); !hasStatus(err, 409) {
		t.Fatal("retired Telegram created another account")
	}
}
