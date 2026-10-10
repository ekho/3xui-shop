package operations

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"example.com/cabinet/backend/db"
	"example.com/cabinet/backend/internal/modules/support"
	"github.com/google/uuid"
)

type tableInventory struct {
	Name   string `json:"name"`
	Count  int64  `json:"count"`
	SHA256 string `json:"sha256"`
}
type sequenceInventory struct {
	Name     string `json:"name"`
	Last     int64  `json:"last"`
	IsCalled bool   `json:"is_called"`
}
type manifest struct {
	Version     int                        `json:"version"`
	OperationID uuid.UUID                  `json:"operation_id"`
	CreatedAt   time.Time                  `json:"created_at"`
	PGMajor     int                        `json:"pg_major"`
	Schema      db.BackupSchema            `json:"schema"`
	DumpSize    int64                      `json:"dump_size"`
	DumpSHA256  string                     `json:"dump_sha256"`
	Tables      []tableInventory           `json:"tables"`
	Sequences   []sequenceInventory        `json:"sequences"`
	Attachments []support.BackupAttachment `json:"attachments"`
}

var sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func privatePath(path string, exists bool, directory bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("INVALID_PACKAGE")
	}
	check := path
	if !exists {
		check = filepath.Dir(path)
	}
	if exists {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("INVALID_PACKAGE")
		}
		if directory && (!info.IsDir() || info.Mode().Perm() != 0700) {
			return errors.New("INVALID_PACKAGE")
		}
		if !directory && (!info.Mode().IsRegular() || info.Mode().Perm() != 0600) {
			return errors.New("INVALID_PACKAGE")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Geteuid() || !directory && stat.Nlink != 1 {
			return errors.New("INVALID_PACKAGE")
		}
	}
	if exists {
		check = filepath.Dir(path)
	}
	for p := check; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("INVALID_PACKAGE")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}

func hashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", errors.New("INVALID_PACKAGE")
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", errors.New("INVALID_PACKAGE")
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// ReadPrivateText is the local command's file-secret boundary.
func ReadPrivateText(path string) (string, error) {
	if privatePath(path, true, false) != nil {
		return "", errors.New("INVALID_PRIVATE_FILE")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() < 1 || info.Size() > 64<<10 {
		return "", errors.New("INVALID_PRIVATE_FILE")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("INVALID_PRIVATE_FILE")
	}
	value := strings.TrimSpace(string(b))
	if value == "" || strings.ContainsRune(value, '\x00') {
		return "", errors.New("INVALID_PRIVATE_FILE")
	}
	return value, nil
}

func readPackage(dir string) (manifest, error) {
	bad := func() (manifest, error) { return manifest{}, errors.New("INVALID_PACKAGE") }
	if privatePath(dir, true, true) != nil {
		return bad()
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 || entries[0].Name() != "database.dump" || entries[1].Name() != "manifest.json" {
		return bad()
	}
	dump := filepath.Join(dir, "database.dump")
	meta := filepath.Join(dir, "manifest.json")
	if privatePath(dump, true, false) != nil || privatePath(meta, true, false) != nil {
		return bad()
	}
	info, err := os.Stat(meta)
	if err != nil || info.Size() > 16<<20 {
		return bad()
	}
	b, err := os.ReadFile(meta)
	if err != nil {
		return bad()
	}
	var m manifest
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF {
		return bad()
	}
	canonical, err := json.MarshalIndent(m, "", "  ")
	if err != nil || !bytes.Equal(b, canonical) || !validManifest(m) {
		return bad()
	}
	size, sum, err := hashFile(dump)
	if err != nil || size != m.DumpSize || sum != m.DumpSHA256 {
		return bad()
	}
	return m, nil
}

func validManifest(m manifest) bool {
	if m.Version != 1 || m.OperationID == uuid.Nil || m.CreatedAt.IsZero() || m.PGMajor < 1 || m.Schema.Version < 1 ||
		!sha256RE.MatchString(m.Schema.Migrations) || !sha256RE.MatchString(m.Schema.Structure) || !sha256RE.MatchString(m.Schema.Portable) ||
		m.DumpSize < 1 || !sha256RE.MatchString(m.DumpSHA256) || len(m.Tables) == 0 {
		return false
	}
	for i, t := range m.Tables {
		if t.Count < 0 || !sha256RE.MatchString(t.SHA256) || t.Name == "" || i > 0 && m.Tables[i-1].Name >= t.Name {
			return false
		}
	}
	for i, s := range m.Sequences {
		if s.Name == "" || i > 0 && m.Sequences[i-1].Name >= s.Name {
			return false
		}
	}
	for i, a := range m.Attachments {
		if a.ID == uuid.Nil || a.Size < 1 || !sha256RE.MatchString(a.SHA256) || i > 0 && m.Attachments[i-1].ID.String() >= a.ID.String() {
			return false
		}
	}
	return true
}
