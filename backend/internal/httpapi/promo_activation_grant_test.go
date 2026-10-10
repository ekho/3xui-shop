package httpapi

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func activationCode(t *testing.T, s *regressionFixture, actor uuid.UUID, days int) bonuses.Promocode {
	t.Helper()
	r, err := s.bonusesOwner.CreatePromocode(context.Background(), actor, uuid.New(), bonuses.CreatePromocodeInput{DurationDays: days, Reason: "owned activation fixture"})
	if err != nil {
		t.Fatal("create owned code", err)
	}
	return r
}
func activationFailure(err error, code string) bool { return err != nil && err.Error() == code }

func TestPromoActivationGrantPreservesAccessAndReplay(t *testing.T) {
	s, e, p, actor, account, _ := accessActors(t)
	ctx := context.Background()
	code := activationCode(t, s, actor, 7)
	expiry := integer(t, p.client["expiryTime"])
	id, sub, panelKey := p.client["id"], p.client["subId"], p.client["email"]
	devices, traffic, used := integer(t, p.client["limitIp"]), integer(t, p.client["totalGB"]), p.up
	key := uuid.New()
	first, err := s.bonusesOwner.ActivatePromocode(ctx, account, key, bonuses.ActivatePromocodeInput{Code: code.Code})
	if err != nil || first.Status != "pending" || first.DurationDays != 7 {
		t.Fatal("accept compensation", err)
	}
	if p.updates != 0 || p.resets != 0 {
		t.Fatal("acceptance wrote panel before durable intent")
	}
	// A fresh service has no in-memory activation state. Replay does not need the panel.
	restarted := bonuses.New(e.Pool, s.accounts, e.Clock)
	restarted.ConfigureSubscriptions(s.subscriptions)
	p.failRead = true
	replay, err := restarted.ActivatePromocode(ctx, account, key, bonuses.ActivatePromocodeInput{Code: "  " + code.Code + "  "})
	if err != nil || replay != first {
		t.Fatal("restart/lost response changed grant", err)
	}
	p.failRead = false
	if err = s.applyAccess(ctx, first.OperationID); err != nil {
		t.Fatal("worker apply", err)
	}
	if err = s.applyAccess(ctx, first.OperationID); err != nil {
		t.Fatal("worker repeat", err)
	}
	if p.updates != 1 || p.resets != 0 || p.up != used || p.client["uuid"] != id || p.client["subId"] != sub || p.client["email"] != panelKey || integer(t, p.client["limitIp"]) != devices || integer(t, p.client["totalGB"]) != traffic || integer(t, p.client["expiryTime"]) != expiry+7*24*60*60*1000 {
		t.Fatal("bonus changed identity/limits/traffic or repeated days")
	}
	current, err := restarted.GetPromocodeActivation(ctx, account, first.OperationID)
	if err != nil || current.Status != "applied" || current.OperationID != first.OperationID {
		t.Fatal("current owner status", err)
	}
	if _, err = restarted.GetPromocodeActivation(ctx, actor, first.OperationID); !activationFailure(err, "PROMOCODE_ACTIVATION_NOT_FOUND") {
		t.Fatal("foreign owner read", err)
	}
	if _, err = restarted.ActivatePromocode(ctx, account, uuid.New(), bonuses.ActivatePromocodeInput{Code: code.Code}); !activationFailure(err, "PROMOCODE_USED") {
		t.Fatal("used code repeated", err)
	}
	if _, err = restarted.ActivatePromocode(ctx, account, key, bonuses.ActivatePromocodeInput{Code: "missing"}); !activationFailure(err, "IDEMPOTENCY_CONFLICT") {
		t.Fatal("changed body accepted", err)
	}
	var n int
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM promocode_events p JOIN audit_events a ON a.id=p.id JOIN access_operations o ON o.id=a.access_operation_id WHERE p.promocode_id=$1 AND p.action='activate' AND a.account_id=$2 AND a.operator_account_id IS NULL AND o.operator_account_id IS NULL AND o.kind='compensate' AND p.after_snapshot->>'access_operation_id'=o.id::text AND p.after_snapshot::text NOT LIKE '%'||$3||'%' AND a.reason NOT LIKE '%'||$3||'%' AND o.reason NOT LIKE '%'||$3||'%'`, code.PromocodeID, account, code.Code).Scan(&n); err != nil || n != 1 {
		t.Fatal("atomic retained history/audit leaked code or grant link", err)
	}
	if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", account); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.ActivatePromocode(ctx, account, key, bonuses.ActivatePromocodeInput{Code: code.Code}); !activationFailure(err, "ACCOUNT_RESTRICTED") {
		t.Fatal("restricted replay allowed", err)
	}
}

func TestPromoActivationConcurrentClientsAndRollback(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	ctx := context.Background()
	actor := verified(t, s, e, "promo-race-operator@example.test")
	if err := s.changeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	a := verified(t, s, e, "promo-race-a@example.test")
	b := verified(t, s, e, "promo-race-b@example.test")
	code := activationCode(t, s, actor, 10)
	var results [2]bonuses.PromocodeActivation
	var failures [2]error
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, account := range []uuid.UUID{a, b} {
		wg.Add(1)
		go func(i int, account uuid.UUID) {
			defer wg.Done()
			<-start
			results[i], failures[i] = s.bonusesOwner.ActivatePromocode(ctx, account, uuid.New(), bonuses.ActivatePromocodeInput{Code: code.Code})
		}(i, account)
	}
	close(start)
	wg.Wait()
	success := 0
	for _, err := range failures {
		if err == nil {
			success++
		} else if !activationFailure(err, "PROMOCODE_USED") {
			t.Fatal("race failed outside code boundary", err)
		}
	}
	if success != 1 || p.adds != 0 {
		t.Fatal("simultaneous clients issued twice or wrote before commit")
	}
	var count int
	if err := e.Pool.QueryRow(ctx, "SELECT count(*) FROM access_operations").Scan(&count); err != nil || count != 1 {
		t.Fatal("race grants", err, count)
	}
	// Fail after the marker update, so code/history/intent/job/replay must all roll back.
	other := verified(t, s, e, "promo-rollback@example.test")
	rollback := activationCode(t, s, actor, 3)
	_, err := e.Pool.Exec(ctx, `CREATE FUNCTION promo_fixture_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='activate' THEN RAISE EXCEPTION 'owned fixture rollback'; END IF; RETURN NEW; END $$; CREATE TRIGGER promo_fixture_fail BEFORE INSERT ON promocode_events FOR EACH ROW EXECUTE FUNCTION promo_fixture_fail()`)
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	if _, err = s.bonusesOwner.ActivatePromocode(ctx, other, key, bonuses.ActivatePromocodeInput{Code: rollback.Code}); !activationFailure(err, "SERVICE_UNAVAILABLE") {
		t.Fatal("injected persistence failure", err)
	}
	var used bool
	if e.Pool.QueryRow(ctx, "SELECT is_activated FROM promocodes WHERE id=$1", rollback.PromocodeID).Scan(&used) != nil || used {
		t.Fatal("failed transaction consumed code")
	}
	if e.Pool.QueryRow(ctx, "SELECT count(*) FROM access_operations WHERE account_id=$1", other).Scan(&count) != nil || count != 0 {
		t.Fatal("failed transaction retained grant")
	}
	if e.Pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind='access_operation' AND args->>'operation_id' NOT IN (SELECT id::text FROM access_operations)").Scan(&count) != nil || count != 0 {
		t.Fatal("orphaned worker job")
	}
	if e.Pool.QueryRow(ctx, "SELECT count(*) FROM idempotency_records WHERE key=$1", key).Scan(&count) != nil || count != 0 {
		t.Fatal("failed transaction retained replay")
	}
	if _, err = e.Pool.Exec(ctx, "DROP TRIGGER promo_fixture_fail ON promocode_events; DROP FUNCTION promo_fixture_fail()"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.bonusesOwner.ActivatePromocode(ctx, other, key, bonuses.ActivatePromocodeInput{Code: rollback.Code}); err != nil {
		t.Fatal("safe repeat after rollback", err)
	}
}

func TestPromoActivationCompensationRestrictions(t *testing.T) {
	for _, kind := range []string{"vpn-ban", "unlimited", "perpetual", "missing-client", "server-unavailable", "expired-traffic-exhausted"} {
		t.Run(kind, func(t *testing.T) {
			s, e, p, actor, account, _ := accessActors(t)
			ctx := context.Background()
			code := activationCode(t, s, actor, 5)
			expected := "ACCESS_NOT_ELIGIBLE"
			switch kind {
			case "vpn-ban":
				_, err := e.Pool.Exec(ctx, "UPDATE accounts SET vpn_banned=true WHERE id=$1", account)
				if err != nil {
					t.Fatal(err)
				}
			case "unlimited":
				_, err := e.Pool.Exec(ctx, "UPDATE accounts SET access_profile='unlimited' WHERE id=$1", account)
				if err != nil {
					t.Fatal(err)
				}
			case "perpetual":
				p.client["expiryTime"] = json.Number("0")
			case "missing-client":
				p.client = nil
			case "server-unavailable":
				p.offline = true
			case "expired-traffic-exhausted":
				p.client["expiryTime"] = json.Number(strconv.FormatInt(e.Clock().Add(-time.Hour).UnixMilli(), 10))
				p.client["enable"] = false
				p.up = integer(t, p.client["totalGB"])
				expected = ""
			}
			out, err := s.bonusesOwner.ActivatePromocode(ctx, account, uuid.New(), bonuses.ActivatePromocodeInput{Code: code.Code})
			if expected != "" {
				if !activationFailure(err, expected) {
					t.Fatal("restriction accepted", err)
				}
				var used bool
				if e.Pool.QueryRow(ctx, "SELECT is_activated FROM promocodes WHERE id=$1", code.PromocodeID).Scan(&used) != nil || used {
					t.Fatal("refusal consumed code")
				}
				return
			}
			if err != nil {
				t.Fatal("expired client acceptance", err)
			}
			if err = s.applyAccess(ctx, out.OperationID); err != nil {
				t.Fatal(err)
			}
			if p.client["enable"] != false || p.resets != 0 || p.up != integer(t, p.client["totalGB"]) || integer(t, p.client["expiryTime"]) != e.Clock().Add(5*24*time.Hour).UnixMilli() {
				t.Fatal("traffic exhaustion bypassed or expiry base changed")
			}
		})
	}
}

func TestPromoActivationWorkerGuardAndUncertainWrite(t *testing.T) {
	for _, mode := range []string{"restricted-before-write", "database-after-panel-write"} {
		t.Run(mode, func(t *testing.T) {
			s, e, p, actor, account, _ := accessActors(t)
			ctx := context.Background()
			code := activationCode(t, s, actor, 4)
			key := uuid.New()
			input := bonuses.ActivatePromocodeInput{Code: code.Code}
			op, err := s.bonusesOwner.ActivatePromocode(ctx, account, key, input)
			if err != nil {
				t.Fatal(err)
			}
			expiry := integer(t, p.client["expiryTime"])
			if mode == "restricted-before-write" {
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", account)
			} else {
				_, err = e.Pool.Exec(ctx, `CREATE FUNCTION promo_finish_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='applied' THEN RAISE EXCEPTION 'owned final-write failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER promo_finish_fail BEFORE UPDATE ON access_operations FOR EACH ROW EXECUTE FUNCTION promo_finish_fail()`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.applyAccess(ctx, op.OperationID); err != nil {
				t.Fatal(err)
			}
			var status string
			var retained int
			if err = e.Pool.QueryRow(ctx, `SELECT o.status,(SELECT count(*) FROM promocode_events e JOIN promocodes p ON p.id=e.promocode_id WHERE p.id=$2 AND p.is_activated AND e.action='activate' AND e.after_snapshot->>'access_operation_id'=o.id::text) FROM access_operations o WHERE o.id=$1`, op.OperationID, code.PromocodeID).Scan(&status, &retained); err != nil || status != "needs_review" || retained != 1 {
				t.Fatal("uncertain operation lost its retained activation", err, status)
			}
			if mode == "restricted-before-write" {
				if p.updates != 0 || integer(t, p.client["expiryTime"]) != expiry {
					t.Fatal("worker ignored current account restriction")
				}
				return
			}
			if p.updates != 1 || integer(t, p.client["expiryTime"]) != expiry+4*24*60*60*1000 {
				t.Fatal("fixture did not reach one absolute panel write")
			}
			if _, err = e.Pool.Exec(ctx, "DROP TRIGGER promo_finish_fail ON access_operations; DROP FUNCTION promo_finish_fail()"); err != nil {
				t.Fatal(err)
			}
			restarted := bonuses.New(e.Pool, s.accounts, e.Clock)
			restarted.ConfigureSubscriptions(s.subscriptions)
			if replay, err := restarted.ActivatePromocode(ctx, account, key, input); err != nil || replay != op {
				t.Fatal("uncertain replay changed the original grant", err)
			}
			if current, err := restarted.GetPromocodeActivation(ctx, account, op.OperationID); err != nil || current.Status != "needs_review" {
				t.Fatal("uncertainty hidden from client", err)
			}
			if _, err = s.reconcileAccessOperation(ctx, actor, account, op.OperationID, uuid.New(), wire.AccessReconcileInput{Reason: "owned readback reconciliation"}); err != nil {
				t.Fatal(err)
			}
			if err = s.applyAccess(ctx, op.OperationID); err != nil {
				t.Fatal(err)
			}
			if current, err := restarted.GetPromocodeActivation(ctx, account, op.OperationID); err != nil || current.Status != "applied" || p.updates != 1 || p.resets != 0 || integer(t, p.client["expiryTime"]) != expiry+4*24*60*60*1000 {
				t.Fatal("reconciliation repeated days or reset traffic", err)
			}
		})
	}
}

func TestPromoActivationRealManagementLockOrder(t *testing.T) {
	for _, action := range []string{"edit", "delete"} {
		for _, who := range []string{"client", "operator"} {
			t.Run(action+"/"+who, func(t *testing.T) {
				s, e, p, actor, account, _ := accessActors(t)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if who == "operator" {
					account = actor
					p.client = nil
				}
				code := activationCode(t, s, actor, 7)
				gate, err := e.Pool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer gate.Release()
				if _, err = gate.Exec(ctx, "SELECT pg_advisory_lock(490049)"); err != nil {
					t.Fatal(err)
				}
				defer gate.Exec(context.Background(), "SELECT pg_advisory_unlock(490049)")
				_, err = e.Pool.Exec(ctx, `CREATE FUNCTION promo_fixture_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.is_activated THEN PERFORM pg_advisory_xact_lock(490049); END IF; RETURN NEW; END $$; CREATE TRIGGER promo_fixture_gate BEFORE UPDATE ON promocodes FOR EACH ROW EXECUTE FUNCTION promo_fixture_gate()`)
				if err != nil {
					t.Fatal(err)
				}
				activated := make(chan error, 1)
				go func() {
					_, err := s.bonusesOwner.ActivatePromocode(ctx, account, uuid.New(), bonuses.ActivatePromocodeInput{Code: code.Code})
					activated <- err
				}()
				waitFor := func(query string) {
					t.Helper()
					for {
						var n int
						if err := e.Pool.QueryRow(ctx, query).Scan(&n); err != nil {
							t.Fatal(err)
						}
						if n > 0 {
							return
						}
						select {
						case <-ctx.Done():
							t.Fatal("owned concurrent lock not reached")
						case <-time.After(10 * time.Millisecond):
						}
					}
				}
				waitFor(`SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory'`)
				changed := make(chan error, 1)
				go func() {
					var err error
					if action == "edit" {
						_, err = s.bonusesOwner.EditPromocode(ctx, actor, code.PromocodeID, uuid.New(), bonuses.EditPromocodeInput{DurationDays: 30, ExpectedRevision: code.Revision, Reason: "owned race"})
					} else {
						_, err = s.bonusesOwner.DeletePromocode(ctx, actor, code.PromocodeID, uuid.New(), bonuses.DeletePromocodeInput{ExpectedRevision: code.Revision, Reason: "owned race"})
					}
					changed <- err
				}()
				waitFor(`SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event<>'advisory'`)
				if _, err = gate.Exec(ctx, "SELECT pg_advisory_unlock(490049)"); err != nil {
					t.Fatal(err)
				}
				if err = <-activated; err != nil {
					t.Fatal("real activation did not win", err)
				}
				if err = <-changed; !activationFailure(err, "PROMOCODE_USED") {
					t.Fatal("management escaped consumed guard", err)
				}
				var snapshot []byte
				if err = e.Pool.QueryRow(ctx, "SELECT after_snapshot FROM promocode_events WHERE promocode_id=$1 AND action='activate'", code.PromocodeID).Scan(&snapshot); err != nil {
					t.Fatal(err)
				}
				var metadata bonuses.PromocodeMetadata
				if json.Unmarshal(snapshot, &metadata) != nil || metadata.DurationDays != 7 || metadata.State != "activated" {
					t.Fatal("used duration/history changed")
				}
			})
		}
	}
}

func TestPromoActivationManagementWinsPreparation(t *testing.T) {
	for _, action := range []string{"edit", "delete", "restrict"} {
		t.Run(action, func(t *testing.T) {
			s, e, p, actor, account, _ := accessActors(t)
			ctx := context.Background()
			code := activationCode(t, s, actor, 7)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			p.beforeRead = func() { once.Do(func() { close(entered); <-release }) }
			finished := make(chan error, 1)
			go func() {
				_, err := s.bonusesOwner.ActivatePromocode(ctx, account, uuid.New(), bonuses.ActivatePromocodeInput{Code: code.Code})
				finished <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("preparation not reached")
			}
			var err error
			expected := "PROMOCODE_REVISION_CONFLICT"
			switch action {
			case "edit":
				_, err = s.bonusesOwner.EditPromocode(ctx, actor, code.PromocodeID, uuid.New(), bonuses.EditPromocodeInput{DurationDays: 30, ExpectedRevision: code.Revision, Reason: "owned edit"})
			case "delete":
				expected = "PROMOCODE_INVALID"
				_, err = s.bonusesOwner.DeletePromocode(ctx, actor, code.PromocodeID, uuid.New(), bonuses.DeletePromocodeInput{ExpectedRevision: code.Revision, Reason: "owned delete"})
			case "restrict":
				expected = "ACCOUNT_RESTRICTED"
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", account)
			}
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			if err = <-finished; !activationFailure(err, expected) {
				t.Fatal("preparation used stale conditions", err)
			}
			var n int
			if e.Pool.QueryRow(ctx, "SELECT count(*) FROM access_operations").Scan(&n) != nil || n != 0 || p.updates != 0 {
				t.Fatal("stale preparation retained effect")
			}
		})
	}
}
