package tests

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"fmt"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Catches losing a queued automatic grant on a whole-graph restart, depending
// on a running bot, or issuing another identity after an external write/local
// commit failure. With NATIVE_DOCKER_STATE this uses actual TLS 3X-UI 3.7.0.
func TestNativeTrialTelegramActivation(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := openMode(t, true, pub)
	ctx := context.Background()
	bot := &nativeBot{}
	_, stop := launchNative(t, f, bot, false, false)
	actor, campaign := nativeCampaign(t, f)
	tg := int64(uuid.New().ID()) + 1000000000
	raw := testkit.SignedMiniAppData(key, 123456789, time.Now(), fmt.Sprintf(`{"id":%d,"first_name":"Owned client","language_code":"en"}`, tg), *campaign.Code)
	status, body, _ := f.send(t, f.public.Client(), "POST", "/api/v1/telegram/mini-app/session", map[string]string{"init_data": raw, "accepted_terms_version": "1", "accepted_privacy_version": "1"}, "", "", false)
	var auth wire.MiniAppSessionResult
	if status != 200 || json.Unmarshal(body, &auth) != nil || auth.Capabilities.TrialMode == nil || *auth.Capabilities.TrialMode != "activate" {
		t.Fatal("native signed trial policy", status)
	}
	assertNativeCampaign(t, f, actor, campaign, auth.Account.AccountId, "telegram", 0)
	if _, err = f.env.Pool.Exec(ctx, `CREATE FUNCTION fail_auto_apply() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='applied' THEN RAISE EXCEPTION 'controlled fixture failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_auto_apply BEFORE UPDATE ON trial_operations FOR EACH ROW EXECUTE FUNCTION fail_auto_apply();`); err != nil {
		t.Fatal(err)
	}
	idempotency := uuid.NewString()
	status, body, _ = f.send(t, f.public.Client(), "POST", "/api/v1/trials/activate", map[string]string{}, auth.CsrfToken, idempotency, false, auth.SessionToken)
	var request wire.TrialRequest
	if status != 201 || json.Unmarshal(body, &request) != nil || request.Status != "approved" || request.OperationId == nil {
		t.Fatal("native activation", status)
	}
	operation := *request.OperationId
	var pending bool
	var before string
	if err = f.env.Pool.QueryRow(ctx, `SELECT status='pending' FROM trial_operations WHERE id=$1`, operation).Scan(&pending); err != nil || !pending {
		t.Fatal("queued trial lost before restart", err)
	}
	if err = f.env.Pool.QueryRow(ctx, `SELECT vpn_id::text||':'||sub_id||':'||panel_key FROM accounts WHERE id=$1`, auth.Account.AccountId).Scan(&before); err != nil {
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
	f.svc.MiniApp = telegram.NewMiniApp(123456789, pub, f.svc.Accounts, time.Now)
	handler = httpapi.New(f.svc, f.env.Pool, f.cfg.HTTP)
	launchNative(t, f, bot, false, true)
	status, body, _ = f.send(t, f.public.Client(), "POST", "/api/v1/trials/activate", map[string]string{}, auth.CsrfToken, idempotency, false, auth.SessionToken)
	var replay wire.TrialRequest
	if status != 200 || json.Unmarshal(body, &replay) != nil || replay.RequestId != request.RequestId || replay.OperationId == nil || *replay.OperationId != operation {
		t.Fatal("restart replay changed issuance", status)
	}
	wait(t, func() bool {
		var state string
		return f.env.Pool.QueryRow(ctx, `SELECT status FROM trial_operations WHERE id=$1`, operation).Scan(&state) == nil && state == "needs_review"
	})
	var target []byte
	if err = f.env.Pool.QueryRow(ctx, `SELECT target FROM trial_operations WHERE id=$1`, operation).Scan(&target); err != nil || len(target) == 0 {
		t.Fatal("uncertain target not retained", err)
	}
	status, _, _ = f.send(t, f.public.Client(), "POST", "/api/v1/trials/activate", map[string]string{}, auth.CsrfToken, uuid.NewString(), false, auth.SessionToken)
	if status != 409 {
		t.Fatal("uncertain grant reissued", status)
	}
	if _, err = f.env.Pool.Exec(ctx, `DROP TRIGGER fail_auto_apply ON trial_operations; DROP FUNCTION fail_auto_apply()`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.Subscriptions.ReconcileTrialOperation(ctx, operation, uuid.New(), subscriptions.ReconcileInput{OperatorTgId: 101, Reason: "Owned automatic trial recovery"}); err != nil {
		t.Fatal("native reconcile", err)
	}
	wait(t, func() bool { return applied(f, operation) })
	assertNativePanel(t, f, operation)
	var after string
	var grants, operations, requests, cards int
	var sameTarget bool
	err = f.env.Pool.QueryRow(ctx, `SELECT vpn_id::text||':'||sub_id||':'||panel_key,(SELECT count(*) FROM trial_grants WHERE account_id=$1),(SELECT count(*) FROM trial_operations WHERE account_id=$1),(SELECT count(*) FROM trial_requests WHERE account_id=$1),(SELECT count(*) FROM telegram_deliveries WHERE kind='approval_card'),(SELECT target=$2::jsonb FROM trial_operations WHERE id=$3) FROM accounts WHERE id=$1`, auth.Account.AccountId, target, operation).Scan(&after, &grants, &operations, &requests, &cards, &sameTarget)
	if err != nil || after != before || grants != 1 || operations != 1 || requests != 1 || cards != 0 || !sameTarget {
		t.Fatal("native recovery changed identity or grant", err)
	}
	status, _, response := f.send(t, f.public.Client(), "GET", "/api/v1/subscription/key", nil, "", "", false, auth.SessionToken)
	if status != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("confirmed native connection unavailable", status)
	}
	assertNativeCampaign(t, f, actor, campaign, auth.Account.AccountId, "telegram", 1)
}

// Catches a frontend/backend bearer or empty-body mismatch that separate mock
// tests cannot see. Only the external SDK is stubbed; every API call is real.
func TestNativeTrialTelegramActivationBrowser(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TESTS") != "1" {
		t.Skip("requires RUN_BROWSER_TESTS=1")
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := openMode(t, true, pub)
	bot := &nativeBot{}
	launchNative(t, f, bot, false, true)
	tg := int64(uuid.New().ID()) + 1000000000
	raw := testkit.SignedMiniAppData(key, 123456789, time.Now(), fmt.Sprintf(`{"id":%d,"first_name":"Owned browser client","language_code":"en"}`, tg), "owned-browser-campaign")
	file := filepath.Join(t.TempDir(), "mini-init.json")
	data, err := json.Marshal(map[string]string{"init_data": raw})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("npm", "run", "test:e2e", "--", "--grep", "Telegram trial real")
	cmd.Dir = filepath.Join(f.root, "web")
	cmd.Env = append(os.Environ(), "E2E_MODE=real", "TEST_ORIGIN="+f.public.URL, "TEST_MINI_INIT_FILE="+file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned signed browser failed: %s", out)
	}
	var operation uuid.UUID
	var grants, operations int
	err = f.env.Pool.QueryRow(context.Background(), `SELECT r.operation_id,(SELECT count(*) FROM trial_grants g WHERE g.account_id=a.id),(SELECT count(*) FROM trial_operations o WHERE o.account_id=a.id) FROM accounts a JOIN trial_requests r ON r.account_id=a.id WHERE a.telegram_id=$1 AND r.decision_source='telegram_auto' AND r.status='approved'`, tg).Scan(&operation, &grants, &operations)
	if err != nil || grants != 1 || operations != 1 {
		t.Fatal("browser repeated trial", err)
	}
	assertNativePanel(t, f, operation)
}
