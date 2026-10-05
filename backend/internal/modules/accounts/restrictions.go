package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Service) SetOperatorRestriction(ctx context.Context, actor, target, key uuid.UUID, in OperatorRestrictionInput) (OperatorRestrictionResult, error) {
	var out OperatorRestrictionResult
	reason := strings.TrimSpace(in.Reason)
	if actor == uuid.Nil || target == uuid.Nil || key == uuid.Nil || !validText(reason, 1, 1000) {
		return out, failure(400, "INVALID_INPUT")
	}
	if actor == target {
		return out, failure(403, "OPERATOR_ACCOUNT_PROTECTED")
	}
	if err := s.RequireOperator(ctx, actor); err != nil {
		return out, err
	}
	principal := "operator-account:" + actor.String()
	hash := bodyHash(struct {
		Target uuid.UUID
		Input  OperatorRestrictionInput
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
	if prior, found, replayErr := replay[OperatorRestrictionResult](ctx, q, principal, "setOperatorRestriction", key, hash); found || replayErr != nil {
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
	out = OperatorRestrictionResult{Restricted: in.Restricted, OperatorAccountId: a.RestrictionOperatorAccountID}
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

func validText(value string, min, max int) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && utf8.RuneCountInString(value) <= max && utf8.RuneCountInString(strings.TrimSpace(value)) >= min
}

func bodyHash(value any) []byte { b, _ := json.Marshal(value); return digest(string(b)) }

func replay[T any](ctx context.Context, q *store.Queries, principal, operation string, key uuid.UUID, hash []byte) (T, bool, error) {
	var out T
	record, err := q.IdempotencyByKey(ctx, store.IdempotencyByKeyParams{Principal: principal, Operation: operation, Key: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, unavailable()
	}
	if !bytes.Equal(record.BodyHash, hash) {
		return out, false, failure(409, "IDEMPOTENCY_CONFLICT")
	}
	if json.Unmarshal(record.Result, &out) != nil {
		return out, false, unavailable()
	}
	return out, true, nil
}

func (s *Service) saveIdempotency(ctx context.Context, q *store.Queries, principal, operation string, key uuid.UUID, hash []byte, out any) error {
	result, err := json.Marshal(out)
	if err != nil {
		return unavailable()
	}
	if q.AddIdempotency(ctx, store.AddIdempotencyParams{Principal: principal, Operation: operation, Key: key, BodyHash: hash, Result: result, CreatedAt: stamp(s.now())}) != nil {
		return unavailable()
	}
	return nil
}
