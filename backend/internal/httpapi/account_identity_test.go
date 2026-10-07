package httpapi

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"net/http"
	"strings"
	"testing"
	"time"
)

func identityMiniLogin(t *testing.T, h http.Handler, e *testkit.Env, cfg app.Config, key ed25519.PrivateKey, user string) wire.MiniAppSessionResult {
	t.Helper()
	raw := testkit.SignedMiniAppData(key, 123, e.Clock(), user, "first_signed_payload")
	body, _ := json.Marshal(map[string]string{"init_data": raw, "accepted_terms_version": "1", "accepted_privacy_version": "1"})
	rr := request(h, "POST", "/api/v1/telegram/mini-app/session", string(body), cfg.HTTP.CabinetOrigin)
	var auth wire.MiniAppSessionResult
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &auth) != nil {
		t.Fatalf("existing signed Mini login: want 200, got %d", rr.Code)
	}
	return auth
}

// Catches denying the new identity operations to the signed Mini owner.
func TestIdentityEmailMissingRoute(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	auth := identityMiniLogin(t, h, e, cfg, key, `{"id":501,"first_name":"Identity owner","language_code":"en"}`)
	for _, tc := range []struct {
		name, method, path, body string
		want                     int
	}{
		{"identity", "GET", "/api/v1/me/identity", "", 200},
		{"email_request", "POST", "/api/v1/telegram/initial-email", `{"email":"independent@example.test"}`, 202},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := miniAppRequest(h, tc.method, tc.path, tc.body, cfg.HTTP.CabinetOrigin, auth.SessionToken, auth.CsrfToken, "")
			if rr.Code != tc.want {
				t.Fatalf("identity owner operation: want %d, got %d", tc.want, rr.Code)
			}
		})
	}
	if rr := miniAppRequest(h, "POST", "/api/v1/telegram/initial-email", `{"email":"bad-source@example.test"}`, cfg.HTTP.CabinetOrigin, "", auth.CsrfToken, auth.SessionToken); rr.Code != 401 {
		t.Fatal("Mini enrollment accepted a cookie")
	}
	if rr := miniAppRequest(h, "POST", "/api/v1/telegram/initial-email", `{"email":"bad-csrf@example.test"}`, cfg.HTTP.CabinetOrigin, auth.SessionToken, "", ""); rr.Code != 403 {
		t.Fatal("Mini enrollment accepted missing CSRF")
	}
	if rr := miniAppRequest(h, "POST", "/api/v1/telegram/initial-email", `{"email":"bad-origin@example.test"}`, "https://attacker.example.test", auth.SessionToken, auth.CsrfToken, ""); rr.Code != 403 {
		t.Fatal("Mini enrollment accepted foreign Origin")
	}
}

// Catches replacing the original account or preserving its old login after an email grant.
func TestIdentityEmailSameAccount(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	auth := identityMiniLogin(t, h, e, cfg, key, `{"id":502,"first_name":"Same account","language_code":"en"}`)
	ctx := context.Background()
	var vpnID uuid.UUID
	var subID, panelKey string
	var version int64
	if err := e.Pool.QueryRow(ctx, `SELECT vpn_id,sub_id,panel_key,credential_version FROM accounts WHERE id=$1`, auth.Account.AccountId).Scan(&vpnID, &subID, &panelKey, &version); err != nil {
		t.Fatal(err)
	}
	rr := miniAppRequest(h, "POST", "/api/v1/telegram/initial-email", `{"email":"independent@example.test"}`, cfg.HTTP.CabinetOrigin, auth.SessionToken, auth.CsrfToken, "")
	var accepted struct {
		ChallengeID uuid.UUID `json:"challenge_id"`
	}
	if rr.Code != 202 || json.Unmarshal(rr.Body.Bytes(), &accepted) != nil || accepted.ChallengeID == uuid.Nil {
		t.Fatalf("initial email request: want 202 with proof, got %d", rr.Code)
	}
	_, _, code := testkit.CredentialMailSecrets(t, e.Pool, cfg.Mail.MailKey, accepted.ChallengeID)
	body, _ := json.Marshal(map[string]any{"challenge_id": accepted.ChallengeID, "code": code, "new_password": "independent long safe password ✨", "accepted_terms_version": "1", "accepted_privacy_version": "1"})
	rr = miniAppRequest(h, "POST", "/api/v1/telegram/initial-email/confirm", string(body), cfg.HTTP.CabinetOrigin, auth.SessionToken, auth.CsrfToken, "")
	if rr.Code != 200 || len(rr.Result().Cookies()) != 0 {
		t.Fatalf("same-account email grant: want 200 without web cookie, got %d", rr.Code)
	}
	var same bool
	if err := e.Pool.QueryRow(ctx, `SELECT vpn_id=$2 AND sub_id=$3 AND panel_key=$4 AND telegram_id=502 AND credential_version=$5 AND kind='web' AND original_kind='telegram' AND email_key='independent@example.test' AND verified_at IS NOT NULL AND password_hash IS NOT NULL AND telegram_start_param='first_signed_payload' FROM accounts WHERE id=$1`, auth.Account.AccountId, vpnID, subID, panelKey, version+1).Scan(&same); err != nil || !same {
		t.Fatal("account origin, identifiers or credentials were not preserved")
	}
	if rr := miniAppRequest(h, "GET", "/api/v1/telegram/mini-app/account", "", "", auth.SessionToken, "", ""); rr.Code != 401 {
		t.Fatal("old Mini session remained usable")
	}
	rr = request(h, "POST", "/api/v1/auth/login", `{"email":"independent@example.test","password":"independent long safe password ✨"}`, cfg.HTTP.CabinetOrigin)
	var login wire.LoginResult
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &login) != nil || login.Account.AccountId != auth.Account.AccountId {
		t.Fatal("independent login did not keep the same account")
	}
}

// Catches the notification worker using password-reset copy or emailing an occupied target.
func TestIdentityCredentialMail(t *testing.T) {
	s, e, cfg, smtp := mailFixture(t, 0)
	cfg.Accounts.Now = e.Clock
	cfg.Mail.Now = e.Clock
	queue, err := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s = composeForTest(e.Pool, e.Redis, queue, cfg)
	ctx := context.Background()
	_, raw, err := s.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 8501, DisplayName: "Email owner", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := s.Accounts.RequestInitialEmail(ctx, raw, accounts.InitialEmailInput{Email: "new-login@example.test"}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	mailID, token, code := testkit.CredentialMailSecrets(t, e.Pool, cfg.Mail.MailKey, proof.ChallengeId)
	if err = s.MailDelivery.SendMail(ctx, mailID); err != nil {
		t.Fatal("owned TLS SMTP delivery", err)
	}
	letters := smtp.Letters()
	if len(letters) != 1 || !strings.Contains(letters[0], code) || !strings.Contains(letters[0], "Telegram Mini App") || strings.Contains(letters[0], token) || strings.Contains(letters[0], "/reset-password") {
		t.Fatal("initial email mail granted the wrong purpose or exposed an unused token")
	}
	if err = s.MailDelivery.SendMail(ctx, mailID); err != nil || len(smtp.Letters()) != 1 {
		t.Fatal("credential mail replay duplicated delivery")
	}
	_, _, busyToken, _ := pendingMail(t, s, e, cfg, "occupied-login@example.test")
	if _, err = s.verifyEmail(ctx, wire.VerifyInput{Token: &busyToken, NewPassword: "existing long safe password ✨"}); err != nil {
		t.Fatal(err)
	}
	e.Advance(61 * time.Second)
	busy, err := s.Accounts.RequestInitialEmail(ctx, raw, accounts.InitialEmailInput{Email: "occupied-login@example.test"}, "127.0.0.2")
	if err != nil {
		t.Fatal("occupied request", err)
	}
	busyMail, _, _ := testkit.CredentialMailSecrets(t, e.Pool, cfg.Mail.MailKey, busy.ChallengeId)
	if err = s.MailDelivery.SendMail(ctx, busyMail); err != nil || len(smtp.Letters()) != 1 {
		t.Fatal("worker sent enrollment mail to another mailbox owner")
	}
}
