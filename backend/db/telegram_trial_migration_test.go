package db_test

import (
	"context"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"os"
	"strings"
	"testing"
)

// Catches erased manual attribution, a fake/absent operator on manual approval,
// malformed automatic decisions, and a downgrade that deletes trial facts.
func TestTelegramTrialMigration(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	var column bool
	if err = e.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='trial_requests' AND column_name='decision_source')`).Scan(&column); err != nil || !column {
		t.Fatal("automatic provenance column is absent", err)
	}
	if _, err = p.DownTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	id, request, op := uuid.New(), uuid.New(), uuid.New()
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,telegram_id,display_name,locale,vpn_id,sub_id,panel_key,terms_version,privacy_version,policy_accepted_at) VALUES($1,'telegram',701,'Owned fixture','ru',$2,'trialmigration01','acct_trial_migration','1','1',now());`, id, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, `INSERT INTO trial_requests(id,account_id,status,comment,created_at) VALUES($1,$2,'pending','',now())`, request, id); err != nil {
		t.Fatal(err)
	}
	var before string
	if err = e.Pool.QueryRow(ctx, `SELECT to_jsonb(r)::text FROM trial_requests r WHERE id=$1`, request).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = p.UpTo(ctx, 27); err != nil {
		t.Fatal(err)
	}
	var after string
	if err = e.Pool.QueryRow(ctx, `SELECT (to_jsonb(r)-'decision_source')::text FROM trial_requests r WHERE id=$1`, request).Scan(&after); err != nil || before != after {
		t.Fatal("migration changed old request", err)
	}
	if _, err = p.Down(ctx); err != nil {
		t.Fatal("manual-only downgrade failed", err)
	}
	if _, err = p.UpTo(ctx, 27); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, `INSERT INTO trial_operations(id,account_id,request_id,status,trial_enabled,period_days,traffic_gb,devices,panel_id,created_at) VALUES($1,$2,$3,'pending',true,3,15,1,'owned',now())`, op, id, request); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE trial_requests SET status='approved',decided_at=now(),operation_id=$2 WHERE id=$1`,
		`UPDATE trial_requests SET status='approved',decided_at=now(),operation_id=$2,operator_tg_id=0 WHERE id=$1`,
		`UPDATE trial_requests SET decision_source='telegram_auto',status='approved',decided_at=now(),operation_id=$2,operator_tg_id=101 WHERE id=$1`,
		`UPDATE trial_requests SET decision_source='telegram_auto',status='approved',operation_id=$2 WHERE id=$1`,
	} {
		if _, err = e.Pool.Exec(ctx, sql, request, op); err == nil {
			t.Fatal("invalid automatic/manual actor accepted")
		}
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE trial_requests SET decision_source='telegram_auto',status='approved',decided_at=now(),operation_id=$2 WHERE id=$1`, request, op); err != nil {
		t.Fatal("explicit automatic decision denied", err)
	}
	if _, err = p.Down(ctx); err == nil || !strings.Contains(err.Error(), "automatic trial facts exist") {
		t.Fatal("automatic facts downgrade allowed", err)
	}
	v, err := p.GetDBVersion(ctx)
	if err != nil || v != 27 {
		t.Fatal("downgrade was not atomic", err)
	}
	var preserved bool
	if err = e.Pool.QueryRow(ctx, `SELECT decision_source='telegram_auto' AND status='approved' AND operation_id=$2 FROM trial_requests WHERE id=$1`, request, op).Scan(&preserved); err != nil || !preserved {
		t.Fatal("automatic fact lost", err)
	}
}
