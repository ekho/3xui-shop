package accounts

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Catches resolving a valid actor once, then accepting its revoked binding or
// credentials in a later transaction (including a second subscriptions phase).
func TestTelegramPrincipalRevocation(t *testing.T) {
	for _, kind := range []string{"current", "version", "binding", "restricted", "role", "foreign_actor"} {
		t.Run(kind, func(t *testing.T) {
			s, e, cfg := fixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			actor := verified(t, s, e, cfg, "support-operator@example.test")
			target := verified(t, s, e, cfg, "support-customer@example.test")
			if err := s.ChangeOperatorRole(ctx, actor, true); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET telegram_id=711 WHERE id=$1", actor); err != nil {
				t.Fatal(err)
			}
			proof, err := s.Lookup(ctx, actor)
			if err != nil {
				t.Fatal(err)
			}
			ctx = context.WithValue(ctx, telegramActorKey{}, telegramActorProof{actor, 711, proof.CredentialVersion})
			mutation := map[string]string{
				"version": "credential_version=credential_version+1", "binding": "telegram_id=712",
				"restricted": "restricted=true",
			}[kind]
			if mutation != "" {
				// The assignments are fixed test literals, never external input.
				if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET "+mutation+" WHERE id=$1", actor); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "role" {
				if err = s.ChangeOperatorRole(context.Background(), actor, false); err != nil {
					t.Fatal(err)
				}
			}
			requestedActor := actor
			if kind == "foreign_actor" {
				requestedActor = target
				if err = s.ChangeOperatorRole(context.Background(), target, true); err != nil {
					t.Fatal(err)
				}
			}
			// Caller-Tx proof must not acquire another pool connection.
			pc := e.Pool.Config().Copy()
			pc.MaxConns = 1
			one, err := pgxpool.NewWithConfig(ctx, pc)
			if err != nil {
				t.Fatal(err)
			}
			defer one.Close()
			owner := New(one, e.Redis, nil, cfg.Config)
			tx, err := one.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			_, err = owner.LockOperatorPair(ctx, tx, requestedActor, target)
			if kind == "current" {
				if err != nil {
					t.Fatal("valid source/one-connection pair rejected", err)
				}
			} else if err == nil {
				t.Fatal("stale Telegram source reached an operator transaction")
			}
		})
	}
}

func TestTelegramPrincipalDeliveryOperatorSource(t *testing.T) {
	for _, kind := range []string{"current", "role", "version", "binding"} {
		t.Run(kind, func(t *testing.T) {
			s, e, cfg := fixture(t)
			ctx := context.Background()
			actor := verified(t, s, e, cfg, "relay-actor@example.test")
			target, _ := identityMiniFixture(t, s, 742)
			if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET telegram_id=741 WHERE id=$1", actor); err != nil {
				t.Fatal(err)
			}
			if err := s.ChangeOperatorRole(ctx, actor, true); err != nil {
				t.Fatal(err)
			}
			a, err := s.Lookup(ctx, actor)
			if err != nil {
				t.Fatal(err)
			}
			ctx = context.WithValue(ctx, telegramActorKey{}, telegramActorProof{actor, 741, a.CredentialVersion})
			switch kind {
			case "role":
				err = s.ChangeOperatorRole(context.Background(), actor, false)
			case "version":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET credential_version=credential_version+1 WHERE id=$1", actor)
			case "binding":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET telegram_id=743 WHERE id=$1", actor)
			}
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			valid, err := s.WithTelegramDelivery(ctx, target.Account.ID, 742, target.Account.CredentialVersion, func(pgx.Tx) error { calls++; return nil })
			if kind == "current" {
				if err != nil || !valid || calls != 1 {
					t.Fatal("valid source/destination rejected", err)
				}
			} else if calls != 0 {
				t.Fatal("revoked source reached recipient wire", kind)
			}
		})
	}
}

func TestTelegramPrincipalCustomerRevocation(t *testing.T) {
	for _, kind := range []string{"current", "version", "binding", "quarantine", "consent", "restricted"} {
		t.Run(kind, func(t *testing.T) {
			s, e, _ := fixture(t)
			a, _ := identityMiniFixture(t, s, 721)
			ctx := context.WithValue(context.Background(), telegramActorKey{}, telegramActorProof{a.Account.ID, 721, a.Account.CredentialVersion})
			mutation := map[string]string{"version": "credential_version=credential_version+1", "binding": "telegram_id=722", "quarantine": "telegram_login_disabled=true", "consent": "terms_version=NULL,privacy_version=NULL,policy_accepted_at=NULL", "restricted": "restricted=true"}[kind]
			if mutation != "" {
				if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET "+mutation+" WHERE id=$1", a.Account.ID); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			_, err = s.Lock(ctx, tx, a.Account.ID)
			if kind == "current" {
				if err != nil {
					t.Fatal("valid customer rejected", err)
				}
			} else if err == nil {
				t.Fatal("stale customer source reached write transaction")
			}
		})
	}
}
