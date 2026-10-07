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

var accountSQL = regexp.MustCompile(`(?i)\b(?:from|join|update|into|truncate(?:\s+table)?)\s+(?:public\.)?(?:accounts|operator_accounts|sessions|registration_challenges|credential_challenges|legacy_approval_snapshots|legacy_approval_events)\b`)

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
		if entry.IsDir() || strings.HasSuffix(rel, "_test.go") || ((owner == "accounts" || owner == "notifications") && rel == "db/maintenance/post_restore_auth.sql") {
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
	// Modules use public peer contracts. Application composition and HTTP
	// projections remain outside their dependency graph.
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

// The executable must use the actual owners, never the transitional global
// service. Composition may configure modules and adapt the Telegram channel.
func compositionViolations(source string) []string {
	file, err := parser.ParseFile(token.NewFileSet(), "composition.go", source, 0)
	if err != nil {
		return []string{err.Error()}
	}
	var violations []string
	for _, imported := range file.Imports {
		path, _ := strconv.Unquote(imported.Path.Value)
		for _, shared := range []string{"platform", "store", "wire"} {
			if path == "example.com/cabinet/backend/internal/"+shared {
				violations = append(violations, "shared import "+path)
			}
		}
	}
	for _, declaration := range file.Decls {
		method, ok := declaration.(*ast.FuncDecl)
		if !ok || method.Recv == nil {
			continue
		}
		receiver := method.Recv.List[0].Type
		if pointer, ok := receiver.(*ast.StarExpr); ok {
			receiver = pointer.X
		}
		name, ok := receiver.(*ast.Ident)
		if !ok || (name.Name != "TrialBridge" && !(name.Name == "Config" && method.Name.Name == "Validate")) {
			violations = append(violations, "domain method in composition: "+method.Name.Name)
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if ok {
			switch method.Sel.Name {
			case "Query", "QueryRow", "Exec", "Begin", "BeginTx":
				violations = append(violations, "database operation in composition: "+method.Sel.Name)
			}
		}
		return true
	})
	return violations
}

func TestRuntimeCompositionBoundary(t *testing.T) {
	for _, source := range []string{
		`package app; import "example.com/cabinet/backend/internal/wire"`,
		`package app; type Modules struct{}; func (m *Modules) Purchase() {}`,
		`package app; type RenamedService struct{}; func (s *RenamedService) Register() {}`,
		`package app; func read() { pool.QueryRow(nil, "SELECT id FROM accounts") }`,
	} {
		if len(compositionViolations(source)) == 0 {
			t.Fatal("negative composition fixture accepted")
		}
	}
	if got := compositionViolations(`package app; type Config struct{}; func (c Config) Validate() {}; type TrialBridge struct{}; func (b *TrialBridge) Decide() {}`); len(got) != 0 {
		t.Fatal("composition/config/channel rejected", got)
	}
	command := exec.Command("go", "list", "-deps", "./cmd/server")
	command.Dir = "../.."
	dependencies, err := command.Output()
	if err != nil {
		t.Fatal("runtime dependency inventory failed", err)
	}
	for _, dependency := range strings.Fields(string(dependencies)) {
		if dependency == "example.com/cabinet/backend/internal/platform" || dependency == "example.com/cabinet/backend/internal/store" {
			t.Errorf("transitional dependency in executable: %s", dependency)
		}
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, violation := range compositionViolations(string(source)) {
			t.Errorf("%s: %s", path, violation)
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

func TestPaymentsSQLBoundary(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)\b(?:from|join|update|into|truncate(?:\s+table)?)\s+(?:public\.)?(?:purchase_orders|purchase_receipts|purchase_refunds)\b`)
	ownsSQL := func(text string) bool {
		return pattern.MatchString(strings.ReplaceAll(text, `"`, ""))
	}
	for _, sql := range []string{
		`SELECT * FROM purchase_orders`, `UPDATE purchase_receipts SET review_reason=$1`, `SELECT * FROM purchase_refunds`, `DELETE FROM public.purchase_refunds`,
		`INSERT INTO purchase_orders VALUES ($1)`, `DELETE FROM public.purchase_receipts`,
		`WITH p AS (SELECT * FROM "public"."purchase_orders") SELECT * FROM p`,
	} {
		if !ownsSQL(sql) {
			t.Fatal("negative fixture bypassed payment ownership", sql)
		}
	}
	if ownsSQL(`SELECT purchase_order_id FROM access_operations`) {
		t.Fatal("foreign owner rejected")
	}
	checkSQLBoundary(t, "payments", ownsSQL)
}

func TestSupportSQLBoundary(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)\b(?:from|join|update|into|truncate(?:\s+table)?)\s+(?:public\.)?(?:support_conversations|support_messages)\b`)
	ownsSQL := func(text string) bool {
		return pattern.MatchString(strings.ReplaceAll(text, `"`, ""))
	}
	for _, sql := range []string{
		`SELECT * FROM support_conversations`, `UPDATE support_messages SET text=$1`,
		`INSERT INTO support_messages VALUES ($1)`, `DELETE FROM public.support_conversations`,
		`WITH p AS (SELECT * FROM "public"."support_messages") SELECT * FROM p`,
	} {
		if !ownsSQL(sql) {
			t.Fatal("negative fixture bypassed support ownership", sql)
		}
	}
	if ownsSQL(`SELECT support_message_id FROM audit_events`) {
		t.Fatal("foreign owner rejected")
	}
	checkSQLBoundary(t, "support", ownsSQL)
}

func TestNotificationsTelegramSQLBoundary(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)\b(?:from|join|update|into|truncate(?:\s+table)?)\s+(?:public\.)?telegram_deliveries\b`)
	ownsSQL := func(text string) bool {
		return pattern.MatchString(strings.ReplaceAll(text, `"`, ""))
	}
	for _, sql := range []string{
		`SELECT * FROM telegram_deliveries`, `SELECT * FROM trial_requests JOIN telegram_deliveries USING (request_id)`,
		`UPDATE telegram_deliveries SET state=$1`, `INSERT INTO telegram_deliveries VALUES ($1)`,
		`DELETE FROM public.telegram_deliveries`, `SELECT * FROM "public"."telegram_deliveries"`,
	} {
		if !ownsSQL(sql) {
			t.Fatal("negative fixture bypassed notification ownership", sql)
		}
	}
	if ownsSQL(`SELECT request_id FROM trial_requests`) {
		t.Fatal("foreign owner rejected")
	}
	checkSQLBoundary(t, "notifications", ownsSQL)
}

func TestNotificationsMailSQLBoundary(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)\b(?:from|join|update|into|truncate(?:\s+table)?)\s+(?:public\.)?mail_deliveries\b`)
	ownsSQL := func(text string) bool { return pattern.MatchString(strings.ReplaceAll(text, `"`, "")) }
	for _, sql := range []string{
		`SELECT * FROM mail_deliveries`, `SELECT * FROM accounts JOIN mail_deliveries USING (email_key)`,
		`UPDATE mail_deliveries SET ciphertext=NULL`, `INSERT INTO mail_deliveries VALUES ($1)`,
		`DELETE FROM public.mail_deliveries`, `SELECT * FROM "public"."mail_deliveries"`,
	} {
		if !ownsSQL(sql) {
			t.Fatal("negative fixture bypassed mail ownership", sql)
		}
	}
	if ownsSQL(`SELECT id FROM credential_challenges`) {
		t.Fatal("foreign owner rejected")
	}
	checkSQLBoundary(t, "notifications", ownsSQL)
}

func TestAuditReportsSQLBoundary(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)\b(?:from|join|update|into|truncate(?:\s+table)?)\s+(?:public\.)?audit_events\b`)
	ownsSQL := func(text string) bool { return pattern.MatchString(strings.ReplaceAll(text, `"`, "")) }
	for _, sql := range []string{
		`SELECT * FROM audit_events`, `SELECT * FROM accounts JOIN audit_events USING (account_id)`,
		`UPDATE audit_events SET reason=NULL`, `INSERT INTO audit_events VALUES ($1)`,
		`DELETE FROM public.audit_events`, `SELECT * FROM "public"."audit_events"`,
	} {
		if !ownsSQL(sql) {
			t.Fatal("negative fixture bypassed audit ownership", sql)
		}
	}
	if ownsSQL(`SELECT actor_type FROM legacy_approval_events`) {
		t.Fatal("foreign owner rejected")
	}
	checkSQLBoundary(t, "audit_reports", ownsSQL)
}

func TestSharedFacadeRemoved(t *testing.T) {
	for _, path := range []string{"../platform", "../store", "../../db/queries"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("transitional directory remains: %s", path)
		}
	}
	config, err := os.ReadFile("../../sqlc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range []string{"queries: db/queries", "out: internal/store"} {
		if strings.Contains(string(config), declaration) {
			t.Errorf("shared generation remains: %s", declaration)
		}
	}
}
