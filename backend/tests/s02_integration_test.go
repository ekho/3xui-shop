package tests

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"example.com/cabinet/backend/internal/wire"
	"fmt"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"testing"
)

func TestS02ResetFlow(t *testing.T) {
	f := open(t)
	ctx := context.Background()
	f.signup(t, "reset-template@example.test")
	id := uuid.New()
	// Existing verified account, without a just-sent registration mail cooldown.
	if _, err := f.env.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) SELECT $1,'reset-flow@example.test',locale,password_hash,verified_at,$2,'abcdef0123456789',$3,terms_version,privacy_version FROM accounts WHERE email_key='reset-template@example.test'`, id, uuid.New(), "acct_"+strings.ReplaceAll(id.String(), "-", "")); err != nil {
		t.Fatal("verified account fixture")
	}
	client := func() *http.Client { c := *f.public.Client(); c.Jar, _ = cookiejar.New(nil); return &c }
	first, second := client(), client()
	for _, c := range []*http.Client{first, second} {
		if status, _, _ := f.send(t, c, "POST", "/api/v1/auth/login", map[string]string{"email": "reset-flow@example.test", "password": "fixture password with Unicode ✨"}, "", "", false); status != 200 {
			t.Fatal("old login", status)
		}
	}
	if err := f.workers.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := f.send(t, first, "POST", "/api/v1/auth/password-reset", map[string]string{"email": "reset-flow@example.test", "locale": "en"}, "", "", false); status != 202 {
		t.Fatal("request", status)
	}
	var token string
	wait(t, func() bool {
		for _, letter := range f.mail.Letters() {
			if strings.Contains(letter, "To: reset-flow@example.test") && strings.Contains(letter, "/reset-password") {
				match := regexp.MustCompile(`#token=([A-Za-z0-9_-]{43})`).FindStringSubmatch(letter)
				if len(match) == 2 {
					token = match[1]
				}
			}
		}
		return token != ""
	})
	if status, _, r := f.send(t, first, "POST", "/api/v1/auth/password-reset/complete", map[string]string{"token": token, "new_password": "New integration password ✨"}, "", "", false); status != 204 || len(r.Cookies()) != 0 {
		t.Fatal("reset completion or cookie")
	}
	for _, c := range []*http.Client{first, second} {
		if status, _, _ := f.send(t, c, "GET", "/api/v1/me", nil, "", "", false); status != 401 {
			t.Fatal("old session alive", status)
		}
	}
	if status, _, _ := f.send(t, first, "POST", "/api/v1/auth/login", map[string]string{"email": "reset-flow@example.test", "password": "fixture password with Unicode ✨"}, "", "", false); status != 401 {
		t.Fatal("old password alive", status)
	}
	if status, _, _ := f.send(t, first, "POST", "/api/v1/auth/login", map[string]string{"email": "reset-flow@example.test", "password": "New integration password ✨"}, "", "", false); status != 200 {
		t.Fatal("new login", status)
	}
}

// Only this isolated fixture's existing reservation scores are aged; entries and hourly budget remain.
func (f *fixture) ageMailBudget(t *testing.T, email string) {
	t.Helper()
	ctx := context.Background()
	h := hmac.New(sha256.New, f.cfg.CodeKey)
	h.Write([]byte(email))
	key := fmt.Sprintf("%s:mail:%x", f.cfg.RateNamespace, h.Sum(nil))
	scores, err := f.env.Redis.ZRangeWithScores(ctx, key, 0, -1).Result()
	if err != nil {
		t.Fatal("own mail budget read")
	}
	for _, score := range scores {
		if err = f.env.Redis.ZAdd(ctx, key, redis.Z{Score: score.Score - 61000, Member: score.Member}).Err(); err != nil {
			t.Fatal("own mail budget aging")
		}
	}
}
func (f *fixture) deliveredProof(t *testing.T, email, purpose string) string {
	t.Helper()
	var token string
	wait(t, func() bool {
		for _, letter := range f.mail.Letters() {
			if strings.Contains(letter, "To: "+email) && strings.Contains(letter, purpose) {
				match := regexp.MustCompile(`#token=([A-Za-z0-9_-]{43})`).FindStringSubmatch(letter)
				if len(match) == 2 {
					token = match[1]
				}
			}
		}
		return token != ""
	})
	return token
}
func TestS02AccountSecurityFlow(t *testing.T) {
	f := open(t)
	ctx := context.Background()
	email := "security-flow@example.test"
	password := "fixture password with Unicode ✨"
	first, _, trial := f.signup(t, email)
	f.python(t, trial.RequestId)
	if err := f.workers.Start(ctx); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool {
		var status string
		f.env.Pool.QueryRow(ctx, `SELECT status FROM trial_operations WHERE request_id=$1`, trial.RequestId).Scan(&status)
		return status == "applied"
	})
	snapshot := func() []byte {
		var data []byte
		err := f.env.Pool.QueryRow(ctx, `SELECT jsonb_build_object('account',jsonb_build_object('id',a.id,'vpn_id',a.vpn_id,'panel_key',a.panel_key,'sub_id',a.sub_id),'grant',to_jsonb(g),'operation',to_jsonb(o),'trial',to_jsonb(r),'trial_used',EXISTS(SELECT 1 FROM trial_grants WHERE account_id=a.id)) FROM accounts a JOIN trial_requests r ON r.account_id=a.id JOIN trial_grants g ON g.account_id=a.id JOIN trial_operations o ON o.account_id=a.id WHERE r.id=$1`, trial.RequestId).Scan(&data)
		if err != nil {
			t.Fatal("ownership snapshot")
		}
		return data
	}
	before := snapshot()
	status, body, _ := f.send(t, first, "GET", "/api/v1/subscription/key", nil, "", "", false)
	if status != 200 {
		t.Fatal("initial key", status)
	}
	var key wire.SubscriptionKey
	json.Unmarshal(body, &key)
	f.panel.mu.Lock()
	calls := f.panel.requests
	f.panel.mu.Unlock()
	if calls == 0 {
		t.Fatal("panel fixture counter was not exercised")
	}
	unchanged := func() {
		if !bytes.Equal(before, snapshot()) {
			t.Fatal("S02 changed account/grant/operation/target/limits/expiry/trial ownership")
		}
		f.panel.mu.Lock()
		defer f.panel.mu.Unlock()
		if f.panel.requests != calls {
			t.Fatal("S02 called panel")
		}
	}
	client := *f.public.Client()
	client.Jar, _ = cookiejar.New(nil)
	second := &client
	login := func(c *http.Client, address, pw string) string {
		status, b, _ := f.send(t, c, "POST", "/api/v1/auth/login", map[string]string{"email": address, "password": pw}, "", "", false)
		if status != 200 {
			t.Fatal("login", status)
		}
		var out wire.LoginResult
		json.Unmarshal(b, &out)
		return out.CsrfToken
	}
	account := func(c *http.Client) wire.AccountResult {
		status, b, _ := f.send(t, c, "GET", "/api/v1/me", nil, "", "", false)
		if status != 200 {
			t.Fatal("account", status)
		}
		var out wire.AccountResult
		json.Unmarshal(b, &out)
		return out
	}
	login(second, email, password)
	f.ageMailBudget(t, email)
	if status, _, _ := f.send(t, first, "POST", "/api/v1/auth/password-reset", map[string]string{"email": email, "locale": "en"}, "", "", false); status != 202 {
		t.Fatal("reset request", status)
	}
	token := f.deliveredProof(t, email, "/reset-password")
	if status, _, _ := f.send(t, first, "POST", "/api/v1/auth/password-reset/complete", map[string]string{"token": token, "new_password": "Reset safe password ✨"}, "", "", false); status != 204 {
		t.Fatal("reset completion", status)
	}
	password = "Reset safe password ✨"
	unchanged()
	for _, c := range []*http.Client{first, second} {
		if status, _, _ := f.send(t, c, "GET", "/api/v1/me", nil, "", "", false); status != 401 {
			t.Fatal("reset left session")
		}
	}
	csrf := login(first, email, password)
	login(second, email, password)
	if status, _, _ := f.send(t, first, "POST", "/api/v1/me/password-change", map[string]string{"current_password": password, "new_password": "Changed safe password ✨"}, csrf, "", false); status != 204 {
		t.Fatal("password change", status)
	}
	password = "Changed safe password ✨"
	unchanged()
	csrf = account(first).CsrfToken
	login(second, email, password)
	if status, _, _ := f.send(t, first, "POST", "/api/v1/me/sessions/revoke-others", map[string]string{"current_password": password}, csrf, "", false); status != 204 {
		t.Fatal("revoke", status)
	}
	unchanged()
	csrf = account(first).CsrfToken
	if status, _, _ := f.send(t, second, "GET", "/api/v1/me", nil, "", "", false); status != 401 {
		t.Fatal("other session alive")
	}
	f.ageMailBudget(t, email)
	target := "changed-flow@example.test"
	if status, _, _ := f.send(t, first, "POST", "/api/v1/me/email-change", map[string]string{"new_email": target, "current_password": password}, csrf, "", false); status != 202 {
		t.Fatal("email request", status)
	}
	oldProof, newProof := f.deliveredProof(t, email, "/confirm-email-change"), f.deliveredProof(t, target, "/confirm-email-change")
	for i, proof := range []string{oldProof, newProof} {
		status, b, r := f.send(t, second, "POST", "/api/v1/auth/email-change/confirm", map[string]string{"token": proof}, "", "", false)
		var out wire.EmailChangeResult
		json.Unmarshal(b, &out)
		if status != 200 || out.Completed != (i == 1) || len(r.Cookies()) != 0 {
			t.Fatal("mailbox confirm", status)
		}
		unchanged()
	}
	csrf = login(first, target, password)
	me := account(first)
	if me.Capabilities.TrialAvailable {
		t.Fatal("email change reset trial-used")
	}
	if status, _, _ := f.send(t, first, "POST", "/api/v1/auth/logout", nil, csrf, "", false); status != 204 {
		t.Fatal("logout", status)
	}
	unchanged()
	login(first, target, password)
	status, body, _ = f.send(t, first, "GET", "/api/v1/subscription/key", nil, "", "", false)
	var afterKey wire.SubscriptionKey
	json.Unmarshal(body, &afterKey)
	if status != 200 || afterKey.SubscriptionUrl != key.SubscriptionUrl {
		t.Fatal("subscription link changed")
	}
}
