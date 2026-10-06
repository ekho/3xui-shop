package httpapi

import (
	"context"
	"crypto/x509"
	"errors"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The real TLS boundary observes PG and applies changes before returning a read.
func preparationPanel(t *testing.T, s *regressionFixture, p *fakePanel, hook func(context.Context)) {
	t.Helper()
	var once sync.Once
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/panel/api/clients/get/") {
			once.Do(func() { ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second); defer cancel(); hook(ctx) })
		}
		p.serve(w, r)
	}))
	t.Cleanup(server.Close)
	s.cfg.VPN.Panel.PanelURL = server.URL
	s.cfg.VPN.Panel.PanelRootCAs = x509.NewCertPool()
	s.cfg.VPN.Panel.PanelRootCAs.AddCert(server.Certificate())
}
func preparationNoTransaction(t *testing.T, ctx context.Context, s *regressionFixture) {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND state='idle in transaction' AND xact_start IS NOT NULL`).Scan(&n); err != nil {
		t.Error(err)
	} else if n != 0 {
		t.Errorf("panel read holds %d SQL transactions", n)
	}
}
func preparationKillOwner(t *testing.T, ctx context.Context, s *regressionFixture) {
	t.Helper()
	var pid int32
	err := s.pool.QueryRow(ctx, `SELECT DISTINCT a.pid FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid WHERE a.datname=current_database() AND a.pid<>pg_backend_pid() AND l.locktype='advisory' AND l.granted`).Scan(&pid)
	if err != nil {
		t.Error("own lock session unavailable", err)
		return
	}
	var killed bool
	if err = s.pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&killed); err != nil || !killed {
		t.Error("own session not terminated", err)
	}
}
func preparationAccessCount(t *testing.T, s *regressionFixture) (int, int) {
	t.Helper()
	var operations, jobs int
	ctx := context.Background()
	if s.pool.QueryRow(ctx, `SELECT count(*) FROM access_operations`).Scan(&operations) != nil || s.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='access_operation'`).Scan(&jobs) != nil {
		t.Fatal("cannot inspect owned fixture effects")
	}
	return operations, jobs
}
func TestRegressionAccessPreparation(t *testing.T) {
	t.Run("no transaction and unavailable replay", func(t *testing.T) {
		s, _, p, actor, target, _ := accessActors(t)
		ctx := context.Background()
		preparationPanel(t, s, p, func(ctx context.Context) { preparationNoTransaction(t, ctx, s) })
		input := wire.AccessOperationInput{Kind: "compensate", Reason: "network phase", Days: ptrInt(1)}
		key := uuid.New()
		first, err := s.createAccessOperation(ctx, actor, target, key, input)
		if err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		preparationPanel(t, s, p, func(context.Context) { calls.Add(1) })
		p.failRead = true
		replay, err := s.createAccessOperation(ctx, actor, target, key, input)
		if err != nil || first.OperationId != replay.OperationId || calls.Load() != 0 {
			t.Fatal("saved replay contacted unavailable panel", err)
		}
	})
	t.Run("observation is not identity", func(t *testing.T) {
		s, _, p, actor, target, trial := accessActors(t)
		preparationPanel(t, s, p, func(ctx context.Context) {
			if _, e := s.pool.Exec(ctx, `UPDATE trial_operations SET observed_at=$2 WHERE id=$1`, trial, time.Date(2026, 10, 1, 0, 0, 1, 0, time.UTC)); e != nil {
				t.Error(e)
			}
		})
		if _, e := s.createAccessOperation(context.Background(), actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "cached observation", Days: ptrInt(1)}); e != nil {
			t.Fatal("traffic observation rejected an unchanged baseline", e)
		}
	})
	t.Run("catalogue revision changed", func(t *testing.T) {
		s, _, p, actor, target, _ := accessActors(t)
		ctx := context.Background()
		plan, err := s.createCataloguePlan(ctx, actor, uuid.New(), wire.CataloguePlanCreateInput{Terms: catalogueTerms(1), Reason: "prepare offer"})
		if err != nil {
			t.Fatal(err)
		}
		beforeOps, beforeJobs := preparationAccessCount(t, s)
		preparationPanel(t, s, p, func(ctx context.Context) {
			if _, e := s.reviseCataloguePlan(ctx, actor, plan.PlanId, uuid.New(), wire.CataloguePlanRevisionInput{ExpectedRevision: plan.Revision, Terms: catalogueTerms(2), Reason: "offer changed"}); e != nil {
				t.Error("catalogue mutation blocked during read", e)
			}
		})
		period := int64(30)
		_, err = s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "assign_plan", Reason: "stale offer", PlanId: &plan.PlanId, Revision: &plan.Revision, PeriodDays: &period})
		ops, jobs := preparationAccessCount(t, s)
		if !catalogueCode(err, "ACCESS_PLAN_CONFLICT") || ops != beforeOps || jobs != beforeJobs || p.otherWrites != 0 {
			t.Fatal("stale catalogue revision queued", err)
		}
	})
	for _, mode := range []string{"actor revoked", "identity changed", "competing trial", "owner lost"} {
		t.Run(mode, func(t *testing.T) {
			s, e := fixture(t)
			p := panelFixture(t, s)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			actor := verified(t, s, e, "prepare-actor@example.test")
			target := verified(t, s, e, "prepare-target@example.test")
			if err := s.changeOperatorRole(ctx, actor, true); err != nil {
				t.Fatal(err)
			}
			beforeOps, beforeJobs := preparationAccessCount(t, s)
			preparationPanel(t, s, p, func(ctx context.Context) {
				preparationNoTransaction(t, ctx, s)
				var err error
				switch mode {
				case "actor revoked":
					err = s.changeOperatorRole(ctx, actor, false)
				case "identity changed":
					_, err = s.pool.Exec(ctx, `UPDATE accounts SET sub_id='fedcba9876543210' WHERE id=$1`, target)
				case "competing trial":
					var r wire.TrialRequest
					r, _, err = s.createTrialRequest(ctx, target, uuid.New(), wire.TrialRequestInput{})
					if err == nil {
						_, _, err = s.decideOperatorTrial(ctx, actor, r.RequestId, uuid.New(), wire.OperatorDecisionInput{Decision: "approve", Reason: "competing trial"})
					}
				case "owner lost":
					preparationKillOwner(t, ctx, s)
				}
				if err != nil {
					t.Error("change blocked during panel read", err)
				}
			})
			_, err := s.createAccessOperation(ctx, actor, target, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Reason: "prepare race", Days: ptrInt(1)})
			if err == nil {
				t.Fatal("changed authority/identity/owner allowed enqueue")
			}
			if mode == "actor revoked" && status(err) != 403 {
				t.Fatal("revoked actor error drift", err)
			}
			ops, jobs := preparationAccessCount(t, s)
			if ops != beforeOps || jobs != beforeJobs || p.adds != 0 || p.otherWrites != 0 {
				t.Fatal("preparation created effects after state change")
			}
		})
	}
}
func TestRegressionMonthlyPreparation(t *testing.T) {
	for _, mode := range []string{"no transaction", "profile changed", "identity changed", "period elapsed", "owner lost"} {
		t.Run(mode, func(t *testing.T) {
			s, p, _, target := unlimitedAccount(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			s.cfg.VPN.AccessResetTimezone = "UTC"
			var clock atomic.Int64
			clock.Store(s.now().UnixNano())
			s.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
			if _, err := s.enqueueMonthlyResets(ctx, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}
			beforeOps, beforeJobs := preparationAccessCount(t, s)
			preparationPanel(t, s, p, func(ctx context.Context) {
				preparationNoTransaction(t, ctx, s)
				var err error
				switch mode {
				case "profile changed":
					_, err = s.pool.Exec(ctx, `UPDATE accounts SET access_profile='regular' WHERE id=$1`, target)
				case "identity changed":
					_, err = s.pool.Exec(ctx, `UPDATE accounts SET sub_id='fedcba9876543210' WHERE id=$1`, target)
				case "period elapsed":
					// The atomic clock advances while preparation awaits its HTTP read.
					clock.Store(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixNano())
				case "owner lost":
					preparationKillOwner(t, ctx, s)
				}
				if err != nil {
					t.Error("monthly state change blocked by network transaction", err)
				}
			})
			err := s.applyMonthlyReset(ctx, target, "2026-10")
			ops, jobs := preparationAccessCount(t, s)
			if mode == "no transaction" {
				if err != nil || ops != beforeOps+1 || jobs != beforeJobs+1 {
					t.Fatal("eligible monthly claim not promoted", err)
				}
				return
			}
			if ops != beforeOps || jobs != beforeJobs {
				t.Fatal("monthly preparation queued changed/elapsed/lost state")
			}
			if mode == "owner lost" && err == nil {
				t.Fatal("monthly owner loss ignored")
			}
			if mode == "identity changed" {
				var snooze *river.JobSnoozeError
				if !errors.As(err, &snooze) {
					t.Fatal("identity conflict did not defer claim", err)
				}
			}
		})
	}
}
