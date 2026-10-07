package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
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
	var acceptedVersions string
	if err := e.Pool.QueryRow(ctx, `SELECT COALESCE(reason,'') FROM audit_events WHERE account_id=$1 AND action='initial_email_confirmed'`, auth.Account.AccountId).Scan(&acceptedVersions); err != nil || acceptedVersions != "terms=1 privacy=1" {
		t.Fatalf("identity grant must keep accepted document versions: got %q, error %v", acceptedVersions, err)
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

func TestIdentityRecoveryMissingRoute(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	ctx := context.Background()
	mini := identityMiniLogin(t, h, e, cfg, key, `{"id":9701,"first_name":"Lost Telegram","language_code":"en"}`)
	operator := supportLogin(t, h, e, cfg, "identity-operator@example.test")
	if _, err := e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, operator.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/operator/clients/" + mini.Account.AccountId.String() + "/identity-recovery"
	body := []byte(`{"email":"restored@example.test","current_password":"long safe password","reason":"Verified through support","confirmed":true}`)
	rr := supportRequest(h, &operator, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, uuid.New())
	var proof struct {
		ChallengeID uuid.UUID `json:"challenge_id"`
	}
	if rr.Code != 202 || json.Unmarshal(rr.Body.Bytes(), &proof) != nil || proof.ChallengeID == uuid.Nil {
		t.Fatalf("operator recovery: want202 with proof, got%d", rr.Code)
	}
	if rr = miniAppRequest(h, "GET", "/api/v1/telegram/mini-app/account", "", "", mini.SessionToken, "", ""); rr.Code != 401 {
		t.Fatal("old Telegram session not quarantined")
	}
	_, _, code := testkit.CredentialMailSecrets(t, e.Pool, cfg.Mail.MailKey, proof.ChallengeID)
	complete, _ := json.Marshal(map[string]any{"challenge_id": proof.ChallengeID, "code": code, "new_password": "independent recovered password ✨", "accepted_terms_version": "1", "accepted_privacy_version": "1"})
	rr = request(h, "POST", "/api/v1/auth/identity-recovery", string(complete), cfg.HTTP.CabinetOrigin)
	if rr.Code != 200 || len(rr.Result().Cookies()) != 0 {
		t.Fatalf("anonymous recovery: want200 without cookie, got%d", rr.Code)
	}
	var same bool
	if err := e.Pool.QueryRow(ctx, `SELECT kind='web' AND original_kind='telegram' AND NOT telegram_login_disabled AND telegram_id IS NULL AND email_key='restored@example.test' AND verified_at IS NOT NULL FROM accounts WHERE id=$1`, mini.Account.AccountId).Scan(&same); err != nil || !same {
		t.Fatal("same-account credentials or reservation transition failed")
	}
	loginBody, _ := json.Marshal(map[string]string{"email": "restored@example.test", "password": "independent recovered password ✨"})
	rr = request(h, "POST", "/api/v1/auth/login", string(loginBody), cfg.HTTP.CabinetOrigin)
	var login wire.LoginResult
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &login) != nil || login.Account.AccountId != mini.Account.AccountId {
		t.Fatal("recovery did not keep account ID")
	}
	if rr = request(h, "POST", "/api/v1/auth/identity-recovery", string(complete), cfg.HTTP.CabinetOrigin); rr.Code != 400 {
		t.Fatal("recovery proof replay accepted")
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

func TestIdentityRecoveryCredentialMail(t *testing.T) {
	for _, scenario := range []string{"owned_tls_delivery", "smtp_failure", "role_revoked"} {
		t.Run(scenario, func(t *testing.T) {
			s, e, cfg, smtp := mailFixture(t, 0)
			cfg.Accounts.Now = e.Clock
			cfg.Mail.Now = e.Clock
			if scenario == "smtp_failure" {
				cfg.Mail.SMTPRootCAs = x509.NewCertPool()
			}
			queue, err := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
			if err != nil {
				t.Fatal(err)
			}
			s = composeForTest(e.Pool, e.Redis, queue, cfg)
			ctx := context.Background()
			_, _, token, _ := pendingMail(t, s, e, cfg, "mail-recovery-operator@example.test")
			password := "long safe operator password ✨"
			if _, err = s.verifyEmail(ctx, wire.VerifyInput{Token: &token, NewPassword: password}); err != nil {
				t.Fatal(err)
			}
			actor, raw, err := s.Accounts.Login(ctx, accounts.LoginInput{Email: "mail-recovery-operator@example.test", Password: password}, "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Accounts.ChangeOperatorRole(ctx, actor.Account.ID, true); err != nil {
				t.Fatal(err)
			}
			target, _, err := s.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 9702, DisplayName: "Mail recovery", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
			if err != nil {
				t.Fatal(err)
			}
			proof, err := s.Accounts.RequestOperatorRecovery(ctx, raw, actor.Account.ID, target.Account.ID, uuid.NewString(), accounts.OperatorRecoveryInput{Email: "mail-restored@example.test", CurrentPassword: password, Reason: "Support validated", Confirmed: true}, "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			mailID, secret, code := testkit.CredentialMailSecrets(t, e.Pool, cfg.Mail.MailKey, proof.ChallengeId)
			if scenario == "role_revoked" {
				if err = s.Accounts.ChangeOperatorRole(ctx, actor.Account.ID, false); err != nil {
					t.Fatal(err)
				}
			}
			err = s.MailDelivery.SendMail(ctx, mailID)
			letters := smtp.Letters()
			if scenario == "owned_tls_delivery" {
				if err != nil || len(letters) != 1 || !strings.Contains(letters[0], "/recover-account?lang=en#token="+secret) || !strings.Contains(letters[0], code) || strings.Contains(letters[0], "/reset-password") {
					t.Fatal("owned recovery mail boundary failed", err)
				}
			} else if len(letters) != 0 || scenario == "smtp_failure" && err == nil || scenario == "role_revoked" && err != nil {
				t.Fatal("failure/role guard did not suppress delivery", err)
			}
			var disabled bool
			if err = e.Pool.QueryRow(ctx, `SELECT telegram_login_disabled FROM accounts WHERE id=$1`, target.Account.ID).Scan(&disabled); err != nil || !disabled {
				t.Fatal("delivery released quarantine", err)
			}
		})
	}
}

// Catches losing the web owner or creating a second Telegram account while linking channels.
func TestIdentityLinkMissingRoute(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	verifiedHTTP(t, h, e, cfg)
	loginBody := `{"email":"login@example.test","password":"my long safe password ✨"}`
	rr := request(h, "POST", "/api/v1/auth/login", loginBody, cfg.HTTP.CabinetOrigin)
	var login wire.LoginResult
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &login) != nil {
		t.Fatal("existing web login failed")
	}
	cookie := rr.Result().Cookies()[0].Value
	signed := testkit.SignedMiniAppData(key, 123, e.Clock(), `{"id":8601,"first_name":"Linked owner","language_code":"en"}`, "original_payload")
	firstBody, _ := json.Marshal(map[string]string{"init_data": signed})
	rr = request(h, "POST", "/api/v1/telegram/mini-app/session", string(firstBody), cfg.HTTP.CabinetOrigin)
	if rr.Code != 409 {
		t.Fatal("first Mini login must collect consent without account creation")
	}
	rr = miniAppRequest(h, "POST", "/api/v1/me/telegram/link", `{"current_password":"my long safe password ✨"}`, cfg.HTTP.CabinetOrigin, "", login.CsrfToken, cookie)
	var proof struct {
		LinkToken string `json:"link_token"`
	}
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &proof) != nil || len(proof.LinkToken) != 43 {
		t.Fatalf("web link proof: want200/opaque43, got%d", rr.Code)
	}
	linkBody, _ := json.Marshal(map[string]string{"init_data": signed, "link_token": proof.LinkToken, "accepted_terms_version": "1", "accepted_privacy_version": "1"})
	rr = request(h, "POST", "/api/v1/telegram/link", string(linkBody), cfg.HTTP.CabinetOrigin)
	if rr.Code != 200 || len(rr.Result().Cookies()) != 0 {
		t.Fatalf("signed ownership link: want200/no cookie, got%d", rr.Code)
	}
	var ids, count int
	if err := e.Pool.QueryRow(context.Background(), `SELECT count(*),count(*) FILTER(WHERE id=$1 AND telegram_id=8601) FROM accounts`, login.Account.AccountId).Scan(&count, &ids); err != nil || count != 1 || ids != 1 {
		t.Fatal("link created or transferred another account")
	}
	if rr := miniAppRequest(h, "GET", "/api/v1/me", "", "", "", "", cookie); rr.Code != 401 {
		t.Fatal("pre-link browser session remained usable")
	}
	rr = request(h, "POST", "/api/v1/telegram/mini-app/session", string(firstBody), cfg.HTTP.CabinetOrigin)
	var mini wire.MiniAppSessionResult
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &mini) != nil || mini.Account.AccountId != login.Account.AccountId {
		t.Fatal("linked Mini login lost the owner")
	}
	// Replay cannot mutate again, and must not accept the newly issued Mini bearer.
	if rr := request(h, "POST", "/api/v1/telegram/link", string(linkBody), cfg.HTTP.CabinetOrigin); rr.Code != 200 {
		t.Fatal("same-owner proof replay rejected")
	}
	if rr := miniAppRequest(h, "POST", "/api/v1/telegram/link", string(linkBody), cfg.HTTP.CabinetOrigin, mini.SessionToken, mini.CsrfToken, ""); rr.Code != 403 {
		t.Fatal("link boundary accepted a bearer as ownership proof")
	}
	rr = request(h, "POST", "/api/v1/auth/login", loginBody, cfg.HTTP.CabinetOrigin)
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &login) != nil {
		t.Fatal("new browser login failed")
	}
	cookie = rr.Result().Cookies()[0].Value
	rr = miniAppRequest(h, "POST", "/api/v1/me/telegram/unlink", `{"current_password":"my long safe password ✨"}`, cfg.HTTP.CabinetOrigin, "", login.CsrfToken, cookie)
	if rr.Code != 200 {
		t.Fatalf("independent unlink: want200, got%d", rr.Code)
	}
	if rr := miniAppRequest(h, "GET", "/api/v1/telegram/mini-app/account", "", "", mini.SessionToken, "", ""); rr.Code != 401 {
		t.Fatal("retired identity retained its Mini session")
	}
	rr = request(h, "POST", "/api/v1/telegram/mini-app/session", string(firstBody), cfg.HTTP.CabinetOrigin)
	if rr.Code != 409 || !strings.Contains(rr.Body.String(), "TELEGRAM_UNLINKED") {
		t.Fatal("retired ID created another account or hid the recovery action")
	}
}

// Catches the new public link route trusting an unverified, wrong-bot or stale launch.
func TestIdentityLinkSignatureBoundary(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	for _, tc := range []struct {
		name, raw, token string
		want             int
	}{
		{"unsigned", "auth_date=1", strings.Repeat("a", 43), 401},
		{"wrong_bot", testkit.SignedMiniAppData(key, 999, e.Clock(), `{"id":8602,"first_name":"Wrong bot"}`, ""), strings.Repeat("a", 43), 401},
		{"old", testkit.SignedMiniAppData(key, 123, e.Clock().Add(-301*time.Second), `{"id":8602,"first_name":"Old launch"}`, ""), strings.Repeat("a", 43), 401},
		{"unknown_proof", testkit.SignedMiniAppData(key, 123, e.Clock(), `{"id":8602,"first_name":"Signed launch"}`, ""), strings.Repeat("a", 43), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"init_data": tc.raw, "link_token": tc.token, "accepted_terms_version": "1", "accepted_privacy_version": "1"})
			if rr := request(h, "POST", "/api/v1/telegram/link", string(body), cfg.HTTP.CabinetOrigin); rr.Code != tc.want {
				t.Fatalf("signed link boundary: want%d got%d", tc.want, rr.Code)
			}
			var count int
			if err := e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM accounts`).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed ownership proof created an account")
			}
		})
	}
}
