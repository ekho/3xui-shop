package httpapi

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"github.com/google/uuid"
)

func referredTrialAccount(t *testing.T, s *regressionFixture, telegramID int64, source string) accounts.Snapshot {
	t.Helper()
	r, _, err := s.accounts.StartTelegramSession(context.Background(), accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: telegramID, DisplayName: "Owned referral trial", Locale: "en"}, StartParam: source, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	return r.Account
}

// Catches replacing the full trial with additive days, a second benefit under
// different keys, losing its configured snapshot, or inventing purchase rewards.
func TestReferredTelegramTrialReservation(t *testing.T) {
	s, e := fixture(t)
	s.cfg.ReferredTrial = bonuses.ReferredTrialConfig{Enabled: true, PeriodDays: 7}
	ctx := context.Background()
	telegramTrialAccount(t, s, 701)
	a := referredTrialAccount(t, s, 702, "701")
	var wg sync.WaitGroup
	type result struct {
		r   subscriptions.TrialRequest
		key uuid.UUID
		err error
	}
	results := make(chan result, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := uuid.New()
			r, _, err := s.subscriptions.ActivateTelegramTrial(ctx, a.ID, key)
			results <- result{r, key, err}
		}()
	}
	wg.Wait()
	close(results)
	var winner result
	created := 0
	for r := range results {
		if r.err == nil {
			winner = r
			created++
		} else if status(subscriptionError(r.err)) != 409 {
			t.Fatal(r.err)
		}
	}
	if created != 1 || count(t, e, "trial_grants") != 1 || count(t, e, "trial_operations") != 1 {
		t.Fatal("duplicate trial benefit")
	}
	var days, traffic, devices, reserved, jobs int64
	var pending bool
	err := e.Pool.QueryRow(ctx, `SELECT o.period_days,o.traffic_gb,o.devices,coalesce(r.referred_bonus_days,0),r.referred_rewarded_at IS NULL,(SELECT count(*) FROM river_job WHERE kind='trial_provision') FROM trial_operations o JOIN referrals r ON r.referred_account_id=o.account_id WHERE o.id=$1`, winner.r.OperationId).Scan(&days, &traffic, &devices, &reserved, &pending, &jobs)
	if err != nil || days != 7 || reserved != 7 || traffic != 15 || devices != 1 || !pending || jobs != 1 {
		t.Fatalf("referral trial snapshot: days=%d reserved=%d pending=%v err=%v", days, reserved, pending, err)
	}
	s.cfg.ReferredTrial.Enabled = false
	s.cfg.ReferredTrial.PeriodDays = 12
	r, fresh, err := s.subscriptions.ActivateTelegramTrial(ctx, a.ID, winner.key)
	if err != nil || fresh || r.RequestId != winner.r.RequestId || *r.OperationId != *winner.r.OperationId {
		t.Fatal("replay lost trial snapshot", err)
	}
	if count(t, e, "referrer_rewards") != 0 || count(t, e, "purchase_orders") != 0 {
		t.Fatal("trial invented paid rewards")
	}
}

// Catches flags/source/legacy history making a fresh benefit out of a used or
// unrelated referral, and ensures zero limits remain unlimited, as before.
func TestReferredTelegramTrialPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, source, change string
		enabled              bool
		want, days           int
	}{
		{"enabled", "701", "", true, 0, 7},
		{"disabled", "701", "", false, 0, 3},
		{"unknown", "unknown-owned-source", "", true, 0, 3},
		{"self", "702", "", true, 0, 3},
		{"consumed", "701", "UPDATE referrals SET referred_bonus_days=7,referred_rewarded_at=now() WHERE referred_account_id=$1", true, 409, 0},
		{"consumed_disabled", "701", "UPDATE referrals SET referred_bonus_days=7,referred_rewarded_at=now() WHERE referred_account_id=$1", false, 409, 0},
		{"legacy_referral", "701", "", true, 0, 3},
		{"original_tg_credentials", "701", "UPDATE accounts SET original_kind='telegram',kind='web',email_key='owned@example.test',password_hash='owned-fixture',verified_at=now() WHERE id=$1", true, 0, 7},
		{"trial_disabled", "701", "", true, 403, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e := fixture(t)
			ctx := context.Background()
			s.cfg.ReferredTrial = bonuses.ReferredTrialConfig{Enabled: tc.enabled, PeriodDays: 7}
			telegramTrialAccount(t, s, 701)
			a := referredTrialAccount(t, s, 702, tc.source)
			if tc.change != "" {
				if _, err := e.Pool.Exec(ctx, tc.change, a.ID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "legacy_referral" {
				// Imported history is inserted intact; relationship IDs cannot be changed later.
				other := referredTrialAccount(t, s, 703, "")
				if _, err := e.Pool.Exec(ctx, `INSERT INTO referrals(id,legacy_referral_id,referrer_account_id,referred_account_id,created_at) VALUES($1,1,$2,$3,now())`, uuid.New(), a.ID, other.ID); err != nil {
					t.Fatal(err)
				}
				a = other
			}
			if tc.name == "trial_disabled" {
				s.cfg.Subscriptions.TrialEnabled = false
			}
			s.cfg.Subscriptions.TrialTrafficGB, s.cfg.Subscriptions.TrialDevices = 0, 0
			r, _, err := s.subscriptions.ActivateTelegramTrial(ctx, a.ID, uuid.New())
			if status(subscriptionError(err)) != tc.want {
				t.Fatalf("want %d, got %v", tc.want, err)
			}
			if tc.want != 0 {
				if count(t, e, "trial_grants") != 0 {
					t.Fatal("denied grant persisted")
				}
				return
			}
			var days, traffic, devices int64
			if err = e.Pool.QueryRow(ctx, `SELECT period_days,traffic_gb,devices FROM trial_operations WHERE id=$1`, r.OperationId).Scan(&days, &traffic, &devices); err != nil || days != int64(tc.days) || traffic != 0 || devices != 0 {
				t.Fatal("wrong policy or unlimited limits", err, days)
			}
		})
	}
}

// Catches committing a referral reservation after queue/audit insertion fails.
func TestReferredTelegramTrialRollback(t *testing.T) {
	for _, target := range []struct{ name, table, condition string }{
		{"queue", "river_job", "NEW.kind='trial_provision'"},
		{"audit", "audit_events", "NEW.action='referral_trial_reserved'"},
	} {
		t.Run(target.name, func(t *testing.T) {
			s, e := fixture(t)
			ctx := context.Background()
			s.cfg.ReferredTrial = bonuses.ReferredTrialConfig{Enabled: true, PeriodDays: 7}
			telegramTrialAccount(t, s, 701)
			a := referredTrialAccount(t, s, 702, "701")
			// All dynamic SQL parts are fixed test-owned literals above.
			sql := fmt.Sprintf(`CREATE FUNCTION fail_referred_trial() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF %s THEN RAISE EXCEPTION 'owned fixture failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_referred_trial BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION fail_referred_trial()`, target.condition, target.table)
			if _, err := e.Pool.Exec(ctx, sql); err != nil {
				t.Fatal(err)
			}
			key := uuid.New()
			_, _, err := s.subscriptions.ActivateTelegramTrial(ctx, a.ID, key)
			if status(subscriptionError(err)) != 503 || count(t, e, "trial_requests") != 0 || count(t, e, "trial_grants") != 0 || count(t, e, "trial_operations") != 0 {
				t.Fatal("partial referral trial committed", err)
			}
			var clean bool
			if err = e.Pool.QueryRow(ctx, `SELECT referred_bonus_days IS NULL AND referred_rewarded_at IS NULL FROM referrals WHERE referred_account_id=$1`, a.ID).Scan(&clean); err != nil || !clean {
				t.Fatal("failed trial consumed referral", err)
			}
			if _, err = e.Pool.Exec(ctx, fmt.Sprintf("DROP TRIGGER fail_referred_trial ON %s", target.table)); err != nil {
				t.Fatal(err)
			}
			if _, created, err := s.subscriptions.ActivateTelegramTrial(ctx, a.ID, key); err != nil || !created {
				t.Fatal("failed key consumed", err)
			}
		})
	}
}

// Catches a selected referral duration bypassing trial limit validation or
// leaving a consumed reservation when any captured limit is unsafe.
func TestReferredTelegramTrialInvalidLimits(t *testing.T) {
	for _, tc := range []struct{ period, traffic, devices int64 }{
		{0, 15, 1}, {-1, 15, 1}, {math.MaxInt64, 15, 1},
		{7, -1, 1}, {7, math.MaxInt64, 1}, {7, 15, -1}, {7, 15, math.MaxInt64},
	} {
		s, e := fixture(t)
		ctx := context.Background()
		s.cfg.ReferredTrial = bonuses.ReferredTrialConfig{Enabled: true, PeriodDays: tc.period}
		s.cfg.Subscriptions.TrialTrafficGB, s.cfg.Subscriptions.TrialDevices = tc.traffic, tc.devices
		telegramTrialAccount(t, s, 701)
		a := referredTrialAccount(t, s, 702, "701")
		_, _, err := s.subscriptions.ActivateTelegramTrial(ctx, a.ID, uuid.New())
		if status(subscriptionError(err)) != 503 || count(t, e, "trial_grants") != 0 || count(t, e, "trial_operations") != 0 || count(t, e, "trial_requests") != 0 {
			t.Fatal("unsafe referred trial limit accepted", err)
		}
		var unreserved bool
		if err = e.Pool.QueryRow(ctx, `SELECT referred_bonus_days IS NULL AND referred_rewarded_at IS NULL FROM referrals WHERE referred_account_id=$1`, a.ID).Scan(&unreserved); err != nil || !unreserved {
			t.Fatal("invalid limits consumed referral", err)
		}
	}
}
