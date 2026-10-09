package operations

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReadiness(t *testing.T) {
	pgErr, redisErr := error(nil), error(nil)
	stopped := make(chan struct{})
	r := NewReadiness(func(context.Context) error { return pgErr }, func(context.Context) error { return redisErr }, stopped)
	if !r.Ready(context.Background()) {
		t.Fatal("started dependencies should be ready")
	}
	pgErr = errors.New("db secret")
	if r.Ready(context.Background()) {
		t.Fatal("database outage accepted")
	}
	pgErr = nil
	redisErr = errors.New("redis secret")
	if r.Ready(context.Background()) {
		t.Fatal("redis outage accepted")
	}
	redisErr = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r.Ready(ctx) {
		t.Fatal("cancelled probe accepted")
	}
	close(stopped)
	if r.Ready(context.Background()) {
		t.Fatal("stopped River accepted")
	}
	r = NewReadiness(func(context.Context) error { return nil }, func(context.Context) error { return nil }, make(chan struct{}))
	r.Stop()
	if r.Ready(context.Background()) {
		t.Fatal("shutdown accepted")
	}
}

func TestReporterDeduplicatesRedactsAndBoundsFailure(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := NewReporter(logger, "operator@example.test", func(ctx context.Context, to, subject, body string) error {
		mu.Lock()
		calls = append(calls, to+"|"+subject+"|"+body)
		mu.Unlock()
		<-ctx.Done()
		return errors.New("smtp secret")
	})
	reporter.Notify("telegram_main", "degraded", "raw provider secret")
	reporter.Notify("telegram_main", "degraded", "raw provider secret")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := reporter.Close(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || strings.Contains(calls[0], "raw provider secret") {
		t.Fatalf("unsafe or duplicate mail: %v", calls)
	}
}

func TestReporterOnlySendsOperationalTransitions(t *testing.T) {
	var sent []string
	r := NewReporter(slog.New(slog.NewTextHandler(io.Discard, nil)), "operator@example.test", func(_ context.Context, _, subject, body string) error {
		sent = append(sent, subject+"|"+body)
		return nil
	})
	for _, event := range []struct{ module, state string }{
		{"backend", "starting"}, {"backend", "ready"}, {"telegram_main", "disabled"},
		{"telegram_main", "degraded"}, {"telegram_main", "running"}, {"telegram_main", "recovered"},
		{"backend", "stopping"}, {"backend", "stopped"}, {"backend", "error"},
	} {
		r.Notify(event.module, event.state, "")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 6 {
		t.Fatal("wrong notice count", sent)
	}
	for _, payload := range sent {
		if strings.Contains(payload, "operator@example.test") {
			t.Fatal("recipient disclosed in payload")
		}
	}
}
