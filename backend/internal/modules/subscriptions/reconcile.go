package subscriptions

import (
	"context"
	"errors"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/subscriptions/internal/store"

	"fmt"

	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) ReconcileTrialOperation(ctx context.Context, id, key uuid.UUID, in ReconcileInput) (ReconcileResult, error) {
	out := ReconcileResult{}
	if !s.operatorAllowed(in.OperatorTgId) {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	if id == uuid.Nil || key == uuid.Nil || !validText(in.Reason, 1, 1000) {
		return out, failure(400, "INVALID_INPUT")
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	principal := fmt.Sprintf("operator:%d", in.OperatorTgId)
	hash := bodyHash(struct {
		ID    uuid.UUID
		Input ReconcileInput
	}{id, in})
	if q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "reconcileTrialOperation", Key: key}) != nil {
		return out, unavailable()
	}
	op, e := s.vpn.TrialStateTx(ctx, tx, id)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if e != nil {
		return out, unavailable()
	}
	a, e := s.lockAccount(ctx, tx, op.AccountID)
	if e != nil {
		return out, unavailable()
	}
	if prior, found, err := replay[ReconcileResult](ctx, q, principal, "reconcileTrialOperation", key, hash); found || err != nil {
		return prior, err
	}
	out, e = s.reconcileOperationLocked(ctx, tx, q, a, op, trialActor{telegramID: in.OperatorTgId}, in.Reason)
	if e != nil {
		return out, e
	}
	if s.saveIdempotency(ctx, q, principal, "reconcileTrialOperation", key, hash, out) != nil || tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	return out, nil
}

func (s *Service) reconcileOperationLocked(ctx context.Context, tx pgx.Tx, q *store.Queries, a accounts.Snapshot, op vpn.TrialState, actor trialActor, reason string) (ReconcileResult, error) {
	var out ReconcileResult
	id := op.ID
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('account-access:'||$1::text,0))`, a.ID.String()).Scan(&locked); err != nil {
		return out, unavailable()
	}
	current, err := s.vpn.TrialStateTx(ctx, tx, id)
	if err != nil {
		return out, unavailable()
	}
	if !locked || current.Status != "needs_review" {
		return out, failure(409, "REQUEST_STATE_CONFLICT")
	}
	if a.Restricted || !accounts.SourceEligible(a) {
		return out, failure(403, "ACCOUNT_RESTRICTED")
	}
	if err = s.vpn.RequeueTrialTx(ctx, tx, id); err != nil {
		return out, unavailable()
	}
	if err = s.trialActorAudit(ctx, tx, "provision_reconcile_requested", a.ID, op.RequestID, &id, actor, reason); err != nil {
		return out, err
	}
	return ReconcileResult{OperationId: id, Status: "provisioning"}, nil
}
