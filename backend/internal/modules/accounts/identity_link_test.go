package accounts

import (
	"context"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"testing"
	"time"
)

const identityWebPassword = " spaces allowed ✨安全 "

func identityWebFixture(t *testing.T, s *Service, e *testkit.Env, cfg fixtureConfig, email string) (Authentication, string) {
	t.Helper()
	verified(t, s, e, cfg, email)
	a, raw, err := s.Login(context.Background(), LoginInput{Email: email, Password: identityWebPassword}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	return a, raw
}
func identityLinkInput(t *testing.T, s *Service, raw string, tg int64) ConfirmTelegramLinkInput {
	t.Helper()
	proof, err := s.StartTelegramLink(context.Background(), raw, CurrentPasswordInput{CurrentPassword: identityWebPassword}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(proof.LinkToken) != 43 {
		t.Fatal("link token is not opaque43")
	}
	return ConfirmTelegramLinkInput{TelegramSessionInput: TelegramSessionInput{TelegramInput: TelegramInput{TelegramID: tg, DisplayName: "Signed identity", Locale: "en"}, StartParam: "first_link", AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"}, LinkToken: proof.LinkToken}
}
func identityWebLogin(t *testing.T, s *Service, email string) string {
	t.Helper()
	_, raw, err := s.Login(context.Background(), LoginInput{Email: email, Password: identityWebPassword}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Catches repeated grants and accepting a link proof after the owner ended or changed credentials.
func TestIdentityLinkReplayAfterLogout(t *testing.T) {
	for _, scenario := range []string{"logout", "revoke_others", "password_change", "expired", "same_replay", "other_identity_replay", "version_replay"} {
		t.Run(scenario, func(t *testing.T) {
			s, e, cfg := fixture(t)
			ctx := context.Background()
			a, raw := identityWebFixture(t, s, e, cfg, "link@example.test")
			in := identityLinkInput(t, s, raw, 9101)
			want := 400
			switch scenario {
			case "logout":
				if err := s.Logout(ctx, raw); err != nil {
					t.Fatal(err)
				}
			case "revoke_others":
				if _, err := s.RevokeOtherSessions(ctx, raw, CurrentPasswordInput{CurrentPassword: identityWebPassword}, "127.0.0.1"); err != nil {
					t.Fatal(err)
				}
			case "password_change":
				if _, err := s.ChangePassword(ctx, raw, PasswordChangeInput{CurrentPassword: identityWebPassword, NewPassword: "changed long safe password ✨"}, "127.0.0.1"); err != nil {
					t.Fatal(err)
				}
			case "expired":
				e.Advance(10 * time.Minute)
			default:
				if _, err := s.ConfirmTelegramLink(ctx, in); err != nil {
					t.Fatal(err)
				}
				if scenario == "same_replay" {
					want = 0
				}
				if scenario == "other_identity_replay" {
					in.TelegramID = 9102
					want = 409
				}
				if scenario == "version_replay" {
					newRaw := identityWebLogin(t, s, "link@example.test")
					if _, err := s.ChangePassword(ctx, newRaw, PasswordChangeInput{CurrentPassword: identityWebPassword, NewPassword: "new independent safe password ✨"}, "127.0.0.1"); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, err := s.ConfirmTelegramLink(ctx, in)
			if want == 0 && err != nil || want != 0 && !hasStatus(err, want) {
				t.Fatal("link replay guard", err)
			}
			var grants int
			if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE account_id=$1 AND action='telegram_linked'`, a.Account.ID).Scan(&grants); err != nil {
				t.Fatal(err)
			}
			expected := 0
			if scenario == "same_replay" || scenario == "other_identity_replay" || scenario == "version_replay" {
				expected = 1
			}
			if grants != expected {
				t.Fatalf("one grant audit expected: want%d got%d", expected, grants)
			}
		})
	}
}

// Catches automatically merging a separate Telegram owner into the web account.
func TestIdentityLinkOwnerConflict(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	web, raw := identityWebFixture(t, s, e, cfg, "conflict@example.test")
	other, _ := identityMiniFixture(t, s, 9201)
	in := identityLinkInput(t, s, raw, 9201)
	if _, err := s.ConfirmTelegramLink(ctx, in); !hasStatus(err, 409) {
		t.Fatal("different owners were linked", err)
	}
	still, err := s.Lookup(ctx, web.Account.ID)
	if err != nil || still.TelegramID != nil {
		t.Fatal("web account took another owner's identity")
	}
	old, err := s.LookupTelegram(ctx, 9201)
	if err != nil || old.ID != other.Account.ID {
		t.Fatal("Telegram account owner was changed")
	}
}

// Catches two web accounts acquiring the same signed Telegram identity concurrently.
func TestIdentityLinkConcurrent(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	a, rawA := identityWebFixture(t, s, e, cfg, "concurrent-a@example.test")
	b, rawB := identityWebFixture(t, s, e, cfg, "concurrent-b@example.test")
	inputs := []ConfirmTelegramLinkInput{identityLinkInput(t, s, rawA, 9301), identityLinkInput(t, s, rawB, 9301)}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, in := range inputs {
		go func(in ConfirmTelegramLinkInput) { <-start; _, err := s.ConfirmTelegramLink(ctx, in); results <- err }(in)
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			success++
		} else if !hasStatus(err, 409) {
			t.Fatal("unexpected concurrency result", err)
		}
	}
	if success != 1 {
		t.Fatal("same Telegram identity has multiple successful owners")
	}
	var count int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE telegram_id=9301 AND id=ANY($1::uuid[])`, []uuid.UUID{a.Account.ID, b.Account.ID}).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent ownership broken")
	}
}

// Catches detached login creating a new account/trial, including a login racing retirement.
func TestIdentityRetirementRace(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	account, raw := identityWebFixture(t, s, e, cfg, "retire@example.test")
	in := identityLinkInput(t, s, raw, 9401)
	if _, err := s.ConfirmTelegramLink(ctx, in); err != nil {
		t.Fatal(err)
	}
	raw = identityWebLogin(t, s, "retire@example.test")
	start := make(chan struct{})
	loginDone := make(chan error, 1)
	go func() { <-start; _, _, err := s.StartTelegramSession(ctx, in.TelegramSessionInput); loginDone <- err }()
	close(start)
	_, unlinkErr := s.UnlinkTelegram(ctx, raw, CurrentPasswordInput{CurrentPassword: identityWebPassword}, "127.0.0.1")
	if err := <-loginDone; err != nil && !hasStatus(err, 409) {
		t.Fatal("unexpected racing login", err)
	}
	if hasStatus(unlinkErr, 409) {
		_, unlinkErr = s.UnlinkTelegram(ctx, raw, CurrentPasswordInput{CurrentPassword: identityWebPassword}, "127.0.0.1")
	}
	if unlinkErr != nil {
		t.Fatal("retirement", unlinkErr)
	}
	if _, _, err := s.StartTelegramSession(ctx, in.TelegramSessionInput); !hasStatus(err, 409) {
		t.Fatal("retired identity created an account", err)
	}
	var rows, reservations int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts),(SELECT count(*) FROM telegram_identity_reservations WHERE telegram_id=9401 AND account_id=$1)`, account.Account.ID).Scan(&rows, &reservations); err != nil || rows != 1 || reservations != 1 {
		t.Fatal("retirement owner/history lost")
	}
	// Another web owner cannot acquire the retired ID even with their own proof.
	_, foreignRaw := identityWebFixture(t, s, e, cfg, "foreign-retire@example.test")
	foreign := identityLinkInput(t, s, foreignRaw, 9401)
	if _, err := s.ConfirmTelegramLink(ctx, foreign); !hasStatus(err, 409) {
		t.Fatal("reservation transferred to another owner", err)
	}
	newRaw := identityWebLogin(t, s, "retire@example.test")
	relink := identityLinkInput(t, s, newRaw, 9401)
	if _, err := s.ConfirmTelegramLink(ctx, relink); err != nil {
		t.Fatal("same owner cannot relink", err)
	}
	linked, err := s.LookupTelegram(ctx, 9401)
	if err != nil || linked.ID != account.Account.ID || linked.VpnID != account.Account.VpnID || linked.SubID != account.Account.SubID || linked.PanelKey != account.Account.PanelKey {
		t.Fatal("relink lost original account/VPN IDs")
	}
}

// Catches enabling self-unlink for billing facts whose Stars ownership is still unknown.
func TestIdentityLegacyUnlinkDenied(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	a, raw := identityWebFixture(t, s, e, cfg, "legacy-unlink@example.test")
	in := identityLinkInput(t, s, raw, 9501)
	if _, err := s.ConfirmTelegramLink(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET legacy_user_id=9501 WHERE id=$1`, a.Account.ID); err != nil {
		t.Fatal(err)
	}
	raw = identityWebLogin(t, s, "legacy-unlink@example.test")
	if _, err := s.UnlinkTelegram(ctx, raw, CurrentPasswordInput{CurrentPassword: identityWebPassword}, "127.0.0.1"); !hasStatus(err, 409) {
		t.Fatal("legacy unlink guard", err)
	}
	identity, err := s.GetIdentity(ctx, a.Account.ID)
	if err != nil || identity.CanUnlink || !identity.TelegramLinked || identity.UnlinkBlockedReason == nil || *identity.UnlinkBlockedReason != "UNLINK_UNAVAILABLE" {
		t.Fatal("legacy UI capabilities differ from write guard")
	}
}
