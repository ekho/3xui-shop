package app

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/operations"
	"example.com/cabinet/backend/internal/modules/telegram"
	"net/http"
	"sync"
	"time"
)

func Serve(ctx context.Context, server *http.Server, result, schedulerResult <-chan error, tg *telegram.Runtime, additional ...*telegram.Runtime) error {
	return serve(ctx, server, result, schedulerResult, tg, nil, nil, 20*time.Second, additional...)
}

func ServeWithOperations(ctx context.Context, server *http.Server, result, schedulerResult <-chan error, tg *telegram.Runtime, readiness *operations.Readiness, reporter *operations.Reporter, additional ...*telegram.Runtime) error {
	return serve(ctx, server, result, schedulerResult, tg, readiness, reporter, 20*time.Second, additional...)
}

func observeTelegram(channel *telegram.Runtime, module string, reporter *operations.Reporter) {
	if channel == nil || reporter == nil {
		return
	}
	var mu sync.Mutex
	degraded := false
	channel.Observe(func(state telegram.State) {
		mu.Lock()
		if state.Status == "degraded" && !degraded {
			degraded = true
			reporter.Notify(module, "degraded", state.Code)
		} else if state.Status == "running" && degraded {
			degraded = false
			reporter.Notify(module, "recovered", "")
		}
		reporter.Notify(module, state.Status, state.Code)
		mu.Unlock()
	})
}

func serve(ctx context.Context, server *http.Server, result, schedulerResult <-chan error, tg *telegram.Runtime, readiness *operations.Readiness, reporter *operations.Reporter, shutdownGrace time.Duration, additional ...*telegram.Runtime) error {
	channelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if reporter != nil {
		reporter.Notify("http", "running", "")
		reporter.Notify("schedulers", "running", "")
		observeTelegram(tg, "telegram_main", reporter)
		for _, channel := range additional {
			observeTelegram(channel, "telegram_support", reporter)
		}
		if readiness != nil && readiness.Ready(ctx) {
			reporter.Notify("backend", "ready", "")
		}
	}
	channelDone := make(chan struct{})
	var channels sync.WaitGroup
	for _, channel := range append([]*telegram.Runtime{tg}, additional...) {
		if channel != nil && channel.State().Enabled {
			channels.Add(1)
			go func() { defer channels.Done(); _ = channel.Run(channelCtx) }()
		}
	}
	go func() { channels.Wait(); close(channelDone) }()
	var err error
	failureModule := ""
	select {
	case err = <-schedulerResult:
		if err == nil && ctx.Err() == nil {
			err = errors.New("scheduler stopped")
		}
		failureModule = "schedulers"
	case err = <-result:
		if errors.Is(err, http.ErrServerClosed) {
			if ctx.Err() == nil {
				err = errors.New("http stopped")
			} else {
				err = nil
			}
		}
		failureModule = "http"
	case <-ctx.Done():
	}
	if readiness != nil {
		readiness.Stop()
	}
	if reporter != nil {
		reporter.Notify("backend", "stopping", "")
		reporter.Notify("http", "stopping", "")
		reporter.Notify("schedulers", "stopping", "")
	}
	cancel()
	stop, done := context.WithTimeout(context.Background(), shutdownGrace)
	defer done()
	shutdownErr := server.Shutdown(stop)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	select {
	case <-channelDone:
	case <-stop.Done():
		if shutdownErr == nil {
			shutdownErr = stop.Err()
		}
	}
	if reporter != nil {
		if err != nil {
			reporter.Notify(failureModule, "error", "SERVICE_UNAVAILABLE")
		}
		if shutdownErr != nil {
			reporter.Notify("http", "error", "SERVICE_UNAVAILABLE")
		} else {
			reporter.Notify("http", "stopped", "")
		}
	}
	if err != nil {
		return err
	}
	return shutdownErr
}
