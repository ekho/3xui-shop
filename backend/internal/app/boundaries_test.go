package app

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const moduleRoot = "example.com/cabinet/backend/internal/modules/"

func forbiddenImport(owner, dependency string) bool {
	if !strings.HasPrefix(dependency, "example.com/cabinet/backend/internal/") {
		return false
	}
	if !strings.HasPrefix(dependency, moduleRoot) {
		return true
	}
	peer := strings.TrimPrefix(dependency, moduleRoot)
	return strings.Contains(peer, "/") && !strings.HasPrefix(dependency, owner+"/")
}

var accountSQL = regexp.MustCompile(`(?i)\b(?:from|join|update|into|truncate(?:\s+table)?)\s+(?:public\.)?(?:accounts|operator_accounts|sessions|registration_challenges|credential_challenges|mail_deliveries|legacy_approval_snapshots|legacy_approval_events)\b`)

func ownsAccountSQL(text string) bool {
	return accountSQL.MatchString(strings.ReplaceAll(text, `"`, ""))
}

func TestAccountsSQLBoundary(t *testing.T) {
	for _, sql := range []string{
		`SELECT * FROM accounts`, `UPDATE accounts SET restricted=true`,
		`INSERT INTO sessions VALUES ($1)`, `DELETE FROM public.operator_accounts`,
		`WITH p AS (SELECT * FROM "public"."credential_challenges") SELECT * FROM p`,
	} {
		if !ownsAccountSQL(sql) {
			t.Fatal("negative fixture bypassed account ownership", sql)
		}
	}
	if ownsAccountSQL(`SELECT account_id FROM trial_requests`) {
		t.Fatal("foreign owner rejected")
	}
	checkSQLBoundary(t, "accounts", ownsAccountSQL)
}

func checkSQLBoundary(t *testing.T, owner string, ownsSQL func(string) bool) {
	t.Helper()
	err := filepath.WalkDir("../..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("../..", path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		// Shared schema and the reviewed restore procedure are operational
		// exceptions. Test-only SQL fixtures never run in the application.
		if entry.IsDir() && (rel == "internal/modules/"+owner || rel == "db/migrations" || rel == "internal/testkit") {
			return filepath.SkipDir
		}
		if entry.IsDir() || strings.HasSuffix(rel, "_test.go") || (owner == "accounts" && rel == "db/maintenance/post_restore_auth.sql") {
			return nil
		}
		switch filepath.Ext(path) {
		case ".sql":
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if ownsSQL(string(body)) {
				t.Errorf("%s SQL outside owner: %s", owner, rel)
			}
		case ".go":
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(literal.Value)
				if err == nil && ownsSQL(text) {
					t.Errorf("%s SQL outside owner: %s", owner, rel)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestModuleBoundaries(t *testing.T) {
	// Only the existing app bridge and legacy HTTP consumers may import
	// platform/store/wire during the remaining owner extractions.
	cmd := exec.Command("go", "list", "-json", "./internal/modules/...")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("module inventory failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	count := 0
	for {
		var pkg struct {
			ImportPath string
			Imports    []string
		}
		err = decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
		owner := moduleRoot + strings.Split(strings.TrimPrefix(pkg.ImportPath, moduleRoot), "/")[0]
		for _, dependency := range pkg.Imports {
			if forbiddenImport(owner, dependency) {
				t.Fatalf("module %s imports forbidden implementation %s", pkg.ImportPath, dependency)
			}
		}
	}
	if count < 2 {
		t.Fatal("Telegram module/private transport missing from inventory")
	}
	for _, dependency := range []string{
		"example.com/cabinet/backend/internal/platform",
		"example.com/cabinet/backend/internal/store",
		"example.com/cabinet/backend/internal/wire",
		"example.com/cabinet/backend/internal/httpapi",
		"example.com/cabinet/backend/internal/app",
		moduleRoot + "payments/internal/store",
	} {
		if !forbiddenImport(moduleRoot+"telegram", dependency) {
			t.Fatal("negative fixture was accepted", dependency)
		}
	}
	for _, dependency := range []string{"context", moduleRoot + "telegram/internal/botapi", moduleRoot + "subscriptions"} {
		if forbiddenImport(moduleRoot+"telegram", dependency) {
			t.Fatal("public/owned dependency rejected")
		}
	}
}

var catalogueSQL = regexp.MustCompile(`(?i)\b(?:from|join|update|into|truncate(?:\s+table)?)\s+(?:public\.)?(?:catalogue_plans|catalogue_revisions)\b`)

func ownsCatalogueSQL(text string) bool {
	return catalogueSQL.MatchString(strings.ReplaceAll(text, `"`, ""))
}

func TestCatalogueSQLBoundary(t *testing.T) {
	for _, sql := range []string{
		`SELECT * FROM catalogue_plans`, `UPDATE catalogue_revisions SET archived=true`,
		`INSERT INTO catalogue_plans VALUES ($1)`, `DELETE FROM public.catalogue_revisions`,
		`WITH p AS (SELECT * FROM "public"."catalogue_plans") SELECT * FROM p`,
	} {
		if !ownsCatalogueSQL(sql) {
			t.Fatal("negative fixture bypassed catalogue ownership", sql)
		}
	}
	if ownsCatalogueSQL(`SELECT plan_id FROM access_operations`) {
		t.Fatal("foreign owner rejected")
	}
	checkSQLBoundary(t, "catalogue", ownsCatalogueSQL)
}

func TestSubscriptionsSQLBoundary(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)\b(?:from|join|update|into|truncate(?:\s+table)?)\s+(?:public\.)?(?:trial_requests|trial_grants|decision_callbacks)\b`)
	checkSQLBoundary(t, "subscriptions", func(text string) bool {
		return pattern.MatchString(strings.ReplaceAll(text, `"`, ""))
	})
}

func TestVPNSQLBoundary(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)\b(?:from|join|update|into|truncate(?:\s+table)?)\s+(?:public\.)?(?:trial_operations|access_operations|monthly_reset_periods)\b`)
	checkSQLBoundary(t, "vpn", func(text string) bool {
		return pattern.MatchString(strings.ReplaceAll(text, `"`, ""))
	})
}
