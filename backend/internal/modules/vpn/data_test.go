package vpn

import (
	"context"
	"errors"
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestAccessOwnerKeepsPhysicalSession(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	authority := accounts.New(e.Pool, e.Redis, nil, accounts.Config{}, nil)
	svc := New(e.Pool, authority, nil, func() Settings { return Settings{} }, e.Clock, nil, PurchaseHooks{})
	account := uuid.New()
	owner, err := svc.OpenAccessOwner(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Release()
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pid int32
	if tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid) != nil || owner.TryLock(ctx) != nil {
		t.Fatal("first owner unavailable")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var other bool
	if e.Pool.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('account-access:'||$1::text,0))`, account).Scan(&other) != nil || other {
		t.Fatal("ownership did not survive commit")
	}
	var killed bool
	if e.Pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&killed) != nil || !killed {
		t.Fatal("cannot terminate own session")
	}
	if tx, err = owner.Begin(ctx); err == nil {
		tx.Rollback(ctx)
		t.Fatal("replacement session accepted after owner loss")
	}
}

func TestTrialOutcomeFailureRollsBack(t *testing.T) {
	for _, mode := range []string{"missing", "failed"} {
		t.Run(mode, func(t *testing.T) {
			e := testkit.Open(t)
			ctx := context.Background()
			account, request, operation := uuid.New(), uuid.New(), uuid.New()
			if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,vpn_id,sub_id,panel_key,kind,locale,email_key,password_hash,verified_at,terms_version,privacy_version) VALUES($1,$2,'0123456789abcdef','fixture-key','web','ru','fixture@example.test','fixture',$3,'t1','p1')`, account, uuid.New(), e.Now); err != nil {
				t.Fatal(err)
			}
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, `INSERT INTO trial_requests(id,account_id,status,comment,created_at,decided_at,operator_tg_id,operation_id) VALUES($1,$2,'approved','',$3,$3,101,$4)`, request, account, e.Now, operation); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO trial_operations(id,account_id,request_id,status,trial_enabled,period_days,traffic_gb,devices,panel_id,created_at) VALUES($1,$2,$3,'pending',true,3,15,1,'dedicated',$4)`, operation, account, request, e.Now); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO trial_grants(account_id,request_id,operation_id,status,created_at) VALUES($1,$2,$3,'reserved',$4)`, account, request, operation, e.Now); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			authority := accounts.New(e.Pool, e.Redis, nil, accounts.Config{}, nil)
			var outcome func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, string) error
			if mode == "failed" {
				outcome = func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, string) error {
					return errors.New("fixture outcome failure")
				}
			}
			svc := New(e.Pool, authority, nil, func() Settings { return Settings{PanelID: "other"} }, e.Clock, outcome, PurchaseHooks{})
			if err := svc.Provision(ctx, operation); err == nil {
				t.Fatal("unsaved outcome accepted")
			}
			var state, grant string
			if e.Pool.QueryRow(ctx, `SELECT status FROM trial_operations WHERE id=$1`, operation).Scan(&state) != nil || state != "provisioning" {
				t.Fatal("VPN review committed without subscription outcome")
			}
			if e.Pool.QueryRow(ctx, `SELECT status FROM trial_grants WHERE operation_id=$1`, operation).Scan(&grant) != nil || grant != "reserved" {
				t.Fatal("grant changed without successful outcome")
			}
		})
	}
}
