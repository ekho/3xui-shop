package httpapi

import (
	"context"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"sync"
	"testing"
)

func telegramTrialAccount(t *testing.T, s *regressionFixture, id int64) accounts.Snapshot {
	t.Helper()
	a, _, err := s.accounts.StartTelegramSession(context.Background(), accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: id, DisplayName: "Owned trial client", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	return a.Account
}

// Catches a second grant/job on replay or concurrent activation, fake operator
// attribution, and capturing the wrong configured trial limits.
func TestTelegramTrialAtomicActivation(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	a := telegramTrialAccount(t, s, 701)
	s.cfg.Accounts.Operators, s.cfg.Subscriptions.Operators = nil, nil
	available, err := s.subscriptions.CanRequestTrial(ctx, a)
	if err != nil || !available || s.subscriptions.TrialMode(a) != "activate" {
		t.Fatal("automatic trial requires an operator", err)
	}
	key := uuid.New()
	type result struct {
		request subscriptions.TrialRequest
		created bool
		err     error
	}
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, c, err := s.subscriptions.ActivateTelegramTrial(ctx, a.ID, key)
			results <- result{r, c, err}
		}()
	}
	wg.Wait()
	close(results)
	var r subscriptions.TrialRequest
	created := 0
	for got := range results {
		if got.err != nil || got.request.Status != "approved" || got.request.OperationId == nil {
			t.Fatal("activation failed", got.err)
		}
		if r.RequestId != uuid.Nil && (r.RequestId != got.request.RequestId || *r.OperationId != *got.request.OperationId) {
			t.Fatal("duplicate activation")
		}
		r = got.request
		if got.created {
			created++
		}
	}
	if created != 1 || count(t, e, "trial_grants") != 1 || count(t, e, "trial_operations") != 1 || count(t, e, "trial_requests") != 1 {
		t.Fatal("activation is not atomic")
	}
	var source string
	var actors bool
	var days, traffic, devices, jobs, cards int64
	err = e.Pool.QueryRow(ctx, `SELECT r.decision_source,r.operator_tg_id IS NULL AND r.operator_account_id IS NULL,o.period_days,o.traffic_gb,o.devices,(SELECT count(*) FROM river_job WHERE kind='trial_provision'),(SELECT count(*) FROM telegram_deliveries WHERE kind='approval_card') FROM trial_requests r JOIN trial_operations o ON o.id=r.operation_id WHERE r.id=$1`, r.RequestId).Scan(&source, &actors, &days, &traffic, &devices, &jobs, &cards)
	if err != nil || source != "telegram_auto" || !actors || days != 3 || traffic != 15 || devices != 1 || jobs != 1 || cards != 0 {
		t.Fatal("automatic provenance/config/queue incorrect", err)
	}
	var audits int
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='trial_activated_telegram' AND request_id=$1 AND operation_id=$2 AND operator_tg_id IS NULL AND operator_account_id IS NULL`, r.RequestId, r.OperationId).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("automatic audit attribution", err)
	}
	if _, _, err = s.subscriptions.ActivateTelegramTrial(ctx, a.ID, uuid.New()); status(subscriptionError(err)) != 409 {
		t.Fatal("new key granted another trial", err)
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.subscriptions.ActivateTelegramTrial(ctx, a.ID, key); status(subscriptionError(err)) != 403 {
		t.Fatal("replay bypassed current restriction", err)
	}
}

// Catches selecting policy from the current login kind and treating unknown
// legacy history or an existing manual request as permission to auto-approve.
func TestTelegramTrialEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, change string
		want         int
		mode         string
	}{
		{"restricted", "UPDATE accounts SET restricted=true WHERE id=$1", 403, "activate"},
		{"banned", "UPDATE accounts SET vpn_banned=true WHERE id=$1", 403, "activate"},
		{"disabled_identity", "UPDATE accounts SET telegram_login_disabled=true WHERE id=$1", 403, "activate"},
		{"legacy_unknown", "UPDATE accounts SET legacy_user_id=701 WHERE id=$1", 403, "request"},
		{"past_subscription", "UPDATE accounts SET had_subscription=true WHERE id=$1", 409, "activate"},
		{"assigned_panel", "UPDATE accounts SET assigned_panel_id='old-panel' WHERE id=$1", 409, "activate"},
		{"tg_credentials", "UPDATE accounts SET original_kind='telegram',kind='web',email_key='owned@example.test',password_hash='owned-fixture',verified_at=now() WHERE id=$1", 0, "activate"},
		{"unlinked_identity", "UPDATE accounts SET original_kind='telegram',kind='web',email_key='owned@example.test',password_hash='owned-fixture',verified_at=now(),telegram_id=NULL WHERE id=$1", 403, "activate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e := fixture(t)
			ctx := context.Background()
			a := telegramTrialAccount(t, s, 701)
			if _, err := e.Pool.Exec(ctx, tc.change, a.ID); err != nil {
				t.Fatal(err)
			}
			a, err := s.accounts.Lookup(ctx, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if s.subscriptions.TrialMode(a) != tc.mode {
				t.Fatal("mode lost original source")
			}
			_, _, err = s.subscriptions.ActivateTelegramTrial(ctx, a.ID, uuid.New())
			if status(subscriptionError(err)) != tc.want {
				t.Fatalf("want %d got %v", tc.want, err)
			}
			if tc.want != 0 && count(t, e, "trial_grants") != 0 {
				t.Fatal("denied activation wrote grant")
			}
		})
	}
	t.Run("web_plus_telegram", func(t *testing.T) {
		s, e := fixture(t)
		ctx := context.Background()
		id := verified(t, s, e, "webtrial@example.test")
		if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=701 WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		a, err := s.accounts.Lookup(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if s.subscriptions.TrialMode(a) != "request" {
			t.Fatal("web link changed policy")
		}
		if _, _, err = s.subscriptions.ActivateTelegramTrial(ctx, id, uuid.New()); status(subscriptionError(err)) != 403 {
			t.Fatal("web bypassed operator", err)
		}
		if r, _, err := s.createTrialRequest(ctx, id, uuid.New(), wire.TrialRequestInput{}); err != nil || r.Status != "pending" {
			t.Fatal("manual web flow changed", err)
		}
	})
	for _, state := range []string{"pending", "rejected"} {
		t.Run(state, func(t *testing.T) {
			s, e := fixture(t)
			ctx := context.Background()
			a := telegramTrialAccount(t, s, 701)
			r, _, err := s.createTrialRequest(ctx, a.ID, uuid.New(), wire.TrialRequestInput{})
			if err != nil {
				t.Fatal(err)
			}
			if state == "rejected" {
				if _, err = s.decideTrialRequest(ctx, r.RequestId, decision(101, "reject")); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err = s.subscriptions.ActivateTelegramTrial(ctx, a.ID, uuid.New()); status(subscriptionError(err)) != 409 {
				t.Fatal("automatic path bypassed manual request", err)
			}
			if count(t, e, "trial_grants") != 0 {
				t.Fatal("manual state bypass granted trial")
			}
		})
	}
	for _, tc := range []struct {
		name      string
		configure func(*regressionFixture)
		want      int
	}{
		{"trial_disabled", func(s *regressionFixture) { s.cfg.Subscriptions.TrialEnabled = false }, 403},
		{"missing_panel", func(s *regressionFixture) { s.cfg.Subscriptions.PanelID = "" }, 503},
		{"invalid_period", func(s *regressionFixture) { s.cfg.Subscriptions.TrialPeriodDays = 0 }, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e := fixture(t)
			a := telegramTrialAccount(t, s, 701)
			tc.configure(s)
			_, _, err := s.subscriptions.ActivateTelegramTrial(context.Background(), a.ID, uuid.New())
			if status(subscriptionError(err)) != tc.want || count(t, e, "trial_requests") != 0 || count(t, e, "trial_grants") != 0 {
				t.Fatal("config denial wrote state", err)
			}
		})
	}
}

// Catches partial request/decision/grant commit when the existing queue fails.
func TestTelegramTrialQueueRollback(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	a := telegramTrialAccount(t, s, 701)
	if _, err := e.Pool.Exec(ctx, `CREATE FUNCTION fail_trial_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='trial_provision' THEN RAISE EXCEPTION 'controlled fixture failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_trial BEFORE INSERT ON river_job FOR EACH ROW EXECUTE FUNCTION fail_trial_insert();`); err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	if _, _, err := s.subscriptions.ActivateTelegramTrial(ctx, a.ID, key); status(subscriptionError(err)) != 503 {
		t.Fatal("queue failure accepted", err)
	}
	if count(t, e, "trial_requests") != 0 || count(t, e, "trial_grants") != 0 || count(t, e, "trial_operations") != 0 || count(t, e, "client_telegram_deliveries") != 0 {
		t.Fatal("partial activation persisted")
	}
	if _, err := e.Pool.Exec(ctx, `DROP TRIGGER fail_trial ON river_job`); err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.subscriptions.ActivateTelegramTrial(ctx, a.ID, key); err != nil || !created {
		t.Fatal("rolled-back key was consumed", err)
	}
}
