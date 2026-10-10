package payments

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func TestLegacyStarsValidation(t *testing.T) {
	f := false
	charge := ""
	zero := int64(0)
	users := []LegacyStarsUser{{SourceLegacyUserID: 1, SourceTgID: 2, StarsChargeID: &charge, IsStarsAutoRenew: &f, StarsExpiresAt: &zero}}
	if err := validateLegacyStars(users); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]LegacyStarsUser{
		"nil section":    nil,
		"duplicate user": {users[0], users[0]},
		"long charge":    {{SourceLegacyUserID: 1, SourceTgID: 2, StarsChargeID: strPtr65(), IsStarsAutoRenew: &f}},
	} {
		t.Run(name, func(t *testing.T) {
			if validateLegacyStars(bad) == nil {
				t.Fatal("invalid Stars snapshot accepted")
			}
		})
	}
	unknown := []LegacyStarsUser{{SourceLegacyUserID: 1, SourceTgID: 2}}
	if err := validateLegacyStars(unknown); err != nil {
		t.Fatal("NULL source recurrence was rejected", err)
	}
	var decoded LegacyStarsUser
	if json.Unmarshal([]byte(`{"source_legacy_user_id":1,"source_tg_id":2}`), &decoded) == nil {
		t.Fatal("missing recurrence field accepted")
	}
	if err := json.Unmarshal([]byte(`{"source_legacy_user_id":1,"source_tg_id":2,"is_stars_auto_renew":null}`), &decoded); err != nil || decoded.IsStarsAutoRenew != nil {
		t.Fatal("explicit unknown recurrence lost", err)
	}
}

func strPtr65() *string {
	s := "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	return &s
}

func TestLegacyStarsImportReplayAndConflict(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	id := uuid.New()
	_, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id,legacy_user_id)
	 VALUES($1,$2,'en','fixture',now(),$3,$4,$5,'1','1',101,1)`, id, id.String()+"@example.test", uuid.New(), strings.ReplaceAll(uuid.NewString(), "-", "")[:16], "acct_"+id.String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Pool.Exec(ctx, `INSERT INTO legacy_account_imports(source_id,account_id,source_tg_id,source_snapshot) VALUES(1,$1,101,'{}'::jsonb)`, id)
	if err != nil {
		t.Fatal(err)
	}
	unknownID := uuid.New()
	_, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id,legacy_user_id)
	 VALUES($1,$2,'en','fixture',now(),$3,$4,$5,'1','1',102,2)`, unknownID, unknownID.String()+"@example.test", uuid.New(), strings.ReplaceAll(uuid.NewString(), "-", "")[:16], "acct_"+unknownID.String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Pool.Exec(ctx, `INSERT INTO legacy_account_imports(source_id,account_id,source_tg_id,source_snapshot) VALUES(2,$1,102,'{}'::jsonb)`, unknownID)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{authority: accounts.New(e.Pool, e.Redis, nil, accounts.Config{Now: e.Clock})}
	on := true
	charge := ""
	expires := int64(0)
	users := []LegacyStarsUser{{SourceLegacyUserID: 1, SourceTgID: 101, StarsChargeID: &charge, IsStarsAutoRenew: &on, StarsExpiresAt: &expires}, {SourceLegacyUserID: 2, SourceTgID: 102}}
	importTx := func(in []LegacyStarsUser) (int, error) {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		n, err := s.ImportLegacyStarsTx(ctx, tx, in)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return n, err
	}
	if n, err := importTx(users); err != nil || n != 2 {
		t.Fatalf("first import: %d %v", n, err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=201 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if n, err := importTx(users); err != nil || n != 0 {
		t.Fatalf("replay after alias move: %d %v", n, err)
	}
	var savedCharge *string
	var savedExpires *int64
	var savedRenew bool
	if err := e.Pool.QueryRow(ctx, `SELECT stars_charge_id,is_stars_auto_renew,stars_expires_at FROM legacy_stars_imports WHERE source_legacy_user_id=1`).Scan(&savedCharge, &savedRenew, &savedExpires); err != nil || savedCharge == nil || *savedCharge != "" || !savedRenew || savedExpires == nil || *savedExpires != 0 {
		t.Fatalf("NULL/zero/empty source lost: %v %v %v %v", savedCharge, savedRenew, savedExpires, err)
	}
	var unknownRenew *bool
	if err := e.Pool.QueryRow(ctx, `SELECT is_stars_auto_renew FROM legacy_stars_imports WHERE source_legacy_user_id=2`).Scan(&unknownRenew); err != nil || unknownRenew != nil {
		t.Fatalf("unknown source recurrence was guessed: %v %v", unknownRenew, err)
	}
	modified := append([]LegacyStarsUser(nil), users...)
	modified[0].StarsChargeID = nil
	if _, err := importTx(modified); err == nil {
		t.Fatal("changed Stars source accepted")
	}
	var orders int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM purchase_orders`).Scan(&orders); err != nil || orders != 0 {
		t.Fatalf("Stars snapshot created native funding: %d %v", orders, err)
	}
}
