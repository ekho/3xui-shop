package vpn

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/vpn/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestServerManagementFirstPrimaryPingPersistsObservation(t *testing.T) {
	_, s, cfg, _, _ := poolFixture(t)
	ctx := context.Background()
	actor := managedActor(t, s, "first-primary-ping@example.test")
	key := uuid.New()
	v, err := s.PingManagedServer(ctx, actor, cfg.PanelID, key, "web")
	if err != nil || !v.Online || v.ObservedAt == nil || v.Revision < 1 {
		t.Fatalf("first primary ping: %+v %v", v, err)
	}
	var online bool
	var revision int64
	if err = s.pool.QueryRow(ctx, `SELECT online,revision FROM vpn_servers WHERE id=$1`, cfg.PanelID).Scan(&online, &revision); err != nil || !online || revision < 1 {
		t.Fatalf("primary observation was not persisted: %t %d %v", online, revision, err)
	}
	replayed, err := s.PingManagedServer(ctx, actor, cfg.PanelID, key, "web")
	if err != nil || replayed.ObservedAt == nil || !replayed.ObservedAt.Equal(*v.ObservedAt) {
		t.Fatalf("first primary ping replay: %+v %v", replayed, err)
	}
}

func TestServerManagementFirstPrimaryDeleteTombstones(t *testing.T) {
	_, s, cfg, _, _ := poolFixture(t)
	ctx := context.Background()
	actor := managedActor(t, s, "first-primary-delete@example.test")
	key := uuid.New()
	v, err := s.DeleteManagedServer(ctx, actor, cfg.PanelID, key, "web")
	if err != nil || !v.Retired || v.Revision < 2 {
		t.Fatalf("first primary delete: %+v %v", v, err)
	}
	var retired bool
	if err = s.pool.QueryRow(ctx, `SELECT retired FROM vpn_servers WHERE id=$1`, cfg.PanelID).Scan(&retired); err != nil || !retired {
		t.Fatalf("primary tombstone absent: %t %v", retired, err)
	}
	if err = s.SyncServers(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT retired FROM vpn_servers WHERE id=$1`, cfg.PanelID).Scan(&retired); err != nil || !retired {
		t.Fatalf("sync resurrected primary: %t %v", retired, err)
	}
	replayed, err := s.DeleteManagedServer(ctx, actor, cfg.PanelID, key, "web")
	if err != nil || !replayed.Retired || replayed.Revision != v.Revision {
		t.Fatalf("first primary delete replay: %+v %v", replayed, err)
	}
}

func TestServerManagementFirstPrimaryAuditFailureRollsBack(t *testing.T) {
	for _, action := range []string{"ping", "delete"} {
		t.Run(action, func(t *testing.T) {
			_, s, cfg, _, _ := poolFixture(t)
			ctx := context.Background()
			actor := managedActor(t, s, "primary-audit-rollback@example.test")
			if _, err := s.pool.Exec(ctx, `CREATE FUNCTION fail_primary_management_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action IN ('server.ping','server.delete') THEN RAISE EXCEPTION 'audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_primary_management_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_primary_management_audit()`); err != nil {
				t.Fatal(err)
			}
			key := uuid.New()
			var err error
			if action == "ping" {
				_, err = s.PingManagedServer(ctx, actor, cfg.PanelID, key, "web")
			} else {
				_, err = s.DeleteManagedServer(ctx, actor, cfg.PanelID, key, "web")
			}
			if err == nil {
				t.Fatal("audit failure accepted")
			}
			var servers, actions int
			if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM vpn_servers WHERE id=$1`, cfg.PanelID).Scan(&servers); err != nil || servers != 0 {
				t.Fatalf("primary registry fact survived audit rollback: %d %v", servers, err)
			}
			if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM vpn_server_actions WHERE actor_id=$1 AND idempotency_key=$2`, actor, key).Scan(&actions); err != nil || actions != 0 {
				t.Fatalf("primary action survived audit rollback: %d %v", actions, err)
			}
		})
	}
}

func TestServerManagementConcurrentDistinctDeleteKeysOneWinner(t *testing.T) {
	_, s, cfg, _, _ := poolFixture(t)
	var arrivals sync.WaitGroup
	arrivals.Add(2)
	release := make(chan struct{})
	panel := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pool-fixture" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/panel/api/inbounds/list":
			w.Write([]byte(`{"success":true,"obj":[{"id":1,"tag":"regular","enable":true}]}`))
		case "/panel/api/clients/list":
			arrivals.Done()
			<-release
			w.Write([]byte(`{"success":true,"obj":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer panel.Close()
	cfg.Panel.PanelRootCAs.AddCert(panel.Certificate())
	ctx := context.Background()
	actor := managedActor(t, s, "concurrent-delete-keys@example.test")
	v, _, err := s.CreateManagedServer(ctx, actor, uuid.New(), ServerInput{Name: "concurrent delete", Host: panel.URL}, "web")
	if err != nil {
		t.Fatal(err)
	}
	keys := []uuid.UUID{uuid.New(), uuid.New()}
	results := make([]Server, 2)
	errs := make([]error, 2)
	var done sync.WaitGroup
	for i := range keys {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			results[i], errs[i] = s.DeleteManagedServer(ctx, actor, v.ID, keys[i], "web")
		}(i)
	}
	arrived := make(chan struct{})
	go func() { arrivals.Wait(); close(arrived) }()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("both deletes did not pass preflight")
	}
	close(release)
	done.Wait()
	successes, conflicts := 0, 0
	for i, err := range errs {
		if err == nil && results[i].Retired {
			successes++
			replayed, replayErr := s.DeleteManagedServer(ctx, actor, v.ID, keys[i], "web")
			if replayErr != nil || !replayed.Retired || replayed.Revision != results[i].Revision {
				t.Fatalf("winner replay %d: %+v %v", i, replayed, replayErr)
			}
			continue
		}
		var domain *Error
		if errors.As(err, &domain) && (domain.Status == 404 || domain.Status == 409) {
			conflicts++
			_, replayErr := s.DeleteManagedServer(ctx, actor, v.ID, keys[i], "web")
			var replayDomain *Error
			if !errors.As(replayErr, &replayDomain) || replayDomain.Status != 404 {
				t.Fatalf("loser repeated with a different outcome: %v", replayErr)
			}
			continue
		}
		t.Fatalf("delete %d outcome: %+v %v", i, results[i], err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("distinct delete outcomes: success=%d conflict=%d", successes, conflicts)
	}
	var actions, audits int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM vpn_server_actions WHERE action='delete' AND server_id=$1`, v.ID).Scan(&actions); err != nil || actions != 1 {
		t.Fatalf("delete action count=%d %v", actions, err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='server.delete' AND operator_account_id=$1`, actor).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("delete audit count=%d %v", audits, err)
	}
}

func TestServerManagementSyncSlowPanelsWithinDeadline(t *testing.T) {
	_, s, cfg, _, _ := poolFixture(t)
	slow := func() *httptest.Server {
		return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer pool-fixture" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(700 * time.Millisecond):
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"obj":[{"id":1,"tag":"regular","enable":true}]}`))
		}))
	}
	a, b := slow(), slow()
	defer a.Close()
	defer b.Close()
	roots := x509.NewCertPool()
	roots.AddCert(a.Certificate())
	roots.AddCert(b.Certificate())
	cfg.Panel.PanelRootCAs = roots
	cfg.Panel.PanelURL = a.URL
	ctx := context.Background()
	actor := managedActor(t, s, "slow-sync@example.test")
	server, _, err := s.CreateManagedServer(ctx, actor, uuid.New(), ServerInput{Name: "slow second", Host: b.URL}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE vpn_servers SET subscription_base_url='https://second.example.test/sub/' WHERE id=$1`, server.ID); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 1250*time.Millisecond)
	defer cancel()
	started := time.Now()
	rows, err := s.SyncManagedServers(deadline, actor, uuid.New(), "web")
	if err != nil || len(rows) != 2 || !rows[0].Online || !rows[1].Online {
		t.Fatalf("two panels did not finish within deadline after %s: %+v %v", time.Since(started), rows, err)
	}
	short, stop := context.WithTimeout(ctx, 300*time.Millisecond)
	defer stop()
	key := uuid.New()
	if _, err = s.SyncManagedServers(short, actor, key, "web"); err == nil {
		t.Fatal("canceled probe committed a partial sync")
	}
	var actions int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM vpn_server_actions WHERE actor_id=$1 AND idempotency_key=$2`, actor, key).Scan(&actions); err != nil || actions != 0 {
		t.Fatalf("canceled sync retained an action: %d %v", actions, err)
	}
}

func managedActor(t *testing.T, s *Service, email string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	_, err := s.pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
	 VALUES($1,$2,'ru','fixture',now(),$3,'cccccccccccccccc',$4,'1','1')`, id, email, uuid.New(), "acct_"+id.String())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.accounts.ChangeOperatorRole(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err = s.accounts.ChangeInfrastructureRole(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestServerManagementCreateReplayConflictAndRetire(t *testing.T) {
	_, s, _, _, secondary := poolFixture(t)
	ctx := context.Background()
	actor := managedActor(t, s, "server-management@example.test")
	key := uuid.New()
	in := ServerInput{ID: "caller-must-not-own-id", Name: "secondary", Host: secondary.URL, MaxClients: 8}
	first, created, err := s.CreateManagedServer(ctx, actor, key, in, "web")
	if err != nil || !created || first.ID == in.ID {
		t.Fatalf("create: %+v %t %v", first, created, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE vpn_servers SET online=true WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, created, err := s.CreateManagedServer(ctx, actor, key, in, "web")
	if err != nil || created || first.ID != second.ID || second.Online != first.Online {
		t.Fatalf("replay: %+v %t %v", second, created, err)
	}
	var raw string
	if err = s.pool.QueryRow(ctx, `SELECT result::text FROM vpn_server_actions WHERE actor_id=$1 AND idempotency_key=$2`, actor, key).Scan(&raw); err != nil || strings.Contains(raw, "SubscriptionBaseURL") || strings.Contains(raw, "PanelToken") {
		t.Fatalf("unsafe action snapshot: %v", err)
	}
	in.Name = "different"
	if _, _, err = s.CreateManagedServer(ctx, actor, key, in, "web"); err == nil {
		t.Fatal("changed input replay")
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='server.create' AND operator_account_id=$1`, actor).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit count %d %v", count, err)
	}
	deleteKey := uuid.New()
	if _, err = s.DeleteManagedServer(ctx, actor, first.ID, deleteKey, "web"); err != nil {
		t.Fatal(err)
	}
	retiredServer, err := s.DeleteManagedServer(ctx, actor, first.ID, deleteKey, "web")
	if err != nil || !retiredServer.Retired {
		t.Fatalf("delete replay: %+v %v", retiredServer, err)
	}
	if _, err = s.GetManagedServer(ctx, actor, first.ID); err == nil {
		t.Fatal("retired server visible")
	}
	if _, err = s.PanelFor(ctx, first.ID); !errors.Is(err, ErrPanel) {
		t.Fatalf("retired panel resolved: %v", err)
	}
	if err = store.New(s.pool).ObservePoolServer(ctx, store.ObservePoolServerParams{ID: first.ID, Revision: first.Revision,
		ObservedAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}, Online: true, Base: ""}); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncServers(ctx); err != nil {
		t.Fatal(err)
	}
	var retired bool
	if err = s.pool.QueryRow(ctx, `SELECT retired FROM vpn_servers WHERE id=$1`, first.ID).Scan(&retired); err != nil || !retired {
		t.Fatalf("sync resurrected retired server: %v", err)
	}
}

func TestServerManagementProviderForeignClientRefusesDelete(t *testing.T) {
	_, s, cfg, _, _ := poolFixture(t)
	foreign := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/panel/api/inbounds/list":
			w.Write([]byte(`{"success":true,"obj":[{"id":1,"tag":"regular","enable":true}]}`))
		case "/panel/api/clients/list":
			w.Write([]byte(`{"success":true,"obj":[{"id":17,"email":"foreign-client","enable":true,"expiryTime":0,"limitIp":0,"totalGB":0,"inboundIds":[]}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer foreign.Close()
	cfg.Panel.PanelRootCAs.AddCert(foreign.Certificate())
	ctx := context.Background()
	actor := managedActor(t, s, "server-foreign@example.test")
	v, _, err := s.CreateManagedServer(ctx, actor, uuid.New(), ServerInput{Name: "foreign", Host: foreign.URL, MaxClients: 0}, "web")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DeleteManagedServer(ctx, actor, v.ID, uuid.New(), "web")
	var domain *Error
	if !errors.As(err, &domain) || domain.Status != 409 || domain.Code != "SERVER_IN_USE" {
		t.Fatalf("foreign client accepted as empty: %v", err)
	}
	var retired bool
	if err = s.pool.QueryRow(ctx, `SELECT retired FROM vpn_servers WHERE id=$1`, v.ID).Scan(&retired); err != nil || retired {
		t.Fatalf("foreign client server retired: %t %v", retired, err)
	}
}

func TestServerManagementDeleteAssignedRefused(t *testing.T) {
	_, s, _, _, secondary := poolFixture(t)
	ctx := context.Background()
	actor := managedActor(t, s, "server-guard@example.test")
	server, _, err := s.CreateManagedServer(ctx, actor, uuid.New(), ServerInput{Name: "secondary", Host: secondary.URL, MaxClients: 8}, "web")
	if err != nil {
		t.Fatal(err)
	}
	client := uuid.New()
	_, err = s.pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,assigned_panel_id)
	 VALUES($1,'client-guard@example.test','ru','fixture',now(),$2,'dddddddddddddddd',$3,'1','1',$4)`, client, uuid.New(), "acct_"+client.String(), server.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondary.Close()
	if _, err = s.DeleteManagedServer(ctx, actor, server.ID, uuid.New(), "web"); err == nil {
		t.Fatal("assigned server retired")
	}
	var serverError *Error
	if !errors.As(err, &serverError) || serverError.Status != 409 || serverError.Code != "SERVER_IN_USE" {
		t.Fatalf("busy server must precede provider read: %v", err)
	}
	if err = s.accounts.RequireInfrastructure(ctx, actor); err != nil {
		t.Fatal(err)
	}
	if err = s.accounts.ChangeOperatorRole(ctx, actor, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListManagedServers(ctx, actor); err == nil {
		t.Fatal("revoked operator read servers")
	}
	var domain *accounts.Error
	if !errors.As(err, &domain) || domain.Status != 403 {
		t.Fatalf("wrong revoked status %v", err)
	}
}

func TestServerManagementOfflineUnknownCannotRetire(t *testing.T) {
	_, s, _, _, secondary := poolFixture(t)
	ctx := context.Background()
	actor := managedActor(t, s, "server-offline@example.test")
	v, _, err := s.CreateManagedServer(ctx, actor, uuid.New(), ServerInput{Name: "secondary", Host: secondary.URL, MaxClients: 0}, "web")
	if err != nil {
		t.Fatal(err)
	}
	secondary.Close()
	_, err = s.DeleteManagedServer(ctx, actor, v.ID, uuid.New(), "web")
	var domain *Error
	if !errors.As(err, &domain) || domain.Status != 503 {
		t.Fatalf("offline client count treated as zero: %v", err)
	}
	var retired bool
	if err = s.pool.QueryRow(ctx, `SELECT retired FROM vpn_servers WHERE id=$1`, v.ID).Scan(&retired); err != nil || retired {
		t.Fatalf("offline server retired: %t %v", retired, err)
	}
}

func TestServerManagementConcurrentCreateAndAuditRollback(t *testing.T) {
	_, s, _, _, secondary := poolFixture(t)
	ctx := context.Background()
	actor := managedActor(t, s, "server-concurrent@example.test")
	in := ServerInput{Name: "secondary", Host: secondary.URL, MaxClients: 2}
	key := uuid.New()
	var wg sync.WaitGroup
	results := make([]Server, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _, errs[i] = s.CreateManagedServer(ctx, actor, key, in, "web")
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].ID != results[1].ID {
		t.Fatalf("concurrent create: %+v %+v %v %v", results[0], results[1], errs[0], errs[1])
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM vpn_server_actions WHERE actor_id=$1`, actor).Scan(&count); err != nil || count != 1 {
		t.Fatalf("action count %d %v", count, err)
	}
	if _, err := s.pool.Exec(ctx, `CREATE FUNCTION fail_server_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='server.create' THEN RAISE EXCEPTION 'audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_server_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_server_audit()`); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.CreateManagedServer(ctx, actor, uuid.New(), ServerInput{Name: "new-name", Host: "https://new-panel.example.test", MaxClients: 0}, "web")
	if err == nil {
		t.Fatal("audit failure committed server")
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM vpn_servers WHERE name='new-name'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("audit failure retained server %d %v", count, err)
	}
}

func TestServerManagementDeleteOwnedFactsBeforeProvider(t *testing.T) {
	for _, kind := range []string{"reserved trial", "reserved access", "trial target", "access target"} {
		t.Run(kind, func(t *testing.T) {
			_, s, _, _, secondary := poolFixture(t)
			ctx := context.Background()
			actor := managedActor(t, s, "server-fact@example.test")
			server, _, err := s.CreateManagedServer(ctx, actor, uuid.New(), ServerInput{Name: "secondary", Host: secondary.URL, MaxClients: 0}, "web")
			if err != nil {
				t.Fatal(err)
			}
			operation := uuid.New()
			target, _ := json.Marshal(map[string]string{"panel_id": server.ID})
			switch kind {
			case "reserved trial", "trial target":
				request := uuid.New()
				_, err = s.pool.Exec(ctx, `INSERT INTO trial_requests(id,account_id,status,comment,created_at) VALUES($1,$2,'pending','',now())`, request, actor)
				if err != nil {
					t.Fatal(err)
				}
				panelID := ""
				if kind == "reserved trial" {
					panelID = server.ID
				}
				_, err = s.pool.Exec(ctx, `INSERT INTO trial_operations(id,account_id,request_id,status,trial_enabled,period_days,traffic_gb,devices,panel_id,created_at,target)
				 VALUES($1,$2,$3,'pending',true,3,0,0,$4,now(),$5)`, operation, actor, request, panelID, string(target))
				if err != nil {
					t.Fatal(err)
				}
				if kind == "reserved trial" {
					_, err = s.pool.Exec(ctx, `INSERT INTO vpn_server_reservations(account_id,server_id,trial_operation_id) VALUES($1,$2,$3)`, actor, server.ID, operation)
					if err != nil {
						t.Fatal(err)
					}
				}
			case "reserved access", "access target":
				_, err = s.pool.Exec(ctx, `INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at)
				 VALUES($1,$2,'compensate','pending','fixture','{}',$3,now(),now())`, operation, actor, string(target))
				if err != nil {
					t.Fatal(err)
				}
				if kind == "reserved access" {
					_, err = s.pool.Exec(ctx, `INSERT INTO vpn_server_reservations(account_id,server_id,access_operation_id) VALUES($1,$2,$3)`, actor, server.ID, operation)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			secondary.Close()
			_, err = s.DeleteManagedServer(ctx, actor, server.ID, uuid.New(), "web")
			var domain *Error
			if !errors.As(err, &domain) || domain.Status != 409 || domain.Code != "SERVER_IN_USE" {
				t.Fatalf("%s guard: %v", kind, err)
			}
			var retired bool
			if err = s.pool.QueryRow(ctx, `SELECT retired FROM vpn_servers WHERE id=$1`, server.ID).Scan(&retired); err != nil || retired {
				t.Fatalf("retired: %t %v", retired, err)
			}
		})
	}
}
