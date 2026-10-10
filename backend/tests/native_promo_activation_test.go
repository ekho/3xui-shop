package tests

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func nativePromoCreate(t *testing.T, f *fixture, actor *http.Client, csrf string, days int) bonuses.Promocode {
	t.Helper()
	status, raw, _ := f.send(t, actor, "POST", "/api/v1/operator/promocodes", map[string]any{"duration_days": days, "reason": "owned activation native fixture"}, csrf, uuid.NewString(), false)
	var out bonuses.Promocode
	if status != 201 || json.Unmarshal(raw, &out) != nil {
		t.Fatal("native code create", status)
	}
	return out
}

func TestNativePromoActivationSharedOwnerRestart(t *testing.T) {
	publicKey, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := openMode(t, true, publicKey)
	ctx := context.Background()
	bot := &nativeBot{}
	_, stop := launchNative(t, f, bot, true, false, true)
	operator, csrf, actor := f.signupAccount(t, nativeEmail("promo-operator"))
	if err = f.svc.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	web, webCSRF, webID := f.signupAccount(t, nativeEmail("promo-web"))
	webCode := nativePromoCreate(t, f, operator, csrf, 7)
	webKey := uuid.NewString()
	status, raw, _ := f.send(t, web, "POST", "/api/v1/promocodes/activate", map[string]string{"code": webCode.Code}, webCSRF, webKey, false)
	var webActivation bonuses.PromocodeActivation
	if status != 201 || json.Unmarshal(raw, &webActivation) != nil || webActivation.Status != "pending" {
		t.Fatal("web queued activation", status)
	}
	status, _, _ = f.send(t, operator, "GET", "/api/v1/promocodes/activations/"+webActivation.OperationID.String(), nil, "", "", false)
	if status != 404 {
		t.Fatal("foreign activation ownership", status)
	}
	tg := int64(uuid.New().ID()) + 1000000000
	signed := testkit.SignedMiniAppData(key, 123456789, time.Now(), fmt.Sprintf(`{"id":%d,"first_name":"Owned promo client","language_code":"en"}`, tg), "")
	status, raw, _ = f.send(t, f.public.Client(), "POST", "/api/v1/telegram/mini-app/session", map[string]string{"init_data": signed, "accepted_terms_version": "1", "accepted_privacy_version": "1"}, "", "", false)
	var mini wire.MiniAppSessionResult
	if status != 200 || json.Unmarshal(raw, &mini) != nil {
		t.Fatal("signed MiniApp identity", status)
	}
	miniCode := nativePromoCreate(t, f, operator, csrf, 3)
	miniKey := uuid.NewString()
	status, raw, _ = f.send(t, f.public.Client(), "POST", "/api/v1/promocodes/activate", map[string]string{"code": miniCode.Code}, mini.CsrfToken, miniKey, false, mini.SessionToken)
	var miniActivation bonuses.PromocodeActivation
	if status != 201 || json.Unmarshal(raw, &miniActivation) != nil {
		t.Fatal("signed MiniApp activation", status)
	}
	status, _, _ = f.send(t, f.public.Client(), "GET", "/api/v1/promocodes/activations/"+webActivation.OperationID.String(), nil, "", "", false, mini.SessionToken)
	if status != 404 {
		t.Fatal("MiniApp read another grant", status)
	}
	oldOrigin, _ := url.Parse(f.public.URL)
	cookies := web.Jar.Cookies(oldOrigin)
	var before string
	if err = f.env.Pool.QueryRow(ctx, "SELECT vpn_id::text||':'||sub_id||':'||panel_key FROM accounts WHERE id=$1", webID).Scan(&before); err != nil {
		t.Fatal(err)
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
	f.svc.MiniApp = telegram.NewMiniApp(123456789, publicKey, f.svc.Accounts, time.Now)
	handler = httpapi.New(f.svc, f.env.Pool, f.cfg.HTTP)
	launchNative(t, f, bot, true, true, true)
	web = f.public.Client()
	web.Jar, _ = cookiejar.New(nil)
	newOrigin, _ := url.Parse(f.public.URL)
	web.Jar.SetCookies(newOrigin, cookies)
	status, raw, _ = f.send(t, web, "POST", "/api/v1/promocodes/activate", map[string]string{"code": webCode.Code}, webCSRF, webKey, false)
	var replay bonuses.PromocodeActivation
	if status != 201 || json.Unmarshal(raw, &replay) != nil || replay != webActivation {
		t.Fatal("restart lost web response/replay", status)
	}
	status, raw, _ = f.send(t, f.public.Client(), "POST", "/api/v1/promocodes/activate", map[string]string{"code": miniCode.Code}, mini.CsrfToken, miniKey, false, mini.SessionToken)
	if status != 201 || json.Unmarshal(raw, &replay) != nil || replay != miniActivation {
		t.Fatal("restart lost MiniApp response/replay", status)
	}
	for _, id := range []uuid.UUID{webActivation.OperationID, miniActivation.OperationID} {
		wait(t, func() bool {
			var state string
			return f.env.Pool.QueryRow(ctx, "SELECT status FROM access_operations WHERE id=$1", id).Scan(&state) == nil && state == "applied"
		})
	}
	// The native fixture loses the first AddClient response. Readback, never a second grant, resolves it.
	var after string
	var operations, history int
	if err = f.env.Pool.QueryRow(ctx, `SELECT vpn_id::text||':'||sub_id||':'||panel_key,(SELECT count(*) FROM access_operations WHERE account_id=$1),(SELECT count(*) FROM promocode_events WHERE action='activate' AND actor_account_id=$1) FROM accounts WHERE id=$1`, webID).Scan(&after, &operations, &history); err != nil || after != before || operations != 1 || history != 1 {
		t.Fatal("restart identities/one-time history", err)
	}
	for _, tc := range []struct {
		id, account uuid.UUID
		days        int
	}{{webActivation.OperationID, webID, 7}, {miniActivation.OperationID, mini.Account.AccountId, 3}} {
		var target []byte
		if err = f.env.Pool.QueryRow(ctx, "SELECT target FROM access_operations WHERE id=$1", tc.id).Scan(&target); err != nil {
			t.Fatal(err)
		}
		var desired struct {
			PanelKey    string `json:"panel_key"`
			DeviceCount int64  `json:"device_count"`
			Traffic     int64  `json:"traffic_limit_bytes"`
			Expiry      int64  `json:"expiry_time_ms"`
		}
		if json.Unmarshal(target, &desired) != nil || desired.DeviceCount != 1 || desired.Traffic != 0 {
			t.Fatal("no-subscription bonus limits")
		}
		client, err := f.svc.VPN.PanelClient().GetClient(ctx, desired.PanelKey)
		if err != nil || client == nil || client.ExpiryTimeMS != desired.Expiry || client.LimitIP != 2 || client.TrafficLimitBytes != 0 {
			t.Fatal("native confirmed bonus target", err)
		}
	}
	status, raw, response := f.send(t, web, "GET", "/api/v1/promocodes/activations/"+webActivation.OperationID.String(), nil, "", "", false)
	if status != 200 || json.Unmarshal(raw, &replay) != nil || replay.Status != "applied" || response.Header.Get("Cache-Control") != "no-store" || strings.Contains(string(raw), webCode.Code) {
		t.Fatal("current web status/privacy", status)
	}
	// A later Telegram command for the same account uses the common owner, extending the same identity.
	tgActor, tgCSRF, actorID := f.signupAccount(t, nativeEmail("promo-tg-operator"))
	if err = f.svc.Accounts.ChangeOperatorRole(ctx, actorID, true); err != nil {
		t.Fatal(err)
	}
	tgCode := nativePromoCreate(t, f, tgActor, tgCSRF, 2)
	bot.reason(tg, "/promocode "+tgCode.Code)
	wait(t, func() bool { return bot.message(tg, "Promocode: 2 days.").ID > 0 })
	var tgOperation uuid.UUID
	if err = f.env.Pool.QueryRow(ctx, "SELECT (after_snapshot->>'access_operation_id')::uuid FROM promocode_events WHERE promocode_id=$1 AND action='activate'", tgCode.PromocodeID).Scan(&tgOperation); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool {
		var state string
		return f.env.Pool.QueryRow(ctx, "SELECT status FROM access_operations WHERE id=$1", tgOperation).Scan(&state) == nil && state == "applied"
	})
	bot.reason(tg, "/promocode_status "+tgOperation.String())
	wait(t, func() bool { return bot.message(tg, "Status: applied").ID > 0 })
	bot.reason(tg, "/promocode "+tgCode.Code)
	wait(t, func() bool { return bot.message(tg, "already been used").ID > 0 })
	bot.mu.Lock()
	defer bot.mu.Unlock()
	for _, m := range bot.messages {
		if strings.Contains(m.Text, tgCode.Code) || strings.Contains(string(m.Markup), tgCode.Code) {
			t.Fatal("Telegram response leaked redeemable code")
		}
	}
}

func TestNativePromoActivationBrowser(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TESTS") != "1" {
		t.Skip("requires RUN_BROWSER_TESTS=1")
	}
	f := open(t)
	client, csrf, account := f.signupAccount(t, nativeEmail("promo-browser"))
	operator, operatorCSRF, actor := f.signupAccount(t, nativeEmail("promo-browser-operator"))
	if err := f.svc.Accounts.ChangeOperatorRole(context.Background(), actor, true); err != nil {
		t.Fatal(err)
	}
	code := nativePromoCreate(t, f, operator, operatorCSRF, 4)
	origin, _ := url.Parse(f.public.URL)
	cookie := ""
	for _, c := range client.Jar.Cookies(origin) {
		if c.Name == "__Host-session" {
			cookie = c.Value
		}
	}
	data, _ := json.Marshal(map[string]any{"client_cookie": cookie, "client_csrf": csrf, "code": code.Code, "days": 4})
	file := filepath.Join(t.TempDir(), "promo-activation.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "tests/native-promo-activation.mjs")
	cmd.Dir = filepath.Join(f.root, "web")
	cmd.Env = append(os.Environ(), "TEST_ORIGIN="+f.public.URL, "TEST_NATIVE_PROMO_ACTIVATION_FILE="+file)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native promo browser: %s", output)
	}
	var n int
	if err := f.env.Pool.QueryRow(context.Background(), "SELECT count(*) FROM access_operations WHERE account_id=$1", account).Scan(&n); err != nil || n != 1 {
		t.Fatal("browser did not create one durable grant", err)
	}
}
