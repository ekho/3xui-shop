package s01

import (
	"bytes"
	"context"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"fmt"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"testing"
	"time"
)

func verified(t *testing.T, s *Service, e *testkit.Env, email string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	r, err := s.Register(ctx, signup(email))
	if err != nil {
		t.Fatal(err)
	}
	_, token, _ := secrets(t, s, e, r.ChallengeId)
	if _, err = s.VerifyEmail(ctx, wire.VerifyInput{Token: &token, NewPassword: "my long safe password ✨"}); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err = e.Pool.QueryRow(ctx, `SELECT id FROM accounts WHERE email_key=$1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func TestSessionBoundary(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	id := verified(t, s, e, "session@example.test")
	in := wire.LoginInput{Email: "session@example.test", Password: "my long safe password ✨"}
	out, raw, err := s.Login(ctx, in, "127.0.0.1")
	if err != nil {
		t.Fatalf("owner login: %v", err)
	}
	if out.Account.AccountId != id || out.CsrfToken == "" || len(raw) != 43 {
		t.Fatal("session creation")
	}
	var hash []byte
	if err = e.Pool.QueryRow(ctx, `SELECT id_hash FROM sessions WHERE account_id=$1`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(hash, []byte(raw)) {
		t.Fatal("raw session stored")
	}
	_, raw2, err := s.Login(ctx, in, "127.0.0.1")
	if err != nil || raw2 == raw {
		t.Fatal("new login must issue new id")
	}
	if _, err = s.Authenticate(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if err = s.Logout(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, raw); status(err) != 401 {
		t.Fatal("logout did not revoke")
	}
	if err = s.Logout(ctx, raw); err != nil {
		t.Fatal("logout retry")
	}
	e.Advance(7 * 24 * time.Hour)
	if _, err = s.Authenticate(ctx, raw2); status(err) != 401 {
		t.Fatal("idle expiry")
	}
	_, raw, err = s.Login(ctx, in, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		e.Advance(6 * 24 * time.Hour)
		if _, err = s.Authenticate(ctx, raw); err != nil {
			t.Fatal("idle access", err)
		}
	}
	e.Advance(6 * 24 * time.Hour)
	if _, err = s.Authenticate(ctx, raw); status(err) != 401 {
		t.Fatal("absolute lifetime extended")
	}
	_, raw, err = s.Login(ctx, in, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	e.Redis.Close()
	if _, err = s.Authenticate(ctx, raw); err != nil {
		t.Fatal("established session requires Redis", err)
	}
	if _, _, err = s.Login(ctx, in, "127.0.0.1"); status(err) != 503 {
		t.Fatal("Redis-down login allowed")
	}
}
func TestLoginRateLimit(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	verified(t, s, e, "limit@example.test")
	for i := range 5 {
		_, _, err := s.Login(ctx, wire.LoginInput{Email: "limit@example.test", Password: "wrong long password"}, "127.0.0.1")
		if status(err) != 401 {
			t.Fatalf("failure %d must be generic401, got %v", i+1, err)
		}
	}
	if _, _, err := s.Login(ctx, wire.LoginInput{Email: "limit@example.test", Password: "my long safe password ✨"}, "127.0.0.1"); status(err) != 429 {
		t.Fatal("sixth email failure not limited")
	}
	e.Advance(15 * time.Minute)
	if _, _, err := s.Login(ctx, wire.LoginInput{Email: "limit@example.test", Password: "my long safe password ✨"}, "127.0.0.1"); err != nil {
		t.Fatal("email limit not released", err)
	}
	for i := range 30 {
		_, _, err := s.Login(ctx, wire.LoginInput{Email: openapi_types.Email(fmt.Sprintf("unknown%d@example.test", i)), Password: "wrong long password"}, "192.0.2.8")
		if status(err) != 401 {
			t.Fatalf("IP failure %d want401 got %v", i+1, err)
		}
	}
	if _, _, err := s.Login(ctx, wire.LoginInput{Email: "last@example.test", Password: "wrong long password"}, "192.0.2.8"); status(err) != 429 {
		t.Fatal("31st IP failure not limited")
	}
	// Saturation waits for context rather than allocating a third Argon2 buffer.
	s.hashSlots <- struct{}{}
	s.hashSlots <- struct{}{}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.hashPassword(cancelled, "my long safe password ✨"); status(err) != 503 {
		t.Fatal("hash wait ignores cancellation")
	}
	<-s.hashSlots
	<-s.hashSlots
}
