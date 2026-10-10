package bonuses_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func account(t *testing.T, e *testkit.Env, source *string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := e.Pool.Exec(context.Background(), `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,registration_source_code)
	 VALUES($1,$2,'en','fixture',now(),$3,$4,$5,'1','1',$6)`, id, id.String()+"@example.test", uuid.New(), strings.ReplaceAll(uuid.NewString(), "-", "")[:16], "acct_"+id.String(), source)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func service(e *testkit.Env) *bonuses.Service {
	return bonuses.New(e.Pool, accounts.New(e.Pool, e.Redis, nil, accounts.Config{Now: e.Clock}), e.Clock)
}

func codeFromURL(t *testing.T, webURL string) string {
	t.Helper()
	parts := strings.Split(webURL, "?invite=")
	if len(parts) != 2 || !strings.HasPrefix(parts[1], "r_") || len(parts[1]) != 34 {
		t.Fatalf("invalid personal link: %q", webURL)
	}
	return parts[1]
}

func TestReadReferralsConcurrentStableLinkAndAuthority(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	s := service(e)
	owner, other := account(t, e, nil), account(t, e, nil)
	const workers = 8
	urls := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.ReadReferrals(ctx, owner, "https://old.example.test")
			urls <- result.WebURL
			errs <- err
		}()
	}
	wg.Wait()
	close(urls)
	close(errs)
	var first string
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for u := range urls {
		if first == "" {
			first = u
		} else if u != first {
			t.Fatalf("concurrent reads minted different links: %q, %q", first, u)
		}
	}
	code := codeFromURL(t, first)
	current, err := s.ReadReferrals(ctx, owner, "https://current.example.test")
	if err != nil || current.WebURL != "https://current.example.test/register?invite="+code {
		t.Fatalf("current origin or stable code lost: %+v, %v", current, err)
	}
	var links, audits int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM referral_links WHERE account_id=$1),
	 (SELECT count(*) FROM audit_events WHERE account_id=$1 AND action='referral_link_created')`, owner).Scan(&links, &audits); err != nil || links != 1 || audits != 1 {
		t.Fatalf("link/audit duplicated or absent: %d/%d, %v", links, audits, err)
	}
	foreign, err := s.ReadReferrals(ctx, other, "https://current.example.test")
	if err != nil || codeFromURL(t, foreign.WebURL) == code {
		t.Fatalf("other account received owner's code: %+v, %v", foreign, err)
	}
	if _, err := s.ReadReferrals(ctx, uuid.New(), "https://current.example.test"); err == nil {
		t.Fatal("missing actor received data")
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadReferrals(ctx, owner, "https://current.example.test"); err == nil {
		t.Fatal("restricted actor received data")
	}
	if _, err := e.Pool.Exec(ctx, `CREATE FUNCTION reject_link_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='referral_link_created' THEN RAISE EXCEPTION 'controlled audit failure'; END IF; RETURN NEW; END $$;
	 CREATE TRIGGER reject_link_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_link_audit()`); err != nil {
		t.Fatal(err)
	}
	failed := account(t, e, nil)
	if _, err := s.ReadReferrals(ctx, failed, "https://current.example.test"); err == nil {
		t.Fatal("link audit failure did not fail read")
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM referral_links WHERE account_id=$1`, failed).Scan(&links); err != nil || links != 0 {
		t.Fatalf("audit failure left personal link: %d, %v", links, err)
	}
}

func TestReadReferralsActualTwoLevelFacts(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	s := service(e)
	root, child, grandchild := account(t, e, nil), account(t, e, nil), account(t, e, nil)
	for _, pair := range [][2]uuid.UUID{{root, child}, {child, grandchild}} {
		if _, err := e.Pool.Exec(ctx, `INSERT INTO referrals(id,referrer_account_id,referred_account_id,created_at) VALUES($1,$2,$3,now())`, uuid.New(), pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		kind, amount, payment string
		level                 any
		rewarded              bool
	}{
		{"DAYS", "3", "one-granted", 1, true},
		{"DAYS", "7", "one-pending", 1, false},
		{"MONEY", "999999999999999999.123456789012345678", "one-money", 1, false},
		{"DAYS", "11", "two-granted", 2, true},
		{"DAYS", "13", "two-pending", 2, false},
		{"MONEY", "0.000000000000000001", "two-money", 2, true},
		{"MONEY", "1", "legacy-unknown", nil, false},
	} {
		if _, err := e.Pool.Exec(ctx, `INSERT INTO referrer_rewards(id,account_id,reward_type,reward_level,amount,payment_id,created_at,rewarded_at)
		 VALUES($1,$2,$3,$4,$5,$6,now(),CASE WHEN $7::boolean THEN now() ELSE NULL END)`, uuid.New(), root, row.kind, row.level, row.amount, row.payment, row.rewarded); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ReadReferrals(ctx, root, "https://cabinet.example.test")
	if err != nil || got.Version != bonuses.ReferralsVersion || got.UnclassifiedRecords != 1 || len(got.Levels) != 2 {
		t.Fatalf("aggregate unavailable or shape wrong: %+v, %v", got, err)
	}
	for i, want := range []bonuses.ReferralLevel{
		{Level: 1, Invited: 1, GrantedDays: "3", PendingDays: "7", GrantedRewards: 1, PendingRewards: 1, MoneyRecords: 1, PendingMoneyRecords: 1},
		{Level: 2, Invited: 1, GrantedDays: "11", PendingDays: "13", GrantedRewards: 1, PendingRewards: 1, MoneyRecords: 1, PendingMoneyRecords: 0},
	} {
		if got.Levels[i] != want {
			t.Fatalf("level %d: got %+v, want %+v", i+1, got.Levels[i], want)
		}
	}
}

func TestCaptureRegistrationOriginalSourceAndAtomicAudit(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	s := service(e)
	inviter := account(t, e, nil)
	link, err := s.ReadReferrals(ctx, inviter, "https://cabinet.example.test")
	if err != nil {
		t.Fatal(err)
	}
	code := codeFromURL(t, link.WebURL)
	capture := func(id uuid.UUID, channel, source string) error {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if err := s.CaptureRegistrationTx(ctx, tx, id, channel, source); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	matched := account(t, e, &code)
	if err := capture(matched, "telegram", code); err != nil {
		t.Fatal(err)
	}
	var relationCount int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM referrals WHERE referred_account_id=$1`, matched).Scan(&relationCount); err != nil || relationCount != 0 {
		t.Fatalf("wrong original channel captured referral: %d, %v", relationCount, err)
	}
	if err := capture(matched, "web", "other"); err != nil {
		t.Fatal(err)
	}
	if err := capture(matched, "web", code); err != nil {
		t.Fatal(err)
	}
	if err := capture(matched, "web", code); err != nil {
		t.Fatal(err)
	}
	unknown := "r_" + strings.Repeat("0", 32)
	for _, source := range []string{unknown, "not-a-referral"} {
		id := account(t, e, &source)
		if err := capture(id, "web", source); err != nil {
			t.Fatal(err)
		}
	}
	selfCode := "r_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	self := account(t, e, &selfCode)
	if _, err := e.Pool.Exec(ctx, `INSERT INTO referral_links(account_id,code,created_at) VALUES($1,$2,now())`, self, selfCode); err != nil {
		t.Fatal(err)
	}
	if err := capture(self, "web", selfCode); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=991 WHERE id=$1`, inviter); err != nil {
		t.Fatal(err)
	}
	activeSource := "991"
	active := account(t, e, &activeSource)
	if err := capture(active, "web", activeSource); err != nil {
		t.Fatal(err)
	}
	retiredOwner := account(t, e, nil)
	if _, err := e.Pool.Exec(ctx, `INSERT INTO telegram_identity_reservations(telegram_id,account_id,retired_at) VALUES(992,$1,now())`, retiredOwner); err != nil {
		t.Fatal(err)
	}
	retiredSource := "992"
	retired := account(t, e, &retiredSource)
	if err := capture(retired, "web", retiredSource); err != nil {
		t.Fatal(err)
	}
	telegramSource := code
	telegramOriginal := account(t, e, &telegramSource)
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET original_kind='telegram' WHERE id=$1`, telegramOriginal); err != nil {
		t.Fatal(err)
	}
	if err := capture(telegramOriginal, "web", code); err != nil {
		t.Fatal(err)
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM referrals WHERE referred_account_id=$1`, telegramOriginal).Scan(&relationCount); err != nil || relationCount != 0 {
		t.Fatalf("linked original Telegram account captured via web: %d, %v", relationCount, err)
	}
	if err := capture(telegramOriginal, "telegram", code); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]uuid.UUID{{matched, inviter}, {active, inviter}, {retired, retiredOwner}, {telegramOriginal, inviter}} {
		var owner uuid.UUID
		if err := e.Pool.QueryRow(ctx, `SELECT referrer_account_id FROM referrals WHERE referred_account_id=$1`, pair[0]).Scan(&owner); err != nil || owner != pair[1] {
			t.Fatalf("wrong inviter for %s: %s, %v", pair[0], owner, err)
		}
	}
	var relations, audits int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM referrals),
	 (SELECT count(*) FROM audit_events WHERE action='referral_registered')`).Scan(&relations, &audits); err != nil || relations != 4 || audits != 4 {
		t.Fatalf("source mismatch, unknown, self or repeat changed graph/audit: %d/%d, %v", relations, audits, err)
	}
	if _, err := e.Pool.Exec(ctx, `CREATE FUNCTION reject_referral_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='referral_registered' THEN RAISE EXCEPTION 'controlled audit failure'; END IF; RETURN NEW; END $$;
	 CREATE TRIGGER reject_referral_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_referral_audit()`); err != nil {
		t.Fatal(err)
	}
	failed := account(t, e, &code)
	if err := capture(failed, "web", code); err == nil {
		t.Fatal("audit failure did not fail capture")
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM referrals WHERE referred_account_id=$1`, failed).Scan(&relations); err != nil || relations != 0 {
		t.Fatalf("audit failure left relationship: %d, %v", relations, err)
	}
}
