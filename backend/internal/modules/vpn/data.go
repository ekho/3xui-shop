package vpn

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"example.com/cabinet/backend/internal/modules/vpn/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

type ProvisionArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (ProvisionArgs) Kind() string { return "trial_provision" }

type AccessArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (AccessArgs) Kind() string { return "access_operation" }

type TrialReservation struct {
	ID, AccountID, RequestID       uuid.UUID
	PeriodDays, TrafficGb, Devices int64
	PanelID                        string
	CreatedAt                      time.Time
}
type TrialState struct {
	ID, AccountID, RequestID                           uuid.UUID
	Status, PanelID                                    string
	PeriodDays, TrafficGb, Devices                     int64
	FirstStartedAt, ObservedAt                         *time.Time
	CreatedAt                                          time.Time
	Target, ProfileSnapshot                            json.RawMessage
	TrafficUpBytes, TrafficDownBytes, TrafficUsedBytes *int64
}
type AccessState struct {
	ID, AccountID                   uuid.UUID
	OperatorAccountID               *uuid.UUID
	Kind, Status, Reason            string
	Desired, Target, CompletedSteps json.RawMessage
	CreatedAt, UpdatedAt            time.Time
	ReviewReason                    *string
}
type TrialMetadata struct {
	ID, AccountID, RequestID uuid.UUID
	Status                   string
	CreatedAt                time.Time
}
type AccessWrite struct {
	ID, AccountID             uuid.UUID
	OperatorAccountID, PlanID *uuid.UUID
	Kind, Reason, Step        string
	Revision, PeriodDays      *int64
	Desired, Target           json.RawMessage
	Immediate                 bool
	CreatedAt                 time.Time
}

func stamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
func digest(s string) []byte               { v := sha256.Sum256([]byte(s)); return v[:] }
func timePtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}
func intPtr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
func textPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
func trialState(v store.TrialOperation) TrialState {
	return TrialState{ID: v.ID, AccountID: v.AccountID, RequestID: v.RequestID, Status: v.Status, PanelID: v.PanelID, PeriodDays: v.PeriodDays, TrafficGb: v.TrafficGb, Devices: v.Devices, FirstStartedAt: timePtr(v.FirstStartedAt), ObservedAt: timePtr(v.ObservedAt), CreatedAt: v.CreatedAt.Time, Target: v.Target, ProfileSnapshot: v.ProfileSnapshot, TrafficUpBytes: intPtr(v.TrafficUpBytes), TrafficDownBytes: intPtr(v.TrafficDownBytes), TrafficUsedBytes: intPtr(v.TrafficUsedBytes)}
}
func accessState(v store.AccessOperation) AccessState {
	return AccessState{ID: v.ID, AccountID: v.AccountID, OperatorAccountID: v.OperatorAccountID, Kind: v.Kind, Status: v.Status, Reason: v.Reason, Desired: v.Desired, Target: v.Target, CompletedSteps: v.CompletedSteps, CreatedAt: v.CreatedAt.Time, UpdatedAt: v.UpdatedAt.Time, ReviewReason: textPtr(v.ReviewReason)}
}
func (s *Service) queries(tx pgx.Tx) *store.Queries {
	if tx == nil {
		return store.New(s.pool)
	}
	return store.New(tx)
}
func (s *Service) ReserveTrialTx(ctx context.Context, tx pgx.Tx, in TrialReservation) error {
	if s.queue == nil || s.queue() == nil {
		return unavailable()
	}
	if e := s.queries(tx).AddOperation(ctx, store.AddOperationParams{ID: in.ID, AccountID: in.AccountID, RequestID: in.RequestID, PeriodDays: in.PeriodDays, TrafficGb: in.TrafficGb, Devices: in.Devices, PanelID: in.PanelID, CreatedAt: stamp(in.CreatedAt)}); e != nil {
		return e
	}
	_, e := s.queue().InsertTx(ctx, tx, ProvisionArgs{OperationID: in.ID}, &river.InsertOpts{Queue: "provision", MaxAttempts: 5})
	return e
}
func (s *Service) TrialStateTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (TrialState, error) {
	v, e := s.queries(tx).OperationByID(ctx, id)
	return trialState(v), e
}
func (s *Service) AccountTrialState(ctx context.Context, account uuid.UUID) (TrialState, error) {
	v, e := store.New(s.pool).AccountOperation(ctx, account)
	return trialState(v), e
}
func (s *Service) UnresolvedAccessTx(ctx context.Context, tx pgx.Tx, a uuid.UUID) (bool, error) {
	return s.queries(tx).UnresolvedAccessExists(ctx, a)
}
func (s *Service) UnresolvedTrialTx(ctx context.Context, tx pgx.Tx, a uuid.UUID) (bool, error) {
	return s.queries(tx).UnresolvedTrialExists(ctx, a)
}
func (s *Service) UnresolvedTx(ctx context.Context, tx pgx.Tx, a uuid.UUID) (bool, error) {
	v, e := s.UnresolvedAccessTx(ctx, tx, a)
	if e != nil || v {
		return v, e
	}
	return s.UnresolvedTrialTx(ctx, tx, a)
}
func (s *Service) RequeueTrialTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if s.queue == nil || s.queue() == nil {
		return unavailable()
	}
	if e := s.queries(tx).RequeueOperation(ctx, id); e != nil {
		return e
	}
	_, e := s.queue().InsertTx(ctx, tx, ProvisionArgs{OperationID: id}, &river.InsertOpts{Queue: "provision", MaxAttempts: 5})
	return e
}
func (s *Service) LatestAccessState(ctx context.Context, a uuid.UUID) (AccessState, error) {
	v, e := store.New(s.pool).LatestAccessOperation(ctx, a)
	return accessState(v), e
}
func (s *Service) LatestAppliedAccessState(ctx context.Context, a uuid.UUID) (AccessState, error) {
	v, e := store.New(s.pool).LatestAppliedAccess(ctx, a)
	return accessState(v), e
}
func (s *Service) AccessStateForAccountTx(ctx context.Context, tx pgx.Tx, id, a uuid.UUID) (AccessState, error) {
	v, e := s.queries(tx).AccessOperationForAccount(ctx, store.AccessOperationForAccountParams{ID: id, AccountID: a})
	return accessState(v), e
}
func (s *Service) TrialMetadata(ctx context.Context, ids []uuid.UUID) ([]TrialMetadata, error) {
	rows, e := store.New(s.pool).TrialMetadata(ctx, ids)
	if e != nil {
		return nil, e
	}
	out := make([]TrialMetadata, 0, len(rows))
	for _, r := range rows {
		out = append(out, TrialMetadata{ID: r.ID, AccountID: r.AccountID, RequestID: r.RequestID, Status: r.Status, CreatedAt: r.CreatedAt.Time})
	}
	return out, nil
}
func accessConflict(err error) error {
	var e *pgconn.PgError
	if errors.As(err, &e) && e.Code == "23505" && e.ConstraintName == "access_one_unresolved_account" {
		return failure(409, "ACCESS_OPERATION_CONFLICT")
	}
	return unavailable()
}
func nullableInt(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}
func (s *Service) QueueAccessTx(ctx context.Context, tx pgx.Tx, in AccessWrite) (AccessState, error) {
	if in.Immediate {
		if _, e := tx.Exec(ctx, "INSERT INTO access_operations(id,account_id,operator_account_id,execution_actor_id,kind,status,reason,plan_id,plan_revision,period_days,desired,target,completed_steps,created_at,updated_at) VALUES($1,$2,$3,$3,$4,'applied',$5,$6,$7,$8,$9,$10,jsonb_build_array($11::text),$12,$12)", in.ID, in.AccountID, in.OperatorAccountID, in.Kind, in.Reason, in.PlanID, nullableInt(in.Revision), nullableInt(in.PeriodDays), in.Desired, in.Target, in.Step, in.CreatedAt); e != nil {
			return AccessState{}, accessConflict(e)
		}
	} else {
		if s.queue == nil || s.queue() == nil {
			return AccessState{}, unavailable()
		}
		if e := s.queries(tx).InsertAccessOperation(ctx, store.InsertAccessOperationParams{ID: in.ID, AccountID: in.AccountID, OperatorAccountID: in.OperatorAccountID, Kind: in.Kind, Reason: in.Reason, PlanID: in.PlanID, PlanRevision: nullableInt(in.Revision), PeriodDays: nullableInt(in.PeriodDays), Desired: in.Desired, Target: in.Target, CreatedAt: stamp(in.CreatedAt)}); e != nil {
			return AccessState{}, accessConflict(e)
		}
		if _, e := s.queue().InsertTx(ctx, tx, AccessArgs{OperationID: in.ID}, &river.InsertOpts{Queue: "provision", MaxAttempts: 5}); e != nil {
			return AccessState{}, unavailable()
		}
	}
	return s.AccessStateForAccountTx(ctx, tx, in.ID, in.AccountID)
}
func (s *Service) RequeueAccessTx(ctx context.Context, tx pgx.Tx, id, a, actor uuid.UUID, ack bool, at time.Time) (AccessState, error) {
	q := s.queries(tx)
	r, e := q.AccessOperationForAccount(ctx, store.AccessOperationForAccountParams{ID: id, AccountID: a})
	if errors.Is(e, pgx.ErrNoRows) {
		return AccessState{}, failure(404, "INVALID_INPUT")
	}
	if e != nil {
		return AccessState{}, unavailable()
	}
	if r.Status != "needs_review" {
		return AccessState{}, failure(409, "ACCESS_OPERATION_CONFLICT")
	}
	if !r.ResetStarted && ack {
		return AccessState{}, failure(400, "INVALID_INPUT")
	}
	if s.queue == nil || s.queue() == nil {
		return AccessState{}, unavailable()
	}
	n, e := q.RequeueAccess(ctx, store.RequeueAccessParams{ID: id, ResetAcknowledged: ack, ExecutionActorID: actor, UpdatedAt: stamp(at)})
	if e != nil || n != 1 {
		return AccessState{}, unavailable()
	}
	if _, e = s.queue().InsertTx(ctx, tx, AccessArgs{OperationID: id}, &river.InsertOpts{Queue: "provision", MaxAttempts: 5}); e != nil {
		return AccessState{}, unavailable()
	}
	r.Status = "pending"
	r.UpdatedAt = stamp(at)
	r.ReviewReason = pgtype.Text{}
	return accessState(r), nil
}
func (s *Service) CheckMonthlyResetTx(ctx context.Context, tx pgx.Tx) error {
	if s.queue == nil || s.queue() == nil {
		return unavailable()
	}
	if _, e := s.monthlyZone(); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, "SELECT 1 FROM monthly_reset_periods LIMIT 0")
	return e
}
