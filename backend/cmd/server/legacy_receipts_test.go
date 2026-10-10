package main

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func TestLegacyReceiptsCLIRoleAndSafeReport(t *testing.T) {
	e := testkit.Open(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(e.Pool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + e.Pool.Config().ConnConfig.Database
	u.RawQuery = "sslmode=disable"
	databaseFile, actorFile := filepath.Join(dir, "database"), filepath.Join(dir, "actor")
	actor := uuid.New()
	for path, text := range map[string]string{databaseFile: u.String(), actorFile: actor.String()} {
		if os.WriteFile(path, []byte(text), 0600) != nil {
			t.Fatal("private fixture failed")
		}
	}
	t.Setenv("DATABASE_URL_FILE", databaseFile)
	t.Setenv("DATABASE_URL", "")
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) VALUES($1,'receipt-cli@example.test','ru','fixture',now(),$2,'dddddddddddddddd',$3,'1','1')`, actor, uuid.New(), "acct_"+actor.String()); err != nil {
		t.Fatal(err)
	}
	if runLegacyReceipts("--operator-file", actorFile) == nil {
		t.Fatal("non-operator accepted")
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,now())`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO legacy_payment_receipts(id,provider,event_kind,source_id,source_reference,amount_minor,currency,occurred_at,proof,state) VALUES($1,'yoomoney','paid','private-source-id','private-reference',123,'RUB',now(),'{"private":"proof"}','review')`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	args, stdout := os.Args, os.Stdout
	defer func() { os.Args, os.Stdout = args, stdout }()
	os.Args = []string{"server", "legacy-payments", "--operator-file", actorFile}
	os.Stdout = writer
	err = run()
	writer.Close()
	os.Stdout = stdout
	body, readErr := io.ReadAll(reader)
	var rows []payments.LegacyReceiptReportRow
	var raw []map[string]any
	if err != nil || readErr != nil || json.Unmarshal(body, &rows) != nil || json.Unmarshal(body, &raw) != nil || len(rows) != 1 || len(raw[0]) != 7 || rows[0].AmountMinor == nil || *rows[0].AmountMinor != 123 {
		t.Fatal("unsafe or missing report", err)
	}
	var jobs int
	if e.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&jobs) != nil || jobs != 0 {
		t.Fatal("report started workers")
	}
	if os.Chmod(actorFile, 0644) != nil || runLegacyReceipts("--operator-file", actorFile) == nil {
		t.Fatal("public operator file accepted")
	}
}
