package httpapi

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"strings"
	"sync"
	"testing"
	"time"
)

func sentInput(t *testing.T, j wire.TelegramJob, chat int64) wire.TelegramResultInput {
	t.Helper()
	var result wire.TelegramResult
	if result.FromTelegramSent(wire.TelegramSent{Kind: "sent", ChatId: chat, MessageId: 11}) != nil {
		t.Fatal("sent fixture")
	}
	return wire.TelegramResultInput{LeaseToken: j.LeaseToken, Result: result}
}
func TestRegressionTelegramLease(t *testing.T) {
	s, e := fixture(t)
	ctx := context.Background()
	account := verified(t, s, e, "lease@example.test")
	r, _, err := s.createTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{Comment: ptr("<b>unsafe</b>")})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.claimTelegramJobs(ctx, wire.ClaimInput{Limit: 1})
	if err != nil || len(out.Jobs) != 1 {
		t.Fatal("claim", err)
	}
	j := out.Jobs[0]
	var leased time.Time
	if err = e.Pool.QueryRow(ctx, `SELECT lease_expires_at-interval '60 seconds' FROM telegram_deliveries WHERE id=$1`, j.JobId).Scan(&leased); err != nil || !j.LeaseExpiresAt.Equal(leased.Add(time.Minute)) {
		t.Fatal("lease not exactly60", err)
	}
	if j.Payload.RequestId != r.RequestId || j.ChatId != 101 || j.LeaseToken == "" {
		t.Fatal("wrong payload")
	}
	raw, _ := json.Marshal(j)
	if strings.Contains(string(raw), "vpn_id") || strings.Contains(string(raw), "sub_id") {
		t.Fatal("private panel credential in delivery")
	}
	bad := sentInput(t, j, 999)
	if status(s.completeTelegramJob(ctx, j.JobId, bad)) != 409 {
		t.Fatal("wrong chat accepted")
	}
	bad = sentInput(t, j, j.ChatId)
	bad.LeaseToken = "wrong"
	if status(s.completeTelegramJob(ctx, j.JobId, bad)) != 409 {
		t.Fatal("wrong lease accepted")
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE telegram_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, j.JobId); err != nil {
		t.Fatal(err)
	}
	if status(s.completeTelegramJob(ctx, j.JobId, sentInput(t, j, j.ChatId))) != 409 {
		t.Fatal("expired ack accepted")
	}
	again, err := s.claimTelegramJobs(ctx, wire.ClaimInput{Limit: 1})
	if err != nil || len(again.Jobs) != 1 || again.Jobs[0].JobId != j.JobId || again.Jobs[0].LeaseToken == j.LeaseToken {
		t.Fatal("lost ack was not reclaimed", err)
	}
	j = again.Jobs[0]
	if err = s.completeTelegramJob(ctx, j.JobId, sentInput(t, j, j.ChatId)); err != nil {
		t.Fatal(err)
	}
	if err = s.completeTelegramJob(ctx, j.JobId, sentInput(t, j, j.ChatId)); err != nil {
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
	out, err = s.claimTelegramJobs(ctx, wire.ClaimInput{Limit: 1})
	if err != nil || len(out.Jobs) != 1 || out.Jobs[0].Payload.Status == "pending" {
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
	results := make(chan wire.ClaimResult, 5)
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.claimTelegramJobs(ctx, wire.ClaimInput{Limit: 1})
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
		if len(out.Jobs) > 1 {
			t.Fatal("batch >1")
		}
		for _, j := range out.Jobs {
			if seen[j.JobId] {
				t.Fatal("duplicate live lease")
			}
			seen[j.JobId] = true
		}
	}
	if len(seen) != 2 {
		t.Fatal("lost operator delivery")
	}
}
