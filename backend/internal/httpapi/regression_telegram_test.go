package httpapi

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"strings"
	"sync"
	"testing"
	"time"
)

func sentInput(j notifications.TelegramJob, chat int64) json.RawMessage {
	raw, _ := json.Marshal(notifications.TelegramSent{Kind: "sent", ChatId: chat, MessageId: 11})
	return raw
}
func TestRegressionTelegramLease(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	account := verified(t, s, e, "lease@example.test")
	r, _, err := s.createTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{Comment: ptr("<b>unsafe</b>")})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.notifications.ClaimTelegramJobs(ctx, 1)
	if err != nil || len(out) != 1 {
		t.Fatal("claim", err)
	}
	j := out[0]
	var leased time.Time
	if err = e.Pool.QueryRow(ctx, `SELECT lease_expires_at-interval '60 seconds' FROM telegram_deliveries WHERE id=$1`, j.JobID).Scan(&leased); err != nil || !j.LeaseExpiresAt.Equal(leased.Add(time.Minute)) {
		t.Fatal("lease not exactly60", err)
	}
	var payload subscriptions.TelegramPayload
	if json.Unmarshal(j.Payload, &payload) != nil || payload.RequestId != r.RequestId || j.ChatID != 101 || j.LeaseToken == "" {
		t.Fatal("wrong payload")
	}
	raw, _ := json.Marshal(j)
	if strings.Contains(string(raw), "vpn_id") || strings.Contains(string(raw), "sub_id") {
		t.Fatal("private panel credential in delivery")
	}
	bad := sentInput(j, 999)
	if status(notificationError(s.notifications.CompleteTelegramJob(ctx, j.JobID, j.LeaseToken, bad))) != 409 {
		t.Fatal("wrong chat accepted")
	}
	bad = sentInput(j, j.ChatID)
	wrong := j.LeaseToken
	j.LeaseToken = "wrong"
	if status(notificationError(s.notifications.CompleteTelegramJob(ctx, j.JobID, j.LeaseToken, bad))) != 409 {
		t.Fatal("wrong lease accepted")
	}
	j.LeaseToken = wrong
	if _, err = e.Pool.Exec(ctx, `UPDATE telegram_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, j.JobID); err != nil {
		t.Fatal(err)
	}
	if status(notificationError(s.notifications.CompleteTelegramJob(ctx, j.JobID, j.LeaseToken, sentInput(j, j.ChatID)))) != 409 {
		t.Fatal("expired ack accepted")
	}
	again, err := s.notifications.ClaimTelegramJobs(ctx, 1)
	if err != nil || len(again) != 1 || again[0].JobID != j.JobID || again[0].LeaseToken == j.LeaseToken {
		t.Fatal("lost ack was not reclaimed", err)
	}
	j = again[0]
	if err = notificationError(s.notifications.CompleteTelegramJob(ctx, j.JobID, j.LeaseToken, sentInput(j, j.ChatID))); err != nil {
		t.Fatal(err)
	}
	if err = notificationError(s.notifications.CompleteTelegramJob(ctx, j.JobID, j.LeaseToken, sentInput(j, j.ChatID))); err != nil {
		t.Fatal("same terminal ack not idempotent", err)
	}
	decisionIn := decision(101, "approve")
	a, err := s.decideTrialRequest(ctx, r.RequestId, decisionIn)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.decideTrialRequest(ctx, r.RequestId, decision(202, "approve"))
	if err != nil || a.OperationId == nil || b.OperationId == nil || *a.OperationId != *b.OperationId || count(t, e, "trial_grants") != 1 {
		t.Fatal("duplicate card duplicated grant", err)
	}
	out, err = s.notifications.ClaimTelegramJobs(ctx, 1)
	if err != nil || len(out) != 1 {
		t.Fatal("stale approval card claim", err)
	}
	if err = json.Unmarshal(out[0].Payload, &payload); err != nil || payload.Status == "pending" {
		t.Fatal("stale approval card payload", err)
	}
}
func TestRegressionTelegramLeaseConcurrent(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	account := verified(t, s, e, "leases@example.test")
	_, _, err := s.createTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan []notifications.TelegramJob, 5)
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.notifications.ClaimTelegramJobs(ctx, 1)
			results <- out
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[uuid.UUID]bool{}
	for out := range results {
		if len(out) > 1 {
			t.Fatal("batch >1")
		}
		for _, j := range out {
			if seen[j.JobID] {
				t.Fatal("duplicate live lease")
			}
			seen[j.JobID] = true
		}
	}
	if len(seen) != 2 {
		t.Fatal("lost operator delivery")
	}
}
