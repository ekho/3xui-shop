package app

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
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
