package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheckOnlyLocalReadyz(t *testing.T) {
	called := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.URL.Path != "/readyz" || !strings.HasPrefix(r.Host, "127.0.0.1:") {
			t.Errorf("unexpected probe %s %s", r.Host, r.URL.Path)
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer s.Close()
	if err := runHealthcheck(safeAddress(t, s.URL)); err != nil || called != 1 {
		t.Fatal(err, called)
	}
	if err := runHealthcheck("example.com:80"); err == nil {
		t.Fatal("external target accepted")
	}
	if err := runHealthcheck("invalid"); err == nil {
		t.Fatal("invalid listen address accepted")
	}
}

func safeAddress(t *testing.T, raw string) string {
	t.Helper()
	return strings.TrimPrefix(raw, "http://")
}

func TestHealthcheckRejectsUnready(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer s.Close()
	if err := runHealthcheck(safeAddress(t, s.URL)); err == nil {
		t.Fatal("unready accepted")
	}
}

func TestHealthcheckIPv6Loopback(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("IPv6 loopback unavailable:", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"ok":true}`)) })}
	defer server.Close()
	go server.Serve(listener)
	if err := runHealthcheck(listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
}
