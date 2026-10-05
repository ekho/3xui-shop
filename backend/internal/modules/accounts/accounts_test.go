package accounts

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func fixture(t *testing.T) (*Service, *testkit.Env, Config) {
	t.Helper()
	e := testkit.Open(t)
	queue, err := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{CabinetOrigin: "https://cabinet.example.test", MailKey: bytes.Repeat([]byte{1}, 32), CodeKey: bytes.Repeat([]byte{2}, 32), TermsVersion: "1", PrivacyVersion: "1", RateNamespace: uuid.NewString(), Now: e.Clock}
	return New(e.Pool, e.Redis, queue, cfg, nil), e, cfg
}

func verified(t *testing.T, s *Service, e *testkit.Env, cfg Config, email string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	r, err := s.Register(ctx, RegisterInput{Email: email, Locale: "ru", AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	var mailID uuid.UUID
	var ciphertext []byte
	if err = e.Pool.QueryRow(ctx, `SELECT id,ciphertext FROM mail_deliveries WHERE challenge_id=$1`, r.ChallengeId).Scan(&mailID, &ciphertext); err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(cfg.MailKey)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], []byte(mailID.String()))
	if err != nil {
		t.Fatal(err)
	}
	var proof struct{ Token string }
	if err = json.Unmarshal(plain, &proof); err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyEmail(ctx, VerifyInput{Token: &proof.Token, NewPassword: " spaces allowed ✨安全 "}); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err = e.Pool.QueryRow(ctx, `SELECT id FROM accounts WHERE email_key=$1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAccountsRegistrationAndSession(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	id := verified(t, s, e, cfg, "owner@example.test")
	out, raw, err := s.Login(ctx, LoginInput{Email: " OWNER@Example.TEST ", Password: " spaces allowed ✨安全 "}, "127.0.0.1")
	if err != nil || out.Account.ID != id || out.CsrfToken == "" || len(raw) != 43 {
		t.Fatalf("existing identity/session contract: %v", err)
	}
	// A new owner reads the already persisted hash/session, without an import or rotation.
	restarted := New(e.Pool, e.Redis, nil, cfg, nil)
	auth, err := restarted.Authenticate(ctx, raw)
	if err != nil || auth.Account.ID != id || auth.CsrfToken != out.CsrfToken {
		t.Fatalf("persisted session: %v", err)
	}
	var storedHash string
	if err = e.Pool.QueryRow(ctx, `SELECT password_hash FROM accounts WHERE id=$1`, id).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(auth.Account)
	if err != nil || bytes.Contains(encoded, []byte(storedHash)) || bytes.Contains(bytes.ToLower(encoded), []byte("passwordhash")) {
		t.Fatal("account snapshot leaked credential")
	}
	if _, _, err = s.Login(ctx, LoginInput{Email: "owner@example.test", Password: "spaces allowed ✨安全"}, "127.0.0.1"); !hasStatus(err, 401) {
		t.Fatal("password was trimmed")
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Authenticate(ctx, raw); !hasStatus(err, 403) {
		t.Fatal("persisted session bypassed restriction")
	}
	if err = s.Logout(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Authenticate(ctx, raw); !hasStatus(err, 401) {
		t.Fatal("logout did not revoke session")
	}
}

func hasStatus(err error, status int) bool {
	var domain *Error
	return errors.As(err, &domain) && domain.Status == status
}

func TestPasswordUnicodeAndCapacity(t *testing.T) {
	for _, tc := range []struct {
		password string
		valid    bool
	}{{strings.Repeat("界", 15), true}, {strings.Repeat("a", 14), false}, {strings.Repeat("界", 129), false}, {"  safe spaced password  ", true}, {"\xff" + strings.Repeat("a", 20), false}, {"films+pic+galeries", false}} {
		if err := validatePassword(tc.password); (err == nil) != tc.valid {
			t.Errorf("password validity: want %v, got %v", tc.valid, err)
		}
	}
	s := New(nil, nil, nil, Config{}, nil)
	s.hashSlots <- struct{}{}
	s.hashSlots <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.hashPassword(ctx, "my long safe password ✨"); !hasStatus(err, 503) {
		t.Fatal("third hash ignored cancellation")
	}
}

func BenchmarkPasswordHash(b *testing.B) {
	s := &Service{hashSlots: make(chan struct{}, 2)}
	for b.Loop() {
		if _, err := s.hashPassword(context.Background(), "long realistic password ✨"); err != nil {
			b.Fatal(err)
		}
	}
}
