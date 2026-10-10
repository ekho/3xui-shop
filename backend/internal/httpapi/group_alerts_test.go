package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/notifications"
	"github.com/google/uuid"
)

func TestGroupAlertDurableDedupAndFreshRights(t *testing.T) {
	for _, change := range []string{"none", "grant", "binding", "version", "restricted"} {
		t.Run(change, func(t *testing.T) {
			s, e := fixture(t)
			ctx := context.Background()
			actor := verified(t, s, e, "group-alert@example.test")
			target := verified(t, s, e, "group-target@example.test")
			if s.accounts.ChangeOperatorRole(ctx, actor, true) != nil || s.accounts.ChangeInfrastructureRole(ctx, actor, true) != nil {
				t.Fatal("fixture grants unavailable")
			}
			if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=701 WHERE id=$1`, actor); err != nil {
				t.Fatal(err)
			}
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.accounts.ReminderRecipientTx(ctx, tx, actor, false)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err = s.notifications.EnqueueGroupAlertTx(ctx, tx, r, target, "panel_unavailable", e.Clock()); err != nil {
					t.Fatal(err)
				}
			}
			if tx.Commit(ctx) != nil {
				t.Fatal("alert commit failed")
			}
			j, err := s.notifications.ClaimClient(ctx)
			if err != nil || j == nil || !strings.Contains(j.ReminderText, "панели") {
				t.Fatal("expected durable localized infrastructure alert", err)
			}
			var count int
			if e.Pool.QueryRow(ctx, `SELECT count(*) FROM client_telegram_deliveries WHERE account_id=$1`, actor).Scan(&count) != nil || count != 1 {
				t.Fatal("duplicate alert")
			}
			switch change {
			case "grant":
				err = s.accounts.ChangeInfrastructureRole(ctx, actor, false)
			case "binding":
				_, err = e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=702 WHERE id=$1`, actor)
			case "version":
				_, err = e.Pool.Exec(ctx, `UPDATE accounts SET credential_version=credential_version+1 WHERE id=$1`, actor)
			case "restricted":
				_, err = e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, actor)
			}
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			if err = s.notifications.DeliverClient(ctx, *j, func() (notifications.ClientOutcome, error) {
				calls++
				return notifications.ClientOutcome{State: "sent", MessageID: 1}, nil
			}); err != nil {
				t.Fatal(err)
			}
			var state string
			if e.Pool.QueryRow(ctx, `SELECT state FROM client_telegram_deliveries WHERE id=$1`, j.ID).Scan(&state) != nil {
				t.Fatal("missing alert state")
			}
			if change == "none" && (calls != 1 || state != "sent") || change != "none" && (calls != 0 || state != "skipped") {
				t.Fatal("stale infrastructure permission reached send", calls, state)
			}
		})
	}
}

func TestGroupAlertEnglishAndRetry(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	actor := verified(t, s, e, "group-english@example.test")
	if s.accounts.ChangeOperatorRole(ctx, actor, true) != nil || s.accounts.ChangeInfrastructureRole(ctx, actor, true) != nil {
		t.Fatal("fixture grant")
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=701,locale='en' WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.accounts.ReminderRecipientTx(ctx, tx, actor, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.notifications.EnqueueGroupAlertTx(ctx, tx, r, actor, "empty_membership", e.Clock()); err != nil || tx.Commit(ctx) != nil {
		t.Fatal(err)
	}
	j, err := s.notifications.ClaimClient(ctx)
	if err != nil || j == nil || !strings.Contains(j.ReminderText, "inbounds") {
		t.Fatal("English infrastructure alert missing", err)
	}
	if err = s.notifications.DeliverClient(ctx, *j, func() (notifications.ClientOutcome, error) {
		return notifications.ClientOutcome{State: "retry", RetryAfter: time.Second}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE client_telegram_deliveries SET available_at=clock_timestamp() WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := s.notifications.ClaimClient(ctx)
	if err != nil || retry == nil || retry.ID != j.ID {
		t.Fatal("outbox retry duplicated or lost alert", err)
	}
	if err = s.notifications.DeliverClient(ctx, *retry, func() (notifications.ClientOutcome, error) {
		return notifications.ClientOutcome{State: "sent", MessageID: 2}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.notifications.DeliverClient(ctx, *retry, func() (notifications.ClientOutcome, error) {
		t.Fatal("completed alert sent twice")
		return notifications.ClientOutcome{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.notifications.EnqueueGroupAlertTx(ctx, nil, r, uuid.Nil, "arbitrary provider data", e.Clock()); err == nil {
		t.Fatal("invalid alert accepted")
	}
}
