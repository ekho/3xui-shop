package testkit

import (
	"context"
	"example.com/cabinet/backend/db"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type Env struct {
	Pool  *pgxpool.Pool
	Redis *redis.Client
	Now   time.Time
	mu    sync.Mutex
	admin *pgxpool.Pool
	name  string
}

func Open(t *testing.T) *Env {
	t.Helper()
	ctx := context.Background()
	read := func(name string) string {
		path := os.Getenv(name)
		if path == "" {
			t.Fatalf("prerequisite: %s must name a test-only URL file", name)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal("prerequisite: cannot read test URL file")
		}
		return strings.TrimSpace(string(b))
	}
	config, err := pgxpool.ParseConfig(read("S01_TEST_DATABASE_URL_FILE"))
	if err != nil {
		t.Fatal("prerequisite: invalid database config")
	}
	if config.ConnConfig.Database != "s01_test" {
		t.Fatal("prerequisite: admin database must be s01_test")
	}
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("prerequisite: cannot open test admin pool")
	}
	name := "s01_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	// name is solely a UUID generated here, never caller input.
	if _, err = admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		admin.Close()
		t.Fatal("prerequisite: cannot create isolated test DB", err)
	}
	config = config.Copy()
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	env := &Env{Pool: pool, admin: admin, name: name, Now: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	t.Cleanup(env.Close)
	if err = db.Migrate(ctx, pool); err != nil {
		t.Fatal("prerequisite: migration failed", err)
	}
	ro, err := redis.ParseURL(read("S01_TEST_REDIS_URL_FILE"))
	if err != nil {
		t.Fatal("prerequisite: invalid redis config")
	}
	env.Redis = redis.NewClient(ro)
	if err = env.Redis.Ping(ctx).Err(); err != nil {
		t.Fatal("prerequisite: redis unavailable")
	}
	return env
}
func (e *Env) Advance(d time.Duration) { e.mu.Lock(); defer e.mu.Unlock(); e.Now = e.Now.Add(d) }
func (e *Env) Clock() time.Time        { e.mu.Lock(); defer e.mu.Unlock(); return e.Now }
func (e *Env) Close() {
	e.Pool.Close()
	if e.Redis != nil {
		e.Redis.Close()
	}
	e.admin.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE %s WITH (FORCE)`, e.name))
	e.admin.Close()
}
