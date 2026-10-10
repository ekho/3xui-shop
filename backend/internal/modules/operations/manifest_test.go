package operations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/db"
	"github.com/google/uuid"
)

func validPackage(t *testing.T) (string, manifest) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "backup")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	dump := []byte("synthetic dump bytes")
	if err := os.WriteFile(filepath.Join(dir, "database.dump"), dump, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(dump)
	zero := strings.Repeat("0", 64)
	m := manifest{Version: 1, OperationID: uuid.New(), CreatedAt: time.Now().UTC(), PGMajor: 17,
		Schema: db.BackupSchema{Version: 36, Migrations: zero, Structure: zero, Portable: zero}, DumpSize: int64(len(dump)), DumpSHA256: hex.EncodeToString(sum[:]),
		Tables: []tableInventory{{Name: "accounts", Count: 1, SHA256: zero}}}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPackage(dir); err != nil {
		t.Fatalf("valid package rejected: %v", err)
	}
	return dir, m
}

func TestReadPackageRejectsExtraFileBeforeRestore(t *testing.T) {
	dir, _ := validPackage(t)
	if err := os.WriteFile(filepath.Join(dir, "extra"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPackage(dir); err == nil || !strings.Contains(err.Error(), "INVALID_PACKAGE") {
		t.Fatalf("extra entry accepted: %v", err)
	}
}

func TestReadPackageRejectsSymlink(t *testing.T) {
	dir, _ := validPackage(t)
	if err := os.Rename(filepath.Join(dir, "database.dump"), filepath.Join(filepath.Dir(dir), "actual")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../actual", filepath.Join(dir, "database.dump")); err != nil {
		t.Fatal(err)
	}
	if _, err := readPackage(dir); err == nil || !strings.Contains(err.Error(), "INVALID_PACKAGE") {
		t.Fatalf("symlink accepted: %v", err)
	}
}

func TestReadPackageRejectsHardlink(t *testing.T) {
	dir, _ := validPackage(t)
	if err := os.Link(filepath.Join(dir, "database.dump"), filepath.Join(filepath.Dir(dir), "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := readPackage(dir); err == nil || !strings.Contains(err.Error(), "INVALID_PACKAGE") {
		t.Fatalf("hardlink accepted: %v", err)
	}
}

func TestReadPackageRejectsUnknownManifestField(t *testing.T) {
	dir, _ := validPackage(t)
	path := filepath.Join(dir, "manifest.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b[:len(b)-1], []byte(",\n  \"unknown\": true\n}")...)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPackage(dir); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestReadPackageRejectsDumpCorruption(t *testing.T) {
	dir, _ := validPackage(t)
	if err := os.WriteFile(filepath.Join(dir, "database.dump"), []byte("synthetic dump bytes!"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPackage(dir); err == nil {
		t.Fatal("changed dump accepted")
	}
}
