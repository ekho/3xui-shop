package httpapi

import (
	"context"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/operations"
	"strings"
	"testing"
)

func TestReadyzDependenciesAndShutdown(t *testing.T) {
	pgErr, redisErr := error(nil), error(nil)
	stopped := make(chan struct{})
	ready := operations.NewReadiness(func(context.Context) error { return pgErr }, func(context.Context) error { return redisErr }, stopped)
	h := New(&app.Modules{}, nil, app.HTTPConfig{}, ready)
	check := func(want int) {
		t.Helper()
		r := request(h, "GET", "/readyz", "", "")
		if r.Code != want || strings.Contains(r.Body.String(), "private dependency") {
			t.Fatalf("want %d got %d: %s", want, r.Code, r.Body.String())
		}
	}
	check(200)
	pgErr = context.DeadlineExceeded
	check(503)
	pgErr = nil
	redisErr = context.DeadlineExceeded
	check(503)
	redisErr = nil
	close(stopped)
	check(503)
	ready = operations.NewReadiness(func(context.Context) error { return nil }, func(context.Context) error { return nil }, make(chan struct{}))
	h = New(&app.Modules{}, nil, app.HTTPConfig{}, ready)
	ready.Stop()
	check(503)
}
