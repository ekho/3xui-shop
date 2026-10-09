package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

func serverPoolFixture(t *testing.T, primaryCapacity int64) (*regressionFixture, *testkit.Env, *fakePanel, *fakePanel) {
	t.Helper()
	s, e := fixture(t)
	a := panelFixture(t, s)
	b := &fakePanel{up: 1234, subscriptionBase: "https://second.example.test/sub/"}
	b.server = httptest.NewTLSServer(http.HandlerFunc(b.serve))
	t.Cleanup(b.server.Close)
	s.cfg.VPN.Panel.PanelRootCAs.AddCert(b.server.Certificate())
	ctx := context.Background()
	if err := s.vpn.SyncServers(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = s.vpn.RegisterServerTx(ctx, tx, vpn.ServerInput{ID: s.cfg.Subscriptions.PanelID, Name: "Primary", Host: a.server.URL, MaxClients: primaryCapacity}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.vpn.RegisterServerTx(ctx, tx, vpn.ServerInput{ID: "second", Name: "Secondary", Host: b.server.URL, MaxClients: 10}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return s, e, a, b
}

func assertPoolTrial(t *testing.T, s *regressionFixture, e *testkit.Env, account, op uuid.UUID, panel string) {
	t.Helper()
	var assigned, bound, state, grant string
	if err := e.Pool.QueryRow(context.Background(), `SELECT a.assigned_panel_id,o.panel_id,o.status,g.status FROM accounts a JOIN trial_operations o ON o.account_id=a.id JOIN trial_grants g ON g.operation_id=o.id WHERE a.id=$1 AND o.id=$2`, account, op).Scan(&assigned, &bound, &state, &grant); err != nil {
		t.Fatal(err)
	}
	if assigned != panel || bound != panel || state != "applied" || grant != "granted" {
		t.Fatalf("assignment=%s bound=%s state=%s grant=%s", assigned, bound, state, grant)
	}
	if count(t, e, "vpn_server_reservations") != 0 {
		t.Fatal("applied reservation leaked")
	}
	sub, err := s.subscription(context.Background(), account)
	if err != nil || sub.Status != "active" || sub.DataStale {
		t.Fatalf("subscription=%+v err=%v", sub, err)
	}
}

func TestServerPoolTrialIssuance(t *testing.T) {
	s, e, a, b := serverPoolFixture(t, 0)
	account, op := approved(t, s, e, "pool-trial@example.test")
	if err := s.provision(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	assertPoolTrial(t, s, e, account, op, "second")
	if a.adds != 0 || b.adds != 1 {
		t.Fatalf("writes primary=%d secondary=%d", a.adds, b.adds)
	}
	login, raw, err := s.login(context.Background(), wire.LoginInput{Email: "pool-trial@example.test", Password: "my long safe password ✨"}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	h := New(app.NewModules(e.Pool, e.Redis, s.queue, s.cfg), e.Pool, s.cfg.HTTP)
	actor := supportSession{account, &http.Cookie{Name: "__Host-session", Value: raw}, login.CsrfToken}
	rr := supportRequest(h, &actor, "GET", "/api/v1/subscription/key", "", nil, "", uuid.Nil)
	var key wire.SubscriptionKey
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &key) != nil || !strings.HasPrefix(key.SubscriptionUrl, "https://second.example.test/sub/") || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP key status=%d", rr.Code)
	}
}

func TestServerPoolTrialIdentityChange(t *testing.T) {
	s, e, a, b := serverPoolFixture(t, 0)
	account, op := approved(t, s, e, "pool-identity@example.test")
	b.afterRead = func() {
		if _, err := e.Pool.Exec(context.Background(), "UPDATE accounts SET vpn_id=$2 WHERE id=$1", account, uuid.New()); err != nil {
			t.Error(err)
		}
	}
	if err := s.provision(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := e.Pool.QueryRow(context.Background(), "SELECT status FROM trial_operations WHERE id=$1", op).Scan(&state); err != nil || state != "needs_review" || a.adds+b.adds != 0 {
		t.Fatalf("changed identity was issued: state=%s writes=%d err=%v", state, a.adds+b.adds, err)
	}
}

func TestServerPoolConcurrentTrials(t *testing.T) {
	s, e, a, b := serverPoolFixture(t, 10)
	account1, op1 := approved(t, s, e, "pool-first@example.test")
	account2, op2 := approved(t, s, e, "pool-next@example.test")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a.beforeRead = func() { once.Do(func() { close(entered); <-release }) }
	done := make(chan error, 1)
	go func() { done <- s.provision(context.Background(), op1) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first trial did not reach read")
	}
	var held string
	if err := e.Pool.QueryRow(context.Background(), "SELECT server_id FROM vpn_server_reservations WHERE account_id=$1", account1).Scan(&held); err != nil || held != s.cfg.Subscriptions.PanelID {
		close(release)
		t.Fatalf("durable first reserve=%s err=%v", held, err)
	}
	if err := s.provision(context.Background(), op2); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertPoolTrial(t, s, e, account1, op1, s.cfg.Subscriptions.PanelID)
	assertPoolTrial(t, s, e, account2, op2, "second")
	if a.adds != 1 || b.adds != 1 || a.client["id"] == b.client["id"] {
		t.Fatal("parallel assignment or identity crossed panels")
	}
}

func TestServerPoolUnavailableTrial(t *testing.T) {
	s, e, a, b := serverPoolFixture(t, 0)
	a.offline, b.offline = true, true
	account, op := approved(t, s, e, "pool-wait@example.test")
	var snooze *river.JobSnoozeError
	if err := s.vpn.Provision(context.Background(), op); !errors.As(err, &snooze) {
		t.Fatalf("no-server must snooze: %v", err)
	}
	var panel, grant, state string
	var first *time.Time
	var attempts int
	var writes bool
	if err := e.Pool.QueryRow(context.Background(), `SELECT o.panel_id,o.first_started_at,o.attempts,o.write_started,o.status,g.status FROM trial_operations o JOIN trial_grants g ON g.operation_id=o.id WHERE o.id=$1`, op).Scan(&panel, &first, &attempts, &writes, &state, &grant); err != nil {
		t.Fatal(err)
	}
	if panel != "" || first != nil || attempts != 0 || writes || state != "pending" || grant != "reserved" || count(t, e, "vpn_server_reservations") != 0 || a.adds+b.adds != 0 {
		t.Fatal("offline trial consumed period or attempted write")
	}
	e.Advance(24 * time.Hour)
	b.offline = false
	if err := s.provision(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	assertPoolTrial(t, s, e, account, op, "second")
	if integer(t, b.client["expiryTime"]) != e.Clock().Add(72*time.Hour).UnixMilli() {
		t.Fatal("waiting consumed trial period")
	}
}

func TestServerPoolTrialLegacyAndRestart(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "new reservation restart", true: "old bound operation"}[legacy], func(t *testing.T) {
			s, e, a, b := serverPoolFixture(t, 0)
			account, op := approved(t, s, e, "pool-restart@example.test")
			if legacy {
				if _, err := e.Pool.Exec(context.Background(), "UPDATE trial_operations SET panel_id=$2 WHERE id=$1", op, s.cfg.Subscriptions.PanelID); err != nil {
					t.Fatal(err)
				}
			}
			panel := b
			want := "second"
			if legacy {
				panel = a
				want = s.cfg.Subscriptions.PanelID
			}
			panel.failRead = true
			if err := s.provision(context.Background(), op); err == nil {
				t.Fatal("unavailable initial read succeeded")
			}
			var bound string
			if err := e.Pool.QueryRow(context.Background(), "SELECT panel_id FROM trial_operations WHERE id=$1", op).Scan(&bound); err != nil || bound != want {
				t.Fatalf("binding=%s err=%v", bound, err)
			}
			panel.failRead = false
			fresh := newRegressionFixture(e.Pool, e.Redis, s.queue, *s.cfg)
			fresh.now = e.Clock
			if err := fresh.provision(context.Background(), op); err != nil {
				t.Fatal(err)
			}
			assertPoolTrial(t, fresh, e, account, op, want)
			before, err := fresh.subscriptionKey(context.Background(), account)
			if err != nil {
				t.Fatal(err)
			}
			if err = fresh.provision(context.Background(), op); err != nil {
				t.Fatal(err)
			}
			after, err := fresh.subscriptionKey(context.Background(), account)
			if err != nil || before.SubscriptionUrl != after.SubscriptionUrl || panel.adds != 1 || count(t, e, "trial_operations") != 1 || count(t, e, "trial_grants") != 1 {
				t.Fatal("restart/replay changed trial or keys")
			}
			if _, err = e.Pool.Exec(context.Background(), "UPDATE vpn_servers SET retired=true,revision=revision+1 WHERE id=$1", want); err != nil {
				t.Fatal(err)
			}
			if _, err = fresh.subscriptionKey(context.Background(), account); status(err) != 409 {
				t.Fatal("retired assigned server leaked key")
			}
			if a.adds+b.adds != 1 {
				t.Fatal("retired assigned client moved")
			}
		})
	}
}
