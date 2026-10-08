package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func campaignCLI(t *testing.T, flag, body string) (map[string]any, error) {
	t.Helper()
	input, err := os.CreateTemp(t.TempDir(), "package")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if _, err = input.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if _, err = input.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	args, stdin, stdout := os.Args, os.Stdin, os.Stdout
	defer func() { os.Args, os.Stdin, os.Stdout = args, stdin, stdout }()
	os.Args, os.Stdin, os.Stdout = []string{"server", "import-legacy-campaigns", flag}, input, writer
	resultErr := run()
	writer.Close()
	os.Stdout = stdout
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.Unmarshal(raw, &out)
	return out, resultErr
}

// Exercises the actual stdin command and full rollback, without a bot/worker.
func TestCampaignImportAtomic(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	file := ownedImportDatabaseFile(t, e)
	t.Setenv("DATABASE_URL_FILE", file)
	m := app.NewModules(e.Pool, e.Redis, nil, &app.Config{})
	ids := map[int64]uuid.UUID{}
	for i, tg := range []int64{701, 702} {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		a, err := m.Accounts.CreateTelegram(ctx, tx, accounts.TelegramInput{TelegramID: tg, DisplayName: "Owned legacy mapping", Locale: "ru"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE accounts SET legacy_user_id=$2 WHERE id=$1`, a.ID, i+5); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		ids[tg] = a.ID
	}
	good := `{"version":1,"source":"owned-copy","campaigns":[{"source_id":9007199254740993,"name":" Exact campaign ✨ ","hash_code":"oldhashx","clicks":17,"is_active":false,"created_at":"2026-10-01T00:04:05.123456Z"}],"users":[{"source_legacy_user_id":5,"source_tg_id":701,"source_invite_name":" Exact campaign ✨ ","is_trial_used":true},{"source_legacy_user_id":6,"source_tg_id":702,"source_invite_name":"Deleted name","is_trial_used":false}]}`
	snapshot := func() string {
		t.Helper()
		var value string
		if err := e.Pool.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(x ORDER BY id) FROM campaigns x),(SELECT jsonb_agg(x ORDER BY account_id) FROM campaign_acquisitions x),(SELECT jsonb_agg(x ORDER BY id) FROM campaign_events x),(SELECT jsonb_agg(x ORDER BY id) FROM purchase_orders x),(SELECT jsonb_agg(x ORDER BY id) FROM river_job x),(SELECT jsonb_agg(x ORDER BY id) FROM access_operations x),(SELECT jsonb_agg(x ORDER BY account_id) FROM trial_grants x))::text`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	if out, err := campaignCLI(t, "--dry-run", good); err != nil || out["inserted_campaigns"] != float64(2) || out["inserted_users"] != float64(2) || snapshot() != before {
		t.Fatal("actual dry-run command or wrote data", err)
	}
	if out, err := campaignCLI(t, "--apply", good); err != nil || out["inserted_campaigns"] != float64(2) || out["inserted_users"] != float64(2) {
		t.Fatal("apply", err)
	}
	after := snapshot()
	var name, code, legacyID, state, source string
	var clicks int64
	if err := e.Pool.QueryRow(ctx, `SELECT name,code,legacy_invite_id::text,state,legacy_clicks,source FROM campaigns WHERE name=' Exact campaign ✨ '`).Scan(&name, &code, &legacyID, &state, &clicks, &source); err != nil || code != "oldhashx" || legacyID != "9007199254740993" || state != "paused" || clicks != 17 || source != "legacy_import" {
		t.Fatal("raw ID/name/hash/state", err)
	}
	var orphan bool
	if err := e.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM campaigns WHERE name='Deleted name' AND code IS NULL AND state='deleted' AND source='legacy_orphan')`).Scan(&orphan); err != nil || !orphan {
		t.Fatal("orphan fabricated code", err)
	}
	var channel string
	var trial bool
	if err := e.Pool.QueryRow(ctx, `SELECT channel,legacy_trial_used FROM campaign_acquisitions WHERE account_id=$1`, ids[701]).Scan(&channel, &trial); err != nil || channel != "legacy_name" || !trial {
		t.Fatal("name-reference/flag", err)
	}
	for _, flag := range []string{"--apply", "--dry-run"} {
		if out, err := campaignCLI(t, flag, good); err != nil || out["inserted_campaigns"] != float64(0) || out["inserted_users"] != float64(0) || snapshot() != after {
			t.Fatal("replay writes", err)
		}
	}
	fresh := `{"source_id":2,"name":"Fresh before conflict","hash_code":"freshx","clicks":0,"is_active":true,"created_at":null},`
	for _, bad := range []string{strings.Replace(good, `"clicks":17`, `"clicks":18`, 1), strings.Replace(good, `"hash_code":"oldhashx"`, `"hash_code":"changedx"`, 1), strings.Replace(good, `"source_legacy_user_id":6`, `"source_legacy_user_id":9`, 1), strings.Replace(good, `"is_trial_used":false`, `"is_trial_used":true`, 1)} {
		bad = strings.Replace(bad, `"campaigns":[`, `"campaigns":[`+fresh, 1)
		if _, err := campaignCLI(t, "--apply", bad); err == nil || snapshot() != after {
			t.Fatal("late conflict kept preceding row/event")
		}
	}
	// A changed legacy ID on the same UUID/TG pair must fail in read-only
	// dry-run too, before a later primary-key conflict would stop apply.
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET legacy_user_id=90 WHERE id=$1`, ids[701]); err != nil {
		t.Fatal(err)
	}
	changedID := strings.Replace(good, `"source_legacy_user_id":5`, `"source_legacy_user_id":90`, 1)
	for _, flag := range []string{"--dry-run", "--apply"} {
		if out, err := campaignCLI(t, flag, changedID); err == nil || out["error"] != "IMPORT_SOURCE_CONFLICT" || snapshot() != after {
			t.Fatal("changed historical pair accepted", flag, err)
		}
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET legacy_user_id=5 WHERE id=$1`, ids[701]); err != nil {
		t.Fatal(err)
	}
	// Moving a validated TG/legacy pair cannot move its old campaign ownership.
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	a, err := m.Accounts.CreateTelegram(ctx, tx, accounts.TelegramInput{TelegramID: 703, DisplayName: "Moved owner", Locale: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	// A Telegram-only account cannot lose its sole sign-in method. This saved
	// fixture has an independent email anchor before the identity pair moves.
	if _, err = tx.Exec(ctx, `UPDATE accounts SET kind='web',original_kind='telegram',email_key='owned-retained@example.test',password_hash='owned-test-only-hash',verified_at=$2,terms_version='1',privacy_version='1',telegram_id=NULL,legacy_user_id=NULL WHERE id=$1`, ids[701], e.Clock()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE accounts SET telegram_id=701,legacy_user_id=5 WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--apply", "--dry-run"} {
		if _, err := campaignCLI(t, flag, good); err == nil || snapshot() != after {
			t.Fatal("historical mapping moved")
		}
	}
	var jobs, orders, grants int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM river_job),(SELECT count(*) FROM purchase_orders),(SELECT count(*) FROM trial_grants)`).Scan(&jobs, &orders, &grants); err != nil || jobs != 0 || orders != 0 || grants != 0 {
		t.Fatal("import started runtime work")
	}
}

func TestCampaignImportStrictCLI(t *testing.T) {
	good := `{"version":1,"source":"owned-copy","campaigns":[],"users":[]}`
	for _, bad := range []string{good + ` {}`, strings.Replace(good, `"version":1`, `"version":2`, 1), strings.Replace(good, `"users":[]`, `"users":null`, 1), strings.Replace(good, `"source":"owned-copy"`, `"source":"\ud800"`, 1), strings.Replace(good, `"source":"owned-copy"`, `"source":"\udc00"`, 1), strings.Replace(good, `"users":[]`, `"users":[],"unknown":"synthetic-private"`, 1), string([]byte{0xff}), strings.Repeat(" ", 32<<20) + good} {
		out, err := campaignCLI(t, "--apply", bad)
		if err == nil || out["error"] != "IMPORT_INVALID_PACKAGE" {
			t.Fatal("unsafe packet not rejected before DB", err)
		}
	}
}
