package bonuses_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func rewardsFixture(t *testing.T) (*testkit.Env, *app.Modules, *app.Config, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	e := testkit.Open(t)
	ctx := context.Background()
	root, inviter, buyer := account(t, e, nil), account(t, e, nil), account(t, e, nil)
	for _, pair := range [][2]uuid.UUID{{root, inviter}, {inviter, buyer}} {
		if _, err := e.Pool.Exec(ctx, `INSERT INTO referrals(id,referrer_account_id,referred_account_id,created_at) VALUES($1,$2,$3,now())`, uuid.New(), pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	order := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,created_at,expires_at)
 VALUES($1,$2,$3,'fixture','{}',100,'AC',now()-interval '1 minute',now()+interval '1 hour')`, order, buyer, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,created_at)
 VALUES($1,$2,now(),100,100,'643','card-incoming',false,false,now())`, "funded:"+order.String(), order); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE purchase_orders SET payment_status='paid',paid_at=now(),active=false,funding_operation_id=$2 WHERE id=$1`, order, "funded:"+order.String()); err != nil {
		t.Fatal(err)
	}
	queue, err := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	c := &app.Config{Accounts: accounts.Config{Now: e.Clock}, Bonuses: bonuses.RewardConfig{Enabled: true, LevelOneDays: 10, LevelTwoDays: 3}}
	return e, app.NewModules(e.Pool, e.Redis, queue, c), c, root, inviter, buyer, order
}

func recordRewards(e *testkit.Env, s *bonuses.Service, order uuid.UUID) error {
	ctx := context.Background()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = s.RecordPaidPurchaseTx(ctx, tx, order); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func TestPaidRewardsConcurrencySnapshotAndRestart(t *testing.T) {
	e, modules, cfg, root, inviter, buyer, order := rewardsFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- recordRewards(e, modules.Bonuses, order) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, fact := range []struct {
		account     uuid.UUID
		level, days int
	}{{inviter, 1, 10}, {root, 2, 3}} {
		var id uuid.UUID
		var days, jobs, audits int
		var key string
		err := e.Pool.QueryRow(ctx, `SELECT id,amount::integer,payment_id,
 (SELECT count(*) FROM river_job WHERE kind='referral_reward' AND args->>'reward_id'=referrer_rewards.id::text),
 (SELECT count(*) FROM audit_events WHERE account_id=referrer_rewards.account_id AND action='referral_reward_created')
 FROM referrer_rewards WHERE account_id=$1 AND source_order_id=$2 AND reward_level=$3`, fact.account, order, fact.level).Scan(&id, &days, &key, &jobs, &audits)
		if err != nil || id != uuid.NewSHA1(order, []byte(fact.account.String())) || days != fact.days || key != "order:"+order.String() || jobs != 1 || audits != 1 {
			t.Fatal("reward/job/audit identity or snapshot lost", err, days, jobs, audits)
		}
	}
	var count int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM referrer_rewards WHERE account_id=$1`, buyer).Scan(&count); err != nil || count != 0 {
		t.Fatal("self reward", err)
	}
	cfg.Bonuses.LevelOneDays, cfg.Bonuses.LevelTwoDays = 12, 5
	queue, _ := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
	restarted := app.NewModules(e.Pool, e.Redis, queue, cfg)
	if err := recordRewards(e, restarted.Bonuses, order); err != nil {
		t.Fatal(err)
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM referrer_rewards WHERE source_order_id=$1 AND amount IN (10,3)`, order).Scan(&count); err != nil || count != 2 {
		t.Fatal("restart/config change reissued or resnapshotted", err)
	}
	cfg.Bonuses.Enabled = false
	if err := recordRewards(e, restarted.Bonuses, order); err != nil {
		t.Fatal(err)
	}
	for _, a := range []uuid.UUID{root, inviter} {
		out, err := restarted.Bonuses.ReadReferrals(ctx, a, "https://cabinet.example.test")
		if err != nil || len(out.Levels) != 2 || out.Levels[0].GrantedDays != "0" || out.Levels[1].GrantedDays != "0" {
			t.Fatal("disabled switch changed retained pending history", err)
		}
	}
}

func TestRewardCreationAtomicityAndUnprovedMoney(t *testing.T) {
	e, modules, _, _, inviter, _, order := rewardsFixture(t)
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, `CREATE FUNCTION reject_reward_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='referral_reward_created' THEN RAISE EXCEPTION 'controlled audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_reward_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_reward_audit()`); err != nil {
		t.Fatal(err)
	}
	if err := recordRewards(e, modules.Bonuses, order); err == nil {
		t.Fatal("audit failure committed reward")
	}
	var facts, jobs int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM referrer_rewards),(SELECT count(*) FROM river_job WHERE kind='referral_reward')`).Scan(&facts, &jobs); err != nil || facts != 0 || jobs != 0 {
		t.Fatal("partial reward transaction", err, facts, jobs)
	}
	if _, err := e.Pool.Exec(ctx, `DROP TRIGGER reject_reward_audit ON audit_events`); err != nil {
		t.Fatal(err)
	}
	if err := recordRewards(e, modules.Bonuses, order); err != nil {
		t.Fatal(err)
	}
	if err := recordRewards(e, modules.Bonuses, uuid.New()); err != nil {
		t.Fatal(err)
	}
	legacy := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO referrer_rewards(id,account_id,reward_type,amount,payment_id,created_at) VALUES($1,$2,'MONEY',7,'legacy-money',now())`, legacy, inviter); err != nil {
		t.Fatal(err)
	}
	if err := modules.Bonuses.ProcessReward(ctx, legacy); err != nil {
		t.Fatal("historical money entered native delivery", err)
	}
	var marked bool
	if err := e.Pool.QueryRow(ctx, `SELECT rewarded_at IS NOT NULL FROM referrer_rewards WHERE id=$1`, legacy).Scan(&marked); err != nil || marked {
		t.Fatal("money marked given", err)
	}
	if err := modules.Bonuses.RecordPaidPurchaseTx(ctx, nil, order); err == nil {
		t.Fatal("nontransactional funding accepted")
	}
	// Retained source funding can become invalid; neither preparation nor the access guard may permit it.
	if _, err := e.Pool.Exec(ctx, `UPDATE purchase_receipts SET review_reason='provider_dispute' WHERE order_id=$1`, order); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewSHA1(order, []byte(inviter.String()))
	if err := modules.Bonuses.ProcessReward(ctx, id); err == nil || !errors.As(err, new(*river.JobSnoozeError)) {
		t.Fatal("unproved source prepared a grant", err)
	}
	var op *uuid.UUID
	if err := e.Pool.QueryRow(ctx, `SELECT access_operation_id FROM referrer_rewards WHERE id=$1`, id).Scan(&op); err != nil || op != nil {
		t.Fatal("unproved source attached native access", err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE referrer_rewards SET amount=11 WHERE id=$1`, id); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatal("days snapshot mutable", err)
	}
}
