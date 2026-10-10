package main

import (
	"context"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInfrastructureAndOperatorCLIFileGrantAndRestrictedRevoke(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	id := uuid.New()
	_, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
		VALUES($1,'cli-fixture@example.test','ru','fixture',now(),$2,'cccccccccccccccc',$3,'1','1')`, id, uuid.New(), "acct_"+id.String())
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	accountFile := filepath.Join(directory, "account")
	databaseFile := filepath.Join(directory, "database")
	if err = os.WriteFile(accountFile, []byte(id.String()+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(os.Getenv("TEST_DATABASE_URL_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(strings.TrimSpace(string(source)))
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + e.Pool.Config().ConnConfig.Database
	if err = os.WriteFile(databaseFile, []byte(parsed.String()), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL_FILE", databaseFile)
	t.Setenv("DATABASE_URL", "")
	if err = runOperatorCommand("grant", "--account-file", accountFile); err != nil {
		t.Fatal("grant", err)
	}
	var roles int
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM operator_accounts WHERE account_id=$1`, id).Scan(&roles); err != nil || roles != 1 {
		t.Fatal("role absent", err)
	}
	if err = runOperatorCommand("grant", "--account-file", accountFile); err != nil {
		t.Fatal("idempotent grant", err)
	}
	if err = runRoleCommand("infrastructure", "grant", "--account-file", accountFile); err != nil {
		t.Fatal("infrastructure grant", err)
	}
	if err = runRoleCommand("infrastructure", "grant", "--account-file", accountFile); err != nil {
		t.Fatal("infrastructure replay", err)
	}
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM infrastructure_operators WHERE account_id=$1`, id).Scan(&roles); err != nil || roles != 1 {
		t.Fatal("infrastructure role absent", err)
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = runOperatorCommand("grant", "--account-file", accountFile); err == nil {
		t.Fatal("restricted regrant")
	}
	if err = runOperatorCommand("revoke", "--account-file", accountFile); err != nil {
		t.Fatal("restricted revoke", err)
	}
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM operator_accounts WHERE account_id=$1`, id).Scan(&roles); err != nil || roles != 0 {
		t.Fatal("role retained", err)
	}
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM infrastructure_operators WHERE account_id=$1`, id).Scan(&roles); err != nil || roles != 0 {
		t.Fatal("infrastructure role retained", err)
	}
	if err = runRoleCommand("infrastructure", "grant", "--account-file", accountFile); err == nil {
		t.Fatal("grant without operator role")
	}
	if err = runOperatorCommand("revoke", "--account-file", accountFile); err != nil {
		t.Fatal("idempotent revoke", err)
	}
	if err = runOperatorCommand("grant", "--account-file", "relative-path"); err == nil {
		t.Fatal("relative file accepted")
	}
}
