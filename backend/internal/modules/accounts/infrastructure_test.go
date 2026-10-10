package accounts

import (
	"context"
	"testing"

	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func TestInfrastructureGrantRequiresExistingVerifiedOperator(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	s := New(e.Pool, e.Redis, nil, Config{Now: e.Clock})
	id := uuid.New()
	_, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
		VALUES($1,'infra-fixture@example.test','ru','fixture',now(),$2,'cccccccccccccccc',$3,'1','1')`, id, uuid.New(), "acct_"+id.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeInfrastructureRole(ctx, id, true); err == nil {
		t.Fatal("grant without operator role")
	}
	if err := s.ChangeOperatorRole(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeInfrastructureRole(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err := s.RequireInfrastructure(ctx, id); err != nil {
		t.Fatal(err)
	}
	ordinary := uuid.New()
	_, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id)
	 VALUES($1,'ordinary-infra@example.test','ru','fixture',now(),$2,'dddddddddddddddd',$3,'1','1',732)`, ordinary, uuid.New(), "acct_"+ordinary.String())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeOperatorRole(ctx, ordinary, true); err != nil {
		t.Fatal(err)
	}
	proof, _, err := s.ResolveTelegramContext(ctx, 732)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = s.RequireInfrastructureTx(proof, tx, id); err == nil {
		t.Fatal("ordinary telegram source borrowed infrastructure actor ID")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeOperatorRole(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RequireInfrastructure(ctx, id); err == nil {
		t.Fatal("revoked operator kept infrastructure privilege")
	}
}
