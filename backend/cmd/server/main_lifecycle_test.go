package main

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/app"
	"net"
	"net/http"
	"testing"
	"time"
)

// Cancellation makes both select cases ready: whether the scheduler result or
// ctx.Done wins, the in-flight request must drain before the server returns.
func TestSchedulerCompletionOnCancelDrainsHTTP(t *testing.T) {
	for range 24 {
		started := make(chan struct{})
		release := make(chan struct{})
		shutdownStarted := make(chan struct{})
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			w.WriteHeader(http.StatusNoContent)
		})}
		server.RegisterOnShutdown(func() { close(shutdownStarted) })
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		serveResult := make(chan error, 1)
		go func() { serveResult <- server.Serve(listener) }()
		requestDone := make(chan error, 1)
		go func() {
			response, err := http.Get("http://" + listener.Addr().String())
			if err == nil {
				response.Body.Close()
			}
			requestDone <- err
		}()
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			server.Close()
			t.Fatal("in-flight request never started")
		}
		ctx, cancel := context.WithCancel(context.Background())
		monthlyResult := make(chan error, 1)
		monthlyResult <- nil
		cancel()
		done := make(chan error, 1)
		go func() { done <- app.Serve(ctx, server, serveResult, monthlyResult, nil) }()
		early := false
		select {
		case <-done:
			early = true
		case <-shutdownStarted:
		case <-time.After(3 * time.Second):
			close(release)
			server.Close()
			t.Fatal("scheduler completion did not stop HTTP")
		}
		close(release)
		if !early {
			select {
			case err = <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				server.Close()
				t.Fatal("graceful shutdown did not finish")
			}
		}
		if err = <-requestDone; err != nil && !errors.Is(err, context.Canceled) {
			server.Close()
			t.Fatal(err)
		}
		server.Close()
		if early {
			t.Fatal("scheduler completion returned before draining HTTP")
		}
	}
}
