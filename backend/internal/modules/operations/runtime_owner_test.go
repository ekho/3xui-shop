package operations

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/testkit"
)

func TestRuntimeOwnerContentionLossAndRestart(t *testing.T) {
	env := testkit.Open(t)
	ctx := context.Background()
	parsed, err := url.Parse(env.Pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + env.Pool.Config().ConnConfig.Database
	databaseURL := parsed.String()
	first, err := AcquireRuntimeOwner(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := AcquireRuntimeOwner(ctx, databaseURL); !errors.Is(err, ErrRuntimeOwned) || second != nil {
		t.Fatalf("second executor acquired ownership: %v", err)
	}
	var terminated bool
	if err := env.Pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, first.conn.PgConn().PID()).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("cannot terminate owner session: %v", err)
	}
	select {
	case <-first.Lost():
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not detect lost session")
	}
	restarted, err := AcquireRuntimeOwner(ctx, databaseURL)
	if err != nil {
		t.Fatal("replacement cannot acquire ownership", err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted.Lost():
		t.Fatal("clean close reported ownership loss")
	default:
	}
}

func TestRuntimeOwnerWarmupLossAndCancel(t *testing.T) {
	env := testkit.Open(t)
	parsed, err := url.Parse(env.Pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + env.Pool.Config().ConnConfig.Database
	databaseURL := parsed.String()
	ctx := context.Background()
	const ownerPIDQuery = `SELECT COALESCE((SELECT l.pid FROM pg_locks l WHERE l.locktype='advisory' AND l.granted AND l.mode='ExclusiveLock' AND l.database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND l.classid::bigint=((hashtextextended('operations.runtime-owner-v1',0)>>32)&4294967295) AND l.objid::bigint=(hashtextextended('operations.runtime-owner-v1',0)&4294967295) LIMIT 1),0)`
	waitPID := func() int32 {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			var pid int32
			if err := env.Pool.QueryRow(ctx, ownerPIDQuery).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if pid != 0 {
				return pid
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("warm-up session did not acquire lock")
		return 0
	}
	type result struct {
		owner *RuntimeOwner
		err   error
	}
	lost := make(chan result, 1)
	go func() { owner, err := AcquireRuntimeOwner(ctx, databaseURL); lost <- result{owner, err} }()
	pid := waitPID()
	var terminated bool
	if err := env.Pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("cannot terminate warm-up session: %v", err)
	}
	select {
	case got := <-lost:
		if got.owner != nil || !errors.Is(got.err, ErrRuntimeOwnerLost) {
			t.Fatalf("lost warm-up returned owner: %v", got.err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("lost warm-up did not fail closed")
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	canceled := make(chan result, 1)
	go func() { owner, err := AcquireRuntimeOwner(cancelCtx, databaseURL); canceled <- result{owner, err} }()
	_ = waitPID()
	cancel()
	select {
	case got := <-canceled:
		if got.owner != nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled warm-up returned owner: %v", got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled warm-up did not close")
	}
	var pidAfter int32
	if err := env.Pool.QueryRow(ctx, ownerPIDQuery).Scan(&pidAfter); err != nil || pidAfter != 0 {
		t.Fatalf("warm-up lock survived cancellation: pid=%d err=%v", pidAfter, err)
	}
}
