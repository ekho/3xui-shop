package tests

import (
	"context"
	"github.com/google/uuid"
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
