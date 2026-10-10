package accounts

import (
	"context"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func legacyUserFixture() LegacyUser {
	used := false
	groups := []string{"banned", "euru"}
	return LegacyUser{SourceID: 17, TgID: 70017, VPNID: "550e8400-e29b-41d4-a716-446655440017", SubID: "660e8400-e29b-41d4-a716-446655440017", FirstName: "Original", LanguageCode: "de", CreatedAt: time.Date(2020, 1, 2, 3, 4, 5, 123456000, time.UTC), IsTrialUsed: &used, InboundGroups: &groups}
}

func importUsers(t *testing.T, e *testkit.Env, users []LegacyUser) (int, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	n, err := ImportLegacyUsersTx(ctx, tx, users)
	if err != nil {
		return n, err
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return n, nil
}

func TestLegacyUsersExactReplayAndUnknownTrial(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	u := legacyUserFixture()
	n, err := importUsers(t, e, []LegacyUser{u})
	if err != nil || n != 1 {
		t.Fatalf("import: %d %v", n, err)
	}
	var id uuid.UUID
	var vpn, sub, key, locale, profile string
	var banned, exact bool
	err = e.Pool.QueryRow(ctx, `SELECT a.id,a.vpn_id::text,a.sub_id,a.panel_key,a.locale,a.access_profile,a.vpn_banned,p.source_snapshot=$1::jsonb FROM accounts a JOIN legacy_account_imports p ON p.account_id=a.id`, `{"source_id":17,"tg_id":70017,"vpn_id":"550e8400-e29b-41d4-a716-446655440017","sub_id":"660e8400-e29b-41d4-a716-446655440017","server_id":null,"first_name":"Original","last_name":null,"username":null,"language_code":"de","created_at":"2020-01-02T03:04:05.123456Z","is_trial_used":false,"inbound_groups":["banned","euru"],"source_invite_name":null}`).Scan(&id, &vpn, &sub, &key, &locale, &profile, &banned, &exact)
	if err != nil || !exact || vpn != u.VPNID || sub != u.SubID || key != "70017" || locale != "ru" || profile != "euru" || !banned {
		t.Fatalf("identity/snapshot: %v", err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state, err := LegacyTrialStateTx(ctx, tx, id)
	if err != nil || state != "unknown" {
		t.Fatalf("false source trial: %q %v", state, err)
	}
	account, source, err := LegacyAccountBySourceTgTx(ctx, tx, u.TgID)
	if err != nil || account != id || source != u.SourceID {
		t.Fatalf("source mapping: %v", err)
	}
	tx.Rollback(ctx)
	if _, err = e.Pool.Exec(ctx, `UPDATE accounts SET display_name='Later native edit' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	n, err = importUsers(t, e, []LegacyUser{u})
	if err != nil || n != 0 {
		t.Fatalf("replay: %d %v", n, err)
	}
	var name string
	if err = e.Pool.QueryRow(ctx, `SELECT display_name FROM accounts WHERE id=$1`, id).Scan(&name); err != nil || name != "Later native edit" {
		t.Fatalf("replay rewrote later state: %v", err)
	}
	u.FirstName = "Changed source"
	if _, err = importUsers(t, e, []LegacyUser{u}); err == nil {
		t.Fatal("changed source accepted")
	}
	u = legacyUserFixture()
	u.SourceID++
	u.TgID++
	u.VPNID = "550e8400-e29b-41d4-a716-446655440018"
	u.SubID = "660e8400-e29b-41d4-a716-446655440018"
	u.IsTrialUsed = nil
	if _, err = importUsers(t, e, []LegacyUser{u}); err != nil {
		t.Fatal("nullable trial source", err)
	}
	if err = e.Pool.QueryRow(ctx, `SELECT account_id FROM legacy_account_imports WHERE source_id=$1`, u.SourceID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	tx, err = e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state, err = LegacyTrialStateTx(ctx, tx, id)
	if err != nil || state != "unknown" {
		t.Fatalf("null source trial: %q %v", state, err)
	}
	tx.Rollback(ctx)
}

func TestLegacyUsersRollbackAndIdentityGuard(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	u := legacyUserFixture()
	bad := u
	bad.SourceID++
	bad.TgID++
	bad.VPNID = "550e8400-e29b-41d4-a716-446655440018"
	bad.SubID = u.SubID
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ImportLegacyUsersTx(ctx, tx, []LegacyUser{u, bad})
	if err == nil {
		t.Fatal("duplicate VPN/sub identity accepted")
	}
	tx.Rollback(ctx)
	var n int
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM accounts`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial account import: %d %v", n, err)
	}
	u.VPNID = "550E8400-E29B-41D4-A716-446655440017"
	if _, err = importUsers(t, e, []LegacyUser{u}); err == nil {
		t.Fatal("noncanonical UUID accepted")
	}
}

func TestLegacyUsersEnrichOnlyMatchingAccount(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	u := legacyUserFixture()
	u.SubID = "abcdefghijklmnop"
	id := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,telegram_id,display_name,locale,vpn_id,sub_id,panel_key,had_subscription) VALUES($1,'telegram',$2,'Current name','en',$3,$4,$5,true)`, id, u.TgID, u.VPNID, u.SubID, "70017"); err != nil {
		t.Fatal(err)
	}
	n, err := importUsers(t, e, []LegacyUser{u})
	if err != nil || n != 1 {
		t.Fatalf("matching account: %d %v", n, err)
	}
	var mapped uuid.UUID
	var name string
	var had bool
	if err = e.Pool.QueryRow(ctx, `SELECT p.account_id,a.display_name,a.had_subscription FROM legacy_account_imports p JOIN accounts a ON a.id=p.account_id`).Scan(&mapped, &name, &had); err != nil || mapped != id || name != "Current name" || !had {
		t.Fatalf("enrichment overwrote current state: %v", err)
	}
	conflict := u
	conflict.SourceID++
	conflict.TgID++
	conflict.VPNID = "550e8400-e29b-41d4-a716-446655440018"
	conflict.SubID = "abcdefghijklmnor"
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,kind,telegram_id,display_name,locale,vpn_id,sub_id,panel_key) VALUES($1,'telegram',$2,'Current name','en',$3,$4,'different-key')`, uuid.New(), conflict.TgID, conflict.VPNID, conflict.SubID); err != nil {
		t.Fatal(err)
	}
	if _, err = importUsers(t, e, []LegacyUser{conflict}); err == nil {
		t.Fatal("existing mismatched panel key accepted")
	}
}

func TestLegacyUsersRejectAmbiguousGroupsWithoutPartialState(t *testing.T) {
	soleBan := []string{"banned"}
	validBan := legacyUserFixture()
	validBan.InboundGroups = &soleBan
	profile, banned := legacyProfile(validBan.InboundGroups)
	if _, err := validateLegacyUser(validBan); err != nil || profile != nil || !banned {
		t.Fatalf("sole ban lost: %v", err)
	}
	for _, groups := range [][]string{{"regular", "banned", "banned"}, {"banned", "new-group"}, {"regular", "euru"}} {
		u := legacyUserFixture()
		u.InboundGroups = &groups
		if _, err := validateLegacyUser(u); err == nil {
			t.Fatalf("ambiguous group accepted before DB: %v", groups)
		}
		t.Run(groups[0], func(t *testing.T) {
			e := testkit.Open(t)
			ctx := context.Background()
			valid := legacyUserFixture()
			bad := valid
			bad.SourceID++
			bad.TgID++
			bad.VPNID = "550e8400-e29b-41d4-a716-446655440018"
			bad.SubID = "660e8400-e29b-41d4-a716-446655440018"
			bad.InboundGroups = &groups
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ImportLegacyUsersTx(ctx, tx, []LegacyUser{valid, bad}); err == nil {
				t.Fatal("invalid groups accepted")
			}
			tx.Rollback(ctx)
			var accounts, sources, grants int
			if err = e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts),(SELECT count(*) FROM legacy_account_imports),(SELECT count(*) FROM trial_grants)`).Scan(&accounts, &sources, &grants); err != nil || accounts != 0 || sources != 0 || grants != 0 {
				t.Fatalf("partial source or grant: %d %d %d %v", accounts, sources, grants, err)
			}
		})
	}
}
