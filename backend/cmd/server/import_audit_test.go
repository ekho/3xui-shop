package main

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/testkit"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func auditCLI(t *testing.T, flag, body string) (map[string]any, error) {
	t.Helper()
	input, err := os.CreateTemp(t.TempDir(), "audit-package")
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
	os.Args, os.Stdin, os.Stdout = []string{"server", "import-legacy-audit", flag}, input, writer
	resultErr := run()
	writer.Close()
	os.Stdout = stdout
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-audit-body") || strings.Contains(string(raw), "Original name") {
		t.Fatal("CLI exposed private input")
	}
	var out map[string]any
	json.Unmarshal(raw, &out)
	return out, resultErr
}

func TestLegacyAuditCLIAtomic(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	t.Setenv("DATABASE_URL_FILE", ownedImportDatabaseFile(t, e))
	event := `{"source_id":9223372036854775807,"created_at":"2026-10-01T00:00:00.123456Z","action":"support.message","target_tg_id":9007199254740993,"actor_type":"operator","actor_id":-9007199254740993,"actor_name":"Original name","source":"support_bot","payload_json":"{\"dm_body\": \"private-audit-body\"}"}`
	good := `{"version":1,"events":[` + event + `]}`
	snapshot := func() string {
		t.Helper()
		var out string
		if err := e.Pool.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(x ORDER BY source_id) FROM legacy_audit_events x),(SELECT jsonb_agg(x ORDER BY source_id) FROM legacy_audit_imports x),(SELECT jsonb_agg(x ORDER BY id) FROM audit_system_events x))::text`).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	if out, err := auditCLI(t, "--dry-run", good); err != nil || out["inserted"] != float64(1) || out["replayed"] != float64(0) || snapshot() != before {
		t.Fatal("read-only audit import not handled", err)
	}
	if out, err := auditCLI(t, "--apply", good); err != nil || out["inserted"] != float64(1) {
		t.Fatal("audit apply", err)
	}
	after := snapshot()
	var payload []byte
	var sourceID, target, actorID, name string
	if err := e.Pool.QueryRow(ctx, `SELECT source_id::text,target_tg_id::text,actor_id::text,actor_name,payload_json FROM legacy_audit_events`).Scan(&sourceID, &target, &actorID, &name, &payload); err != nil || sourceID != "9223372036854775807" || target != "9007199254740993" || actorID != "-9007199254740993" || name != "Original name" || string(payload) != `{"dm_body": "private-audit-body"}` {
		t.Fatal("raw legacy facts changed", err)
	}
	for _, flag := range []string{"--dry-run", "--apply"} {
		if out, err := auditCLI(t, flag, good); err != nil || out["inserted"] != float64(0) || out["replayed"] != float64(1) || snapshot() != after {
			t.Fatal("audit replay wrote history", err)
		}
	}
	fresh := strings.Replace(event, `"source_id":9223372036854775807`, `"source_id":2`, 1)
	conflict := strings.Replace(event, `"actor_name":"Original name"`, `"actor_name":"Changed name"`, 1)
	for _, flag := range []string{"--dry-run", "--apply"} {
		if out, err := auditCLI(t, flag, `{"version":1,"events":[`+fresh+`,`+conflict+`]}`); err == nil || out["error"] != "IMPORT_SOURCE_CONFLICT" || snapshot() != after {
			t.Fatal("partial import survived source conflict", err)
		}
	}
	if out, err := auditCLI(t, "--apply", `{"version":1,"events":[`+event+`,`+event+`]}`); err == nil || out["error"] != "IMPORT_INVALID_PACKAGE" || snapshot() != after {
		t.Fatal("duplicate import accepted", err)
	}
	var jobs, orders int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM river_job),(SELECT count(*) FROM purchase_orders)`).Scan(&jobs, &orders); err != nil || jobs != 0 || orders != 0 {
		t.Fatal("import started runtime work", err)
	}
}

func TestLegacyAuditCLIStrict(t *testing.T) {
	good := `{"version":1,"events":[]}`
	for _, bad := range []string{good + `{}`, strings.Replace(good, `"version":1`, `"version":2`, 1), strings.Replace(good, `[]`, `null`, 1), `{"version":1}`, `{"version":1,"events":[],"body":"private-audit-body"}`, `{"version":1,"events":[{"actor_name":"\ud800"}]}`, string([]byte{0xff}), strings.Repeat(" ", 32<<20) + good} {
		if out, err := auditCLI(t, "--apply", bad); err == nil || out["error"] != "IMPORT_INVALID_PACKAGE" {
			t.Fatal("unsafe audit packet reached DB", err)
		}
	}
}

func TestDecodeLegacyAuditFields(t *testing.T) {
	event := `{"source_id":1,"created_at":"2026-10-01T00:00:00.123456Z","action":"support.message","payload_json":"{\"body\": \"raw\\\\ud800\"}"}`
	good := `{"version":1,"events":[` + event + `]}`
	if _, err := decodeLegacyAuditPackage(strings.NewReader(good)); err != nil {
		t.Fatal("valid escaped literal payload", err)
	}
	for _, bad := range []string{
		strings.Replace(good, `"source_id":1`, `"source_id":0`, 1),
		strings.Replace(good, `"source_id":1`, `"source_id":9223372036854775808`, 1),
		strings.Replace(good, `.123456Z`, `.123456001Z`, 1),
		strings.Replace(good, `2026-10-01T00:00:00.123456Z`, `0001-01-01T00:00:00Z`, 1),
		strings.Replace(good, `"action":"support.message"`, `"action":" "`, 1),
		strings.Replace(good, `"action":"support.message"`, `"action":"`+strings.Repeat("a", 129)+`"`, 1),
		strings.Replace(good, `"action":"support.message"`, `"action":"support.message","target_tg_id":0`, 1),
		`{"version":1,"events":[{"source_id":1,"created_at":"2026-10-01T00:00:00Z","action":"support.message","payload_json":"[]"}]}`,
		`{"version":1,"events":[{"source_id":1,"created_at":"2026-10-01T00:00:00Z","action":"support.message","payload_json":"null"}]}`,
		`{"version":1,"events":[{"source_id":1,"created_at":"2026-10-01T00:00:00Z","action":"support.message","payload_json":"{\"body\":\"\\ud800\"}"}]}`,
	} {
		if _, err := decodeLegacyAuditPackage(strings.NewReader(bad)); err == nil {
			t.Fatal("invalid/lossy legacy audit field accepted")
		}
	}
}

func TestLegacyAuditOriginalTimestamp(t *testing.T) {
	t.Setenv("DATABASE_URL_FILE", filepath.Join(t.TempDir(), "missing-database"))
	for _, stamp := range []string{"2026-10-01T00:00:00.1234560001Z", "2026-10-01T00:00:00.000000000000000001+03:00"} {
		for _, flag := range []string{"--dry-run", "--apply"} {
			t.Run(stamp+flag, func(t *testing.T) {
				body := `{"version":1,"events":[{"source_id":1,"created_at":"` + stamp + `","action":"support.message"}]}`
				if out, err := auditCLI(t, flag, body); err == nil || out["error"] != "IMPORT_INVALID_PACKAGE" {
					t.Fatal("lossy original timestamp reached database lookup", out["error"])
				}
			})
		}
	}
	for _, stamp := range []string{"2026-10-01T00:00:00.123456000000000000Z", "2026-10-01T03:00:00.123456000000000000+03:00"} {
		body := `{"version":1,"events":[{"source_id":1,"created_at":"` + stamp + `","action":"support.message"}]}`
		packet, err := decodeLegacyAuditPackage(strings.NewReader(body))
		if err != nil || packet.Events[0].CreatedAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00") != "2026-10-01T00:00:00.123456Z" {
			t.Fatal("exact timestamp with redundant zeros changed", err)
		}
		if _, err := decodeLegacyAuditPackage(strings.NewReader(strings.Replace(body, `"action":"support.message"`, `"action":"support.message","unknown":true`, 1))); err == nil {
			t.Fatal("unknown legacy row field accepted")
		}
	}
}
