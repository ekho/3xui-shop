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

func TestAccessOperationHistoryBlocksDowngrade(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	id := uuid.New()
	account := uuid.New()
	if _, err := e.Pool.Exec(ctx, "INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) VALUES($1,'access-db@example.test','ru','fixture',now(),$2,'aaaaaaaaaaaaaaaa',$3,'1','1')", account, uuid.New(), "acct_"+account.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, "INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at) VALUES($1,$2,'reset_traffic','applied','fixture','{}','{}',now(),now())", id, account); err != nil {
		t.Fatal(err)
	}
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatalf("empty profile layer should downgrade: %v", err)
	}
	if _, err = provider.Down(ctx); err == nil || !strings.Contains(err.Error(), "access operation downgrade blocked") {
		t.Fatalf("history downgrade: %v", err)
	}
	var retained int
	if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM access_operations WHERE id=$1", id).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("history lost: %v", err)
	}
}

func TestAccessSequenceBackfillsExistingChronology(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	account, older, newer := uuid.New(), uuid.New(), uuid.New()
	if _, err = e.Pool.Exec(ctx, "INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) VALUES($1,'sequence-db@example.test','ru','fixture',now(),$2,'aaaaaaaaaaaaaaaa',$3,'1','1')", account, uuid.New(), "acct_"+account.String()); err != nil {
		t.Fatal(err)
	}
	// Physical insert order is deliberately opposite to historical created_at.
	if _, err = e.Pool.Exec(ctx, "INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at) VALUES($1,$2,'reset_traffic','applied','newer','{}','{}','2026-10-02T00:00:00Z','2026-10-02T00:00:00Z'),($3,$2,'reset_traffic','applied','older','{}','{}','2026-10-01T00:00:00Z','2026-10-01T00:00:00Z')", newer, account, older); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var latest uuid.UUID
	if err = e.Pool.QueryRow(ctx, "SELECT id FROM access_operations WHERE account_id=$1 ORDER BY sequence DESC LIMIT 1", account).Scan(&latest); err != nil || latest != newer {
		t.Fatalf("migration reordered confirmed source: %v latest=%s", err, latest)
	}
	third := uuid.New()
	if _, err = e.Pool.Exec(ctx, "INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at) VALUES($1,$2,'reset_traffic','applied','third','{}','{}','2026-10-02T00:00:00Z','2026-10-02T00:00:00Z')", third, account); err != nil {
		t.Fatal(err)
	}
	if err = e.Pool.QueryRow(ctx, "SELECT id FROM access_operations WHERE account_id=$1 ORDER BY sequence DESC LIMIT 1", account).Scan(&latest); err != nil || latest != third {
		t.Fatalf("new operation did not follow backfill: %v latest=%s", err, latest)
	}
}

func TestAccessProfileBackfillsOnlyConfirmedHistory(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	accessAccount, trialAccount, unknownAccount := uuid.New(), uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{accessAccount, trialAccount, unknownAccount} {
		if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,assigned_panel_id)
			VALUES($1,$2,'ru','fixture',now(),$3,$4,$5,'1','1','panel')`, id, "profile-backfill-"+id.String()+"@example.test", uuid.New(), strings.ReplaceAll(id.String(), "-", "")[:16], "acct_"+id.String()); err != nil {
			t.Fatalf("account %d: %v", i, err)
		}
	}
	if _, err = e.Pool.Exec(ctx, `INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at)
		VALUES($1,$2,'reset_traffic','applied','earlier','{}','{"profile":"regular"}','2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
		($3,$2,'reset_traffic','applied','later','{}','{"profile":"euru"}','2026-10-02T00:00:00Z','2026-10-02T00:00:00Z')`, uuid.New(), accessAccount, uuid.New()); err != nil {
		t.Fatal(err)
	}
	request, operation := uuid.New(), uuid.New()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO trial_requests(id,account_id,status,comment,created_at,decided_at,operator_tg_id,operation_id)
		VALUES($1,$2,'approved','fixture',now(),now(),1,$3)`, request, trialAccount, operation); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO trial_operations(id,account_id,request_id,status,trial_enabled,period_days,traffic_gb,devices,panel_id,created_at,target)
		VALUES($1,$2,$3,'applied',true,7,100,1,'panel',now(),'{"profile":"regular"}')`, operation, trialAccount, request); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO trial_grants(account_id,request_id,operation_id,status,created_at,granted_at)
		VALUES($1,$2,$3,'granted',now(),now())`, trialAccount, request, operation); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   uuid.UUID
		want *string
	}{{accessAccount, ptrString("euru")}, {trialAccount, ptrString("regular")}, {unknownAccount, nil}} {
		var profile *string
		if err = e.Pool.QueryRow(ctx, "SELECT access_profile FROM accounts WHERE id=$1", tc.id).Scan(&profile); err != nil {
			t.Fatal(err)
		}
		if profile == nil && tc.want != nil || profile != nil && (tc.want == nil || *profile != *tc.want) {
			t.Fatalf("profile backfill mismatch: got %v want %v", profile, tc.want)
		}
	}
}

func ptrString(s string) *string { return &s }
