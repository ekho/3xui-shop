package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"example.com/cabinet/backend/db"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/platform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func main() {
	if err := run(); err != nil {
		slog.Error("backend stopped", "code", "SERVICE_UNAVAILABLE")
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) >= 3 && os.Args[1] == "catalogue" {
		return runCatalogueCommand(os.Args[2:])
	}
	if len(os.Args) == 3 && os.Args[1] == "import-legacy-approvals" {
		return runLegacyApprovalImport(os.Args[2])
	}
	if len(os.Args) == 5 && os.Args[1] == "operator" {
		return runOperatorCommand(os.Args[2], os.Args[3], os.Args[4])
	}
	if len(os.Args) != 2 || (os.Args[1] != "serve" && os.Args[1] != "migrate" && os.Args[1] != "reconcile") {
		slog.Error("usage: server serve|migrate|reconcile or server operator grant|revoke --account-file <absolute-path>")
		return errors.New("invalid command")
	}
	cfg, err := platform.LoadConfig()
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
	svc := app.NewService(pool, limiter, queue, cfg)
	var tg *telegram.Runtime
	if os.Args[1] == "serve" {
		tgConfig, e := telegram.LoadConfig(cfg.Operators)
		if e != nil {
			return e
		}
		if tgConfig.Enabled && cfg.AdapterToken != "" {
			return errors.New("disable legacy bot API before enabling native Telegram")
		}
		tg, e = app.NewTelegram(tgConfig, svc, nil)
		if e != nil {
			return e
		}
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &vpn.ProvisionWorker{Service: svc.VPN()})
	river.AddWorker(workers, &vpn.AccessWorker{Service: svc.VPN()})
	river.AddWorker(workers, &platform.PurchaseWorker{Service: svc})
	river.AddWorker(workers, &vpn.MonthlyResetWorker{Service: svc.VPN()})
	queues := map[string]river.QueueConfig{"provision": {MaxWorkers: 2}}
	if os.Args[1] == "serve" {
		river.AddWorker(workers, &platform.MailWorker{Service: svc})
		queues[river.QueueDefault] = river.QueueConfig{MaxWorkers: 2}
	}
	worker, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Workers: workers, Queues: queues, RescueStuckJobsAfter: vpn.ProvisionRescueAfter, Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))})
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
	if os.Args[1] == "reconcile" {
		<-ctx.Done()
		return nil
	}
	monthlyResult := make(chan error, 1)
	go func() { monthlyResult <- svc.VPN().RunMonthlyResetScheduler(ctx) }()
	address := os.Getenv("LISTEN_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	server := &http.Server{Addr: address, Handler: httpapi.New(svc, cfg), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	return app.Serve(ctx, server, result, monthlyResult, tg)
}

func runOperatorCommand(action, flag, path string) error {
	if (action != "grant" && action != "revoke") || flag != "--account-file" || !filepath.IsAbs(path) {
		return errors.New("invalid operator command")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("invalid operator account file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return errors.New("unreadable operator account file")
	}
	id, err := uuid.Parse(strings.TrimSpace(string(raw)))
	if err != nil || id == uuid.Nil {
		return errors.New("invalid operator account file")
	}
	databaseURL, err := platform.SecretFile("DATABASE_URL")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return errors.New("database unavailable")
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return errors.New("database unavailable")
	}
	if err = app.NewService(pool, nil, nil, platform.Config{}).ChangeOperatorRole(ctx, id, action == "grant"); err != nil {
		return errors.New("operator role change failed")
	}
	return nil
}
