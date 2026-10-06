package httpapi

import (
	"context"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/telegram"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNativeLifecycleTelegramFailure(t *testing.T) {
	for _, code := range []string{"401", "409"} {
		t.Run(code, func(t *testing.T) {
			_, modules, _, _ := bridgeFixture(t)
			h := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":` + code + `}`))}, nil
			})}
			tg, err := app.NewTelegram(telegram.Config{Enabled: true, Token: "123456789:abcdefghijklmnopqrstuvwxyz012345678", Operators: []int64{101}}, modules.Subscriptions, modules.Notifications, h)
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })}
			t.Cleanup(func() { server.Close() })
			httpResult := make(chan error, 1)
			go func() { httpResult <- server.Serve(listener) }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- app.Serve(ctx, server, httpResult, make(chan error), tg) }()
			deadline := time.Now().Add(2 * time.Second)
			for !tg.State().Degraded && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !tg.State().Degraded {
				t.Fatal("channel not degraded")
			}
			resp, err := http.Get("http://" + listener.Addr().String())
			if err != nil {
				t.Fatal("HTTP stopped with Telegram", err)
			}
			resp.Body.Close()
			select {
			case <-done:
				t.Fatal("Telegram stopped whole app")
			default:
			}
			cancel()
			select {
			case err = <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown hung")
			}
		})
	}
}

func TestNativeLifecycleDrain(t *testing.T) {
	_, modules, _, _ := bridgeFixture(t)
	started, stopped := make(chan string, 2), make(chan string, 2)
	h := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "getWebhookInfo") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"url":""}}`))}, nil
		}
		started <- r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		<-r.Context().Done()
		stopped <- "cancelled"
		return nil, r.Context().Err()
	})}
	tg, err := app.NewTelegram(telegram.Config{Enabled: true, Token: "123456789:abcdefghijklmnopqrstuvwxyz012345678", Operators: []int64{101, 202}}, modules.Subscriptions, modules.Notifications, h)
	if err != nil {
		t.Fatal(err)
	}
	requestStarted, release, shutdownStarted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseRequest := sync.OnceFunc(func() { close(release) })
	defer releaseRequest()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { close(requestStarted); <-release; w.WriteHeader(204) })}
	server.RegisterOnShutdown(func() { close(shutdownStarted) })
	defer server.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpResult := make(chan error, 1)
	go func() { httpResult <- server.Serve(listener) }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- app.Serve(ctx, server, httpResult, make(chan error), tg) }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("poll/send did not start")
		}
	}
	requestDone := make(chan error, 1)
	go func() {
		response, e := http.Get("http://" + listener.Addr().String())
		if e == nil {
			response.Body.Close()
		}
		requestDone <- e
	}()
	select {
	case <-requestStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP request did not start")
	}
	cancel()
	select {
	case <-shutdownStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not start")
	}
	for range 2 {
		select {
		case <-stopped:
		case <-time.After(3 * time.Second):
			t.Fatal("poll/send ignored cancellation")
		}
	}
	select {
	case <-done:
		t.Fatal("HTTP request not drained")
	default:
	}
	releaseRequest()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown hung")
	}
	if err = <-requestDone; err != nil {
		t.Fatal(err)
	}
}
