package main

import (
	"context"
	"errors"
	"example.com/cabinet/backend/db"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/httpapi"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/operations"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		slog.Error("backend stopped", "code", "SERVICE_UNAVAILABLE")
		os.Exit(1)
	}
}
func run() (runErr error) {
	if len(os.Args) >= 3 && os.Args[1] == "backup" {
		return runBackupCommand(os.Args[2:])
	}
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		return runHealthcheck(listenAddress())
	}
	if len(os.Args) >= 3 && os.Args[1] == "catalogue" {
		return runCatalogueCommand(os.Args[2:])
	}
	if len(os.Args) == 3 && os.Args[1] == "import-legacy-approvals" {
		return runLegacyApprovalImport(os.Args[2])
	}
	if len(os.Args) == 3 && os.Args[1] == "import-legacy-payments" {
		return runLegacyPaymentImport(os.Args[2])
	}
	if len(os.Args) == 3 && os.Args[1] == "import-legacy-campaigns" {
		return runLegacyCampaignImport(os.Args[2])
	}
	if len(os.Args) == 3 && os.Args[1] == "import-legacy-audit" {
		return runLegacyAuditImport(os.Args[2])
	}
	if len(os.Args) == 3 && os.Args[1] == "import-legacy-support" {
		return runLegacySupportImport(os.Args[2])
	}
	if len(os.Args) == 5 && os.Args[1] == "operator" {
		return runOperatorCommand(os.Args[2], os.Args[3], os.Args[4])
	}
	if len(os.Args) != 2 || (os.Args[1] != "serve" && os.Args[1] != "migrate" && os.Args[1] != "reconcile") {
		slog.Error("usage: server serve|migrate|reconcile or server operator grant|revoke --account-file <absolute-path>")
		return errors.New("invalid command")
	}
	cfg, err := app.LoadConfig()
	if err != nil {
		slog.Error("invalid configuration")
		return err
	}
	var reporter *operations.Reporter
	if os.Args[1] == "serve" {
		reporter = operations.NewReporter(slog.Default(), cfg.OperationsEmail, func(ctx context.Context, to, subject, body string) error {
			return notifications.SendSMTP(ctx, cfg.Mail, to, subject, body)
		})
		reporter.Notify("backend", "starting", "")
		defer func() {
			if runErr != nil {
				reporter.Notify("backend", "error", "SERVICE_UNAVAILABLE")
			} else {
				reporter.Notify("backend", "stopped", "")
			}
			stop, done := context.WithTimeout(context.Background(), 4*time.Second)
			defer done()
			_ = reporter.Close(stop)
		}()
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	skipPoolClose := false
	defer func() {
		// A timed-out worker may still own a connection; process exit closes it without blocking here.
		if !skipPoolClose {
			pool.Close()
		}
	}()
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
	if os.Args[1] == "serve" {
		if err = limiter.Ping(ctx).Err(); err != nil {
			return errors.New("redis unavailable")
		}
	}
	queue, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))})
	if err != nil {
		return err
	}
	svc := app.NewModules(pool, limiter, queue, &cfg)
	var tg *telegram.Runtime
	var supportTG *telegram.Runtime
	var auditMirror func(context.Context, string) error
	if os.Args[1] == "serve" {
		tgConfig, e := telegram.LoadConfig(cfg.Accounts.Operators)
		if e != nil {
			return e
		}
		supportConfig, e := telegram.LoadSupportConfig()
		if e == nil {
			e = telegram.ValidatePollingBots(tgConfig, supportConfig)
		}
		if e != nil {
			slog.Warn("Telegram support channel unavailable", "code", "INVALID_CONFIGURATION")
			supportTG = telegram.NewUnavailableSupport()
		}
		mirrorConfig, e := telegram.LoadAuditMirrorConfig()
		if e == nil {
			auditMirror, e = telegram.NewAuditMirror(mirrorConfig, nil)
		}
		if e != nil {
			slog.Warn("Telegram audit mirror unavailable", "code", "INVALID_CONFIGURATION")
			auditMirror = func(context.Context, string) error { return &telegram.ActionError{Code: "INVALID_CONFIGURATION"} }
		}
		if tgConfig.Enabled && cfg.HTTP.AdapterToken != "" {
			return errors.New("disable legacy bot API before enabling native Telegram")
		}
		tg, e = app.NewTelegram(tgConfig, svc, cfg.HTTP.CabinetOrigin, nil)
		svc.MiniApp = app.NewTelegramMiniApp(tgConfig, svc.Accounts, cfg.Accounts.Now)
		if e != nil {
			return e
		}
		if supportTG == nil {
			supportTG, e = app.NewSupportTelegram(supportConfig, svc, cfg.HTTP.CabinetOrigin, nil)
			if e != nil {
				return e
			}
		}
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &vpn.ProvisionWorker{Service: svc.VPN})
	river.AddWorker(workers, &vpn.AccessWorker{Service: svc.VPN})
	river.AddWorker(workers, &payments.PurchaseWorker{Service: svc.Payments})
	river.AddWorker(workers, &payments.YooKassaWorker{Service: svc.Payments})
	river.AddWorker(workers, &payments.CryptomusWorker{Service: svc.Payments})
	river.AddWorker(workers, &payments.HeleketWorker{Service: svc.Payments})
	river.AddWorker(workers, &vpn.MonthlyResetWorker{Service: svc.VPN})
	queues := map[string]river.QueueConfig{"provision": {MaxWorkers: 2}, "payments": {MaxWorkers: 2}}
	if os.Args[1] == "serve" {
		river.AddWorker(workers, &notifications.MailWorker{Service: svc.MailDelivery})
		queues[river.QueueDefault] = river.QueueConfig{MaxWorkers: 2}
	}
	worker, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Workers: workers, Queues: queues, RescueStuckJobsAfter: vpn.ProvisionRescueAfter, Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))})
	if err != nil {
		return err
	}
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	if err = worker.Start(workerCtx); err != nil {
		return err
	}
	if reporter != nil {
		reporter.Notify("river", "running", "")
	}
	defer func() {
		if reporter != nil {
			reporter.Notify("river", "stopping", "")
		}
		stopped, err := stopRiver(worker, 20*time.Second, 3*time.Second)
		workerCancel()
		if err != nil {
			runErr = errors.Join(runErr, err)
			if reporter != nil {
				reporter.Notify("river", "error", "SERVICE_UNAVAILABLE")
			}
		}
		if !stopped {
			skipPoolClose = true
		}
		if stopped && reporter != nil {
			reporter.Notify("river", "stopped", "")
		}
	}()
	if os.Args[1] == "reconcile" {
		<-ctx.Done()
		return nil
	}
	address := listenAddress()
	readiness := operations.NewReadiness(pool.Ping, func(ctx context.Context) error { return limiter.Ping(ctx).Err() }, worker.Stopped())
	server := &http.Server{Addr: address, Handler: httpapi.New(svc, pool, cfg.HTTP, readiness), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	schedulerCtx, schedulerCancel := context.WithCancel(ctx)
	defer schedulerCancel()
	schedulerResult := make(chan error, 5)
	go func() {
		select {
		case <-worker.Stopped():
			if schedulerCtx.Err() == nil {
				if reporter != nil {
					reporter.Notify("river", "error", "SERVICE_UNAVAILABLE")
				}
				select {
				case schedulerResult <- errors.New("river stopped"):
				default:
				}
			}
		case <-schedulerCtx.Done():
		}
	}()
	var schedulers sync.WaitGroup
	startScheduler := func(name string, run func(context.Context) error) {
		schedulers.Add(1)
		go func() {
			defer schedulers.Done()
			if reporter != nil {
				reporter.Notify(name, "running", "")
			}
			err := run(schedulerCtx)
			if reporter != nil {
				if err != nil && schedulerCtx.Err() == nil {
					reporter.Notify(name, "error", "SERVICE_UNAVAILABLE")
				} else {
					reporter.Notify(name, "stopped", "")
				}
			}
			schedulerResult <- err
		}()
	}
	startScheduler("scheduler_monthly", svc.VPN.RunMonthlyResetScheduler)
	startScheduler("scheduler_stars", svc.Payments.RunStarsSubscriptionScheduler)
	startScheduler("scheduler_reminders", svc.Reminders.RunScheduler)
	startScheduler("scheduler_audit", func(ctx context.Context) error { return svc.AuditReports.RunScheduler(ctx, auditMirror) })
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	serveErr := app.ServeWithOperations(schedulerCtx, server, result, schedulerResult, tg, readiness, reporter, supportTG)
	schedulerCancel()
	schedulerDone := make(chan struct{})
	go func() { schedulers.Wait(); close(schedulerDone) }()
	select {
	case <-schedulerDone:
		if reporter != nil {
			reporter.Notify("schedulers", "stopped", "")
		}
	case <-time.After(20 * time.Second):
		return errors.New("schedulers did not stop")
	}
	return serveErr
}

func listenAddress() string {
	if address := os.Getenv("LISTEN_ADDRESS"); address != "" {
		return address
	}
	return "127.0.0.1:8080"
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
	databaseURL, err := app.SecretFile("DATABASE_URL")
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
	if err = app.NewModules(pool, nil, nil, &app.Config{}).Accounts.ChangeOperatorRole(ctx, id, action == "grant"); err != nil {
		return errors.New("operator role change failed")
	}
	return nil
}
