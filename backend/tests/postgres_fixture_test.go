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
		!postgresFixtureProjectRE.MatchString(metadata.Project) || !postgresFixtureIDRE.MatchString(metadata.ContainerID) ||
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

func controlledPostgresContainer(t *testing.T, root string, cfg *pgx.ConnConfig) string {
	t.Helper()
	if path, specified := os.LookupEnv("TEST_POSTGRES_FIXTURE_FILE"); specified {
		metadata, err := readPostgresFixtureMetadata(path)
		if err != nil {
			t.Fatal("private PostgreSQL fixture prerequisite", err)
		}
		// Check the source pool's identity before even inspecting the named container.
		if err := validateOwnedPostgresConfig(cfg); err != nil {
			t.Fatal("owned PostgreSQL connection prerequisite", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		const format = `{{printf "{\"id\":%q,\"labels\":%s,\"ports\":%s}" .Id (json .Config.Labels) (json .NetworkSettings.Ports)}}`
		command := exec.CommandContext(ctx, "docker", "inspect", "--type=container", "--format", format, metadata.ContainerID)
		output, err := command.Output()
		if err != nil || len(output) > 64<<10 {
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
	if cfg.Host != "127.0.0.1" || cfg.Port != 55491 {
		t.Fatal("restore fixture requires local controlled Compose PostgreSQL")
	}
	lookup := exec.Command("docker", "compose", "-f", filepath.Join(root, "deploy/acceptance/compose.test.yml"), "ps", "-q", "postgres")
	id, err := lookup.Output()
	container := strings.TrimSpace(string(id))
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{12,64}$`).MatchString(container) {
		t.Fatal("controlled PostgreSQL container prerequisite")
	}
	return container
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
