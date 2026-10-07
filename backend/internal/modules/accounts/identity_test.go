package accounts

import (
	"context"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"testing"
	"time"
)

func identityMiniFixture(t *testing.T, s *Service, id int64) (Authentication, string) {
	t.Helper()
	a, raw, err := s.StartTelegramSession(context.Background(), TelegramSessionInput{TelegramInput: TelegramInput{TelegramID: id, DisplayName: "Identity owner", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1", StartParam: "first"})
	if err != nil {
		t.Fatal(err)
	}
	return a, raw
}
func identityEmailProof(t *testing.T, s *Service, e *testkit.Env, cfg fixtureConfig, raw, email string) InitialEmailCompleteInput {
	t.Helper()
	r, err := s.RequestInitialEmail(context.Background(), raw, InitialEmailInput{Email: email}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	_, _, code := testkit.CredentialMailSecrets(t, e.Pool, cfg.MailKey, r.ChallengeId)
	return InitialEmailCompleteInput{ChallengeId: r.ChallengeId, Code: code, NewPassword: "a long independent password ✨", AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"}
}

// Catches two credential owners winning one mailbox or a losing grant leaving partial credentials.
func TestIdentityEmailClaimRace(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	a, rawA := identityMiniFixture(t, s, 8001)
	b, rawB := identityMiniFixture(t, s, 8002)
	proofA := identityEmailProof(t, s, e, cfg, rawA, "claim@example.test")
	e.Advance(61 * time.Second)
	proofB := identityEmailProof(t, s, e, cfg, rawB, "claim@example.test")
	e.Advance(61 * time.Second)
	r, err := s.Register(ctx, RegisterInput{Email: "claim@example.test", Locale: "en", AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	_, token, _ := testkit.MailSecrets(t, e.Pool, cfg.MailKey, r.ChallengeId)
	start := make(chan struct{})
	results := make(chan error, 3)
	for _, x := range []struct {
		raw string
		in  InitialEmailCompleteInput
	}{{rawA, proofA}, {rawB, proofB}} {
		go func(raw string, in InitialEmailCompleteInput) {
			<-start
			_, err := s.CompleteInitialEmail(ctx, raw, in, "127.0.0.2")
			results <- err
		}(x.raw, x.in)
	}
	go func() {
		<-start
		_, err := s.VerifyEmail(ctx, VerifyInput{Token: &token, NewPassword: "registration long password ✨"})
		results <- err
	}()
	close(start)
	success := 0
	for i := 0; i < 3; i++ {
		err := <-results
		if err == nil {
			success++
		} else if !hasStatus(err, 400) {
			t.Fatal("unexpected claim failure", err)
		}
	}
	if success != 1 {
		t.Fatalf("one mailbox grant expected, got %d", success)
	}
	var owners, partial int
	if err = e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts WHERE email_key='claim@example.test'),(SELECT count(*) FROM accounts WHERE id=ANY($1::uuid[]) AND kind='telegram' AND (email_key IS NOT NULL OR password_hash IS NOT NULL OR original_kind IS NOT NULL))`, []uuid.UUID{a.Account.ID, b.Account.ID}).Scan(&owners, &partial); err != nil || owners != 1 || partial != 0 {
		t.Fatal("mailbox owner or rollback violated")
	}
}

// Catches accepting a stale, foreign, exhausted or policy-mismatched enrollment proof.
func TestIdentityEmailProofGuards(t *testing.T) {
	for _, scenario := range []string{"foreign", "expired", "five_guesses", "replacement", "legal", "password", "restricted", "quarantined"} {
		t.Run(scenario, func(t *testing.T) {
			s, e, cfg := fixture(t)
			ctx := context.Background()
			a, raw := identityMiniFixture(t, s, 8101)
			proof := identityEmailProof(t, s, e, cfg, raw, "guard@example.test")
			want := 400
			switch scenario {
			case "foreign":
				_, raw = identityMiniFixture(t, s, 8102)
			case "expired":
				e.Advance(10 * time.Minute)
			case "five_guesses":
				bad := proof
				bad.Code = "00000000"
				if bad.Code == proof.Code {
					bad.Code = "00000001"
				}
				for i := 0; i < 5; i++ {
					if _, err := s.CompleteInitialEmail(ctx, raw, bad, "127.0.0.1"); !hasStatus(err, 400) {
						t.Fatal("wrong code accepted", err)
					}
				}
			case "replacement":
				e.Advance(61 * time.Second)
				newProof := identityEmailProof(t, s, e, cfg, raw, "guard@example.test")
				if newProof.ChallengeId == proof.ChallengeId {
					t.Fatal("proof was not replaced")
				}
			case "legal":
				proof.AcceptedTermsVersion = "old"
			case "password":
				proof.NewPassword = "short"
			case "restricted":
				want = 403
				e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, a.Account.ID)
			case "quarantined":
				want = 401
				e.Pool.Exec(ctx, `UPDATE accounts SET telegram_login_disabled=true WHERE id=$1`, a.Account.ID)
			}
			if _, err := s.CompleteInitialEmail(ctx, raw, proof, "127.0.0.1"); !hasStatus(err, want) {
				t.Fatal("identity proof guard", err)
			}
			var unchanged bool
			if err := e.Pool.QueryRow(ctx, `SELECT kind='telegram' AND email_key IS NULL AND password_hash IS NULL AND original_kind IS NULL AND credential_version=0 FROM accounts WHERE id=$1`, a.Account.ID).Scan(&unchanged); err != nil || !unchanged {
				t.Fatal("rejected enrollment changed credentials")
			}
		})
	}
}

// Catches revealing mailbox ownership or delivering an enrollment code to its existing owner.
func TestIdentityEmailOccupiedUniform(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	owner := verified(t, s, e, cfg, "occupied@example.test")
	e.Advance(61 * time.Second)
	_, raw := identityMiniFixture(t, s, 8201)
	occupied, err := s.RequestInitialEmail(ctx, raw, InitialEmailInput{Email: "occupied@example.test"}, "127.0.0.1")
	if err != nil || occupied.ResendAfter != 60 || occupied.ChallengeId == uuid.Nil {
		t.Fatal("occupied request differs")
	}
	tx, _ := e.Pool.Begin(ctx)
	valid, err := s.MailProofValidTx(ctx, tx, nil, &occupied.ChallengeId)
	tx.Rollback(ctx)
	if err != nil || valid {
		t.Fatal("occupied mailbox mail was authorized")
	}
	free, err := s.RequestInitialEmail(ctx, raw, InitialEmailInput{Email: "free@example.test"}, "127.0.0.2")
	if err != nil || free.ResendAfter != occupied.ResendAfter || free.ChallengeId == uuid.Nil {
		t.Fatal("free request differs")
	}
	tx, _ = e.Pool.Begin(ctx)
	valid, err = s.MailProofValidTx(ctx, tx, nil, &free.ChallengeId)
	tx.Rollback(ctx)
	if err != nil || !valid {
		t.Fatal("free mailbox proof was suppressed")
	}
	var unchanged bool
	if err = e.Pool.QueryRow(ctx, `SELECT kind='web' AND email_key='occupied@example.test' AND original_kind IS NULL AND credential_version=0 FROM accounts WHERE id=$1`, owner).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("existing mailbox owner changed")
	}
}
