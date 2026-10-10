package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupRejectsUnprivateOperatorFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "operator")
	if err := os.WriteFile(file, []byte("00000000-0000-0000-0000-000000000001"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runBackupCommand([]string{"create", "--operator-file", file, "--directory", filepath.Join(t.TempDir(), "backup")}); err == nil || !strings.Contains(err.Error(), "INVALID_OPERATOR_FILE") {
		t.Fatalf("public operator file accepted: %v", err)
	}
}
