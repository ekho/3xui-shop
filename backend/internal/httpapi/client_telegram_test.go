package httpapi

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/telegram"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestClientTelegramOutbox(t *testing.T) {
	for _, mode := range []string{"sent", "lost", "forbidden", "skipped", "lease", "expired", "short-lease", "metadata", "invalid-result"} {
		t.Run(mode, func(t *testing.T) {
			_, svc, e, _ := bridgeFixture(t)
			ctx := context.Background()
			a, _, err := svc.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 701, DisplayName: "Fixture", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
			if err != nil {
				t.Fatal(err)
			}
			n := notifications.ClientNotice{AccountID: a.Account.ID, TelegramID: 701, CredentialVersion: a.Account.CredentialVersion, Locale: "ru", EventKey: "fixture:outcome", Route: "cabinet"}
			for range 2 {
				tx, err := e.Pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				if err = svc.Notifications.EnqueueClientTx(ctx, tx, n, e.Clock()); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
			rollback, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rollback.Rollback(ctx)
			discarded := n
			discarded.EventKey = "fixture:rollback"
			if err = svc.Notifications.EnqueueClientTx(ctx, rollback, discarded, e.Clock()); err != nil {
				t.Fatal(err)
			}
			rollback.Rollback(ctx)
			var count int
			if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM client_telegram_deliveries").Scan(&count); err != nil || count != 1 {
				t.Fatal("enqueue/replay/rollback lost atomicity", err, count)
			}
			j, err := svc.Notifications.ClaimClient(ctx)
			if err != nil || j == nil || len(j.LeaseToken) != 43 || j.TelegramID != 701 {
				t.Fatal("claim", err)
			}
			// The database clock, not the independently controlled business clock, owns delivery eligibility.
			if !j.LeaseExpiresAt.After(time.Now().Add(-5 * time.Minute)) {
				t.Fatal("lease used business clock")
			}
			switch mode {
			case "skipped":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET telegram_login_disabled=true WHERE id=$1", a.Account.ID)
			case "lease":
				j.LeaseToken = strings.Repeat("x", 43)
			case "expired":
				_, err = e.Pool.Exec(ctx, "UPDATE client_telegram_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", j.ID)
			case "short-lease":
				_, err = e.Pool.Exec(ctx, "UPDATE client_telegram_deliveries SET lease_expires_at=clock_timestamp()+interval '5 seconds' WHERE id=$1", j.ID)
			case "metadata":
				j.Route = "support"
			}
			if err != nil {
				t.Fatal(err)
			}
			sends := 0
			lost := errors.New("owned lost response")
			send := func() (notifications.ClientOutcome, error) {
				sends++
				switch mode {
				case "lost":
					return notifications.ClientOutcome{}, lost
				case "forbidden":
					return notifications.ClientOutcome{State: "failed", Code: "forbidden"}, nil
				case "invalid-result":
					return notifications.ClientOutcome{State: "sent", MessageID: 0}, nil
				default:
					return notifications.ClientOutcome{State: "sent", MessageID: 42}, nil
				}
			}
			err = svc.Notifications.DeliverClient(ctx, *j, send)
			wantState, wantSends := "sent", 1
			switch mode {
			case "lost":
				wantState = "pending"
				if !errors.Is(err, lost) {
					t.Fatal("unknown send falsely completed", err)
				}
			case "forbidden":
				wantState = "failed"
			case "skipped":
				wantState, wantSends = "skipped", 0
			case "lease", "expired", "short-lease", "metadata":
				wantState, wantSends = "pending", 0
				if err == nil {
					t.Fatal("invalid lease/metadata accepted")
				}
			case "invalid-result":
				wantState = "pending"
				if err == nil {
					t.Fatal("invalid transport result accepted")
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			var state string
			if err = e.Pool.QueryRow(ctx, "SELECT state FROM client_telegram_deliveries WHERE id=$1", j.ID).Scan(&state); err != nil || state != wantState || sends != wantSends {
				t.Fatal("delivery outcome", err, state, sends)
			}
			if mode == "sent" {
				if err = svc.Notifications.DeliverClient(ctx, *j, send); err != nil || sends != 1 {
					t.Fatal("completed replay resent notice", err, sends)
				}
			}
			if mode == "lost" {
				if _, err = e.Pool.Exec(ctx, "UPDATE client_telegram_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", j.ID); err != nil {
					t.Fatal(err)
				}
				// A freshly assembled owner, using the persisted job, recovers after restart.
				restarted := notifications.New(e.Pool, func() []int64 { return []int64{101} }, svc.Accounts.OperatorAllowed, nil, svc.Accounts.WithTelegramDelivery)
				recovered, err := restarted.ClaimClient(ctx)
				if err != nil || recovered == nil || recovered.ID != j.ID || recovered.LeaseToken == j.LeaseToken {
					t.Fatal("restart lost pending delivery", err)
				}
				if err = restarted.DeliverClient(ctx, *recovered, func() (notifications.ClientOutcome, error) {
					return notifications.ClientOutcome{State: "sent", MessageID: 43}, nil
				}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestClientTelegramDomainNotifications(t *testing.T) {
	ctx := context.Background()
	t.Run("trial decision replay", func(t *testing.T) {
		bridge, svc, e, request := bridgeFixture(t)
		var target uuid.UUID
		if err := e.Pool.QueryRow(ctx, "SELECT account_id FROM trial_requests WHERE id=$1", request).Scan(&target); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET telegram_id=701 WHERE id=$1", target); err != nil {
			t.Fatal(err)
		}
		in := telegram.TrialDecision{RequestID: request, ActorID: 101, Action: "approve", CallbackID: "client-trial"}
		for range 2 {
			if _, err := bridge.Decide(ctx, in); err != nil {
				t.Fatal(err)
			}
		}
		var count int
		if err := e.Pool.QueryRow(ctx, "SELECT count(*) FROM client_telegram_deliveries WHERE account_id=$1 AND route='cabinet'", target).Scan(&count); err != nil || count != 1 {
			t.Fatal("trial result was disconnected/duplicated", err, count)
		}
		_ = svc
	})
	t.Run("funded applied outcome", func(t *testing.T) {
		s, e, account, plan := purchaseFixture(t)
		panelFixture(t, s)
		order, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
		if err != nil {
			t.Fatal(err)
		}
		applied := completeRenewalPayment(t, s, account, order)
		if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET telegram_id=702 WHERE id=$1", account); err != nil {
			t.Fatal(err)
		}
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err = s.payments.RecordPurchaseAccessTx(ctx, tx, *applied.AccessOperationId, "applied", ""); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		var count int
		if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM client_telegram_deliveries WHERE account_id=$1 AND route=$2", account, "orders:"+order.OrderId.String()).Scan(&count); err != nil || count != 1 {
			t.Fatal("applied payment result disconnected/duplicated", err, count)
		}
	})
	t.Run("operator text and attachment replies", func(t *testing.T) {
		_, svc, e, _ := bridgeFixture(t)
		a, _, err := svc.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 703, DisplayName: "Fixture", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
		if err != nil {
			t.Fatal(err)
		}
		op := uuid.New()
		if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,password_hash,verified_at,locale,vpn_id,sub_id,panel_key,terms_version,privacy_version)VALUES($1,'operator-fixture@example.test','fixture',now(),'ru',$2,'optelegramnotice','op-notice','1','1')`, op, uuid.New()); err != nil {
			t.Fatal(err)
		}
		if _, err = e.Pool.Exec(ctx, "INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,now())", op); err != nil {
			t.Fatal(err)
		}
		for _, file := range []bool{false, true} {
			key := uuid.New()
			text, name, data := "Private support text", "", []byte(nil)
			if file {
				text, name, data = "", "proof.png", []byte("owned fixture bytes")
			}
			for range 2 {
				if _, _, err = svc.Support.CreateSupportMessage(ctx, op, a.Account.ID, true, key, text, name, data); err != nil {
					t.Fatal(err)
				}
			}
		}
		var count int
		if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM client_telegram_deliveries WHERE account_id=$1 AND route='support'", a.Account.ID).Scan(&count); err != nil || count != 2 {
			t.Fatal("support result disconnected/duplicated", err, count)
		}
	})
}

// A credential/binding change must not turn an idempotent business-result replay into a failed transaction.
func TestClientTelegramOutboxCredentialReplay(t *testing.T) {
	_, svc, e, _ := bridgeFixture(t)
	ctx := context.Background()
	a, _, err := svc.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 701, DisplayName: "Fixture", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	n := notifications.ClientNotice{AccountID: a.Account.ID, TelegramID: 701, CredentialVersion: a.Account.CredentialVersion, Locale: "ru", EventKey: "fixture:stable-event", Route: "cabinet"}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = svc.Notifications.EnqueueClientTx(ctx, tx, n, e.Clock()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET credential_version=credential_version+1 WHERE id=$1", a.Account.ID); err != nil {
		t.Fatal(err)
	}
	n.CredentialVersion++
	tx, err = e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = svc.Notifications.EnqueueClientTx(ctx, tx, n, e.Clock()); err != nil {
		t.Fatal("credential change broke domain replay", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	var version int64
	if err = e.Pool.QueryRow(ctx, "SELECT count(*),min(credential_version) FROM client_telegram_deliveries WHERE account_id=$1", a.Account.ID).Scan(&count, &version); err != nil || count != 1 || version != 0 {
		t.Fatal("replay moved old recipient proof", err, count, version)
	}
}
