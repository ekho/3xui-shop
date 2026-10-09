package app

import (
	"bytes"
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/operations"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestServeDoesNotAnnounceReadyOnRedisOutage(t *testing.T) {
	checked := make(chan struct{})
	ready := operations.NewReadiness(func(context.Context) error { return nil }, func(context.Context) error { close(checked); return errors.New("redis private address") }, make(chan struct{}))
	var notices []string
	reporter := operations.NewReporter(slog.New(slog.NewTextHandler(io.Discard, nil)), "operator@example.test", func(_ context.Context, _, subject, _ string) error { notices = append(notices, subject); return nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeWithOperations(ctx, &http.Server{}, make(chan error), make(chan error), nil, ready, reporter)
	}()
	select {
	case <-checked:
	case <-time.After(time.Second):
		t.Fatal("readiness was not checked")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown stalled")
	}
	stop, release := context.WithTimeout(context.Background(), time.Second)
	defer release()
	if err := reporter.Close(stop); err != nil {
		t.Fatal(err)
	}
	for _, subject := range notices {
		if strings.Contains(subject, "ready") {
			t.Fatal("false ready notice", notices)
		}
	}
}

func TestShutdownTimeoutReportsHTTPError(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { close(started); <-release; w.WriteHeader(204) })}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	requestDone := make(chan struct{})
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			resp.Body.Close()
		}
		close(requestDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("request did not start")
	}
	var log bytes.Buffer
	reporter := operations.NewReporter(slog.New(slog.NewTextHandler(&log, nil)), "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, server, result, make(chan error), nil, nil, reporter, time.Millisecond) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown timeout was ignored")
	}
	if !strings.Contains(log.String(), "module=http state=error") || strings.Contains(log.String(), "module=http state=stopped") {
		t.Fatal("false HTTP lifecycle state", log.String())
	}
	close(release)
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("request did not finish")
	}
}

func TestShutdownTimeoutCancelsActiveRequest(t *testing.T) {
	started, handlerDone := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(handlerDone) })}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, server, result, make(chan error), nil, nil, nil, time.Millisecond) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown timeout was ignored")
	}
	select {
	case <-handlerDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed-out drain left handler active")
	}
}
