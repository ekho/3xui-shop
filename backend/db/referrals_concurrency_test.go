package db_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestReferralConcurrentOppositeEdgesNeverCycle(t *testing.T) {
	e := testkit.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, b := referralAccount(t, e), referralAccount(t, e)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, pair := range [][2]uuid.UUID{{a, b}, {b, a}} {
		wg.Add(1)
		go func(from, to uuid.UUID) {
			defer wg.Done()
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				results <- err
				return
			}
			defer tx.Rollback(ctx)
			<-start
			_, err = tx.Exec(ctx, `INSERT INTO referrals(id,referrer_account_id,referred_account_id,created_at) VALUES($1,$2,$3,now())`, uuid.New(), from, to)
			if err == nil {
				err = tx.Commit(ctx)
			}
			results <- err
		}(pair[0], pair[1])
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if ctx.Err() != nil {
			t.Fatal("concurrent graph writes stalled", err)
		} else {
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Fatal("opposite edge failed for an unexpected reason", err)
			}
		}
	}
	var count int
	if err := e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM referrals`).Scan(&count); err != nil || success != 1 || count != 1 {
		t.Fatalf("opposite edges formed cycle or both failed: successes=%d rows=%d err=%v", success, count, err)
	}
	tx, err := e.Pool.BeginTx(context.Background(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), `INSERT INTO referrals(id,referrer_account_id,referred_account_id,created_at) VALUES($1,$2,$3,now())`, uuid.New(), referralAccount(t, e), referralAccount(t, e)); err == nil {
		t.Fatal("repeatable-read graph write bypassed freshness guard")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatal("repeatable-read graph write failed for an unexpected reason", err)
		}
	}
}
