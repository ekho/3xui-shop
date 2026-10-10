package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/wire"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"testing"
)

// Normalizing the old raw result before hashing would reject persisted results.
func TestTelegramDeliveryPersistedCompatibility(t *testing.T) {
	ctx := context.Background()
	for _, pair := range []struct {
		old     string
		current any
	}{
		{`{"chat_id":101,"kind":"sent","message_id":42}`, notifications.TelegramSent{ChatId: 101, Kind: "sent", MessageId: 42}},
		{`{"code":"timeout","kind":"delivery_failed"}`, notifications.TelegramFailed{Code: "timeout", Kind: "delivery_failed"}},
	} {
		current, err := json.Marshal(pair.current)
		if err != nil || string(current) != pair.old {
			t.Fatal("stored outcome JSON changed", err)
		}
	}
	for _, state := range []string{"sent", "failed"} {
		t.Run(state, func(t *testing.T) {
			_, svc, e, _ := bridgeFixture(t)
			jobs, err := svc.Notifications.ClaimTelegramJobs(ctx, 1)
			if err != nil || len(jobs) != 1 {
				t.Fatal("owned claim", err)
			}
			j := jobs[0]
			raw := json.RawMessage(fmt.Sprintf("{ \"message_id\": 42, \"extra\": true, \"kind\": \"sent\", \"chat_id\": %d }", j.ChatID))
			code, message := any(nil), any(int64(42))
			if state == "failed" {
				raw = json.RawMessage("{ \"kind\": \"delivery_failed\", \"extra\": true, \"code\": \"timeout\" }")
				code, message = "timeout", nil
			}
			oldBytes, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(oldBytes)
			if _, err = e.Pool.Exec(ctx, `UPDATE telegram_deliveries SET state=$2,result_hash=$3,message_id=$4,failure_code=$5,completed_at=clock_timestamp() WHERE id=$1`, j.JobID, state, hash[:], message, code); err != nil {
				t.Fatal(err)
			}
			var before, after string
			if err = e.Pool.QueryRow(ctx, `SELECT to_jsonb(d)::text FROM telegram_deliveries d WHERE id=$1`, j.JobID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err = svc.Notifications.CompleteTelegramJob(ctx, j.JobID, j.LeaseToken, raw); err != nil {
				t.Fatal("old raw result replay", err)
			}
			if err = e.Pool.QueryRow(ctx, `SELECT to_jsonb(d)::text FROM telegram_deliveries d WHERE id=$1`, j.JobID).Scan(&after); err != nil || before != after {
				t.Fatal("replay changed saved delivery", err)
			}
			for _, in := range []struct {
				raw  json.RawMessage
				code string
			}{
				{json.RawMessage(fmt.Sprintf(`{"chat_id":%d,"kind":"sent","message_id":42}`, j.ChatID)), "REQUEST_STATE_CONFLICT"},
				{json.RawMessage(`{"code":"forbidden","kind":"delivery_failed"}`), "REQUEST_STATE_CONFLICT"},
				{json.RawMessage(`{"code":"edit_failed","kind":"delivery_failed"}`), "REQUEST_STATE_CONFLICT"},
				{json.RawMessage(`{"code":"network","kind":"delivery_failed"}`), "REQUEST_STATE_CONFLICT"},
				{json.RawMessage(`{"code":"invalid_response","kind":"delivery_failed"}`), "REQUEST_STATE_CONFLICT"},
				{json.RawMessage(`{"code":"unknown","kind":"delivery_failed"}`), "INVALID_INPUT"},
				{json.RawMessage(`{"chat_id":999,"kind":"sent","message_id":42}`), "REQUEST_STATE_CONFLICT"},
				{json.RawMessage(`{"chat_id":0,"kind":"sent","message_id":42}`), "INVALID_INPUT"},
				{json.RawMessage(`{"kind":"sent","message_id":0}`), "INVALID_INPUT"},
				{json.RawMessage(`{"kind":"unknown"}`), "INVALID_INPUT"},
				{json.RawMessage("{"), "INVALID_INPUT"},
			} {
				err = svc.Notifications.CompleteTelegramJob(ctx, j.JobID, j.LeaseToken, in.raw)
				if err == nil || err.Error() != in.code {
					t.Fatal("result validation/replay", err, in.code)
				}
			}
			for _, in := range []struct {
				id    uuid.UUID
				token string
			}{{uuid.Nil, j.LeaseToken}, {j.JobID, "short"}, {j.JobID, strings.Repeat("x", 43)}} {
				err = svc.Notifications.CompleteTelegramJob(ctx, in.id, in.token, raw)
				if err == nil || err.Error() != "REQUEST_STATE_CONFLICT" {
					t.Fatal("lease validation", err)
				}
			}
			if err = svc.Notifications.CompleteTelegramJob(ctx, uuid.Nil, "short", json.RawMessage("{")); err == nil || err.Error() != "REQUEST_STATE_CONFLICT" {
				t.Fatal("invalid id/token must precede JSON validation", err)
			}
			denied := notifications.New(e.Pool, func() []int64 { return nil }, func(int64) bool { return false }, nil, nil)
			if err = denied.CompleteTelegramJob(ctx, j.JobID, j.LeaseToken, raw); err == nil || err.Error() != "REQUEST_STATE_CONFLICT" {
				t.Fatal("revoked operator completed a delivery", err)
			}
			empty, err := denied.ClaimTelegramJobs(ctx, 1)
			if err != nil || empty == nil || len(empty) != 0 {
				t.Fatal("claim ignored allowlist or returned null jobs", err)
			}
			if _, err = e.Pool.Exec(ctx, `UPDATE telegram_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, j.JobID); err != nil {
				t.Fatal(err)
			}
			if err = svc.Notifications.CompleteTelegramJob(ctx, j.JobID, j.LeaseToken, raw); err == nil || err.Error() != "REQUEST_STATE_CONFLICT" {
				t.Fatal("expired completed replay accepted", err)
			}
		})
	}
}

// Losing the caller transaction or using a stored card breaks these flows.
func TestTelegramDeliveryComposition(t *testing.T) {
	ctx := context.Background()
	t.Run("current card and Tx ports", func(t *testing.T) {
		b, svc, e, id := bridgeFixture(t)
		d, err := b.Claim(ctx)
		if err != nil || d == nil {
			t.Fatal("bridge claim", err)
		}
		if err = b.Complete(ctx, *d, telegram.DeliveryOutcome{Kind: "sent", ChatID: d.ChatID, MessageID: 99}); err != nil {
			t.Fatal(err)
		}
		decision, err := b.Decide(ctx, telegram.TrialDecision{RequestID: id, ActorID: d.ChatID, Action: "reject", CallbackID: "current-card"})
		if err != nil || decision.Card.TargetMessageID == nil || *decision.Card.TargetMessageID != 99 || decision.Card.Status != "rejected" {
			t.Fatal("current card/last sent message", err)
		}
		next, err := b.Claim(ctx)
		if err != nil || next == nil || next.Card.Status != "rejected" {
			t.Fatal("claim rendered stored pending card", err)
		}
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		message, err := svc.Notifications.LatestTelegramMessageTx(ctx, tx, id, d.ChatID)
		if err != nil || message == nil || *message != 99 {
			t.Fatal("latest sent", err)
		}
		state, err := svc.Notifications.LatestTelegramStateTx(ctx, tx, id, d.ChatID)
		if err != nil || state != "pending" {
			t.Fatal("newest decision notification state", err)
		}
		message, err = svc.Notifications.LatestTelegramMessageTx(ctx, tx, id, 999)
		if err != nil || message != nil {
			t.Fatal("absent message", err)
		}
		state, err = svc.Notifications.LatestTelegramStateTx(ctx, tx, id, 999)
		if err != nil || state != "pending" {
			t.Fatal("absent state", err)
		}
		var before, during, after int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM telegram_deliveries`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if err = svc.Notifications.EnqueueTelegramTx(ctx, tx, id, nil, d.ChatID, "approval_card", json.RawMessage(`{"comment":"rollback"}`), e.Clock()); err != nil {
			t.Fatal(err)
		}
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM telegram_deliveries`).Scan(&during); err != nil || during != before+1 {
			t.Fatal("enqueue not visible in caller Tx", err)
		}
		if err = tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM telegram_deliveries`).Scan(&after); err != nil || after != before {
			t.Fatal("enqueue survived caller rollback", err)
		}
	})
	t.Run("card failure rolls back lease", func(t *testing.T) {
		_, _, e, _ := bridgeFixture(t)
		owner := notifications.New(e.Pool, func() []int64 { return []int64{101} }, func(id int64) bool { return id == 101 }, func(context.Context, pgx.Tx, uuid.UUID, int64) (json.RawMessage, error) {
			return nil, errors.New("controlled card failure")
		}, nil)
		if _, err := owner.ClaimTelegramJobs(ctx, 1); err == nil {
			t.Fatal("card failure accepted")
		}
		var attempts, leased int
		if err := e.Pool.QueryRow(ctx, `SELECT coalesce(sum(attempts),0),count(lease_hash) FROM telegram_deliveries`).Scan(&attempts, &leased); err != nil || attempts != 0 || leased != 0 {
			t.Fatal("card failure retained lease/attempt", err)
		}
		if _, err := owner.ClaimTelegramJobs(ctx, 0); err == nil || err.Error() != "INVALID_INPUT" {
			t.Fatal("batch limit validation", err)
		}
	})
	t.Run("outbox failure rolls back trial", func(t *testing.T) {
		_, svc, e, _ := bridgeFixture(t)
		account, key := paymentAccount(t, e), uuid.New()
		var before, after, requests, audits, idem int
		if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM telegram_deliveries`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Pool.Exec(ctx, `CREATE FUNCTION reject_delivery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'controlled outbox failure'; END $$;
			CREATE TRIGGER reject_delivery BEFORE INSERT ON telegram_deliveries FOR EACH ROW EXECUTE FUNCTION reject_delivery()`); err != nil {
			t.Fatal(err)
		}
		_, _, err := svc.createTrialRequest(ctx, account, key, wire.TrialRequestInput{})
		if err == nil || err.Error() != "SERVICE_UNAVAILABLE" {
			t.Fatal("outbox failure accepted", err)
		}
		err = e.Pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM trial_requests WHERE account_id=$1),
			(SELECT count(*) FROM audit_events WHERE account_id=$1),
			(SELECT count(*) FROM idempotency_records WHERE key=$2),
			(SELECT count(*) FROM telegram_deliveries)`, account, key).Scan(&requests, &audits, &idem, &after)
		if err != nil || requests != 0 || audits != 0 || idem != 0 || after != before {
			t.Fatal("partial trial/outbox commit", err)
		}
	})
}
