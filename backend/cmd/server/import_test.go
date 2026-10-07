package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/testkit"
)

func TestLegacyApprovalCLIStrictPackage(t *testing.T) {
	good := `{"version":1,"users":[],"approval_events":[]}`
	if _, err := decodeLegacyApprovalPackage(strings.NewReader(good)); err != nil {
		t.Fatal("valid empty packet", err)
	}
	for _, body := range []string{
		`{"version":2,"users":[],"approval_events":[]}`,
		`{"version":1,"users":[],"approval_events":[],"payload":"secret"}`,
		`{"version":1,"users":[],"approval_events":[]} {}`,
	} {
		if _, err := decodeLegacyApprovalPackage(strings.NewReader(body)); err == nil {
			t.Fatal("invalid packet accepted")
		}
	}
}

// Weak decoding must not silently replace an invalid raw financial identifier.
func TestDecodeLegacyPaymentPackage(t *testing.T) {
	good := `{"version":1,"users":[],"transactions":[]}`
	if p, err := decodeLegacyPaymentPackage(strings.NewReader(good)); err != nil || p.Version != 1 {
		t.Fatal("valid package rejected", err)
	}
	for _, body := range []string{
		strings.Replace(good, `"version":1`, `"version":2`, 1),
		`{"version":1,"users":null,"transactions":[]}`,
		good + ` {}`,
		`{"version":1,"users":[],"transactions":[],"secret":"do-not-echo"}`,
		`{"version":1,"users":[],"transactions":[{"source_id":1,"unknown":"do-not-echo"}]}`,
		`{"version":1,"users":[],"transactions":[{"subscription":"\ud800"}]}`,
		`{"version":1,"users":[],"transactions":[{"subscription":"\udc00"}]}`,
		string([]byte{0xff}), strings.Repeat(" ", 32<<20) + good,
	} {
		if _, err := decodeLegacyPaymentPackage(strings.NewReader(body)); err == nil {
			t.Fatal("unsafe/lossy package accepted")
		}
	}
	for _, value := range []string{`\ud83d\ude00`, `\\ud800`, `✨`} {
		body := `{"version":1,"users":[],"transactions":[{"subscription":"` + value + `"}]}`
		if _, err := decodeLegacyPaymentPackage(strings.NewReader(body)); err != nil {
			t.Fatal("valid Unicode/raw escape rejected")
		}
	}
}

// The actual command must use its FILE DB and avoid starting serve/workers.
func TestLegacyPaymentCLI(t *testing.T) {
	e := testkit.Open(t)
	file := filepath.Join(t.TempDir(), "database-url")
	if err := os.WriteFile(file, []byte(e.Pool.Config().ConnString()), 0600); err != nil {
		t.Fatal("cannot prepare own database file")
	}
	t.Setenv("DATABASE_URL_FILE", file)
	input, err := os.CreateTemp(t.TempDir(), "package")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if _, err = input.WriteString(`{"version":1,"users":[],"transactions":[]}`); err != nil {
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
	os.Args = []string{"server", "import-legacy-payments", "--dry-run"}
	os.Stdin = input
	os.Stdout = writer
	err = run()
	writer.Close()
	os.Stdout = stdout
	if err != nil {
		t.Fatal("import command was not handled", err)
	}
	body, err := io.ReadAll(reader)
	var result map[string]int
	if err != nil || json.Unmarshal(body, &result) != nil || len(result) != 3 || result["inserted"] != 0 {
		t.Fatal("command did not produce a safe import result")
	}
	var jobs int
	if err = e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("import started runtime work")
	}
}
