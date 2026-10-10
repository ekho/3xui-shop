package tests

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func nativeReferralRead(t *testing.T, f *fixture, client *http.Client, token ...string) wire.ReferralsResult {
	t.Helper()
	status, raw, resp := f.send(t, client, "GET", "/api/v1/referrals", nil, "", "", false, token...)
	var out wire.ReferralsResult
	if status != 200 || json.Unmarshal(raw, &out) != nil || out.Version != "2026-10-10-referrals-v1" || len(out.Levels) != 2 || out.Levels[0].Level != 1 || out.Levels[1].Level != 2 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("owned referral response unavailable", status)
	}
	for _, level := range out.Levels {
		if !regexp.MustCompile(`^(0|[1-9][0-9]*)$`).MatchString(level.GrantedDays) || !regexp.MustCompile(`^(0|[1-9][0-9]*)$`).MatchString(level.PendingDays) {
			t.Fatal("referral days lost integer text contract")
		}
	}
	return out
}

func nativeReferralCode(t *testing.T, out wire.ReferralsResult) string {
	t.Helper()
	u, err := url.Parse(out.WebUrl)
	if err != nil || u.Scheme != "https" || u.Path != "/register" || len(u.Query()) != 1 || !regexp.MustCompile(`^r_[0-9a-f]{32}$`).MatchString(u.Query().Get("invite")) {
		t.Fatal("personal registration link malformed")
	}
	return u.Query().Get("invite")
}

func nativeReferralInit(key ed25519.PrivateKey, tg int64, source string) string {
	return testkit.SignedMiniAppData(key, 123456789, time.Now(), fmt.Sprintf(`{"id":%d,"first_name":"Owned referral","language_code":"en"}`, tg), source)
}

func nativeReferralMini(t *testing.T, f *fixture, key ed25519.PrivateKey, tg int64, source string) wire.MiniAppSessionResult {
	t.Helper()
	status, raw, _ := f.send(t, f.public.Client(), "POST", "/api/v1/telegram/mini-app/session", map[string]string{"init_data": nativeReferralInit(key, tg, source), "accepted_terms_version": "1", "accepted_privacy_version": "1"}, "", "", false)
	var out wire.MiniAppSessionResult
	if status != 200 || json.Unmarshal(raw, &out) != nil || out.Account.AccountId == uuid.Nil {
		t.Fatal("signed referral session unavailable", status)
	}
	return out
}

func nativeReferralLink(t *testing.T, f *fixture, client *http.Client, csrf, email string, key ed25519.PrivateKey, tg int64, source string) string {
	t.Helper()
	status, raw, _ := f.send(t, client, "POST", "/api/v1/me/telegram/link", wire.CurrentPasswordInput{CurrentPassword: "fixture password with Unicode ✨"}, csrf, "", false)
	var challenge wire.TelegramLinkChallenge
	if status != 200 || json.Unmarshal(raw, &challenge) != nil {
		t.Fatal("owned referral link challenge unavailable", status)
	}
	status, _, _ = f.send(t, f.public.Client(), "POST", "/api/v1/telegram/link", map[string]string{"init_data": nativeReferralInit(key, tg, source), "link_token": challenge.LinkToken, "accepted_terms_version": "1", "accepted_privacy_version": "1"}, "", "", false)
	if status != 200 {
		t.Fatal("signed referral identity link unavailable", status)
	}
	status, raw, _ = f.send(t, client, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "fixture password with Unicode ✨"}, "", "", false)
	var login wire.LoginResult
	if status != 200 || json.Unmarshal(raw, &login) != nil {
		t.Fatal("owned referral relogin unavailable", status)
	}
	return login.CsrfToken
}

func nativeReferralRewards(t *testing.T, f *fixture, actor uuid.UUID) {
	t.Helper()
	for _, row := range []struct {
		kind, amount string
		level        any
		granted      bool
	}{{"DAYS", "9007199254740993", 1, true}, {"DAYS", "4", 1, false}, {"DAYS", "3", 2, true}, {"DAYS", "2", 2, false}, {"MONEY", "12345678901234567890.123456789012345678", 1, false}, {"MONEY", "1", nil, true}} {
		var granted any
		if row.granted {
			granted = time.Now()
		}
		if _, err := f.env.Pool.Exec(context.Background(), `INSERT INTO referrer_rewards(id,account_id,reward_type,reward_level,amount,payment_id,created_at,rewarded_at) VALUES($1,$2,$3,$4,$5::numeric,$6,now(),$7)`, uuid.New(), actor, row.kind, row.level, row.amount, uuid.NewString(), granted); err != nil {
			t.Fatal("owned persisted referral reward", err)
		}
	}
}

func TestNativeReferralsFlow(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f, bot := openMode(t, true, pub), &nativeBot{}
	_, stop := launchNative(t, f, bot, true, false, true)
	ctx := context.Background()
	email := nativeEmail("referral-inviter")
	a, csrf, aid := f.signupAccount(t, email)
	first := nativeReferralRead(t, f, a)
	code := nativeReferralCode(t, first)
	b, _, bid := f.signupAccount(t, nativeEmail("referral-first"), code)
	bcode := nativeReferralCode(t, nativeReferralRead(t, f, b))
	_, _, cid := f.signupAccount(t, nativeEmail("referral-second"), bcode)
	other, _, oid := f.signupAccount(t, nativeEmail("referral-other"))
	nativeReferralRewards(t, f, aid)
	expected := nativeReferralRead(t, f, a)
	if expected.WebUrl != first.WebUrl || expected.UnclassifiedRecords != 1 || expected.Levels[0].Invited != 1 || expected.Levels[1].Invited != 1 || expected.Levels[0].GrantedDays != "9007199254740993" || expected.Levels[0].PendingDays != "4" || expected.Levels[1].GrantedDays != "3" || expected.Levels[1].PendingDays != "2" || expected.Levels[0].GrantedRewards != 1 || expected.Levels[0].PendingRewards != 1 || expected.Levels[0].MoneyRecords != 1 || expected.Levels[0].PendingMoneyRecords != 1 {
		t.Fatal("two-level persisted facts changed")
	}
	foreign := nativeReferralRead(t, f, other)
	if foreign.WebUrl == first.WebUrl || foreign.UnclassifiedRecords != 0 || foreign.Levels[0].Invited != 0 || foreign.Levels[0].GrantedDays != "0" || foreign.Levels[1].Invited != 0 {
		t.Fatal("referral facts leaked to another account")
	}
	for _, request := range []struct {
		client *http.Client
		path   string
		body   any
		want   int
	}{{f.public.Client(), "/api/v1/referrals", nil, 401}, {a, "/api/v1/referrals?account_id=" + oid.String(), nil, 400}, {a, "/api/v1/referrals", map[string]string{"account_id": oid.String()}, 400}} {
		if status, _, _ := f.send(t, request.client, "GET", request.path, request.body, "", "", false); status != request.want {
			t.Fatal("referral read accepted actor override or anonymous access", status)
		}
	}
	tg := int64(uuid.New().ID()) + 1000000000
	csrf = nativeReferralLink(t, f, a, csrf, email, key, tg, bcode)
	linked := nativeReferralMini(t, f, key, tg, bcode)
	if linked.Account.AccountId != aid || !reflect.DeepEqual(nativeReferralRead(t, f, f.public.Client(), linked.SessionToken), expected) {
		t.Fatal("later signed identity link changed account or original source")
	}
	legacyID := int64(uuid.New().ID()) + 1000000000
	legacyCode := fmt.Sprint(tg)
	bot.reason(legacyID, "/start "+legacyCode)
	wait(t, func() bool { return bot.message(legacyID, "Подписка,").ID > 0 })
	if !strings.Contains(string(bot.message(legacyID, "Подписка,").Markup), "https://t.me/fixture_bot?startapp="+legacyCode) {
		t.Fatal("old start lost exact numeric payload")
	}
	status, _, _ := f.send(t, f.public.Client(), "POST", "/api/v1/telegram/mini-app/session", map[string]string{"init_data": nativeReferralInit(key, legacyID, legacyCode)}, "", "", false)
	var count int
	if status != 409 || f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE telegram_id=$1`, legacyID).Scan(&count) != nil || count != 0 {
		t.Fatal("legacy start attributed before signed consent", status)
	}
	legacy := nativeReferralMini(t, f, key, legacyID, legacyCode)
	if later := nativeReferralMini(t, f, key, legacyID, bcode); later.Account.AccountId != legacy.Account.AccountId {
		t.Fatal("repeat changed signed account")
	}
	for referred, inviter := range map[uuid.UUID]uuid.UUID{bid: aid, cid: bid, legacy.Account.AccountId: aid} {
		var stored uuid.UUID
		if err := f.env.Pool.QueryRow(ctx, `SELECT referrer_account_id FROM referrals WHERE referred_account_id=$1`, referred).Scan(&stored); err != nil || stored != inviter {
			t.Fatal("first inviter changed after repeat/link", err)
		}
	}
	for _, source := range []string{"unknown-owned-source", "SELF"} {
		actor := int64(uuid.New().ID()) + 1000000000
		if source == "SELF" {
			source = fmt.Sprint(actor)
		}
		out := nativeReferralMini(t, f, key, actor, source)
		var saved string
		if f.env.Pool.QueryRow(ctx, `SELECT registration_source_code FROM accounts WHERE id=$1`, out.Account.AccountId).Scan(&saved) != nil || saved != source || f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM referrals WHERE referred_account_id=$1`, out.Account.AccountId).Scan(&count) != nil || count != 0 {
			t.Fatal("unknown/self source was lost or invented a referral")
		}
	}
	expected = nativeReferralRead(t, f, a)
	bot.reason(tg, "/referrals")
	wait(t, func() bool { return bot.message(tg, "Ваша ссылка:").ID > 0 })
	if text := bot.message(tg, "Ваша ссылка:").Text; !strings.Contains(text, first.WebUrl) || !strings.Contains(text, "9007199254740993") || !strings.Contains(text, "Ожидает дней: 4") {
		t.Fatal("native command lost own aggregate/link")
	}
	old := nativeMessage{ID: 7001, Chat: tg, Markup: json.RawMessage(`{"inline_keyboard":[[{"text":"Invitations","callback_data":"referral"}]]}`)}
	nativeClientCallback(bot, tg, old, "referral")
	wait(t, func() bool { return bot.message(tg, "Your link:").ID > 0 })
	if message := bot.message(tg, "Your link:"); !strings.Contains(message.Text, first.WebUrl) || !strings.Contains(string(message.Markup), "/mini-app/cabinet/referrals?lang=en") {
		t.Fatal("old callback lost native referral route")
	}
	// A forged callback points another actor at the original private chat.
	bot.mu.Lock()
	before := len(bot.messages)
	bot.sequence++
	bot.updates = append(bot.updates, map[string]any{"update_id": bot.sequence, "callback_query": map[string]any{"id": uuid.NewString(), "from": map[string]any{"id": legacyID, "language_code": "en"}, "message": map[string]any{"message_id": old.ID, "date": 1, "from": map[string]any{"id": 123456789, "is_bot": true}, "chat": map[string]any{"id": tg, "type": "private"}, "reply_markup": old.Markup}, "data": "referral"}})
	bot.mu.Unlock()
	wait(t, func() bool { bot.mu.Lock(); defer bot.mu.Unlock(); return len(bot.updates) == 0 })
	bot.mu.Lock()
	after := len(bot.messages)
	bot.mu.Unlock()
	if after != before {
		t.Fatal("forged callback sent private referral data")
	}
	stop()
	queue, err := river.NewClient(riverpgxv5.New(f.env.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var handler http.Handler
	f.public = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(f.public.Close)
	f.cfg.HTTP.CabinetOrigin = f.public.URL
	f.svc = app.NewModules(f.env.Pool, f.env.Redis, queue, &f.cfg)
	f.svc.MiniApp = telegram.NewMiniApp(123456789, pub, f.svc.Accounts, time.Now)
	handler = httpapi.New(f.svc, f.env.Pool, f.cfg.HTTP)
	launchNative(t, f, bot, true, false, true)
	restored := nativeReferralRead(t, f, a)
	if nativeReferralCode(t, restored) != code || !strings.HasPrefix(restored.WebUrl, f.public.URL+"/register?") || !reflect.DeepEqual(restored.Levels, expected.Levels) || restored.UnclassifiedRecords != expected.UnclassifiedRecords {
		t.Fatal("restart lost code/history or retained the old deployment origin")
	}
	if status, _, _ := f.send(t, a, "POST", "/api/v1/me/telegram/unlink", wire.CurrentPasswordInput{CurrentPassword: "fixture password with Unicode ✨"}, csrf, "", false); status != 200 {
		t.Fatal("owned referral unlink", status)
	}
	retired := nativeReferralMini(t, f, key, int64(uuid.New().ID())+1000000000, legacyCode)
	var retained uuid.UUID
	if f.env.Pool.QueryRow(ctx, `SELECT referrer_account_id FROM referrals WHERE referred_account_id=$1`, retired.Account.AccountId).Scan(&retained) != nil || retained != aid {
		t.Fatal("retired old identity lost the inviter")
	}
	if err = f.svc.Accounts.ChangeOperatorRole(ctx, oid, true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Accounts.SetOperatorRestriction(ctx, oid, legacy.Account.AccountId, uuid.New(), accounts.OperatorRestrictionInput{Restricted: true, Reason: "Owned referral permission acceptance"}); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := f.send(t, f.public.Client(), "GET", "/api/v1/referrals", nil, "", "", false, legacy.SessionToken); status != 401 && status != 403 {
		t.Fatal("revoked/restricted identity read private referral data", status)
	}
	prior := bot.message(legacyID, "").ID
	bot.reason(legacyID, "/referrals")
	wait(t, func() bool { return bot.message(legacyID, "Подписка,").ID > prior })
	if strings.Contains(bot.message(legacyID, "").Text, "r_") {
		t.Fatal("restricted native identity received a personal link")
	}
	var links, captures, jobs int
	if f.env.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events WHERE account_id=$1 AND action='referral_link_created'),(SELECT count(*) FROM audit_events WHERE action='referral_registered'),(SELECT count(*) FROM access_operations)`, aid).Scan(&links, &captures, &jobs) != nil || links != 1 || captures != 4 || jobs != 0 {
		t.Fatal("read/repeat/restart duplicated audit or issued access", links, captures, jobs)
	}
}

func TestNativeReferralsBrowser(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TESTS") != "1" {
		t.Skip("requires RUN_BROWSER_TESTS=1")
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f, bot := openMode(t, true, pub), &nativeBot{}
	launchNative(t, f, bot, true, false, true)
	email := nativeEmail("referral-browser-inviter")
	client, csrf, actor := f.signupAccount(t, email)
	out := nativeReferralRead(t, f, client)
	code := nativeReferralCode(t, out)
	f.signupAccount(t, nativeEmail("referral-browser-child"), code)
	nativeReferralRewards(t, f, actor)
	tg := int64(uuid.New().ID()) + 1000000000
	nativeReferralLink(t, f, client, csrf, email, key, tg, "ignored-late-source")
	uri, err := url.Parse(f.public.URL)
	if err != nil {
		t.Fatal(err)
	}
	var cookie string
	for _, item := range client.Jar.Cookies(uri) {
		if item.Name == "__Host-session" {
			cookie = item.Value
		}
	}
	if cookie == "" {
		t.Fatal("native referral browser session missing")
	}
	newEmail := nativeEmail("referral-browser-registration")
	data, err := json.Marshal(map[string]string{"client_cookie": cookie, "mini_init_data": nativeReferralInit(key, tg, "ignored-late-source"), "web_url": out.WebUrl, "registration_email": newEmail})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "native-referrals.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "tests/native-referrals.mjs")
	cmd.Dir = filepath.Join(f.root, "web")
	cmd.Env = append(os.Environ(), "TEST_ORIGIN="+f.public.URL, "TEST_NATIVE_REFERRALS_FILE="+file)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Logf("native browser assertion failed: %s", output)
		t.Fatal("native referrals browser failed", err)
	}
	var token string
	wait(t, func() bool {
		for _, letter := range f.letters(t, newEmail) {
			if match := regexp.MustCompile(`#token=([A-Za-z0-9_-]{43})`).FindStringSubmatch(letter); len(match) == 2 {
				token = match[1]
			}
		}
		return token != ""
	})
	if status, _, _ := f.send(t, f.public.Client(), "POST", "/api/v1/auth/verify-email", map[string]string{"token": token, "new_password": "owned browser registration password ✨"}, "", "", false); status != 200 {
		t.Fatal("rendered referral registration did not verify", status)
	}
	var captured, inviter uuid.UUID
	if f.env.Pool.QueryRow(context.Background(), `SELECT a.id,r.referrer_account_id FROM accounts a JOIN referrals r ON r.referred_account_id=a.id WHERE a.email_key=$1 AND a.registration_source_code=$2`, newEmail, code).Scan(&captured, &inviter) != nil || captured == uuid.Nil || inviter != actor {
		t.Fatal("rendered invitation lost source during mail verification")
	}
}
