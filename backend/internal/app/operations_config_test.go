package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationsEmailConfigFailClosed(t *testing.T) {
	t.Setenv("OPERATIONS_EMAIL", "plaintext@example.test")
	if _, err := readOperationsEmail(); err == nil {
		t.Fatal("plaintext address accepted")
	}
	os.Unsetenv("OPERATIONS_EMAIL")
	t.Setenv("OPERATIONS_EMAIL_FILE", "")
	if got, err := readOperationsEmail(); err != nil || got != "" {
		t.Fatal(got, err)
	}
	path := filepath.Join(t.TempDir(), "recipient")
	t.Setenv("OPERATIONS_EMAIL_FILE", path)
	for _, value := range []string{"", "name <recipient@example.test>", "recipient@example.test\r\nBcc: victim@example.test", "a@example.test,b@example.test"} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readOperationsEmail(); err == nil || (value != "" && strings.Contains(err.Error(), value)) {
			t.Fatalf("unsafe recipient %q accepted or disclosed", value)
		}
	}
	if err := os.WriteFile(path, []byte("operator@example.test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readOperationsEmail(); err != nil || got != "operator@example.test" {
		t.Fatal(got, err)
	}
	t.Setenv("OPERATIONS_EMAIL", "conflict@example.test")
	if _, err := readOperationsEmail(); err == nil {
		t.Fatal("conflicting address accepted")
	}
}
