package httpapi

import (
	"bytes"
	"context"
	"errors"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/telegram"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"strings"
	"testing"
)

func bridgeFixture(t *testing.T) (*app.TrialBridge, *compositionFixture, *testkit.Env, uuid.UUID) {
	t.Helper()
	e := testkit.Open(t)
	queue, err := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{HTTP: app.HTTPConfig{CabinetOrigin: "https://cabinet.example.test"}, Accounts: accounts.Config{TermsVersion: "1", PrivacyVersion: "1", CodeKey: bytes.Repeat([]byte{2}, 32), RateNamespace: uuid.NewString(), Operators: []int64{101, 202}}, Mail: notifications.MailConfig{MailKey: bytes.Repeat([]byte{1}, 32)}, Subscriptions: subscriptions.Config{PanelID: "dedicated-test", TrialEnabled: true, TrialPeriodDays: 3, TrialTrafficGB: 15, TrialDevices: 1}}
	svc := composeForTest(e.Pool, e.Redis, queue, cfg)
	id := uuid.New()
	_, err = e.Pool.Exec(context.Background(), `INSERT INTO accounts (id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) VALUES ($1,'trial@example.test','ru','fixture',now(),$2,'0123456789abcdef',$3,'1','1')`, id, uuid.New(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := svc.createTrialRequest(context.Background(), id, uuid.New(), wire.TrialRequestInput{})
	if err != nil {
		t.Fatal(err)
	}
	// The host and Docker clocks need not agree; make the fixture deliverable
	// using the database clock that owns lease eligibility.
	if _, err = e.Pool.Exec(context.Background(), `UPDATE telegram_deliveries SET available_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	return app.NewTrialBridge(svc.Subscriptions, svc.Notifications), svc, e, r.RequestId
}

func TestTrialBridgeDecisionReplay(t *testing.T) {
	b, _, e, id := bridgeFixture(t)
	in := telegram.TrialDecision{RequestID: id, ActorID: 101, Action: "approve", CallbackID: "replayed"}
	a, err := b.Decide(context.Background(), in)
	if err != nil || a.Trial.OperationID == nil {
		t.Fatal("decision failed", err)
	}
	second, err := b.Decide(context.Background(), in)
	if err != nil || second.Trial.OperationID == nil || *second.Trial.OperationID != *a.Trial.OperationID || a.Card.RequestID != id || a.Card.Email != "trial@example.test" {
		t.Fatal("replay/card mapping", err)
	}
	var grants, operations int
	if err = e.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM trial_grants),(SELECT count(*) FROM trial_operations)`).Scan(&grants, &operations); err != nil || grants != 1 || operations != 1 {
		t.Fatal("duplicate grant", err)
	}
	_, err = b.Decide(context.Background(), telegram.TrialDecision{RequestID: id, ActorID: 202, Action: "reject", CallbackID: "opposite"})
	var action *telegram.ActionError
	if !errors.As(err, &action) || action.Code != "REQUEST_STATE_CONFLICT" || action.CurrentRequestStatus != "approved" {
		t.Fatal("conflict mapping", err)
	}
}

func TestTrialBridgeUnauthorized(t *testing.T) {
	b, _, e, id := bridgeFixture(t)
	_, err := b.Decide(context.Background(), telegram.TrialDecision{RequestID: id, ActorID: 999, Action: "approve", CallbackID: "unauthorized"})
	var action *telegram.ActionError
	if !errors.As(err, &action) || action.Code != "INVALID_CREDENTIALS" {
		t.Fatal("unauthorized actor", err)
	}
	var count int
	if err = e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM trial_grants`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unauthorized grant", err)
	}
}

func TestTrialBridgeLeaseMapping(t *testing.T) {
	b, _, _, id := bridgeFixture(t)
	d, err := b.Claim(context.Background())
	if err != nil || d == nil || d.Card.RequestID != id || (d.ChatID != 101 && d.ChatID != 202) || len(d.LeaseToken) != 43 || d.LeaseExpiresAt.IsZero() {
		t.Fatal("lease mapping", err)
	}
	out := telegram.DeliveryOutcome{Kind: "sent", ChatID: d.ChatID, MessageID: 42}
	if err = b.Complete(context.Background(), *d, out); err != nil {
		t.Fatal(err)
	}
	if err = b.Complete(context.Background(), *d, out); err != nil {
		t.Fatal("result replay", err)
	}
	d.LeaseToken = strings.Repeat("x", 43)
	if err = b.Complete(context.Background(), *d, out); err == nil || err.Error() != "REQUEST_STATE_CONFLICT" {
		t.Fatal("wrong lease accepted", err)
	}
}
