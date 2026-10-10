package main

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/operations"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLegacyJSONRejectsDuplicateAndLossyTimestamp(t *testing.T) {
	type row struct {
		CreatedAt time.Time `json:"created_at"`
		Value     string    `json:"value"`
	}
	for _, body := range []string{
		`{"created_at":"2026-10-10T00:00:00Z","value":"one","value":"two"}`,
		`{"created_at":"2026-10-10T00:00:00.0000000001Z","value":"one"}`,
		`{"created_at":"2026-10-10T00:00:00.000000001Z","value":"one"}`,
	} {
		if _, err := decodeLegacyJSON[row](strings.NewReader(body)); err == nil {
			t.Fatal("lossy or ambiguous input accepted")
		}
	}
	if _, err := decodeLegacyJSON[row](strings.NewReader(`{"created_at":"2026-10-10T00:00:00.000001Z","value":"one"}`)); err != nil {
		t.Fatal("exact microseconds rejected")
	}
}

func syntheticMigrationPacket(t *testing.T) (operations.LegacyPackage, []byte) {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal("source root unavailable")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("private source directory unavailable")
	}
	source := filepath.Join(dir, "source.sqlite")
	command := exec.Command("python3", filepath.Join(root, "tests/fixtures/legacy_snapshot.py"), source)
	command.Dir = root
	if command.Run() != nil {
		t.Fatal("synthetic source generation failed")
	}
	command = exec.Command("python3", filepath.Join(root, "deploy/data-migration/export_legacy.py"), source, "--source", "synthetic-source", "--support-bot-id", "12345", "--support-group-id", "-10012345")
	command.Dir = root
	raw, err := command.Output()
	if err != nil {
		t.Fatal("complete source export failed")
	}
	p, err := decodeLegacyMigration(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal("real exporter and CLI package disagree")
	}
	return p, raw
}

func TestLegacyMigrationStrictCompletePacket(t *testing.T) {
	_, raw := syntheticMigrationPacket(t)
	var object map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&object) != nil {
		t.Fatal("fixture packet invalid")
	}
	for _, change := range []func(map[string]any){
		func(p map[string]any) { delete(p, "stars") },
		func(p map[string]any) { p["users"].([]any)[0].(map[string]any)["unknown"] = true },
		func(p map[string]any) { delete(p["users"].([]any)[0].(map[string]any), "is_trial_used") },
		func(p map[string]any) { p["stars"].([]any)[0].(map[string]any)["unknown"] = true },
		func(p map[string]any) { p["payments"].(map[string]any)["users"] = []any{} },
		func(p map[string]any) {
			p["catalogue_source"].(map[string]any)["plans"].([]any)[0].(map[string]any)["prices_json"] = `{"RUB":{"30":120.50,"30":120.50}}`
		},
		func(p map[string]any) {
			p["audit"].(map[string]any)["events"].([]any)[0].(map[string]any)["payload_json"] = `{"x":1,"x":2}`
		},
	} {
		var changed map[string]any
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.UseNumber()
		_ = d.Decode(&changed)
		change(changed)
		body, _ := json.Marshal(changed)
		if _, err := decodeLegacyMigration(strings.NewReader(string(body))); err == nil {
			t.Fatal("partial or extra input accepted")
		}
	}
}

func TestLegacyMigrationCLIAtomicDryRunApplyReplay(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	p, raw := syntheticMigrationPacket(t)
	actor := uuid.New()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("private directory unavailable")
	}
	_, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) VALUES($1,$2,'en','fixture',now(),$3,$4,$5,'1','1')`, actor, actor.String()+"@example.test", uuid.New(), strings.ReplaceAll(uuid.NewString(), "-", "")[:16], "acct_"+actor.String())
	if err != nil {
		t.Fatal("operator prerequisite unavailable")
	}
	owner := accounts.New(e.Pool, e.Redis, nil, accounts.Config{Now: e.Clock})
	if owner.ChangeOperatorRole(ctx, actor, true) != nil {
		t.Fatal("operator role prerequisite unavailable")
	}
	actorFile := filepath.Join(dir, "operator")
	if os.WriteFile(actorFile, []byte(actor.String()), 0600) != nil {
		t.Fatal("private operator file unavailable")
	}
	dbFile := ownedImportDatabaseFile(t, e)
	dbFile, err = filepath.EvalSymlinks(dbFile)
	if err != nil {
		t.Fatal("private database file unavailable")
	}
	connection, err := os.ReadFile(dbFile)
	uri, parseErr := url.Parse(string(connection))
	if err != nil || parseErr != nil {
		t.Fatal("private test connection invalid")
	}
	query := uri.Query()
	query.Del("dbname")
	uri.RawQuery = query.Encode()
	if os.WriteFile(dbFile, []byte(uri.String()), 0600) != nil {
		t.Fatal("private test connection unavailable")
	}
	t.Setenv("DATABASE_URL_FILE", dbFile)
	t.Setenv("DATABASE_URL", "")
	command := func(flag string, body []byte) (operations.LegacyReport, error) {
		t.Helper()
		input, err := os.CreateTemp(dir, "packet-")
		if err != nil {
			t.Fatal("private input unavailable")
		}
		defer os.Remove(input.Name())
		defer input.Close()
		if _, err = input.Write(body); err != nil {
			t.Fatal("private input unavailable")
		}
		_, _ = input.Seek(0, 0)
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal("output pipe unavailable")
		}
		defer reader.Close()
		args, stdin, stdout := os.Args, os.Stdin, os.Stdout
		defer func() { os.Args, os.Stdin, os.Stdout = args, stdin, stdout }()
		os.Args, os.Stdin, os.Stdout = []string{"server", "import-legacy", flag, "--operator-file", actorFile}, input, writer
		runErr := run()
		_ = writer.Close()
		os.Stdout = stdout
		out, _ := io.ReadAll(reader)
		var report operations.LegacyReport
		if runErr == nil && json.Unmarshal(out, &report) != nil {
			t.Fatal("redacted result invalid")
		}
		return report, runErr
	}
	counts := func() (int, int, int, int) {
		var users, runs, jobs, orders int
		if e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM legacy_account_imports),(SELECT count(*) FROM legacy_migration_runs),(SELECT count(*) FROM river_job),(SELECT count(*) FROM purchase_orders)`).Scan(&users, &runs, &jobs, &orders) != nil {
			t.Fatal("database assertion unavailable")
		}
		return users, runs, jobs, orders
	}
	dry, err := command("--dry-run", raw)
	if err != nil || !dry.DryRun || dry.Counts["users"] != 3 {
		t.Fatal("actual dry-run failed", err)
	}
	if u, r, j, o := counts(); u != 0 || r != 0 || j != 0 || o != 0 {
		t.Fatal("dry-run left imported facts or work")
	}
	applied, err := command("--apply", raw)
	if err != nil || applied.DryRun || applied.Replayed || applied.Totals.RewardDays != "10" || applied.Totals.RewardMoneyUnknownCurrency != "2.250000000000000000" || applied.Unknown["trial"] != 2 || applied.Unknown["recurring"] != 3 {
		t.Fatal("complete apply or exact reconciliation failed", err)
	}
	if u, r, j, o := counts(); u != 3 || r != 1 || j != 0 || o != 0 {
		t.Fatal("import facts missing or runtime work created")
	}
	for _, u := range p.Users {
		var exact bool
		if e.Pool.QueryRow(ctx, `SELECT vpn_id::text=$2 AND sub_id=$3 AND panel_key=$4 AND created_at=$5 FROM accounts WHERE legacy_user_id=$1`, u.SourceID, u.VPNID, u.SubID, strconv.FormatInt(u.TgID, 10), u.CreatedAt).Scan(&exact) != nil || !exact {
			t.Fatal("source identity or original timestamp changed")
		}
	}
	replay, err := command("--apply", raw)
	if err != nil || !replay.Replayed || len(replay.Inserted) != 0 || replay.SourceDigest != applied.SourceDigest {
		t.Fatal("CLI restart replay failed", err)
	}
	p.Users[0].FirstName = "Changed source"
	conflict, _ := json.Marshal(p)
	if _, err = command("--apply", conflict); err == nil {
		t.Fatal("source overwrite accepted")
	}
	if owner.ChangeOperatorRole(ctx, actor, false) != nil {
		t.Fatal("role revocation prerequisite unavailable")
	}
	if _, err = command("--apply", raw); err == nil {
		t.Fatal("revoked operator replay accepted")
	}
}

func TestLegacyMigrationLateConflictRollsBackAllOwners(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	p, _ := syntheticMigrationPacket(t)
	modules := app.NewModules(e.Pool, e.Redis, nil, &app.Config{Accounts: accounts.Config{Now: e.Clock}})
	actor := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) VALUES($1,$2,'en','fixture',now(),$3,$4,$5,'1','1')`, actor, actor.String()+"@example.test", uuid.New(), strings.ReplaceAll(uuid.NewString(), "-", "")[:16], "acct_"+actor.String()); err != nil || modules.Accounts.ChangeOperatorRole(ctx, actor, true) != nil {
		t.Fatal("operator prerequisite unavailable")
	}
	historical := p.Audit
	historical.Events = append(historical.Events[:0:0], p.Audit.Events[len(p.Audit.Events)-1])
	historical.Events[0].Action = "system.preexisting_source"
	if _, err := modules.AuditReports.ImportLegacy(ctx, historical, false); err != nil {
		t.Fatal("preexisting owner history prerequisite unavailable")
	}
	if _, err := modules.LegacyImport.Import(ctx, actor, p, false); err == nil {
		t.Fatal("late source conflict accepted")
	}
	var remaining int
	if e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM legacy_account_imports)+(SELECT count(*) FROM legacy_server_imports)+(SELECT count(*) FROM legacy_bonus_imports)+(SELECT count(*) FROM legacy_stars_imports)+(SELECT count(*) FROM legacy_migration_runs)+(SELECT count(*) FROM catalogue_plans)+(SELECT count(*) FROM campaigns)+(SELECT count(*) FROM legacy_support_imports)+(SELECT count(*) FROM river_job)`).Scan(&remaining) != nil || remaining != 0 {
		t.Fatal("late failure left partial owners or runtime work")
	}
}
