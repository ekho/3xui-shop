package tests

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
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
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type nativeMaintenanceStatus struct {
	Enabled   bool    `json:"enabled"`
	Revision  int64   `json:"revision"`
	ChangedAt *string `json:"changed_at"`
}

func sameMaintenanceStatus(a, b nativeMaintenanceStatus) bool {
	if a.Enabled != b.Enabled || a.Revision != b.Revision || (a.ChangedAt == nil) != (b.ChangedAt == nil) {
		return false
	}
	if a.ChangedAt == nil {
		return true
	}
	left, leftErr := time.Parse(time.RFC3339Nano, *a.ChangedAt)
	right, rightErr := time.Parse(time.RFC3339Nano, *b.ChangedAt)
	return leftErr == nil && rightErr == nil && left.Equal(right)
}

func nativeMaintenanceError(body []byte) string {
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &response)
	return response.Error.Code
}

func maintenanceStatus(t *testing.T, f *fixture, client *http.Client, path string) nativeMaintenanceStatus {
	t.Helper()
	status, body, _ := f.send(t, client, "GET", path, nil, "", "", false)
	var current nativeMaintenanceStatus
	if status != http.StatusOK || json.Unmarshal(body, &current) != nil {
		t.Fatal("maintenance status unavailable", status)
	}
	return current
}

func setNativeMaintenance(t *testing.T, f *fixture, client *http.Client, csrf string, enabled bool, revision int64, key string) nativeMaintenanceStatus {
	t.Helper()
	status, body, _ := f.send(t, client, "POST", "/api/v1/operator/maintenance", map[string]any{
		"enabled": enabled, "expected_revision": revision, "reason": "Owned native boundary acceptance", "confirmed": true,
	}, csrf, key, false)
	var current nativeMaintenanceStatus
	if status != http.StatusOK || json.Unmarshal(body, &current) != nil || current.Enabled != enabled || current.Revision != revision+1 || current.ChangedAt == nil {
		t.Fatal("protected maintenance transition unavailable", status)
	}
	return current
}

func nativeMaintenanceStart(bot *nativeBot, actor int64, lang string) {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	bot.sequence++
	bot.updates = append(bot.updates, map[string]any{"update_id": bot.sequence, "message": map[string]any{
		"message_id": 1000 + bot.sequence, "date": time.Now().Unix(),
		"from": map[string]any{"id": actor, "is_bot": false, "language_code": lang},
		"chat": map[string]any{"id": actor, "type": "private"}, "text": "/start",
	}})
}

// The admitted invoice survives the switch. New admission is denied in both
// client channels while Telegram money, River, and operator decisions continue.
func TestNativeMaintenanceFlow(t *testing.T) {
	f, bot, key, plan := nativeStarsFixture(t)
	ctx := context.Background()
	runtime, stop := launchNative(t, f, bot, true, true, true)
	wait(t, runtime.StarsGateway().Ready)
	operator, operatorCSRF, actor := f.signupAccount(t, nativeEmail("maintenance-operator"))
	if err := f.svc.Accounts.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal("operator fixture role", err)
	}
	webClient, webCSRF, webAccount := f.signupAccount(t, nativeEmail("maintenance-web"))
	status, body, _ := f.send(t, webClient, "POST", "/api/v1/trial-requests", map[string]string{"comment": "admitted before switch"}, webCSRF, uuid.NewString(), false)
	var admittedTrial wire.TrialRequest
	if status != http.StatusCreated || json.Unmarshal(body, &admittedTrial) != nil {
		t.Fatal("pre-switch trial admission", status)
	}
	card := cardFor(t, bot, 101, admittedTrial.RequestId)
	tg := int64(uuid.New().ID()) + 1000000000
	mini, order := nativeStarsOrder(t, f, key, tg, plan)
	defaultState := maintenanceStatus(t, f, f.public.Client(), "/api/v1/maintenance")
	if defaultState.Enabled || defaultState.Revision != 0 || defaultState.ChangedAt != nil {
		t.Fatal("maintenance default is not off")
	}
	if got := maintenanceStatus(t, f, operator, "/api/v1/operator/maintenance"); !sameMaintenanceStatus(got, defaultState) {
		t.Fatal("operator and public status differ before switch")
	}
	if status, _, _ := f.send(t, webClient, "POST", "/api/v1/operator/maintenance", map[string]any{"enabled": true, "expected_revision": 0, "reason": "denied", "confirmed": true}, webCSRF, uuid.NewString(), false); status != http.StatusForbidden {
		t.Fatal("client changed protected mode", status)
	}
	enabledKey := uuid.NewString()
	enabled := setNativeMaintenance(t, f, operator, operatorCSRF, true, 0, enabledKey)
	if got := maintenanceStatus(t, f, f.public.Client(), "/api/v1/maintenance"); !sameMaintenanceStatus(got, enabled) {
		t.Fatal("public status did not reflect protected switch")
	}
	if status, body, _ := f.send(t, webClient, "POST", "/api/v1/trial-requests", map[string]string{"comment": "new while paused"}, webCSRF, uuid.NewString(), false); status != http.StatusServiceUnavailable || nativeMaintenanceError(body) != "MAINTENANCE" {
		t.Fatal("web trial admission escaped maintenance", status)
	}
	newOrder := map[string]any{"action": "purchase", "payment_method": "telegram_stars", "payment_type": "STARS", "plan_id": plan, "revision": 1, "period_days": 30}
	if status, body, _ := f.send(t, f.public.Client(), "POST", "/api/v1/orders", newOrder, mini.CsrfToken, uuid.NewString(), false, mini.SessionToken); status != http.StatusServiceUnavailable || nativeMaintenanceError(body) != "MAINTENANCE" {
		t.Fatal("signed Mini purchase admission escaped maintenance", status)
	}
	if status, _, _ := f.send(t, webClient, "POST", "/api/v1/support/messages", map[string]string{"text": "Owned support remains available"}, webCSRF, uuid.NewString(), false); status != http.StatusCreated {
		t.Fatal("existing client support was stopped", status)
	}
	bot.callback(101, card.ID, "a", admittedTrial.RequestId, uuid.NewString())
	wait(t, func() bool { return trialStatus(f, admittedTrial.RequestId) == "approved" })
	trialOperation := operationFor(t, f, admittedTrial.RequestId)
	wait(t, func() bool { return applied(f, trialOperation) })
	assertNativePanel(t, f, trialOperation)
	proof := nativeStarsProof(tg, order.OrderId, "owned-maintenance-charge", time.Now())
	bot.preCheckout(proof)
	wait(t, func() bool {
		bot.mu.Lock()
		defer bot.mu.Unlock()
		return len(bot.starsPreChecks) == 1
	})
	bot.mu.Lock()
	precheckoutAccepted := bot.starsPreChecks[0]
	bot.mu.Unlock()
	if !precheckoutAccepted {
		t.Fatal("accepted invoice precheckout was blocked")
	}
	bot.starsMoney(proof, false)
	wait(t, func() bool {
		var state string
		return f.env.Pool.QueryRow(ctx, `SELECT fulfillment_status FROM purchase_orders WHERE id=$1`, order.OrderId).Scan(&state) == nil && state == "applied"
	})
	target := assertNativeStarsPanel(t, f, order.OrderId)
	for lang, marker := range map[string]string{"ru": "технические работы", "en": "maintenance"} {
		chat := int64(uuid.New().ID()) + 1000000000
		nativeMaintenanceStart(bot, chat, lang)
		wait(t, func() bool { return strings.Contains(strings.ToLower(bot.message(chat, "").Text), marker) })
		message := bot.message(chat, "")
		if !strings.Contains(string(message.Markup), "mini-app/cabinet") || !strings.Contains(string(message.Markup), "support") {
			t.Fatal("maintenance start lost cabinet or support route", lang)
		}
	}
	// A second assembled graph reads the same durable row, without local cache.
	queue, err := river.NewClient(riverpgxv5.New(f.env.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	second := app.NewModules(f.env.Pool, f.env.Redis, queue, &f.cfg)
	secondServer := httptest.NewTLSServer(httpapi.New(second, f.env.Pool, f.cfg.HTTP))
	defer secondServer.Close()
	resp, err := secondServer.Client().Get(secondServer.URL + "/api/v1/maintenance")
	if err != nil {
		t.Fatal("second assembled status read", err)
	}
	var secondState nativeMaintenanceStatus
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&secondState) != nil || !sameMaintenanceStatus(secondState, enabled) {
		resp.Body.Close()
		t.Fatal("second assembled instance diverged")
	}
	resp.Body.Close()
	stop()
	queue, err = river.NewClient(riverpgxv5.New(f.env.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var handler http.Handler
	f.public = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(f.public.Close)
	f.cfg.HTTP.CabinetOrigin = f.public.URL
	f.svc = app.NewModules(f.env.Pool, f.env.Redis, queue, &f.cfg)
	f.svc.MiniApp = telegram.NewMiniApp(123456789, key.Public().(ed25519.PublicKey), f.svc.Accounts, time.Now)
	handler = httpapi.New(f.svc, f.env.Pool, f.cfg.HTTP)
	runtime, _ = launchNative(t, f, bot, true, true, true)
	wait(t, runtime.StarsGateway().Ready)
	if got := maintenanceStatus(t, f, f.public.Client(), "/api/v1/maintenance"); !sameMaintenanceStatus(got, enabled) {
		t.Fatal("restart lost enabled state")
	}
	bot.starsMoney(proof, false)
	wait(t, func() bool { bot.mu.Lock(); defer bot.mu.Unlock(); return len(bot.updates) == 0 })
	var receipts, operations, grants, trialGrants, trials int
	if err := f.env.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM purchase_receipts WHERE order_id=$1), (SELECT count(*) FROM access_operations WHERE purchase_order_id=$1), (SELECT count(*) FROM purchase_orders WHERE account_id=$2), (SELECT count(*) FROM trial_grants WHERE account_id=$3), (SELECT count(*) FROM trial_requests WHERE account_id=$3)`, order.OrderId, mini.Account.AccountId, webAccount).Scan(&receipts, &operations, &grants, &trialGrants, &trials); err != nil || receipts != 1 || operations != 1 || grants != 1 || trialGrants != 1 || trials != 1 {
		t.Fatal("duplicate or restart changed paid grant", err)
	}
	if restored := assertNativeStarsPanel(t, f, order.OrderId); restored.VPNID != target.VPNID || restored.SubID != target.SubID || restored.PanelKey != target.PanelKey {
		t.Fatal("restart changed paid access identity")
	}
	if status, _, _ := f.send(t, f.public.Client(), "GET", "/api/v1/subscription/key", nil, "", "", false, mini.SessionToken); status != http.StatusOK {
		t.Fatal("existing access read stopped", status)
	}
	if status, _, _ := f.send(t, f.public.Client(), "GET", "/api/v1/orders/"+order.OrderId.String(), nil, "", "", false, mini.SessionToken); status != http.StatusOK {
		t.Fatal("accepted order history read stopped", status)
	}
	if status, body, _ := f.send(t, operator, "POST", "/api/v1/operator/maintenance", map[string]any{"enabled": false, "expected_revision": 0, "reason": "stale", "confirmed": true}, operatorCSRF, uuid.NewString(), false); status != http.StatusConflict || nativeMaintenanceError(body) != "MAINTENANCE_CONFLICT" {
		t.Fatal("stale operator decision changed mode", status)
	}
	// The old successful reply is a snapshot; it must not reapply its toggle.
	if status, _, _ := f.send(t, operator, "POST", "/api/v1/operator/maintenance", map[string]any{"enabled": true, "expected_revision": 0, "reason": "Owned native boundary acceptance", "confirmed": true}, operatorCSRF, enabledKey, false); status != http.StatusOK {
		t.Fatal("operator replay unavailable", status)
	}
	current := maintenanceStatus(t, f, f.public.Client(), "/api/v1/maintenance")
	if !sameMaintenanceStatus(current, enabled) {
		t.Fatal("stale decision or replay changed authoritative state")
	}
	disabled := setNativeMaintenance(t, f, operator, operatorCSRF, false, enabled.Revision, uuid.NewString())
	if got := maintenanceStatus(t, f, f.public.Client(), "/api/v1/maintenance"); !sameMaintenanceStatus(got, disabled) {
		t.Fatal("disable did not open admission")
	}
	status, body, _ = f.send(t, operator, "POST", "/api/v1/operator/maintenance", map[string]any{"enabled": true, "expected_revision": 0, "reason": "Owned native boundary acceptance", "confirmed": true}, operatorCSRF, enabledKey, false)
	var saved nativeMaintenanceStatus
	if status != http.StatusOK || json.Unmarshal(body, &saved) != nil || !sameMaintenanceStatus(saved, enabled) || !sameMaintenanceStatus(maintenanceStatus(t, f, f.public.Client(), "/api/v1/maintenance"), disabled) {
		t.Fatal("old accepted replay re-enabled maintenance", status)
	}
	newClient, newCSRF, _ := f.signupAccount(t, nativeEmail("maintenance-reopened"))
	status, _, _ = f.send(t, newClient, "POST", "/api/v1/trial-requests", map[string]string{"comment": "after switch"}, newCSRF, uuid.NewString(), false)
	if status != http.StatusCreated {
		t.Fatal("new admission remained blocked after disable", status)
	}
}

// Chromium loads the built UI from the assembled TLS server. Only the Telegram
// browser SDK is stubbed; status and operator mutations use the real Go API.
func TestNativeMaintenanceBrowser(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TESTS") != "1" {
		t.Skip("requires RUN_BROWSER_TESTS=1")
	}
	f, bot, key, _ := nativeStarsFixture(t)
	launchNative(t, f, bot, true, true, true)
	operator, operatorCSRF, actor := f.signupAccount(t, nativeEmail("maintenance-browser-operator"))
	if err := f.svc.Accounts.ChangeOperatorRole(context.Background(), actor, true); err != nil {
		t.Fatal("browser operator role", err)
	}
	client, _, _ := f.signupAccount(t, nativeEmail("maintenance-browser-client"))
	origin, err := url.Parse(f.public.URL)
	if err != nil {
		t.Fatal(err)
	}
	cookie := func(c *http.Client) string {
		for _, entry := range c.Jar.Cookies(origin) {
			if entry.Name == "__Host-session" {
				return entry.Value
			}
		}
		return ""
	}
	if cookie(operator) == "" || cookie(client) == "" {
		t.Fatal("browser fixture session cookie missing")
	}
	tg := int64(uuid.New().ID()) + 1000000000
	data, err := json.Marshal(map[string]string{
		"operator_cookie": cookie(operator), "operator_csrf": operatorCSRF, "client_cookie": cookie(client),
		"mini_init_data": nativeStarsInit(key, tg),
	})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "native-maintenance.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "tests/native-maintenance.mjs")
	cmd.Dir = filepath.Join(f.root, "web")
	cmd.Env = append(os.Environ(), "TEST_ORIGIN="+f.public.URL, "TEST_NATIVE_MAINTENANCE_FILE="+file)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Logf("native browser assertion failed: %s", output)
		t.Fatal("native maintenance browser failed", err)
	}
	if got := maintenanceStatus(t, f, f.public.Client(), "/api/v1/maintenance"); got.Enabled || got.Revision != 4 {
		t.Fatal("browser did not complete protected enable/conflict/recovery/disable")
	}
}
