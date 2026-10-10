package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/payments/internal/store"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type StarsSubscriptionControlInput struct {
	Action    string `json:"action"`
	Confirmed bool   `json:"confirmed"`
}

func (s *Service) setStarsControlTx(ctx context.Context, tx pgx.Tx, receipt string, key uuid.UUID, action, reason, source string, actor *uuid.UUID) error {
	id := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO stars_subscription_controls(id,subscription_receipt_id,idempotency_key,action,reason,source,actor_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, receipt, key, action, reason, source, actor, s.now()); err != nil {
		return unavailable()
	}
	_, err := tx.Exec(ctx, `UPDATE stars_subscriptions SET desired_action=$2,control_state='pending',latest_control_id=$3 WHERE first_receipt_id=$1`, receipt, action, id)
	if err != nil {
		return unavailable()
	}
	return nil
}

// Commands survive a lost HTTP reply. Replays return current owner facts and
// retry only a latest, unconfirmed native setter; they never resurrect an old intent.
func (s *Service) ControlStarsSubscription(ctx context.Context, account, key uuid.UUID, in StarsSubscriptionControlInput) (StarsSubscription, error) {
	var empty StarsSubscription
	if account == uuid.Nil || key == uuid.Nil || !in.Confirmed || (in.Action != "cancel" && in.Action != "resume") {
		return empty, failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	a, err := s.lockAccount(ctx, tx, account)
	if err != nil {
		return empty, unavailable()
	}
	if a.Restricted {
		return empty, failure(403, "ACCOUNT_RESTRICTED")
	}
	if !accounts.SourceEligible(a) {
		return empty, failure(403, "INVALID_CREDENTIALS")
	}
	q := store.New(tx)
	principal := "account:" + account.String()
	operation := "starsSubscriptionControl"
	hash := bodyHash(in)
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: operation, Key: key}); err != nil {
		return empty, unavailable()
	}
	_, found, err := replay[StarsSubscriptionControlInput](ctx, q, principal, operation, key, hash)
	if err != nil {
		return empty, err
	}
	if !found {
		if in.Action == "resume" {
			if err = s.allowNew(ctx, tx); err != nil {
				return empty, err
			}
		}
		subs, err := s.starsSubscriptionsTx(ctx, tx, account)
		if err != nil {
			return empty, err
		}
		if len(subs) == 0 {
			return empty, failure(409, "STARS_CONTROL_NOT_ELIGIBLE")
		}
		if in.Action == "resume" {
			eligible, err := s.starsResumeEligibleTx(ctx, tx, a, subs[0], false)
			if err != nil {
				return empty, err
			}
			if !eligible {
				return empty, failure(409, "STARS_CONTROL_NOT_ELIGIBLE")
			}
			if err = s.setStarsControlTx(ctx, tx, subs[0].key, key, "resume", "Client requested resume", "client", &account); err != nil {
				return empty, err
			}
		} else {
			for _, sub := range subs {
				if starsCancelProved(sub) || sub.desired == "cancel" {
					continue
				}
				if err = s.setStarsControlTx(ctx, tx, sub.key, key, "cancel", "Client requested cancellation", "client", &account); err != nil {
					return empty, err
				}
			}
		}
		if err = s.saveIdempotency(ctx, q, principal, operation, key, hash, in); err != nil {
			return empty, err
		}
		if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: account, CreatedAt: s.now(), Action: "stars_subscription_control_requested"}); err != nil {
			return empty, unavailable()
		}
	}
	if tx.Commit(ctx) != nil {
		return empty, unavailable()
	}
	// Native uncertainty is returned as persisted state, not an erased command.
	_ = s.applyStarsControls(ctx, account)
	return s.StarsSubscription(ctx, account)
}

func (s *Service) applyStarsControls(ctx context.Context, account uuid.UUID) error {
	owner, err := s.vpn.OpenAccessOwner(ctx, account)
	if err != nil {
		return unavailable()
	}
	defer owner.Release()
	if err = owner.TryLock(ctx); err != nil {
		if errors.Is(err, vpn.ErrBusy) {
			return nil
		}
		return unavailable()
	}
	// Snapshot once: a command that supersedes in-flight I/O waits for the next
	// pass instead of creating an unbounded native retry loop.
	subs, err := s.starsSubscriptionsTx(ctx, nil, account)
	if err != nil {
		return err
	}
	failed := false
	for _, snapshot := range subs {
		if ctx.Err() != nil {
			return unavailable()
		}
		if snapshot.latest == nil || snapshot.control == "confirmed" || snapshot.desired == "none" {
			continue
		}
		tx, err := owner.Begin(ctx)
		if err != nil {
			return unavailable()
		}
		a, err := s.lockAccount(ctx, tx, account)
		if err != nil {
			tx.Rollback(ctx)
			return unavailable()
		}
		sub, err := scanStarsSubscription(tx.QueryRow(ctx, "SELECT "+starsSubscriptionColumns+" FROM stars_subscriptions s WHERE s.first_receipt_id=$1 FOR UPDATE", snapshot.key))
		if err != nil {
			tx.Rollback(ctx)
			return unavailable()
		}
		if sub.latest == nil || *sub.latest != *snapshot.latest || sub.control == "confirmed" {
			tx.Rollback(ctx)
			continue
		}
		id, action := *sub.latest, sub.desired
		if action == "resume" {
			eligible, e := s.starsResumeEligibleTx(ctx, tx, a, sub, true)
			if e != nil {
				tx.Rollback(ctx)
				return e
			}
			if !eligible {
				if err = s.RequireStarsCancellationTx(ctx, tx, account, "Resume authority changed"); err != nil {
					tx.Rollback(ctx)
					return err
				}
				if tx.Commit(ctx) != nil {
					return unavailable()
				}
				continue
			}
		}
		if tx.Commit(ctx) != nil {
			return unavailable()
		}
		// No SQL transaction is held during provider I/O. Original payer/first
		// charge remain usable after unlink, quarantine or operator restriction.
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		nativeErr := unavailable()
		if s.stars != nil && s.stars.BotID == sub.bot && s.stars.EditSubscription != nil && (s.stars.Ready == nil || s.stars.Ready()) {
			nativeErr = s.stars.EditSubscription(callCtx, sub.payer, sub.charge, action == "cancel")
		}
		cancel()
		state, code := "confirmed", ""
		var proof []byte
		if nativeErr != nil {
			failed = true
			state, code = "uncertain", "SERVICE_UNAVAILABLE"
			var domain *Error
			if errors.As(nativeErr, &domain) && domain.Code == "STARS_CONTROL_REJECTED" {
				state, code = "rejected", "STARS_CONTROL_REJECTED"
			}
		} else {
			proof, _ = json.Marshal(starsControlProof{Provider: "telegram_stars", Result: true, BotID: sub.bot, PayerID: sub.payer, ChargeID: sub.charge, Canceled: action == "cancel", ControlID: id})
		}
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		tx, err = owner.Begin(finishCtx)
		if err == nil {
			_, err = s.lockAccount(finishCtx, tx, account)
			if err == nil {
				_, err = tx.Exec(finishCtx, `UPDATE stars_subscription_controls SET state=$2,proof=$3,error_code=NULLIF($4,''),finished_at=$5 WHERE id=$1 AND state<>'confirmed'`, id, state, proof, code, s.now())
			}
			if err == nil {
				if state == "confirmed" {
					_, err = tx.Exec(finishCtx, `UPDATE stars_subscriptions SET control_state='confirmed',bot_canceled=$3,native_proof=$4,provider_state=CASE WHEN $3 THEN provider_state ELSE 'unknown' END WHERE first_receipt_id=$1 AND latest_control_id=$2`, sub.key, id, action == "cancel", proof)
				} else {
					_, err = tx.Exec(finishCtx, `UPDATE stars_subscriptions SET control_state=$3 WHERE first_receipt_id=$1 AND latest_control_id=$2`, sub.key, id, state)
				}
			}
			if err == nil {
				err = auditreports.RecordTx(finishCtx, tx, auditreports.Event{ID: uuid.New(), AccountID: account, CreatedAt: s.now(), Action: "stars_subscription_control_" + state})
			}
			if err == nil {
				err = tx.Commit(finishCtx)
			} else {
				tx.Rollback(finishCtx)
			}
		}
		finishCancel()
		if err != nil {
			return unavailable()
		}
	}
	if failed {
		return unavailable()
	}
	return nil
}
