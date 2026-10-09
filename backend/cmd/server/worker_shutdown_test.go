package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type slowCleanupArgs struct{}

func (slowCleanupArgs) Kind() string { return "slow_cleanup_test" }

type slowCleanupWorker struct {
	river.WorkerDefaults[slowCleanupArgs]
	pool              *pgxpool.Pool
	started, released chan struct{}
}

func (w *slowCleanupWorker) Work(ctx context.Context, _ *river.Job[slowCleanupArgs]) error {
	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer func() { conn.Release(); close(w.released) }()
	close(w.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestRiverTimeoutCancelsHeldDatabaseConnection(t *testing.T) {
	env := testkit.Open(t)
	worker := &slowCleanupWorker{pool: env.Pool, started: make(chan struct{}), released: make(chan struct{})}
	workers := river.NewWorkers()
	river.AddWorker(workers, worker)
	client, err := river.NewClient(riverpgxv5.New(env.Pool), &river.Config{Workers: workers, Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err = client.Insert(ctx, slowCleanupArgs{}, nil); err != nil {
		t.Fatal(err)
	}
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.StopAndCancel(context.Background())
	select {
	case <-worker.started:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not start")
	}
	stopped, err := stopRiver(client, 20*time.Millisecond, 2*time.Second)
	if !stopped || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("grace timeout not escalated", stopped, err)
	}
	select {
	case <-worker.released:
	case <-time.After(time.Second):
		t.Fatal("held connection survived hard stop")
	}
	if env.Pool.Stat().AcquiredConns() != 0 {
		t.Fatal("database connection still acquired")
	}
}
