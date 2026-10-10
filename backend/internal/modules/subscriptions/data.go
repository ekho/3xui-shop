package subscriptions

import (
	"context"
	"errors"
	"strconv"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/subscriptions/internal/store"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Service) requireOperator(ctx context.Context, actor uuid.UUID) error {
	return accountError(s.accounts.RequireOperator(ctx, actor))
}
func (s *Service) CanRequestTrial(ctx context.Context, a accounts.Snapshot) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return false, unavailable()
	}
	defer tx.Rollback(ctx)
	return s.canRequestTrial(ctx, tx, store.New(tx), a)
}
func (s *Service) RecordTrialOutcomeTx(ctx context.Context, tx pgx.Tx, request, operation uuid.UUID, status string) error {
	q := store.New(tx)
	r, e := q.TrialByID(ctx, request)
	if e != nil || r.OperationID == nil || *r.OperationID != operation || r.Status != "approved" {
		return unavailable()
	}
	a, e := s.accounts.LookupTx(ctx, tx, r.AccountID)
	if e != nil {
		return unavailable()
	}
	action, kind, view := "provision_needs_review", "provision_review", "needs_review"
	if status == "applied" {
		if r.DecisionSource == "telegram_auto" && s.config().ReferredTrialApplied != nil {
			if s.config().ReferredTrialApplied(ctx, tx, a.ID, request, operation) != nil {
				return unavailable()
			}
		}
		if q.GrantApplied(ctx, store.GrantAppliedParams{OperationID: operation, GrantedAt: stamp(s.now())}) != nil {
			return unavailable()
		}
		action, kind, view = "provision_applied", "provision_applied", "active"
	} else if status != "needs_review" {
		return unavailable()
	}
	if s.audit(ctx, tx, action, a.ID, &request, &operation, 0, "") != nil || s.notify(ctx, tx, a, r, kind, view) != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) CardTx(ctx context.Context, tx pgx.Tx, request uuid.UUID, actor int64) (TelegramPayload, error) {
	q := store.New(tx)
	r, e := q.TrialByID(ctx, request)
	if e != nil {
		return TelegramPayload{}, unavailable()
	}
	a, e := s.accounts.LookupTx(ctx, tx, r.AccountID)
	if e != nil {
		return TelegramPayload{}, unavailable()
	}
	state := r.Status
	if r.OperationID != nil {
		op, e := s.vpn.TrialStateTx(ctx, tx, *r.OperationID)
		if e != nil {
			return TelegramPayload{}, unavailable()
		}
		switch op.Status {
		case "pending", "provisioning":
			state = "provisioning"
		case "needs_review":
			state = "needs_review"
		case "applied":
			state = "active"
			if op.FirstStartedAt != nil && !s.now().Before(op.FirstStartedAt.Add(time.Duration(op.PeriodDays)*24*time.Hour)) {
				state = "expired"
			}
		}
	}
	return s.payload(ctx, tx, a, r, actor, state)
}
func (s *Service) TrialHistory(ctx context.Context, actor, target uuid.UUID, before *time.Time, id *uuid.UUID) ([]OperatorTrialRequest, bool, error) {
	if e := s.requireOperator(ctx, actor); e != nil {
		return nil, false, e
	}
	if _, e := s.operatorClientAccount(ctx, actor, target); e != nil {
		return nil, false, e
	}
	var cursor pgtype.Timestamptz
	var key uuid.UUID
	if (before == nil) != (id == nil) || id != nil && *id == uuid.Nil {
		return nil, false, failure(400, "INVALID_INPUT")
	}
	if before != nil {
		cursor = stamp(*before)
		key = *id
	}
	rows, e := store.New(s.pool).OperatorTrialPage(ctx, store.OperatorTrialPageParams{AccountID: target, BeforeCreatedAt: cursor, BeforeID: key})
	if e != nil {
		return nil, false, unavailable()
	}
	more := len(rows) > 50
	if more {
		rows = rows[:50]
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if r.OperationID != nil {
			ids = append(ids, *r.OperationID)
		}
	}
	metadata, e := s.vpn.TrialMetadata(ctx, ids)
	if e != nil {
		return nil, false, unavailable()
	}
	operations := make(map[uuid.UUID]vpn.TrialMetadata, len(metadata))
	for _, o := range metadata {
		operations[o.ID] = o
	}
	out := make([]OperatorTrialRequest, 0, len(rows))
	for _, r := range rows {
		v := OperatorTrialRequest{RequestId: r.ID, Status: r.Status, Comment: r.Comment, CreatedAt: r.CreatedAt.Time, OperationId: r.OperationID, PreviousRequestId: r.PreviousRequestID, OperatorAccountId: r.OperatorAccountID}
		if r.DecidedAt.Valid {
			v.DecidedAt = &r.DecidedAt.Time
		}
		if r.Reason.Valid {
			v.Reason = &r.Reason.String
		}
		if r.OperatorTgID.Valid {
			id := strconv.FormatInt(r.OperatorTgID.Int64, 10)
			v.OperatorTgId = &id
		}
		if r.OperationID != nil {
			if o, ok := operations[*r.OperationID]; ok {
				v.Operation = &OperatorOperation{OperationId: o.ID, Status: o.Status, CreatedAt: o.CreatedAt}
			}
		}
		out = append(out, v)
	}
	return out, more, nil
}
func (s *Service) operatorClientAccount(ctx context.Context, actor, target uuid.UUID) (accounts.Snapshot, error) {
	if e := s.requireOperator(ctx, actor); e != nil {
		return accounts.Snapshot{}, e
	}
	if target == uuid.Nil {
		return accounts.Snapshot{}, failure(400, "INVALID_INPUT")
	}
	a, e := s.accountByID(ctx, target)
	if errors.Is(e, pgx.ErrNoRows) {
		return a, failure(404, "INVALID_INPUT")
	}
	if e != nil {
		return a, unavailable()
	}
	return a, nil
}
func (s *Service) OperatorSubscription(ctx context.Context, actor, target uuid.UUID) (Subscription, error) {
	a, e := s.operatorClientAccount(ctx, actor, target)
	if e != nil {
		return Subscription{}, e
	}
	var out Subscription
	if a.Restricted {
		out, e = s.restrictedOperatorSubscription(ctx, a)
	} else {
		out, e = s.Subscription(ctx, target)
	}
	out.VpnBanned = a.VpnBanned
	if out.AccessProfile == "" {
		if a.AccessProfile != nil {
			out.AccessProfile = *a.AccessProfile
		} else {
			out.AccessProfile = "unknown"
		}
	}
	return out, e
}
func (s *Service) readTrialProfile(ctx context.Context, op vpn.TrialState) (vpn.ProfileRead, error) {
	grant, e := store.New(s.pool).GrantStatus(ctx, store.GrantStatusParams{OperationID: op.ID, AccountID: op.AccountID})
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return vpn.ProfileRead{}, vpn.ErrPanel
	}
	if e != nil || grant != "granted" {
		return vpn.ProfileRead{}, vpn.ErrIdentity
	}
	return s.vpn.ReadTrialProfile(ctx, op)
}

func (s *Service) PendingOperatorTrials(ctx context.Context, actor uuid.UUID) ([]PendingOperatorTrial, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.accounts.LockOperatorPair(ctx, tx, actor, actor); err != nil {
		return nil, false, accountError(err)
	}
	rows, err := store.New(tx).PendingOperatorTrials(ctx)
	if err != nil {
		return nil, false, unavailable()
	}
	more := len(rows) > 50
	if more {
		rows = rows[:50]
	}
	out := make([]PendingOperatorTrial, 0, len(rows))
	for _, r := range rows {
		out = append(out, PendingOperatorTrial{AccountID: r.AccountID, RequestID: r.ID, CreatedAt: r.CreatedAt.Time})
	}
	if tx.Commit(ctx) != nil {
		return nil, false, unavailable()
	}
	return out, more, nil
}
