package db_test

import (
	"bytes"
	"context"
	"example.com/cabinet/backend/db"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"os"
	"strings"
	"testing"
)

func jobKindProvider(t *testing.T, e *testkit.Env) *goose.Provider {
	t.Helper()
	database := stdlib.OpenDBFromPool(e.Pool)
	t.Cleanup(func() { database.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func seedJobKindSources(t *testing.T, e *testkit.Env) (uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	account, mail, trial, access := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	request := uuid.New()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
		 VALUES($1,$2,'ru','fixture',now(),$3,'aaaaaaaaaaaaaaaa',$4,'1','1')`, []any{account, "kind-" + account.String() + "@example.test", uuid.New(), "acct_" + account.String()}},
		{`INSERT INTO mail_deliveries(id,email_key,created_at) VALUES($1,'kind-mail@example.test',now())`, []any{mail}},
		{`INSERT INTO trial_requests(id,account_id,status,comment,created_at,decided_at,operator_tg_id,operation_id)
		 VALUES($1,$2,'approved','fixture',now(),now(),1,$3)`, []any{request, account, trial}},
		{`INSERT INTO trial_operations(id,account_id,request_id,status,trial_enabled,period_days,traffic_gb,devices,panel_id,created_at)
		 VALUES($1,$2,$3,'provisioning',true,3,15,1,'panel',now())`, []any{trial, account, request}},
		{`INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at)
		 VALUES($1,$2,'reset_traffic','applied','fixture','{}','{}',now(),now())`, []any{access, account}},
		{`INSERT INTO monthly_reset_periods(account_id,local_period,timezone,status,created_at,updated_at)
		 VALUES($1,'2026-10','UTC','waiting',now(),now())`, []any{account}},
	} {
		if _, err = tx.Exec(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return account, mail, trial, access
}

func TestSemanticJobKindMigrationPreservesRiverHistory(t *testing.T) {
	e := testkit.Open(t) // Fresh database runs the guard before River creates its tables.
	ctx := context.Background()
	provider := jobKindProvider(t, e)
	if _, err := provider.DownTo(ctx, 13); err != nil {
		t.Fatal(err)
	}
	account, mail, trial, access := seedJobKindSources(t, e)
	jobs := []struct {
		args  string
		queue string
		kind  string
	}{
		{`{"delivery_id":"` + mail.String() + `"}`, "default", "mail_delivery"},
		{`{"operation_id":"` + trial.String() + `"}`, "provision", "trial_provision"},
		{`{"operation_id":"` + access.String() + `"}`, "provision", "access_operation"},
		{`{"account_id":"` + account.String() + `","local_period":"2026-10"}`, "provision", "monthly_traffic_reset"},
	}
	states := []string{"available", "running", "retryable", "scheduled", "completed", "discarded", "cancelled"}
	var ids []int64
	var snapshots [][]byte
	for _, job := range jobs {
		for _, state := range states {
			var id int64
			finalized := state == "completed" || state == "discarded" || state == "cancelled"
			err := e.Pool.QueryRow(ctx, `INSERT INTO river_job(kind,args,queue,state,attempt,max_attempts,attempted_at,attempted_by,errors,finalized_at,metadata)
			 VALUES('legacy_job',$1,$2,$3,2,5,now(),ARRAY['worker'],ARRAY['{"message":"fixture"}'::jsonb],
			 CASE WHEN $4 THEN now() ELSE NULL END,'{"source":"fixture"}') RETURNING id`, job.args, job.queue, state, finalized).Scan(&id)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
			var before []byte
			if err := e.Pool.QueryRow(ctx, `SELECT (to_jsonb(j)-'kind')::text FROM river_job j WHERE id=$1`, id).Scan(&before); err != nil {
				t.Fatal(err)
			}
			snapshots = append(snapshots, before)
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		var kind string
		var after []byte
		err := e.Pool.QueryRow(ctx, `SELECT kind,(to_jsonb(j)-'kind')::text FROM river_job j WHERE id=$1`, id).Scan(&kind, &after)
		if err != nil {
			t.Fatal(err)
		}
		want := jobs[i/len(states)]
		if kind != want.kind || !bytes.Equal(after, snapshots[i]) {
			t.Fatalf("River row %d lost kind or history: %s", id, kind)
		}
	}
}

func TestSemanticJobKindMigrationRejectsUnknownAtomically(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	provider := jobKindProvider(t, e)
	if _, err := provider.DownTo(ctx, 13); err != nil {
		t.Fatal(err)
	}
	account, mail, trial, _ := seedJobKindSources(t, e)
	for _, args := range []string{`{"delivery_id":"` + mail.String() + `"}`, `{}`} {
		if _, err := e.Pool.Exec(ctx, `INSERT INTO river_job(kind,args,queue,max_attempts) VALUES('legacy_job',$1,'default',5)`, args); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Migrate(ctx, e.Pool); err == nil || !strings.Contains(err.Error(), "unclassified") {
		t.Fatalf("unknown job should block migration: %v", err)
	}
	var unchanged int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='legacy_job'`).Scan(&unchanged); err != nil || unchanged != 2 {
		t.Fatalf("partial kind rewrite: count=%d err=%v", unchanged, err)
	}
	var version int64
	if err := e.Pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 13 {
		t.Fatalf("failed migration advanced version: %d %v", version, err)
	}
	if _, err := e.Pool.Exec(ctx, `DELETE FROM river_job WHERE args='{}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at)
	 VALUES($1,$2,'reset_traffic','applied','fixture','{}','{}',now(),now())`, trial, account); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO river_job(kind,args,queue,max_attempts) VALUES('legacy_job',$1,'provision',5)`, `{"operation_id":"`+trial.String()+`"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous job should block migration: %v", err)
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind='legacy_job'`).Scan(&unchanged); err != nil || unchanged != 2 {
		t.Fatalf("partial ambiguous rewrite: count=%d err=%v", unchanged, err)
	}
	if _, err := e.Pool.Exec(ctx, `DELETE FROM river_job WHERE queue='provision'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE river_job SET unique_key=decode('01','hex')`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err == nil || !strings.Contains(err.Error(), "unique River key") {
		t.Fatalf("unreviewed uniqueness should block migration: %v", err)
	}
}
