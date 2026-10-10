package operations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const ordinaryConnectionURL = "postgres://backup_operator:example@127.0.0.1:5432/backup_source?sslmode=disable"

func TestConnectionContractRejectsAmbiguousURLs(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
	}{
		{"multi-host", "postgres://backup_operator:example@first:5432,second:5432/backup_source?sslmode=disable"},
		{"query-host", ordinaryConnectionURL + "&host=second"},
		{"channel-binding", ordinaryConnectionURL + "&channel_binding=require"},
		{"target-session-attrs", ordinaryConnectionURL + "&target_session_attrs=read-write"},
		{"repeated-sslmode", ordinaryConnectionURL + "&sslmode=verify-full"},
		{"verify-full-without-root", "postgres://backup_operator:example@db.example:5432/backup_source?sslmode=verify-full"},
		{"verify-ca-without-root", "postgres://backup_operator:example@db.example:5432/backup_source?sslmode=verify-ca"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateConnectionURL(tc.url); err == nil || err.Error() != "INVALID_DATABASE_URL" {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestConnectionContractPreservesSupportedTLS(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		mode string
		root string
	}{
		{"ordinary", ordinaryConnectionURL, "disable", ""},
		{"verify-full", "postgres://backup_operator:example@db.example:5432/backup_source?sslmode=verify-full&sslrootcert=system", "verify-full", "system"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateConnectionURL(tc.url); err != nil {
				t.Fatalf("supported URL rejected: %v", err)
			}
			_, env, err := connectionConfig(tc.url)
			if err != nil {
				t.Fatal(err)
			}
			contains := func(s string) bool {
				for _, item := range env {
					if item == s {
						return true
					}
				}
				return false
			}
			if !contains("PGSSLMODE="+tc.mode) || tc.root != "" && !contains("PGSSLROOTCERT="+tc.root) {
				t.Fatal("libpq environment omitted a supported TLS option")
			}
		})
	}
}

func TestConnectionContractRejectsAmbientPGOptions(t *testing.T) {
	for _, name := range []string{"PGHOST", "PGCHANNELBINDING", "PGTARGETSESSIONATTRS", "PGSSLROOTCERT"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "unexpected")
			if err := ValidateConnectionURL(ordinaryConnectionURL); err == nil || err.Error() != "INVALID_DATABASE_URL" {
				t.Fatalf("ambient setting accepted: %v", err)
			}
		})
	}
}

func TestConnectionContractIgnoresPostgresImageMetadata(t *testing.T) {
	t.Setenv("PGDATA", "/var/lib/postgresql/data")
	t.Setenv("PG_MAJOR", "17")
	t.Setenv("PG_VERSION", "17.11")
	if err := ValidateConnectionURL(ordinaryConnectionURL); err != nil {
		t.Fatalf("container metadata rejected: %v", err)
	}
}

func TestRejectedConnectionHasNoPackageOrDatabaseSideEffects(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(root, "package")
	invalid := ordinaryConnectionURL + "&channel_binding=require"
	create := NewBackup(nil, invalid, "")
	if err := create.Create(context.Background(), uuid.New(), packageDir); err == nil || err.Error() != "INVALID_DATABASE_URL" {
		t.Fatalf("create did not reject before database use: %v", err)
	}
	if _, err := os.Lstat(packageDir); !os.IsNotExist(err) {
		t.Fatalf("create changed package path: %v", err)
	}
	rehearse := NewBackup(nil, ordinaryConnectionURL, invalid)
	if err := rehearse.Rehearse(context.Background(), uuid.New(), packageDir, "rehearsal_probe"); err == nil || !strings.Contains(err.Error(), "INVALID_DATABASE_URL") {
		t.Fatalf("rehearse did not reject before database use: %v", err)
	}
}
