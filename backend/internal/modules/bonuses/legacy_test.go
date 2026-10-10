package bonuses

import (
	"context"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func TestLegacyBonusValidation(t *testing.T) {
	when := time.Date(2026, 10, 1, 1, 2, 3, 123456000, time.UTC)
	used := true
	base := func() LegacyPackage {
		return LegacyPackage{Version: 1, Source: "sqlite", Promocodes: []LegacyPromocode{{SourceID: 1, Code: "p", Duration: 1, IsActivated: &used, CreatedAt: when}}, Referrals: []LegacyReferral{{SourceID: 1, ReferredTgID: 2, ReferrerTgID: 1, CreatedAt: when}}, Rewards: []LegacyReward{{SourceID: 1, UserTgID: 1, RewardType: "MONEY", Amount: "99999999999999999999.123456789012345678", PaymentID: "payment", CreatedAt: when}}}
	}
	if err := validateLegacyPackage(base()); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*LegacyPackage){
		"missing arrays":   func(p *LegacyPackage) { p.Referrals = nil },
		"missing boolean":  func(p *LegacyPackage) { p.Promocodes[0].IsActivated = nil },
		"invalid actor":    func(p *LegacyPackage) { p.Promocodes[0].ActivatedBy = new(int64) },
		"duplicate source": func(p *LegacyPackage) { p.Rewards = append(p.Rewards, p.Rewards[0]) },
		"fractional days":  func(p *LegacyPackage) { p.Rewards[0].RewardType, p.Rewards[0].Amount = "DAYS", "1.5" },
		"numeric overflow": func(p *LegacyPackage) { p.Rewards[0].Amount = "100000000000000000000" },
		"numeric scale":    func(p *LegacyPackage) { p.Rewards[0].Amount = "0.0000000000000000001" },
		"nanosecond loss":  func(p *LegacyPackage) { p.Rewards[0].CreatedAt = when.Add(time.Nanosecond) },
		"self link":        func(p *LegacyPackage) { p.Referrals[0].ReferrerTgID = 2 },
		"cycle": func(p *LegacyPackage) {
			p.Referrals = append(p.Referrals, LegacyReferral{SourceID: 2, ReferredTgID: 1, ReferrerTgID: 2, CreatedAt: when})
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := base()
			change(&p)
			if validateLegacyPackage(p) == nil {
				t.Fatal("invalid historical source accepted")
			}
		})
	}
}

func legacyBonusAccount(t *testing.T, e *testkit.Env, sourceID, tgID int64) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := e.Pool.Exec(context.Background(), `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id,legacy_user_id)
	 VALUES($1,$2,'en','fixture',now(),$3,$4,$5,'1','1',$6,$7)`, id, id.String()+"@example.test", uuid.New(), strings.ReplaceAll(uuid.NewString(), "-", "")[:16], "acct_"+id.String(), tgID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Pool.Exec(context.Background(), `INSERT INTO legacy_account_imports(source_id,account_id,source_tg_id,source_snapshot) VALUES($1,$2,$3,'{}'::jsonb)`, sourceID, id, tgID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLegacyBonusImportReplayAndConflict(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	a := legacyBonusAccount(t, e, 1, 101)
	_ = legacyBonusAccount(t, e, 2, 102)
	service := &Service{authority: accounts.New(e.Pool, e.Redis, nil, accounts.Config{Now: e.Clock}), now: e.Clock}
	activated := true
	when := time.Date(2026, 9, 1, 2, 3, 4, 123456000, time.UTC)
	p := LegacyPackage{Version: 1, Source: "sqlite", Promocodes: []LegacyPromocode{{SourceID: 1, Code: "old-code", Duration: 3, IsActivated: &activated, CreatedAt: when}}, Referrals: []LegacyReferral{{SourceID: 1, ReferredTgID: 102, ReferrerTgID: 101, CreatedAt: when}}, Rewards: []LegacyReward{{SourceID: 1, UserTgID: 101, RewardType: "MONEY", Amount: "12.340000000000000000", PaymentID: "old-payment", CreatedAt: when}}}
	importTx := func(in LegacyPackage) (int, error) {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		n, err := service.ImportLegacyTx(ctx, tx, in)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return n, err
	}
	if n, err := importTx(p); err != nil || n != 3 {
		t.Fatalf("first import: %d, %v", n, err)
	}
	// The source ledger is immutable, while current reward delivery may advance.
	if _, err := e.Pool.Exec(ctx, `UPDATE referrer_rewards SET rewarded_at=$1 WHERE legacy_reward_id=1`, when.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=201 WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	if n, err := importTx(p); err != nil || n != 0 {
		t.Fatalf("source replay after alias move: %d, %v", n, err)
	}
	var money, source string
	var rewarded time.Time
	if err := e.Pool.QueryRow(ctx, `SELECT amount::text,source_snapshot->>'source',rewarded_at FROM referrer_rewards JOIN legacy_bonus_imports ON entity_id=id WHERE kind='reward' AND source_id=1`).Scan(&money, &source, &rewarded); err != nil || money != "12.340000000000000000" || source != "sqlite" || !rewarded.Equal(when.Add(time.Hour)) {
		t.Fatalf("reward lost original or current state: %q %q %v %v", money, source, rewarded, err)
	}
	p.Rewards[0].Amount = "13"
	p.Promocodes = append(p.Promocodes, LegacyPromocode{SourceID: 2, Code: "rollback-code", Duration: 2, IsActivated: &activated, CreatedAt: when})
	if _, err := importTx(p); err == nil {
		t.Fatal("changed source snapshot accepted")
	}
	var count int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM legacy_bonus_imports`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("conflicting package changed ledger: %d, %v", count, err)
	}
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM promocodes WHERE code='rollback-code'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("late conflict left a promocode: %d, %v", count, err)
	}
}
