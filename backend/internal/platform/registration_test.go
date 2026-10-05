package platform

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Service, *testkit.Env) {
	t.Helper()
	e := testkit.Open(t)
	q, err := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{CabinetOrigin: "https://cabinet.example.test", MailKey: bytes.Repeat([]byte{1}, 32), CodeKey: bytes.Repeat([]byte{2}, 32), TermsVersion: "1", PrivacyVersion: "1", RateNamespace: uuid.NewString()}
	cfg.Operators = []int64{101, 202}
	cfg.AdapterToken = strings.Repeat("x", 43)
	cfg.PanelID = "dedicated-test"
	cfg.TrialEnabled = true
	cfg.TrialPeriodDays = 3
	cfg.TrialTrafficGB = 15
	cfg.TrialDevices = 1
	s := NewService(e.Pool, e.Redis, q, cfg)
	s.now = e.Clock
	return s, e
}
func signup(email string) wire.RegisterInput {
	return wire.RegisterInput{Email: openapi_types.Email(email), Locale: "ru", AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"}
}
func count(t *testing.T, e *testkit.Env, table string) int {
	t.Helper()
	var n int
	if err := e.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func secrets(t *testing.T, s *Service, e *testkit.Env, challenge uuid.UUID) (uuid.UUID, string, string) {
	t.Helper()
	var id uuid.UUID
	var ciphertext []byte
	err := e.Pool.QueryRow(context.Background(), `SELECT id,ciphertext FROM mail_deliveries WHERE challenge_id=$1`, challenge).Scan(&id, &ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(s.cfg.MailKey)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], []byte(id.String()))
	if err != nil {
		t.Fatal(err)
	}
	var body struct{ Token, Code string }
	if json.Unmarshal(plain, &body) != nil {
		t.Fatal("bad mail")
	}
	return id, body.Token, body.Code
}
func status(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := err.(*Error); ok {
		return e.Status
	}
	return -1
}
func ptr[T any](v T) *T { return &v }
func TestRegistrationOwnership(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	r, err := s.Register(ctx, signup(" OWNER+one@Example.TEST "))
	if err != nil {
		t.Fatalf("registration should enqueue challenge: %v", err)
	}
	if count(t, e, "accounts") != 0 {
		t.Fatal("unverified signup created account")
	}
	_, token, _ := secrets(t, s, e, r.ChallengeId)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := s.VerifyEmail(ctx, wire.VerifyInput{Token: &token, NewPassword: " spaces allowed ✨安全 "})
			if err == nil && bool(v.Verified) {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 || count(t, e, "accounts") != 1 || count(t, e, "sessions") != 0 {
		t.Fatalf("exactly one verified account: successes=%d", ok)
	}
	var email, pw, sub, key string
	var id, vpn uuid.UUID
	if err = e.Pool.QueryRow(ctx, `SELECT id,email_key,password_hash,vpn_id,sub_id,panel_key FROM accounts`).Scan(&id, &email, &pw, &vpn, &sub, &key); err != nil {
		t.Fatal(err)
	}
	if email != "owner+one@example.test" || id == vpn || len(sub) != 16 || key != "acct_"+strings.ReplaceAll(id.String(), "-", "") || strings.Contains(pw, "spaces allowed") {
		t.Fatal("identity or password boundary")
	}
	if matched, err := s.checkPassword(ctx, " spaces allowed ✨安全 ", pw); err != nil || !matched {
		t.Fatal("password changed")
	}
	if matched, _ := s.checkPassword(ctx, "spaces allowed ✨安全", pw); matched {
		t.Fatal("password trimmed")
	}
	e.Advance(time.Minute)
	r2, err := s.Register(ctx, signup(email))
	if err != nil {
		t.Fatal(err)
	}
	if r2.ChallengeId == r.ChallengeId || count(t, e, "accounts") != 1 || count(t, e, "registration_challenges") != 1 {
		t.Fatal("existing account signup must not reset password")
	}
}
func TestRegistrationChallenge(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	r, err := s.Register(ctx, signup("code@example.test"))
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	_, token, code := secrets(t, s, e, r.ChallengeId)
	if len(token) != 43 || len(code) != 8 {
		t.Fatal("secret strength")
	}
	var tokenTTL, codeTTL time.Duration
	var created, te, ce time.Time
	e.Pool.QueryRow(ctx, `SELECT created_at,token_expires_at,code_expires_at FROM registration_challenges WHERE id=$1`, r.ChallengeId).Scan(&created, &te, &ce)
	tokenTTL = te.Sub(created)
	codeTTL = ce.Sub(created)
	if tokenTTL != 24*time.Hour || codeTTL != 10*time.Minute {
		t.Fatal("expiry limits")
	}
	bad := wire.VerifyInput{ChallengeId: &r.ChallengeId, Code: ptr("notdigits"), NewPassword: "long safe password \u2603"}
	if status(sVerify(s, ctx, bad)) != 400 {
		t.Fatal("malformed code accepted")
	}
	wrong := "00000000"
	if wrong == code {
		wrong = "99999999"
	}
	bad.Code = &wrong
	for range 5 {
		if status(sVerify(s, ctx, bad)) != 400 {
			t.Fatal("invalid guess accepted")
		}
	}
	bad.Code = &code
	if status(sVerify(s, ctx, bad)) != 400 {
		t.Fatal("sixth guess accepted")
	}
	e.Advance(time.Minute)
	_, err = s.ResendVerification(ctx, wire.ResendInput{Email: "code@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if status(sVerify(s, ctx, wire.VerifyInput{Token: &token, NewPassword: bad.NewPassword})) != 400 {
		t.Fatal("revoked token accepted")
	}
	if _, err = s.ResendVerification(ctx, wire.ResendInput{Email: "code@example.test"}); status(err) != 429 {
		t.Fatal("minute mail limit")
	}
	for range 3 {
		e.Advance(time.Minute)
		if _, err = s.ResendVerification(ctx, wire.ResendInput{Email: "code@example.test"}); err != nil {
			t.Fatal(err)
		}
	}
	e.Advance(time.Minute)
	if _, err = s.ResendVerification(ctx, wire.ResendInput{Email: "code@example.test"}); status(err) != 429 {
		t.Fatal("hour mail limit")
	}
	e.Redis.Close()
	if _, err = s.Register(ctx, signup("redis@example.test")); status(err) != 503 {
		t.Fatal("redis fail closed")
	}
}
func sVerify(s *Service, ctx context.Context, v wire.VerifyInput) error {
	_, err := s.VerifyEmail(ctx, v)
	return err
}
func TestMailRevocation(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	r, err := s.Register(ctx, signup("mail@example.test"))
	if err != nil {
		t.Fatalf("registration: %v", err)
	}
	id, _, _ := secrets(t, s, e, r.ChallengeId)
	e.Advance(time.Minute)
	if _, err = s.ResendVerification(ctx, wire.ResendInput{Email: "mail@example.test"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SendMail(ctx, id); err != nil {
		t.Fatal("revoked mail should complete without sending", err)
	}
	var data []byte
	if err = e.Pool.QueryRow(ctx, `SELECT ciphertext FROM mail_deliveries WHERE id=$1`, id).Scan(&data); err != nil || len(data) != 0 {
		t.Fatal("revoked secret retained")
	}
	var args []byte
	if err = e.Pool.QueryRow(ctx, `SELECT args FROM river_job WHERE kind='mail_delivery' LIMIT 1`).Scan(&args); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	json.Unmarshal(args, &payload)
	if len(payload) != 1 || payload["delivery_id"] == nil {
		t.Fatal("mail secrets leaked into job")
	}
}
func TestPasswordUnicode(t *testing.T) {
	for _, tc := range []struct {
		p  string
		ok bool
	}{{strings.Repeat("界", 15), true}, {strings.Repeat("a", 14), false}, {strings.Repeat("界", 129), false}, {"  safe spaced password  ", true}, {"\xff" + strings.Repeat("a", 20), false}, {"films+pic+galeries", false}} {
		if err := validatePassword(tc.p); (err == nil) != tc.ok {
			t.Errorf("Unicode/password validation: want valid=%v length=%d", tc.ok, len(tc.p))
		}
	}
}

var _ pgx.Tx

func TestMailDeliveryTLS(t *testing.T) {
	s, e := fixture(t)
	smtp := testkit.MailServer(t)
	s.cfg.SMTPAddress = smtp.Address
	s.cfg.SMTPFrom = "no-reply@example.test"
	s.cfg.SMTPRootCAs = smtp.Roots
	ctx := context.Background()
	r, err := s.Register(ctx, signup("delivery@example.test"))
	if err != nil {
		t.Fatal(err)
	}
	id, token, code := secrets(t, s, e, r.ChallengeId)
	if err = s.SendMail(ctx, id); err != nil {
		t.Fatal("TLS SMTP delivery", err)
	}
	letters := smtp.Letters()
	if len(letters) != 1 || !strings.Contains(letters[0], "/verify-email#token="+token) || !strings.Contains(letters[0], code) {
		t.Fatal("verification letter")
	}
	var data []byte
	e.Pool.QueryRow(ctx, `SELECT ciphertext FROM mail_deliveries WHERE id=$1`, id).Scan(&data)
	if len(data) != 0 {
		t.Fatal("delivered secret retained")
	}
	if err = s.SendMail(ctx, id); err != nil || len(smtp.Letters()) != 1 {
		t.Fatal("delivered mail must not send twice")
	}
	e.Advance(time.Minute)
	r, err = s.Register(ctx, signup("expired@example.test"))
	if err != nil {
		t.Fatal(err)
	}
	id, token, _ = secrets(t, s, e, r.ChallengeId)
	e.Advance(24 * time.Hour)
	if err = s.SendMail(ctx, id); err != nil || len(smtp.Letters()) != 1 {
		t.Fatal("expired challenge mailed")
	}
	if status(sVerify(s, ctx, wire.VerifyInput{Token: &token, NewPassword: "a safe long password"})) != 400 {
		t.Fatal("expired token accepted")
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

func TestRegistrationConfig(t *testing.T) {
	for _, name := range []string{"BOT_OPERATOR_IDS", "BOT_ADAPTER_TOKEN", "BOT_ADAPTER_TOKEN_FILE", "LEGACY_BOT_API_ENABLED", "TRIAL_ENABLED", "TRIAL_PERIOD", "TRIAL_TRAFFIC_GB", "BONUS_DEVICES_COUNT", "PANEL_ID"} {
		t.Setenv(name, "")
	}
	dir := t.TempDir()
	for key, val := range map[string]string{"DATABASE_URL": "postgres://fixture", "REDIS_URL": "redis://fixture", "MAIL_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), "CODE_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))} {
		p := filepath.Join(dir, key)
		if err := os.WriteFile(p, []byte(val), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, "")
		t.Setenv(key+"_FILE", p)
	}
	t.Setenv("CABINET_ORIGIN", "https://cabinet.example.test")
	t.Setenv("TERMS_VERSION", "1")
	t.Setenv("PRIVACY_VERSION", "1")
	t.Setenv("SMTP_USER", "")
	t.Setenv("SMTP_CA_FILE", "")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal("file-only config", err)
	}
	t.Run("native-operators", func(t *testing.T) {
		t.Setenv("BOT_OPERATOR_IDS", "101,202")
		t.Setenv("LEGACY_BOT_API_ENABLED", "false")
		native, err := LoadConfig()
		if err != nil || native.AdapterToken != "" || len(native.Operators) != 2 {
			t.Fatal("native operators require legacy secret", err)
		}
		t.Setenv("LEGACY_BOT_API_ENABLED", "true")
		if _, err = LoadConfig(); err == nil {
			t.Fatal("legacy transport accepted missing secret")
		}
		t.Setenv("LEGACY_BOT_API_ENABLED", "false")
		t.Setenv("BOT_OPERATOR_IDS", "101,bad")
		if _, err = LoadConfig(); err == nil {
			t.Fatal("bad allowlist accepted")
		}
	})
	for _, origin := range []string{"http://example.test", "https://example.test/path", "https://example.test?query=1", "https://user@example.test", "https://example.test#fragment"} {
		cfg.CabinetOrigin = origin
		if cfg.Validate() == nil {
			t.Error("invalid origin accepted")
		}
	}
	t.Setenv("MAIL_KEY", "conflicting-value")
	if _, err = LoadConfig(); err == nil {
		t.Fatal("conflicting secret inputs accepted")
	}
}
