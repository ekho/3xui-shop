package operations

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type Readiness struct {
	pg, redis func(context.Context) error
	river     <-chan struct{}
	stopping  atomic.Bool
}

func NewReadiness(pg, redis func(context.Context) error, river <-chan struct{}) *Readiness {
	return &Readiness{pg: pg, redis: redis, river: river}
}

func (r *Readiness) Stop() { r.stopping.Store(true) }

func (r *Readiness) Ready(ctx context.Context) bool {
	if r == nil || r.stopping.Load() || r.pg == nil || r.redis == nil || r.river == nil || ctx.Err() != nil {
		return false
	}
	select {
	case <-r.river:
		return false
	default:
	}
	return r.pg(ctx) == nil && ctx.Err() == nil && r.redis(ctx) == nil && ctx.Err() == nil && !r.stopping.Load() && riverRunning(r.river)
}

func riverRunning(stopped <-chan struct{}) bool {
	select {
	case <-stopped:
		return false
	default:
		return true
	}
}

type notice struct{ module, state, code string }
type Send func(context.Context, string, string, string) error

type Reporter struct {
	logger *slog.Logger
	mu     sync.Mutex
	last   map[string]notice
	mail   chan notice
	done   chan struct{}
	cancel context.CancelFunc
	closed bool
}

func knownModule(value string) bool {
	switch value {
	case "backend", "http", "river", "schedulers", "scheduler_monthly", "scheduler_stars", "scheduler_reminders", "scheduler_audit", "telegram_main", "telegram_support":
		return true
	}
	return false
}

func knownState(value string) bool {
	switch value {
	case "starting", "running", "ready", "stopping", "stopped", "error", "disabled", "degraded", "recovered":
		return true
	}
	return false
}

func knownCode(value string) bool {
	switch value {
	case "", "DISABLED", "SERVICE_UNAVAILABLE", "UNAUTHORIZED", "CONFLICT", "RATE_LIMITED", "FORBIDDEN", "BAD_REQUEST", "THREAD_NOT_FOUND", "NOT_MODIFIED", "INVALID_RESPONSE", "INVALID_INPUT", "UNAVAILABLE", "REQUEST_STATE_CONFLICT", "UNSUPPORTED_PAYMENT", "WEBHOOK_CONFIGURED", "MINI_APP_NOT_CONFIGURED", "SUPPORT_BOT_MISMATCH", "SUPPORT_GROUP_NOT_FORUM", "SUPPORT_ADMIN_REQUIRED", "INVALID_CONFIGURATION":
		return true
	}
	return false
}

func NewReporter(logger *slog.Logger, recipient string, send Send) *Reporter {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Reporter{logger: logger, last: make(map[string]notice)}
	if recipient != "" && send != nil {
		ctx, cancel := context.WithCancel(context.Background())
		r.cancel = cancel
		r.mail = make(chan notice, 8)
		r.done = make(chan struct{})
		go func() {
			defer close(r.done)
			for n := range r.mail {
				attempt, done := context.WithTimeout(ctx, 2*time.Second)
				err := send(attempt, recipient, "Backend operation: "+n.module+" "+n.state, "module="+n.module+"\nstate="+n.state+"\ncode="+n.code+"\n")
				done()
				if err != nil {
					logger.Warn("operations mail failed", "module", n.module, "code", "SMTP_UNAVAILABLE")
				}
			}
		}()
	}
	return r
}

func (r *Reporter) Notify(module, state, code string) {
	if !knownModule(module) {
		module = "backend"
	}
	if !knownState(state) {
		state = "error"
	}
	if !knownCode(code) {
		code = "SERVICE_UNAVAILABLE"
	}
	n := notice{module, state, code}
	r.mu.Lock()
	if r.closed || r.last[module] == n {
		r.mu.Unlock()
		return
	}
	r.last[module] = n
	r.logger.Info("operations state", "module", module, "state", state, "code", code)
	if r.mail != nil && shouldMail(n) {
		select {
		case r.mail <- n:
		default:
			r.logger.Warn("operations mail dropped", "module", module, "code", "QUEUE_FULL")
		}
	}
	r.mu.Unlock()
}

func shouldMail(n notice) bool {
	if n.module == "backend" {
		return n.state == "ready" || n.state == "stopping" || n.state == "stopped" || n.state == "error"
	}
	return (n.module == "telegram_main" || n.module == "telegram_support") && (n.state == "degraded" || n.state == "recovered")
}

func (r *Reporter) Close(ctx context.Context) error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		if r.mail != nil {
			close(r.mail)
		}
	}
	done := r.done
	r.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		r.cancel()
		return nil
	case <-ctx.Done():
		r.cancel()
		return ctx.Err()
	}
}
