package httpapi

import (
	"context"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func TestServerPoolOwnerLookupBeforeWatch(t *testing.T) {
	for _, kind := range []string{"trial", "access"} {
		t.Run(kind, func(t *testing.T) {
			s, e, _, _ := serverPoolFixture(t, 10)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var operation uuid.UUID
			var run func() error
			if kind == "trial" {
				_, operation = approved(t, s, e, "pool-watch-trial@example.test")
				// A prebound legacy operation reaches concrete lookup after its lease.
				if _, err := e.Pool.Exec(ctx, "UPDATE trial_operations SET panel_id=$2 WHERE id=$1", operation, s.cfg.Subscriptions.PanelID); err != nil {
					t.Fatal(err)
				}
				run = func() error { return s.provision(ctx, operation) }
			} else {
				actor := renewalOperator(t, s, e)
				account := verified(t, s, e, "pool-watch-access@example.test")
				op, err := s.createAccessOperation(ctx, actor, account, uuid.New(), wire.AccessOperationInput{Kind: "compensate", Days: ptrInt(1), Reason: "owned lookup barrier"})
				if err != nil {
					t.Fatal(err)
				}
				operation = op.OperationId
				run = func() error { return s.applyAccess(ctx, operation) }
			}
			gate, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if _, err = gate.Exec(ctx, "LOCK vpn_servers IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- run() }()
			for {
				var blocked bool
				if err = e.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%vpn_servers%')`).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err = <-done:
					t.Fatal("worker did not reach concrete lookup", err)
				case <-ctx.Done():
					t.Fatal("owned lookup barrier timed out")
				case <-time.After(10 * time.Millisecond):
				}
			}
			// Four watchdog intervals cannot turn a slow SQL lookup into owner loss.
			select {
			case err = <-done:
				t.Fatal("lookup watchdog cancelled its own connection", err)
			case <-time.After(time.Second):
			}
			if err = gate.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("worker did not recover after concrete lookup")
			}
			var state string
			query := "SELECT status FROM trial_operations WHERE id=$1"
			if kind == "access" {
				query = "SELECT status FROM access_operations WHERE id=$1"
			}
			if err = e.Pool.QueryRow(ctx, query, operation).Scan(&state); err != nil || state != "applied" {
				t.Fatal("delayed lookup did not issue once", state, err)
			}
		})
	}
}
