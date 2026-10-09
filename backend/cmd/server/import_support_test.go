package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	"example.com/cabinet/backend/internal/testkit"
)

// A real CLI invocation must validate before DB access and never start runtime.
func TestLegacySupportCLI(t *testing.T) {
	e := testkit.Open(t)
	t.Setenv("DATABASE_URL_FILE", ownedImportDatabaseFile(t, e))
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"dry", `{"version":1,"bot_id":973,"group_id":-10074001,"tickets":[]}`, true},
		{"unknown", `{"version":1,"bot_id":973,"group_id":-10074001,"tickets":[],"secret":"do-not-echo"}`, false},
		{"raw-fraction", `{"version":1,"bot_id":973,"group_id":-10074001,"tickets":[{"source_id":1,"tg_id":731,"thread_id":null,"status":"open","created_at":"2026-10-01T00:00:00.12345600001Z","updated_at":"2026-10-01T00:00:01Z"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, err := os.CreateTemp(t.TempDir(), "package")
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			if _, err = input.WriteString(tc.body); err != nil {
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
			os.Args = []string{"server", "import-legacy-support", "--dry-run"}
			os.Stdin = input
			os.Stdout = writer
			err = run()
			writer.Close()
			os.Stdout = stdout
			body, readErr := io.ReadAll(reader)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.valid {
				var result map[string]int
				if err != nil || json.Unmarshal(body, &result) != nil || len(result) != 3 || result["inserted"] != 0 {
					t.Fatal("support CLI not handled safely", err)
				}
			} else {
				var result map[string]string
				if err == nil || json.Unmarshal(body, &result) != nil || result["error"] != "IMPORT_INVALID_PACKAGE" || len(result) != 1 {
					t.Fatal("unsafe support package accepted or leaked", err)
				}
			}
		})
	}
	var jobs int
	if e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job`).Scan(&jobs) != nil || jobs != 0 {
		t.Fatal("import started jobs")
	}
}
