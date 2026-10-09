package vpn

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func poolFixture(t *testing.T) (*testkit.Env, *Service, *Settings, *httptest.Server, *httptest.Server) {
	t.Helper()
	e := testkit.Open(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pool-fixture" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /panel/api/inbounds/list":
			w.Write([]byte(`{"success":true,"obj":[{"id":1,"tag":"regular","enable":true}]}`))
		case "POST /panel/api/setting/all":
			w.Write([]byte(`{"success":true,"obj":{"subEnable":true,"subURI":"https://secondary.example.test/sub/"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	a, b := httptest.NewTLSServer(handler), httptest.NewTLSServer(handler)
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)
	ca := x509.NewCertPool()
	ca.AddCert(a.Certificate())
	ca.AddCert(b.Certificate())
	cfg := &Settings{PanelID: "a", SubscriptionBaseURL: "https://primary.example.test/sub/", Panel: Config{PanelURL: a.URL, PanelToken: "pool-fixture", PanelRootCAs: ca}}
	s := New(e.Pool, accounts.New(e.Pool, e.Redis, nil, accounts.Config{Now: e.Clock}), nil, func() Settings { return *cfg }, e.Clock, nil, PurchaseHooks{})
	return e, s, cfg, a, b
}

func registerPoolServer(t *testing.T, e *testkit.Env, s *Service, in ServerInput) Server {
	t.Helper()
	ctx := context.Background()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	v, err := s.RegisterServerTx(ctx, tx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestServerPoolRegistry(t *testing.T) {
	e, s, cfg, a, b := poolFixture(t)
	ctx := context.Background()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ServersTx(ctx, tx)
	if err != nil || len(rows) != 1 || rows[0].ID != "a" || rows[0].MaxClients != nil {
		t.Fatalf("legacy readonly fallback: %+v %v", rows, err)
	}
	tx.Rollback(ctx)
	var n int
	if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM vpn_servers").Scan(&n); err != nil || n != 0 {
		t.Fatalf("query seeded registry: %d %v", n, err)
	}
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("../../../db/migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 34); err != nil {
		t.Fatalf("empty downgrade: %v", err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncServers(ctx); err != nil {
		t.Fatal(err)
	}
	v := registerPoolServer(t, e, s, ServerInput{ID: "b", Name: "Secondary", Host: b.URL, MaxClients: 2})
	if v.ID != "b" || v.MaxClients == nil || *v.MaxClients != 2 || v.Online {
		t.Fatalf("registry: %+v", v)
	}
	for _, in := range []ServerInput{
		{ID: "b", Name: "Replacement", Host: a.URL, MaxClients: 2},
		{ID: "c", Name: "Secondary", Host: "https://third.example.test", MaxClients: 2},
		{ID: "c", Name: "Third", Host: b.URL, MaxClients: 2},
		{ID: "../bad", Name: "Bad", Host: "https://third.example.test", MaxClients: 2},
		{ID: "c", Name: "", Host: "https://third.example.test", MaxClients: 2},
		{ID: "c", Name: "Third", Host: "http://third.example.test", MaxClients: 2},
		{ID: "c", Name: "Third", Host: "https://user@third.example.test", MaxClients: 2},
		{ID: "c", Name: "Third", Host: "https://third.example.test/?query=1", MaxClients: 2},
		{ID: "c", Name: "Third", Host: "https://third.example.test", MaxClients: -1},
	} {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.RegisterServerTx(ctx, tx, in)
		tx.Rollback(ctx)
		if err == nil {
			t.Fatalf("accepted invalid registry input: %+v", in)
		}
	}
	cfg.Panel.PanelURL = "https://replacement.example.test"
	if err = s.SyncServers(ctx); err != nil {
		t.Fatal(err)
	}
	panel, err := s.PanelFor(ctx, "a")
	if err != nil || panel.cfg.PanelURL != a.URL {
		t.Fatalf("startup host replaced durable host: %v", err)
	}
	panel.Close()
	if _, err = e.Pool.Exec(ctx, "UPDATE vpn_servers SET retired=true, revision=revision+1 WHERE id='b'"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PanelFor(ctx, "b"); !errors.Is(err, ErrPanel) {
		t.Fatalf("retired resolved: %v", err)
	}
	if _, err = s.PanelFor(ctx, "unknown"); !errors.Is(err, ErrPanel) {
		t.Fatalf("unknown resolved: %v", err)
	}
	tx, err = e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RegisterServerTx(ctx, tx, ServerInput{ID: "b", Name: "Secondary", Host: b.URL, MaxClients: 2})
	tx.Rollback(ctx)
	if err == nil {
		t.Fatal("retired ID revived")
	}
	if _, err = provider.DownTo(ctx, 34); err == nil || !strings.Contains(err.Error(), "server pool downgrade blocked") {
		t.Fatalf("history downgrade: %v", err)
	}
}

func TestServerPoolSelection(t *testing.T) {
	e, s, _, _, b := poolFixture(t)
	ctx := context.Background()
	if err := s.SyncServers(ctx); err != nil {
		t.Fatal(err)
	}
	registerPoolServer(t, e, s, ServerInput{ID: "b", Name: "Secondary", Host: b.URL, MaxClients: 1})
	if err := s.SyncServers(ctx); err != nil {
		t.Fatal(err)
	}
	selectID := func(want string) {
		t.Helper()
		v, err := s.AvailableServer(ctx)
		if err != nil || v.ID != want {
			t.Fatalf("selection=%s want=%s err=%v", v.ID, want, err)
		}
	}
	selectID("a")
	assigned := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,assigned_panel_id)
	 VALUES($1,'pool-assigned@example.test','ru','fixture',now(),$2,'aaaaaaaaaaaaaaaa',$3,'1','1','a')`, assigned, uuid.New(), "acct_"+assigned.String()); err != nil {
		t.Fatal(err)
	}
	selectID("b")
	reserved, op := uuid.New(), uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
	 VALUES($1,'pool-reserved@example.test','ru','fixture',now(),$2,'bbbbbbbbbbbbbbbb',$3,'1','1')`, reserved, uuid.New(), "acct_"+reserved.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at)
	 VALUES($1,$2,'reset_traffic','pending','pool fixture','{}','{}',now(),now())`, op, reserved); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, "INSERT INTO vpn_server_reservations(account_id,server_id,access_operation_id) VALUES($1,'b',$2)", reserved, op); err != nil {
		t.Fatal(err)
	}
	selectID("a")
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ServersTx(ctx, tx)
	tx.Rollback(ctx)
	if err != nil || len(rows) != 2 || rows[0].AssignedClients != 1 || rows[1].ReservedClients != 1 {
		t.Fatalf("literal loads: %+v %v", rows, err)
	}
	if _, err = e.Pool.Exec(ctx, "UPDATE vpn_servers SET max_clients=0 WHERE id='a'"); err != nil {
		t.Fatal(err)
	}
	selectID("a") // Both full: stable tie chooses a.
	if _, err = e.Pool.Exec(ctx, "UPDATE vpn_servers SET max_clients=2 WHERE id='b'"); err != nil {
		t.Fatal(err)
	}
	selectID("b") // Available below its cap wins over the full primary.
	if _, err = e.Pool.Exec(ctx, "UPDATE vpn_servers SET retired=true, revision=revision+1"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AvailableServer(ctx); !errors.Is(err, ErrPanel) {
		t.Fatalf("offline selection: %v", err)
	}
}

func TestPanelSubscriptionBase(t *testing.T) {
	for _, tc := range []struct {
		name, host, want string
		settings         map[string]any
	}{
		{"uri", "https://panel.example.test", "https://edge.example.test/custom/", map[string]any{"subEnable": true, "subURI": "https://edge.example.test/custom"}},
		{"host", "https://panel.example.test:2053/panel", "https://panel.example.test:2096/sub/", map[string]any{"subEnable": true, "subPort": 2096, "subPath": "/sub/", "subKeyFile": "key", "subCertFile": "cert"}},
		{"domain", "https://panel.example.test", "https://edge.example.test/sub/", map[string]any{"subEnable": true, "subDomain": "edge.example.test", "subPort": 443, "subPath": "/sub", "subKeyFile": "key", "subCertFile": "cert"}},
		{"ipv6", "https://[::1]:2053", "https://[::1]:2096/sub/", map[string]any{"subEnable": true, "subPort": 2096, "subPath": "/sub/", "subKeyFile": "key", "subCertFile": "cert"}},
		{"disabled", "https://panel.example.test", "", map[string]any{"subEnable": false, "subURI": "https://edge.example.test/sub/"}},
		{"plaintext", "https://panel.example.test", "", map[string]any{"subEnable": true, "subURI": "http://edge.example.test/sub/"}},
		{"credentials", "https://panel.example.test", "", map[string]any{"subEnable": true, "subURI": "https://user:pass@edge.example.test/sub/"}},
		{"query", "https://panel.example.test", "", map[string]any{"subEnable": true, "subURI": "https://edge.example.test/sub/?secret=1"}},
		{"fragment", "https://panel.example.test", "", map[string]any{"subEnable": true, "subURI": "https://edge.example.test/sub/#one"}},
		{"port", "https://panel.example.test", "", map[string]any{"subEnable": true, "subPort": 70000, "subPath": "/sub/", "subKeyFile": "key", "subCertFile": "cert"}},
		{"partial-tls", "https://panel.example.test", "", map[string]any{"subEnable": true, "subPort": 2096, "subPath": "/sub/", "subKeyFile": "key"}},
		{"path", "https://panel.example.test", "", map[string]any{"subEnable": true, "subPort": 2096, "subPath": "sub/?x=1", "subKeyFile": "key", "subCertFile": "cert"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.settings)
			if err != nil {
				t.Fatal(err)
			}
			got, err := panelSubscriptionBase(tc.host, raw)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("accepted settings: %s", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("base=%s want=%s err=%v", got, tc.want, err)
			}
		})
	}
	e, s, _, _, b := poolFixture(t)
	registerPoolServer(t, e, s, ServerInput{ID: "b", Name: "Secondary", Host: b.URL, MaxClients: 2})
	if err := s.SyncServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"a": "https://primary.example.test/sub/", "b": "https://secondary.example.test/sub/"} {
		got, err := s.SubscriptionBase(context.Background(), id)
		if err != nil || got != want {
			t.Fatalf("stored %s base=%s want=%s err=%v", id, got, want, err)
		}
	}
	b.Close()
	if err := s.SyncServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := s.SubscriptionBase(context.Background(), "b")
	if err != nil || got != "https://secondary.example.test/sub/" {
		t.Fatalf("failure destroyed base: %s %v", got, err)
	}
}

func TestServerPoolSyncRevision(t *testing.T) {
	t.Run("newer observation wins", func(t *testing.T) {
		e, s, cfg, _, _ := poolFixture(t)
		started, release := make(chan struct{}), make(chan struct{})
		var first atomic.Bool
		p := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if first.CompareAndSwap(false, true) {
				close(started)
				<-release
				w.Write([]byte(`{"success":true,"obj":[{"id":1,"tag":"regular","enable":true}]}`))
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		t.Cleanup(p.Close)
		cfg.Panel.PanelRootCAs.AddCert(p.Certificate())
		cfg.Panel.PanelURL = p.URL
		done := make(chan error, 1)
		go func() { done <- s.SyncServers(context.Background()) }()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("probe did not start")
		}
		if err := s.SyncServers(context.Background()); err != nil {
			close(release)
			t.Fatal(err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		var online bool
		if err := e.Pool.QueryRow(context.Background(), "SELECT online FROM vpn_servers WHERE id='a'").Scan(&online); err != nil || online {
			t.Fatalf("old success replaced newer failure: %v", err)
		}
	})
	t.Run("retirement wins", func(t *testing.T) {
		e, s, cfg, _, _ := poolFixture(t)
		started, release := make(chan struct{}), make(chan struct{})
		var blocked atomic.Bool
		p := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if blocked.CompareAndSwap(false, true) {
				close(started)
				<-release
			}
			w.Write([]byte(`{"success":true,"obj":[{"id":1,"tag":"regular","enable":true}]}`))
		}))
		t.Cleanup(p.Close)
		cfg.Panel.PanelRootCAs.AddCert(p.Certificate())
		cfg.Panel.PanelURL = p.URL
		done := make(chan error, 1)
		go func() { done <- s.SyncServers(context.Background()) }()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("probe did not start")
		}
		if _, err := e.Pool.Exec(context.Background(), "UPDATE vpn_servers SET retired=true, online=false, revision=revision+1 WHERE id='a'"); err != nil {
			close(release)
			t.Fatal(err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		var online bool
		if err := e.Pool.QueryRow(context.Background(), "SELECT online FROM vpn_servers WHERE id='a'").Scan(&online); err != nil || online {
			t.Fatalf("stale probe revived retired server: %v", err)
		}
	})
}
