package main

import (
	"context"
	"errors"
	"example.com/cabinet/backend/db"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/s01"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		slog.Error("backend stopped", "code", "SERVICE_UNAVAILABLE")
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 2 || (os.Args[1] != "serve" && os.Args[1] != "migrate") {
		slog.Error("usage: server serve|migrate")
		return errors.New("invalid command")
	}
	cfg, err := s01.LoadConfig()
	if err != nil {
		slog.Error("invalid configuration")
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if os.Args[1] == "migrate" {
		return db.Migrate(ctx, pool)
	}
	if err = pool.Ping(ctx); err != nil {
		return err
	}
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return err
	}
	opts.DialTimeout = 2 * time.Second
	opts.ReadTimeout = 2 * time.Second
	opts.WriteTimeout = 2 * time.Second
	limiter := redis.NewClient(opts)
	defer limiter.Close()
	queue, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))})
	if err != nil {
		return err
	}
	svc := s01.NewService(pool, limiter, queue, cfg)
	workers := river.NewWorkers()
	river.AddWorker(workers, &s01.MailWorker{Service: svc})
	river.AddWorker(workers, &s01.ProvisionWorker{Service: svc})
	worker, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Workers: workers, Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 2}, "provision": {MaxWorkers: 2}}, Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))})
	if err != nil {
		return err
	}
	if err = worker.Start(ctx); err != nil {
		return err
	}
	defer func() {
		stop, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		worker.Stop(stop)
	}()
	address := os.Getenv("LISTEN_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	server := &http.Server{Addr: address, Handler: httpapi.New(svc, cfg), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	select {
	case err = <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		stop, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		return server.Shutdown(stop)
	}
}
