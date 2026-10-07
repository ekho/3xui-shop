package accounts

import (
	"bytes"
	"context"
	"github.com/google/uuid"
	"strings"
	"sync"
	"testing"
	"time"
)

// Catches duplicate identity creation, incomplete consent and cross-source session reuse.
func TestMiniAppAccountsSession(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	in := TelegramSessionInput{TelegramInput: TelegramInput{TelegramID: 989, DisplayName: "Telegram client", Locale: "en"}, StartParam: "legacy_payload"}
	if _, _, err := s.StartTelegramSession(ctx, in); !hasStatus(err, 409) {
		t.Fatal("missing consent accepted")
	}
	var n int
	e.Pool.QueryRow(ctx, `SELECT count(*) FROM accounts`).Scan(&n)
	if n != 0 {
		t.Fatal("no-consent account created")
	}
	in.AcceptedTermsVersion = "1"
	in.AcceptedPrivacyVersion = "1"
	first, raw, err := s.StartTelegramSession(ctx, in)
	if err != nil || !strings.HasPrefix(raw, "mini_") || first.Account.EmailKey != nil || first.Account.PasswordSet {
		t.Fatalf("Telegram session failed: %v", err)
	}
	var stored []byte
	var source string
	var bound int64
	var consent time.Time
	var payload string
	if err = e.Pool.QueryRow(ctx, `SELECT s.id_hash,s.auth_source,s.telegram_id,a.policy_accepted_at,a.telegram_start_param FROM sessions s JOIN accounts a ON a.id=s.account_id WHERE a.id=$1`, first.Account.ID).Scan(&stored, &source, &bound, &consent, &payload); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, digest(raw)) || source != "telegram" || bound != 989 || !consent.Equal(e.Clock()) || payload != "legacy_payload" {
		t.Fatal("persisted source/consent contract")
	}
	in.StartParam = "replacement"
	again, _, err := s.StartTelegramSession(ctx, in)
	if err != nil || again.Account.ID != first.Account.ID || again.Account.VpnID != first.Account.VpnID || again.Account.PanelKey != first.Account.PanelKey || again.Account.SubID != first.Account.SubID {
		t.Fatal("repeat changed identity/keys")
	}
	e.Pool.QueryRow(ctx, `SELECT telegram_start_param FROM accounts WHERE id=$1`, first.Account.ID).Scan(&payload)
	if payload != "legacy_payload" {
		t.Fatal("first attribution overwritten")
	}
	if _, err = s.Authenticate(ctx, raw); !hasStatus(err, 401) {
		t.Fatal("mini bearer accepted as web cookie")
	}
	if _, err = s.AuthenticateTelegram(ctx, raw, false); err != nil {
		t.Fatal("live session denied")
	}
	e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, first.Account.ID)
	if _, err = s.AuthenticateTelegram(ctx, raw, false); !hasStatus(err, 403) {
		t.Fatal("restriction bypassed")
	}
	if _, err = s.AuthenticateTelegram(ctx, raw, true); err != nil {
		t.Fatal("restricted logout context unavailable")
	}
	if err = s.LogoutTelegram(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateTelegram(ctx, raw, false); !hasStatus(err, 401) {
		t.Fatal("logout did not revoke")
	}
	var events int
	e.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE account_id=$1 AND action IN ('mini_app_consent','mini_app_login','mini_app_logout')`, first.Account.ID).Scan(&events)
	if events != 4 {
		t.Fatal("transactional audit missing")
	}
	// Old operator-created Telegram accounts retain their identifiers on first Mini login.
	tx, _ := e.Pool.Begin(ctx)
	old, _ := s.CreateTelegram(ctx, tx, TelegramInput{TelegramID: 990, DisplayName: "Existing", Locale: "ru"})
	tx.Commit(ctx)
	in.TelegramID = 990
	existing, oldRaw, err := s.StartTelegramSession(ctx, in)
	if err != nil || existing.Account.ID != old.ID || existing.Account.VpnID != old.VpnID {
		t.Fatal("old identity replaced")
	}
	e.Advance(7 * 24 * time.Hour)
	if _, err = s.AuthenticateTelegram(ctx, oldRaw, false); !hasStatus(err, 401) {
		t.Fatal("idle expiry bypassed")
	}
	// Web session remains independent, including after its account gains Telegram ID.
	web := verified(t, s, e, cfg, "mini-independent@example.test")
	e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=991 WHERE id=$1`, web)
	browser, webRaw, err := s.Login(ctx, LoginInput{Email: "mini-independent@example.test", Password: " spaces allowed ✨安全 "}, "127.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	in.TelegramID = 991
	linked, linkedRaw, err := s.StartTelegramSession(ctx, in)
	if err != nil || linked.Account.ID != browser.Account.ID || linked.Account.EmailKey == nil {
		t.Fatal("linked existing account not found")
	}
	if _, err = s.AuthenticateTelegram(ctx, webRaw, false); !hasStatus(err, 401) {
		t.Fatal("web cookie used for Mini App")
	}
	e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=NULL WHERE id=$1`, web)
	if _, err = s.AuthenticateTelegram(ctx, linkedRaw, false); !hasStatus(err, 401) {
		t.Fatal("changed Telegram binding reused")
	}
	if _, err = s.Authenticate(ctx, webRaw); err != nil {
		t.Fatal("Mini revocation revoked browser")
	}
	if browser.Account.ID == uuid.Nil {
		t.Fatal("missing identity")
	}
}

func TestMiniAppAbsoluteExpiry(t *testing.T) {
	s, e, _ := fixture(t)
	ctx := context.Background()
	_, raw, err := s.StartTelegramSession(ctx, TelegramSessionInput{TelegramInput: TelegramInput{TelegramID: 661, DisplayName: "TTL", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		e.Advance(5 * 24 * time.Hour)
		if _, err = s.AuthenticateTelegram(ctx, raw, false); err != nil {
			t.Fatal("active session prematurely expired")
		}
	}
	e.Advance(5 * 24 * time.Hour)
	if _, err = s.AuthenticateTelegram(ctx, raw, false); !hasStatus(err, 401) {
		t.Fatal("absolute session lifetime extended")
	}
}

func TestMiniAppConcurrentIdentity(t *testing.T) {
	s, e, _ := fixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	out := make(chan Authentication, 6)
	errs := make(chan error, 6)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			a, _, err := s.StartTelegramSession(ctx, TelegramSessionInput{TelegramInput: TelegramInput{TelegramID: 662, DisplayName: "Concurrent", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
			if err != nil {
				errs <- err
			} else {
				out <- a
			}
		}()
	}
	close(start)
	wg.Wait()
	close(out)
	close(errs)
	var id uuid.UUID
	count := 0
	for a := range out {
		count++
		if id == uuid.Nil {
			id = a.Account.ID
		} else if id != a.Account.ID {
			t.Fatal("concurrent login split identity")
		}
	}
	for err := range errs {
		if !hasStatus(err, 409) {
			t.Fatal(err)
		}
	}
	var rows int
	e.Pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE telegram_id=662`).Scan(&rows)
	if count == 0 || rows != 1 {
		t.Fatal("concurrent identity not unique")
	}
}

func TestMiniAppConcurrentLogoutIsIdempotent(t *testing.T) {
	s, e, _ := fixture(t)
	ctx := context.Background()
	a, raw, err := s.StartTelegramSession(ctx, TelegramSessionInput{TelegramInput: TelegramInput{TelegramID: 663, DisplayName: "Logout", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	hold, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(ctx)
	if _, err = hold.Exec(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, a.Account.ID); err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- s.LogoutTelegram(workerCtx, raw) }()
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	waiting := false
	for !waiting {
		select {
		case <-deadline.C:
			t.Fatal("logout actors did not reach held account lock")
		case <-tick.C:
			var n int
			if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FROM accounts%'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			waiting = n >= 2
		}
	}
	if err = hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = <-results; err != nil {
			t.Fatal(err)
		}
	}
	var n int
	e.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE account_id=$1 AND action='mini_app_logout'`, a.Account.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("logout audit duplicated: want 1 got %d", n)
	}
}
