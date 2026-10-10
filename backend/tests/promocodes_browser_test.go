package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestPromocodesBrowser(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TESTS") != "1" {
		t.Skip("requires RUN_BROWSER_TESTS=1")
	}
	f := open(t)
	operator, csrf, actor := f.signupAccount(t, nativeEmail("promocode-browser-operator"))
	if err := f.svc.Accounts.ChangeOperatorRole(context.Background(), actor, true); err != nil {
		t.Fatal("browser operator role", err)
	}
	client, _, clientID := f.signupAccount(t, nativeEmail("promocode-browser-client"))
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
	data, err := json.Marshal(map[string]string{"operator_cookie": cookie(operator), "operator_csrf": csrf, "client_cookie": cookie(client), "used_code": "USED_FIXTURE_48", "used_account_id": clientID.String()})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "promocodes-browser.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(phase string) {
		t.Helper()
		cmd := exec.Command("node", "tests/native-promocodes.mjs")
		cmd.Dir = filepath.Join(f.root, "web")
		cmd.Env = append(os.Environ(), "TEST_ORIGIN="+f.public.URL, "TEST_NATIVE_PROMOCODES_FILE="+file, "TEST_NATIVE_PROMOCODES_PHASE="+phase)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("native promocodes browser %s failed: %s", phase, output)
		}
	}
	run("core")
	_, err = f.env.Pool.Exec(context.Background(), `INSERT INTO promocodes(id,code,duration_days,revision,created_at,is_activated,activated_account_id,activated_by_tg_id,activated_at,legacy_source,legacy_promocode_id)
 VALUES($1,$2,400,1,NULL,true,$3,9223372036854775807,NULL,'fixture',9223372036854775807)`, uuid.New(), "USED_FIXTURE_48", clientID)
	if err != nil {
		t.Fatal("used legacy fixture insert", err)
	}
	run("used")
}
