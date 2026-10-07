package accounts

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
	"time"
)

func TestTelegramDeliveryGuard(t *testing.T) {
	for _, kind := range []string{"current", "restricted", "quarantine", "version", "binding", "foreign", "consent"} {
		t.Run(kind, func(t *testing.T) {
			s, e, _ := fixture(t)
			ctx := context.Background()
			auth, _, err := s.StartTelegramSession(ctx, TelegramSessionInput{TelegramInput: TelegramInput{TelegramID: 701, DisplayName: "Fixture", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
			if err != nil {
				t.Fatal(err)
			}
			target, tg, version := auth.Account.ID, int64(701), auth.Account.CredentialVersion
			switch kind {
			case "restricted":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", target)
			case "quarantine":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET telegram_login_disabled=true WHERE id=$1", target)
			case "version":
				version++
			case "binding":
				tg = 702
			case "foreign":
				target = uuid.New()
			case "consent":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET terms_version=NULL,privacy_version=NULL,policy_accepted_at=NULL WHERE id=$1", target)
			}
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			valid, err := s.WithTelegramDelivery(ctx, target, tg, version, func(tx pgx.Tx) error { calls++; _, err := tx.Exec(ctx, "SELECT 1"); return err })
			if err != nil || valid != (kind == "current") || calls != map[bool]int{true: 1, false: 0}[kind == "current"] {
				t.Fatal("stale identity reached delivery", err, valid, calls)
			}
		})
	}
}
func TestTelegramDeliveryGuardSingleConnection(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	auth, _, err := s.StartTelegramSession(ctx, TelegramSessionInput{TelegramInput: TelegramInput{TelegramID: 701, DisplayName: "Fixture", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	config := e.Pool.Config().Copy()
	config.MaxConns = 1
	single, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()
	owner := New(single, e.Redis, nil, cfg.Config)
	valid, err := owner.WithTelegramDelivery(ctx, auth.Account.ID, 701, auth.Account.CredentialVersion, func(tx pgx.Tx) error { var x int; return tx.QueryRow(ctx, "SELECT 1").Scan(&x) })
	if err != nil || !valid {
		t.Fatal("one-connection guard deadlocked", err)
	}
}
func TestTelegramDeliveryGuardUnlink(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	web, _ := identityWebFixture(t, s, e, cfg, "delivery@example.test")
	raw := identityWebLogin(t, s, "delivery@example.test")
	in := identityLinkInput(t, s, raw, 701)
	if _, err := s.ConfirmTelegramLink(ctx, in); err != nil {
		t.Fatal(err)
	}
	raw = identityWebLogin(t, s, "delivery@example.test")
	a, err := s.Lookup(ctx, web.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := s.WithTelegramDelivery(ctx, a.ID, 701, a.CredentialVersion, func(pgx.Tx) error { close(entered); <-release; return nil })
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("guard not entered")
	}
	_, err = s.UnlinkTelegram(ctx, raw, CurrentPasswordInput{CurrentPassword: identityWebPassword}, "127.0.0.1")
	close(release)
	if err == nil || err.Error() != "REQUEST_STATE_CONFLICT" {
		t.Fatal("unlink committed while send held binding", err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = s.UnlinkTelegram(ctx, raw, CurrentPasswordInput{CurrentPassword: identityWebPassword}, "127.0.0.1"); err != nil {
		t.Fatal("released guard blocked unlink", err)
	}
}
