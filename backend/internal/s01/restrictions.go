package s01

import (
	"context"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"strings"
	"time"
)

func (s *Service) SetOperatorRestriction(ctx context.Context, actor, target, key uuid.UUID, in wire.OperatorRestrictionInput) (wire.OperatorRestrictionResult, error) {
	var out wire.OperatorRestrictionResult
	reason := strings.TrimSpace(in.Reason)
	if actor == uuid.Nil || target == uuid.Nil || key == uuid.Nil || !validText(reason, 1, 1000) {
		return out, failure(400, "INVALID_INPUT")
	}
	if actor == target {
		return out, failure(403, "OPERATOR_ACCOUNT_PROTECTED")
	}
	if err := s.RequireSupportOperator(ctx, actor); err != nil {
		return out, err
	}
	principal := "operator-account:" + actor.String()
	hash := bodyHash(struct {
		Target uuid.UUID
		Input  wire.OperatorRestrictionInput
	}{target, in})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	a, err := s.lockOperatorPair(ctx, tx, actor, target)
	if err != nil {
		return out, err
	}
	q := store.New(tx)
	if err := q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "setOperatorRestriction", Key: key}); err != nil {
		return out, unavailable()
	}
	if prior, found, replayErr := replay[wire.OperatorRestrictionResult](ctx, q, principal, "setOperatorRestriction", key, hash); found || replayErr != nil {
		return prior, replayErr
	}
	protected, err := q.OperatorRoleExists(ctx, target)
	if err != nil {
		return out, unavailable()
	}
	if protected {
		return out, failure(403, "OPERATOR_ACCOUNT_PROTECTED")
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	if a.Restricted != in.Restricted {
		if err := q.SetOperatorAccountRestriction(ctx, store.SetOperatorAccountRestrictionParams{ID: target, Restricted: in.Restricted, RestrictionChangedAt: stamp(now), RestrictionOperatorAccountID: &actor}); err != nil {
			return out, unavailable()
		}
		if in.Restricted {
			if err := q.DeleteAccountSessions(ctx, target); err != nil {
				return out, unavailable()
			}
			if err := s.revokeCredentialProofs(ctx, tx, target); err != nil {
				return out, err
			}
		}
		action := "account_unrestricted"
		if in.Restricted {
			action = "account_restricted"
		}
		if err := q.AddOperatorAudit(ctx, store.AddOperatorAuditParams{ID: uuid.New(), CreatedAt: stamp(now), Action: action, AccountID: target, OperatorAccountID: &actor, Reason: pgtype.Text{String: reason, Valid: true}}); err != nil {
			return out, unavailable()
		}
		a.RestrictionChangedAt = stamp(now)
		a.RestrictionOperatorAccountID = &actor
	}
	out = wire.OperatorRestrictionResult{Restricted: in.Restricted, OperatorAccountId: a.RestrictionOperatorAccountID}
	if a.RestrictionChangedAt.Valid {
		out.ChangedAt = &a.RestrictionChangedAt.Time
	}
	if err := s.saveIdempotency(ctx, q, principal, "setOperatorRestriction", key, hash, out); err != nil {
		return out, err
	}
	if err := tx.Commit(ctx); err != nil {
		return out, unavailable()
	}
	return out, nil
}
