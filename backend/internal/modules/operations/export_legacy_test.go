package operations

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExportLegacyPreservesSQLiteRealPrecision(t *testing.T) {
	path := syntheticLegacySource(t)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE referrer_rewards SET amount=? WHERE reward_type='MONEY'`, 12345.678901234567); err != nil {
		t.Fatal(err)
	}
	var original float64
	if err := db.QueryRow(`SELECT amount FROM referrer_rewards WHERE reward_type='MONEY'`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	packet, err := ExportLegacy(path, "synthetic-source", 12345, -10012345)
	if err != nil {
		t.Fatal("valid source rejected")
	}
	want := fmt.Sprintf("%.18f", original)
	if got := packet.Bonuses.Rewards[1].Amount; got != want {
		t.Fatalf("SQLite REAL lost precision: direct=%s exported=%s", want, got)
	}
	for _, value := range []any{1e20, 1e-19, []byte("123.4")} {
		if _, err := db.Exec(`UPDATE referrer_rewards SET amount=? WHERE reward_type='MONEY'`, value); err != nil {
			t.Fatal(err)
		}
		if !ExportFailed(path, "synthetic-source") {
			t.Fatal("unsafe reward amount accepted")
		}
	}
}

func syntheticLegacySource(t *testing.T) string {
	t.Helper()
	root, e := filepath.Abs("../../../..")
	if e != nil {
		t.Fatal(e)
	}
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "source.sqlite")
	cmd := exec.Command("python3", filepath.Join(root, "tests/fixtures/legacy_snapshot.py"), path)
	if e = cmd.Run(); e != nil {
		t.Fatal("fixture generation failed")
	}
	return path
}
func TestExportLegacyCompleteReadOnlyAndStrict(t *testing.T) {
	path := syntheticLegacySource(t)
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(before)
	packet, e := ExportLegacy(path, "synthetic-source", 12345, -10012345)
	if e != nil {
		t.Fatal("valid source rejected")
	}
	raw, e := MarshalLegacyExport(packet)
	if e != nil || len(raw) > MaxLegacyExportPacket || ValidateLegacyPackage(packet) != nil {
		t.Fatal("incomplete packet")
	}
	if len(packet.Users) != 3 || len(packet.Payments.Transactions) != 4 || len(packet.Bonuses.Rewards) != 2 || len(packet.Approvals.ApprovalEvents) != 1 || len(packet.Support.Tickets) != 2 || packet.Catalogue.Plans[0].Prices["RUB"]["30"] != "120.50" {
		t.Fatal("source facts lost")
	}
	after, e := os.ReadFile(path)
	if e != nil || sha256.Sum256(after) != hash {
		t.Fatal("source mutated")
	}
	if !ExportFailed(path, "Bad Source") {
		t.Fatal("invalid source accepted")
	}
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if !ExportFailed(path, "synthetic-source") {
		t.Fatal("public source accepted")
	}
	if e = os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(filepath.Dir(path), "linked.sqlite")
	if e = os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if !ExportFailed(link, "synthetic-source") {
		t.Fatal("symlink accepted")
	}
	for _, statement := range []string{
		`UPDATE plans SET prices='{"RUB":{"30":1,"30":2}}'`,
		`UPDATE audit_log SET payload='{"x":"\ud800"}' WHERE id=1`,
		`UPDATE referrer_rewards SET amount=1.5 WHERE reward_type='DAYS'`,
		`UPDATE audit_log SET created_at='2026-10-10 12:13:14.1234567' WHERE id=1`,
		`ALTER TABLE users ADD COLUMN unexpected TEXT`,
	} {
		if e = os.WriteFile(path, before, 0600); e != nil {
			t.Fatal(e)
		}
		db, e := sql.Open("sqlite3", path)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(statement); e != nil {
			t.Fatal(e)
		}
		if e = db.Close(); e != nil {
			t.Fatal(e)
		}
		if !ExportFailed(path, "synthetic-source") {
			t.Fatalf("invalid source accepted: %s", statement)
		}
	}
}
func ExportFailed(path, source string) bool {
	_, e := ExportLegacy(path, source, 12345, -10012345)
	return e != nil
}
