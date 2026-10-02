package main

import (
	"context"
	"example.com/cabinet/backend/internal/testkit"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogueCLIStrictPackageAndApply(t *testing.T) {
	good := `{"version":1,"durations":[30],"plans":[{"legacy_plan_id":1,"devices":1,"traffic_gb":0,"profile":"regular","hidden":false,"prices":{"RUB":{"30":"123.45"},"USD":{"30":"0"},"XTR":{"30":"0"}}}]}`
	if _, err := decodeLegacyCatalogue(strings.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"version":2,"durations":[],"plans":[]}`, `{"version":1,"durations":[],"plans":[],"extra":1}`, `{"version":1,"durations":null,"plans":[]}`, `{"version":1,"durations":[],"plans":null}`, `{"version":1,"durations":[],"plans":[]} {}`, `{"version":1,"durations":[30],"plans":[{"legacy_plan_id":1,"devices":1,"profile":"regular","hidden":false,"prices":{}}]}`} {
		if _, err := decodeLegacyCatalogue(strings.NewReader(body)); err == nil {
			t.Fatal("invalid package accepted")
		}
	}
	e := testkit.Open(t)
	source, err := os.ReadFile(os.Getenv("TEST_DATABASE_URL_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(strings.TrimSpace(string(source)))
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + e.Pool.Config().ConnConfig.Database
	path := filepath.Join(t.TempDir(), "database")
	if err = os.WriteFile(path, []byte(parsed.String()), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL_FILE", path)
	t.Setenv("DATABASE_URL", "")
	stdin := os.Stdin
	defer func() { os.Stdin = stdin }()
	input := filepath.Join(t.TempDir(), "package")
	if err = os.WriteFile(input, []byte(good), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"--dry-run", "--apply", "--apply"} {
		f, openErr := os.Open(input)
		if openErr != nil {
			t.Fatal(openErr)
		}
		os.Stdin = f
		if runErr := runCatalogueCommand([]string{"import-legacy", mode}); runErr != nil {
			t.Fatal(mode, runErr)
		}
		f.Close()
		var count int
		if queryErr := e.Pool.QueryRow(context.Background(), "SELECT count(*) FROM catalogue_plans").Scan(&count); queryErr != nil {
			t.Fatal(queryErr)
		}
		want := 1
		if mode == "--dry-run" {
			want = 0
		}
		if count != want {
			t.Fatalf("%s count %d", mode, count)
		}
	}
	if err = runCatalogueCommand([]string{"seed-unlimited"}); err != nil {
		t.Fatal(err)
	}
	if err = runCatalogueCommand([]string{"seed-unlimited"}); err != nil {
		t.Fatal(err)
	}
}
