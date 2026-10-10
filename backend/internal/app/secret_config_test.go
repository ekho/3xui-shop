package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretFileNeverFallsBackOrDisclosesInput(t *testing.T) {
	for _, mode := range []string{"missing", "unreadable", "directory", "empty", "conflict", "valid"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private-path-fixture")
			t.Setenv("DATABASE_URL", "")
			t.Setenv("DATABASE_URL_FILE", path)
			if mode == "missing" {
				t.Setenv("DATABASE_URL_FILE", "")
				t.Setenv("DATABASE_URL", "private-env-fixture")
			} else if mode == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal("fixture directory failed")
				}
			} else if mode != "unreadable" {
				value := "private-file-fixture\n"
				if mode == "empty" {
					value = " \n "
				}
				if err := os.WriteFile(path, []byte(value), 0600); err != nil {
					t.Fatal("fixture file failed")
				}
			}
			if mode == "conflict" {
				t.Setenv("DATABASE_URL", "private-env-fixture")
			}
			value, err := SecretFile("DATABASE_URL")
			if mode == "valid" {
				if err != nil || value != "private-file-fixture" {
					t.Fatal("valid secret file rejected")
				}
				return
			}
			if err == nil || value != "" || !strings.Contains(err.Error(), "DATABASE_URL") {
				t.Fatal("invalid secret input accepted")
			}
			for _, input := range []string{path, "private-env-fixture", "private-file-fixture"} {
				if strings.Contains(err.Error(), input) {
					t.Fatal("secret input disclosed")
				}
			}
		})
	}
}
