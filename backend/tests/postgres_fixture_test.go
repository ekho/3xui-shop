package tests

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/operations"
	"github.com/jackc/pgx/v5"
)

type postgresFixtureMetadata struct {
	Project     string `json:"project"`
	ContainerID string `json:"container_id"`
}

type postgresPortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type postgresFixtureInspect struct {
	ID     string                           `json:"id"`
	Labels map[string]string                `json:"labels"`
	Ports  map[string][]postgresPortBinding `json:"ports"`
}

var (
	postgresFixtureProjectRE = regexp.MustCompile(`^cabinet-[a-z][a-z0-9]*-[0-9a-f]{8}$`)
	postgresFixtureIDRE      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	postgresFixtureDBRE      = regexp.MustCompile(`^platform_test_[0-9a-f]{32}$`)
	composeProjectRE         = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

func readPostgresFixtureMetadata(path string) (postgresFixtureMetadata, error) {
	bad := func() (postgresFixtureMetadata, error) {
		return postgresFixtureMetadata{}, errors.New("invalid private PostgreSQL fixture metadata")
	}
	text, err := operations.ReadPrivateText(path)
	if err != nil {
		return bad()
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var metadata postgresFixtureMetadata
	if decoder.Decode(&metadata) != nil || decoder.Decode(new(any)) != io.EOF ||
		!postgresFixtureProjectRE.MatchString(metadata.Project) || !postgresFixtureIDRE.MatchString(metadata.ContainerID) {
		return bad()
	}
	return metadata, nil
}

func validateOwnedPostgresConfig(cfg *pgx.ConnConfig) error {
	if cfg == nil || cfg.Host != "127.0.0.1" || cfg.Port == 0 || cfg.User != "platform_test" || !postgresFixtureDBRE.MatchString(cfg.Database) {
		return errors.New("PostgreSQL fixture connection mismatch")
	}
	return nil
}

func validateOwnedPostgresFixture(metadata postgresFixtureMetadata, cfg *pgx.ConnConfig, inspected postgresFixtureInspect) error {
	if validateOwnedPostgresConfig(cfg) != nil ||
		!composeProjectRE.MatchString(metadata.Project) || !postgresFixtureIDRE.MatchString(metadata.ContainerID) ||
		inspected.ID != metadata.ContainerID || inspected.Labels["com.docker.compose.project"] != metadata.Project ||
		inspected.Labels["com.docker.compose.service"] != "postgres" {
		return errors.New("PostgreSQL fixture ownership mismatch")
	}
	bindings := inspected.Ports["5432/tcp"]
	if len(bindings) != 1 || bindings[0].HostIP != "127.0.0.1" || bindings[0].HostPort != strconv.Itoa(int(cfg.Port)) {
		return errors.New("PostgreSQL fixture port mismatch")
	}
	return nil
}

func ownedPostgresComposeFile(path string) (string, error) {
	bad := errors.New("invalid owned PostgreSQL Compose file")
	if path == "" {
		return "", bad
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", bad
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return "", bad
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return "", bad
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", bad
	}
	canonicalInfo, err := os.Lstat(canonical)
	if err != nil || !os.SameFile(info, canonicalInfo) {
		return "", bad
	}
	return canonical, nil
}

func composeProjectName(raw []byte) (string, error) {
	var config struct {
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || !composeProjectRE.MatchString(config.Name) {
		return "", errors.New("invalid PostgreSQL Compose project")
	}
	return config.Name, nil
}

func selectPostgresFixture(private, compose *postgresFixtureMetadata) (postgresFixtureMetadata, error) {
	bad := errors.New("PostgreSQL fixture selectors disagree")
	if private != nil && (!postgresFixtureProjectRE.MatchString(private.Project) || !postgresFixtureIDRE.MatchString(private.ContainerID)) {
		return postgresFixtureMetadata{}, bad
	}
	if compose != nil && (!composeProjectRE.MatchString(compose.Project) || !postgresFixtureIDRE.MatchString(compose.ContainerID)) {
		return postgresFixtureMetadata{}, bad
	}
	if private != nil && compose != nil && *private != *compose {
		return postgresFixtureMetadata{}, bad
	}
	if private != nil {
		return *private, nil
	}
	if compose != nil {
		return *compose, nil
	}
	return postgresFixtureMetadata{}, bad
}

func boundedDockerOutput(ctx context.Context, max int64, args ...string) ([]byte, error) {
	bad := errors.New("owned PostgreSQL Docker prerequisite")
	command := exec.CommandContext(ctx, "docker", args...)
	command.Stderr = io.Discard
	pipe, err := command.StdoutPipe()
	if err != nil {
		return nil, bad
	}
	if err = command.Start(); err != nil {
		_ = pipe.Close()
		return nil, bad
	}
	output, err := io.ReadAll(io.LimitReader(pipe, max+1))
	if err != nil || int64(len(output)) > max {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, bad
	}
	if command.Wait() != nil {
		return nil, bad
	}
	return output, nil
}

func composePostgresFixture(ctx context.Context, path string, cfg *pgx.ConnConfig) (postgresFixtureMetadata, error) {
	bad := func() (postgresFixtureMetadata, error) {
		return postgresFixtureMetadata{}, errors.New("owned PostgreSQL Compose prerequisite")
	}
	config, err := boundedDockerOutput(ctx, 1<<20, "compose", "-f", path, "config", "--format", "json")
	if err != nil {
		return bad()
	}
	project, err := composeProjectName(config)
	if err != nil {
		return bad()
	}
	port, err := boundedDockerOutput(ctx, 4096, "compose", "-f", path, "port", "postgres", "5432")
	if err != nil || strings.TrimSpace(string(port)) != "127.0.0.1:"+strconv.Itoa(int(cfg.Port)) {
		return bad()
	}
	id, err := boundedDockerOutput(ctx, 4096, "compose", "-f", path, "ps", "-q", "postgres")
	container := strings.TrimSpace(string(id))
	if err != nil || !postgresFixtureIDRE.MatchString(container) {
		return bad()
	}
	return postgresFixtureMetadata{Project: project, ContainerID: container}, nil
}

func controlledPostgresContainer(t *testing.T, root string, cfg *pgx.ConnConfig) string {
	t.Helper()
	if err := validateOwnedPostgresConfig(cfg); err != nil {
		t.Fatal("owned PostgreSQL connection prerequisite", err)
	}
	privatePath, hasPrivate := os.LookupEnv("TEST_POSTGRES_FIXTURE_FILE")
	composePath := os.Getenv("TEST_POSTGRES_COMPOSE_FILE")
	hasCompose := composePath != "" // Existing #44 callers may export an empty optional selector.
	if !hasPrivate && !hasCompose {
		if cfg.Port != 55491 {
			t.Fatal("restore fixture requires local controlled Compose PostgreSQL")
		}
		composePath = filepath.Join(root, "deploy/acceptance/compose.test.yml")
		hasCompose = true
	}
	var private, composed *postgresFixtureMetadata
	if hasPrivate {
		metadata, err := readPostgresFixtureMetadata(privatePath)
		if err != nil {
			t.Fatal("private PostgreSQL fixture prerequisite", err)
		}
		private = &metadata
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if hasCompose {
		path, err := ownedPostgresComposeFile(composePath)
		if err != nil {
			t.Fatal("owned PostgreSQL Compose file prerequisite", err)
		}
		metadata, err := composePostgresFixture(ctx, path, cfg)
		if err != nil {
			t.Fatal("owned PostgreSQL Compose prerequisite", err)
		}
		composed = &metadata
	}
	metadata, err := selectPostgresFixture(private, composed)
	if err != nil {
		t.Fatal("owned PostgreSQL fixture selector prerequisite", err)
	}
	const format = `{{printf "{\"id\":%q,\"labels\":%s,\"ports\":%s}" .Id (json .Config.Labels) (json .NetworkSettings.Ports)}}`
	output, err := boundedDockerOutput(ctx, 64<<10, "inspect", "--type=container", "--format", format, metadata.ContainerID)
	if err != nil {
		t.Fatal("owned PostgreSQL container inspect prerequisite")
	}
	var inspected postgresFixtureInspect
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&inspected) != nil || decoder.Decode(new(any)) != io.EOF || validateOwnedPostgresFixture(metadata, cfg, inspected) != nil {
		t.Fatal("owned PostgreSQL container identity or port prerequisite")
	}
	return metadata.ContainerID
}

const fixtureID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestOwnedPostgresFixtureGuard(t *testing.T) {
	meta := postgresFixtureMetadata{Project: "cabinet-backup-1234abcd", ContainerID: fixtureID}
	cfg := &pgx.ConnConfig{}
	cfg.Host, cfg.Port, cfg.User, cfg.Database = "127.0.0.1", 55491, "platform_test", "platform_test_0123456789abcdef0123456789abcdef"
	inspect := postgresFixtureInspect{
		ID: fixtureID,
		Labels: map[string]string{
			"com.docker.compose.project": "cabinet-backup-1234abcd",
			"com.docker.compose.service": "postgres",
		},
		Ports: map[string][]postgresPortBinding{
			"5432/tcp": {{HostIP: "127.0.0.1", HostPort: "55491"}},
		},
	}
	if err := validateOwnedPostgresFixture(meta, cfg, inspect); err != nil {
		t.Fatalf("owned fixture rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*postgresFixtureMetadata, *pgx.ConnConfig, *postgresFixtureInspect)
	}{
		{"wrong-id", func(_ *postgresFixtureMetadata, _ *pgx.ConnConfig, i *postgresFixtureInspect) {
			i.ID = strings.Repeat("b", 64)
		}},
		{"wrong-project", func(_ *postgresFixtureMetadata, _ *pgx.ConnConfig, i *postgresFixtureInspect) {
			i.Labels["com.docker.compose.project"] = "cabinet-backup-deadbeef"
		}},
		{"wrong-service", func(_ *postgresFixtureMetadata, _ *pgx.ConnConfig, i *postgresFixtureInspect) {
			i.Labels["com.docker.compose.service"] = "redis"
		}},
		{"wrong-port", func(_ *postgresFixtureMetadata, _ *pgx.ConnConfig, i *postgresFixtureInspect) {
			i.Ports["5432/tcp"][0].HostPort = "55492"
		}},
		{"wrong-binding-address", func(_ *postgresFixtureMetadata, _ *pgx.ConnConfig, i *postgresFixtureInspect) {
			i.Ports["5432/tcp"][0].HostIP = "0.0.0.0"
		}},
		{"wrong-config-address", func(_ *postgresFixtureMetadata, c *pgx.ConnConfig, _ *postgresFixtureInspect) { c.Host = "localhost" }},
		{"wrong-config-user", func(_ *postgresFixtureMetadata, c *pgx.ConnConfig, _ *postgresFixtureInspect) { c.User = "other" }},
		{"wrong-config-database", func(_ *postgresFixtureMetadata, c *pgx.ConnConfig, _ *postgresFixtureInspect) {
			c.Database = "platform_test"
		}},
		{"extra-port-binding", func(_ *postgresFixtureMetadata, _ *pgx.ConnConfig, i *postgresFixtureInspect) {
			i.Ports["5432/tcp"] = append(i.Ports["5432/tcp"], postgresPortBinding{HostIP: "127.0.0.1", HostPort: "55492"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c, i := meta, *cfg, inspect
			i.Labels = make(map[string]string, len(inspect.Labels))
			for k, v := range inspect.Labels {
				i.Labels[k] = v
			}
			i.Ports = map[string][]postgresPortBinding{"5432/tcp": append([]postgresPortBinding(nil), inspect.Ports["5432/tcp"]...)}
			tc.change(&m, &c, &i)
			if err := validateOwnedPostgresFixture(m, &c, i); err == nil {
				t.Fatal("foreign or ambiguous fixture accepted")
			}
		})
	}
}

func TestPrivatePostgresFixtureMetadata(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "fixture.json")
	valid := `{"project":"cabinet-backup-1234abcd","container_id":"` + fixtureID + `"}`
	for _, tc := range []struct {
		name, body string
		mode       os.FileMode
		valid      bool
	}{
		{"valid", valid, 0600, true},
		{"world-readable", valid, 0644, false},
		{"bad-json", `{`, 0600, false},
		{"short-id", `{"project":"cabinet-backup-1234abcd","container_id":"abc"}`, 0600, false},
		{"foreign-project", `{"project":"shared-default","container_id":"` + fixtureID + `"}`, 0600, false},
		{"extra-field", valid[:len(valid)-1] + `,"other":true}`, 0600, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.body), tc.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			_, err := readPostgresFixtureMetadata(path)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected metadata result: %v", err)
			}
		})
	}
}

func TestOwnedPostgresComposeFile(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "compose.json")
	if err := os.WriteFile(path, []byte(`{"name":"cabinet-backup-1234abcd"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := ownedPostgresComposeFile(path); err != nil || got != path {
		t.Fatalf("owned public Compose file rejected: %v", err)
	}
	if err := os.Chmod(path, 0664); err != nil {
		t.Fatal(err)
	}
	if _, err := ownedPostgresComposeFile(path); err == nil {
		t.Fatal("group-writable Compose file accepted")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias.json")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := ownedPostgresComposeFile(alias); err == nil {
		t.Fatal("symlink Compose file accepted")
	}
	if _, err := ownedPostgresComposeFile(""); err == nil {
		t.Fatal("empty explicit Compose selector accepted")
	}
}

func TestPostgresFixtureSelectors(t *testing.T) {
	metadata := postgresFixtureMetadata{Project: "cabinet-backup-1234abcd", ContainerID: fixtureID}
	compose := metadata
	for _, tc := range []struct {
		name    string
		private *postgresFixtureMetadata
		compose *postgresFixtureMetadata
		valid   bool
	}{
		{"metadata-only", &metadata, nil, true},
		{"compose-only", nil, &compose, true},
		{"both-agree", &metadata, &compose, true},
		{"both-project-conflict", &metadata, &postgresFixtureMetadata{Project: "cabinet-backup-deadbeef", ContainerID: fixtureID}, false},
		{"both-container-conflict", &metadata, &postgresFixtureMetadata{Project: metadata.Project, ContainerID: strings.Repeat("b", 64)}, false},
		{"neither", nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectPostgresFixture(tc.private, tc.compose)
			if (err == nil) != tc.valid || tc.valid && got != metadata {
				t.Fatalf("fixture selectors returned wrong identity: %v", err)
			}
		})
	}
}

func TestComposeProjectName(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"owned", `{"name":"cabinet-backup-1234abcd","services":{"postgres":{}}}`, true},
		{"missing", `{"services":{"postgres":{}}}`, false},
		{"unsafe", `{"name":"../foreign"}`, false},
		{"malformed", `{`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := composeProjectName([]byte(tc.raw))
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected Compose project result: %v", err)
			}
		})
	}
}
