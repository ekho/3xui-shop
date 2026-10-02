package s01

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/riverqueue/river"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

type ProvisionArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (ProvisionArgs) Kind() string { return "s01_provision" }
func (s *Service) operatorAllowed(actor int64) bool {
	if actor <= 0 {
		return false
	}
	for _, id := range s.cfg.Operators {
		if id == actor {
			return true
		}
	}
	return false
}
func validText(value string, min, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && utf8.RuneCountInString(strings.TrimSpace(value)) >= min
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
func publicTrial(r store.TrialRequest) wire.TrialRequest {
	var decided *time.Time
	if r.DecidedAt.Valid {
		decided = &r.DecidedAt.Time
	}
	return wire.TrialRequest{RequestId: r.ID, Status: wire.TrialRequestStatus(r.Status), CreatedAt: r.CreatedAt.Time, DecidedAt: decided, OperationId: r.OperationID, PreviousRequestId: r.PreviousRequestID}
}
func (s *Service) trialEligibility(ctx context.Context, q *store.Queries, a store.Account) error {
	if !a.VerifiedAt.Valid {
		return failure(403, "EMAIL_VERIFICATION_REQUIRED")
	}
	if a.Restricted {
		return failure(403, "ACCOUNT_RESTRICTED")
	}
	grant, err := q.HasGrant(ctx, a.ID)
	if err != nil {
		return unavailable()
	}
	if grant || a.HadSubscription || a.AssignedPanelID.Valid {
		return failure(409, "TRIAL_ALREADY_USED")
	}
	if !s.cfg.TrialEnabled {
		return failure(403, "TRIAL_DISABLED")
	}
	return nil
}
func (s *Service) audit(ctx context.Context, q *store.Queries, action string, account uuid.UUID, requestID, operationID *uuid.UUID, actor int64, reason string) error {
	var operator pgtype.Int8
	if actor > 0 {
		operator = pgtype.Int8{Int64: actor, Valid: true}
	}
	var text pgtype.Text
	if reason != "" {
		text = pgtype.Text{String: reason, Valid: true}
	}
	if q.AddAudit(ctx, store.AddAuditParams{ID: uuid.New(), CreatedAt: stamp(s.now()), Action: action, AccountID: account, RequestID: requestID, OperationID: operationID, OperatorTgID: operator, Reason: text}) != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) payload(ctx context.Context, q *store.Queries, a store.Account, r store.TrialRequest, actor int64, status string) (wire.TelegramPayload, error) {
	var target *int64
	if actor > 0 {
		message, err := q.LatestTelegramMessage(ctx, store.LatestTelegramMessageParams{RequestID: r.ID, ChatID: actor})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return wire.TelegramPayload{}, unavailable()
		}
		if message.Valid {
			target = &message.Int64
		}
	}
	var email *openapi_types.Email
	if a.EmailKey != "" {
		value := openapi_types.Email(a.EmailKey)
		email = &value
	}
	return wire.TelegramPayload{RequestId: r.ID, OperationId: r.OperationID, TargetMessageId: target, Email: email, Comment: r.Comment, CreatedAt: r.CreatedAt.Time, Status: wire.TelegramPayloadStatus(status)}, nil
}
func (s *Service) notify(ctx context.Context, q *store.Queries, a store.Account, r store.TrialRequest, kind, status string) error {
	seen := map[int64]bool{}
	if len(s.cfg.Operators) == 0 {
		return unavailable()
	}
	for _, actor := range s.cfg.Operators {
		if actor <= 0 {
			return unavailable()
		}
		if seen[actor] {
			continue
		}
		seen[actor] = true
		payload, err := s.payload(ctx, q, a, r, actor, status)
		if err != nil {
			return err
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return unavailable()
		}
		if q.AddTelegramDelivery(ctx, store.AddTelegramDeliveryParams{ID: uuid.New(), RequestID: r.ID, OperationID: r.OperationID, ChatID: actor, Kind: kind, Payload: data, CreatedAt: stamp(s.now())}) != nil {
			return unavailable()
		}
	}
	return nil
}
func (s *Service) CreateTrialRequest(ctx context.Context, accountID, key uuid.UUID, in wire.TrialRequestInput) (wire.TrialRequest, bool, error) {
	out := wire.TrialRequest{}
	comment := ""
	if in.Comment != nil {
		comment = *in.Comment
	}
	if accountID == uuid.Nil || key == uuid.Nil || !validText(comment, 0, 1000) {
		return out, false, failure(400, "INVALID_INPUT")
	}
	principal := "account:" + accountID.String()
	hash := bodyHash(in)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, false, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "createTrialRequest", Key: key}) != nil {
		return out, false, unavailable()
	}
	a, err := q.LockAccount(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, false, unavailable()
	}
	if prior, found, e := replay[wire.TrialRequest](ctx, q, principal, "createTrialRequest", key, hash); found || e != nil {
		return prior, false, e
	}
	if err = s.trialEligibility(ctx, q, a); err != nil {
		return out, false, err
	}
	current, err := q.CurrentTrial(ctx, accountID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, false, unavailable()
	}
	created := false
	if err == nil {
		if current.Status == "rejected" {
			return out, false, failure(409, "TRIAL_RECONSIDERATION_REQUIRED")
		}
		if current.Status != "pending" {
			return out, false, failure(409, "TRIAL_ALREADY_USED")
		}
	} else {
		current, err = q.AddTrial(ctx, store.AddTrialParams{ID: uuid.New(), AccountID: accountID, Comment: comment, CreatedAt: stamp(s.now())})
		if err != nil {
			return out, false, unavailable()
		}
		created = true
		if s.audit(ctx, q, "trial_requested", accountID, &current.ID, nil, 0, "") != nil || s.notify(ctx, q, a, current, "approval_card", "pending") != nil {
			return out, false, unavailable()
		}
	}
	out = publicTrial(current)
	if err = s.saveIdempotency(ctx, q, principal, "createTrialRequest", key, hash, out); err != nil {
		return out, false, err
	}
	if tx.Commit(ctx) != nil {
		return out, false, unavailable()
	}
	return out, created, nil
}
func (s *Service) CurrentTrialRequest(ctx context.Context, accountID uuid.UUID) (wire.CurrentTrialRequest, error) {
	r, err := store.New(s.pool).CurrentTrial(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return wire.CurrentTrialRequest{Request: nil}, nil
	}
	if err != nil {
		return wire.CurrentTrialRequest{}, unavailable()
	}
	out := publicTrial(r)
	return wire.CurrentTrialRequest{Request: &out}, nil
}
func (s *Service) decisionResult(ctx context.Context, q *store.Queries, a store.Account, r store.TrialRequest, actor int64) (wire.DecisionResult, error) {
	card, err := s.payload(ctx, q, a, r, actor, r.Status)
	if err != nil {
		return wire.DecisionResult{}, err
	}
	state, err := q.LatestTelegramState(ctx, store.LatestTelegramStateParams{RequestID: r.ID, ChatID: actor})
	if errors.Is(err, pgx.ErrNoRows) {
		state = "pending"
	} else if err != nil {
		return wire.DecisionResult{}, unavailable()
	}
	return wire.DecisionResult{Request: publicTrial(r), OperationId: r.OperationID, Card: card, DeliveryState: wire.DecisionResultDeliveryState(state)}, nil
}
func (s *Service) DecideTrialRequest(ctx context.Context, id uuid.UUID, in wire.DecisionInput) (wire.DecisionResult, error) {
	out := wire.DecisionResult{}
	if !s.operatorAllowed(in.OperatorTgId) {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	reason := ""
	if in.Reason != nil {
		reason = *in.Reason
	}
	if id == uuid.Nil || (in.Decision != "approve" && in.Decision != "reject") || !validText(in.CallbackQueryId, 1, 128) || !validText(reason, 0, 1000) {
		return out, failure(400, "INVALID_INPUT")
	}
	hash := bodyHash(struct {
		RequestID uuid.UUID
		Input     wire.DecisionInput
	}{id, in})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if q.LockDecisionCallback(ctx, in.CallbackQueryId) != nil {
		return out, unavailable()
	}
	r, err := q.TrialByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, unavailable()
	}
	a, err := q.LockAccount(ctx, r.AccountID)
	if err != nil {
		return out, unavailable()
	}
	r, err = q.LockTrial(ctx, id)
	if err != nil {
		return out, unavailable()
	}
	callback, err := q.CallbackByID(ctx, in.CallbackQueryId)
	if err == nil {
		if callback.RequestID != id || callback.OperatorTgID != in.OperatorTgId || !bytes.Equal(callback.BodyHash, hash) {
			return out, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		if json.Unmarshal(callback.Result, &out) != nil {
			return out, unavailable()
		}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, unavailable()
	}
	desired := "approved"
	if in.Decision == "reject" {
		desired = "rejected"
	}
	if r.Status != "pending" && r.Status != desired {
		var op any
		if r.OperationID != nil {
			op = *r.OperationID
		}
		return out, &Error{Status: 409, Code: "REQUEST_STATE_CONFLICT", Message: "REQUEST_STATE_CONFLICT", Details: map[string]any{"current_request_status": r.Status, "operation_id": op}}
	}
	if r.Status == "pending" {
		var operation *uuid.UUID
		if in.Decision == "approve" {
			if err = s.trialEligibility(ctx, q, a); err != nil {
				return out, err
			}
			c := s.cfg
			if c.PanelID == "" || c.TrialPeriodDays <= 0 || c.TrialPeriodDays > math.MaxInt64/int64(24*time.Hour) || c.TrialTrafficGB < 0 || c.TrialTrafficGB > math.MaxInt64/(1024*1024*1024) || c.TrialDevices < 0 || c.TrialDevices == math.MaxInt64 {
				return out, unavailable()
			}
			op := uuid.New()
			operation = &op
			if q.AddOperation(ctx, store.AddOperationParams{ID: op, AccountID: a.ID, RequestID: id, PeriodDays: c.TrialPeriodDays, TrafficGb: c.TrialTrafficGB, Devices: c.TrialDevices, PanelID: c.PanelID, CreatedAt: stamp(s.now())}) != nil {
				return out, unavailable()
			}
			if q.ReserveGrant(ctx, store.ReserveGrantParams{AccountID: a.ID, RequestID: id, OperationID: op, CreatedAt: stamp(s.now())}) != nil {
				return out, unavailable()
			}
			if _, err = s.queue.InsertTx(ctx, tx, ProvisionArgs{OperationID: op}, &river.InsertOpts{Queue: "provision", MaxAttempts: 5}); err != nil {
				return out, unavailable()
			}
		} else if reason == "" {
			reason = "support_declined"
		}
		r, err = q.DecideTrial(ctx, store.DecideTrialParams{ID: id, Status: desired, DecidedAt: stamp(s.now()), OperatorTgID: pgtype.Int8{Int64: in.OperatorTgId, Valid: true}, Reason: pgtype.Text{String: reason, Valid: reason != ""}, OperationID: operation})
		if err != nil {
			return out, unavailable()
		}
		if s.audit(ctx, q, "trial_"+desired, a.ID, &id, operation, in.OperatorTgId, reason) != nil || s.notify(ctx, q, a, r, "request_decided", desired) != nil {
			return out, unavailable()
		}
	}
	out, err = s.decisionResult(ctx, q, a, r, in.OperatorTgId)
	if err != nil {
		return out, err
	}
	result, err := json.Marshal(out)
	if err != nil {
		return out, unavailable()
	}
	if q.AddCallback(ctx, store.AddCallbackParams{ID: in.CallbackQueryId, RequestID: id, OperatorTgID: in.OperatorTgId, BodyHash: hash, Result: result, CreatedAt: stamp(s.now())}) != nil || tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	return out, nil
}
func (s *Service) ReconsiderTrialRequest(ctx context.Context, id, key uuid.UUID, in wire.ReconsiderInput) (wire.TrialRequest, error) {
	out := wire.TrialRequest{}
	if !s.operatorAllowed(in.OperatorTgId) {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	if id == uuid.Nil || key == uuid.Nil || !validText(in.Reason, 1, 1000) {
		return out, failure(400, "INVALID_INPUT")
	}
	principal := fmt.Sprintf("operator:%d", in.OperatorTgId)
	hash := bodyHash(struct {
		RequestID uuid.UUID
		Input     wire.ReconsiderInput
	}{id, in})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "reconsiderTrialRequest", Key: key}) != nil {
		return out, unavailable()
	}
	old, err := q.TrialByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, unavailable()
	}
	a, err := q.LockAccount(ctx, old.AccountID)
	if err != nil {
		return out, unavailable()
	}
	old, err = q.LockTrial(ctx, id)
	if err != nil {
		return out, unavailable()
	}
	if prior, found, e := replay[wire.TrialRequest](ctx, q, principal, "reconsiderTrialRequest", key, hash); found || e != nil {
		return prior, e
	}
	if err = s.trialEligibility(ctx, q, a); err != nil {
		return out, err
	}
	current, err := q.CurrentTrial(ctx, a.ID)
	if err != nil {
		return out, unavailable()
	}
	if old.Status != "rejected" || current.ID != id {
		return out, failure(409, "REQUEST_STATE_CONFLICT")
	}
	r, err := q.AddTrial(ctx, store.AddTrialParams{ID: uuid.New(), AccountID: a.ID, Comment: old.Comment, CreatedAt: stamp(s.now()), PreviousRequestID: &id})
	if err != nil {
		return out, unavailable()
	}
	if s.audit(ctx, q, "trial_reconsidered", a.ID, &r.ID, nil, in.OperatorTgId, in.Reason) != nil || s.notify(ctx, q, a, r, "approval_card", "pending") != nil {
		return out, unavailable()
	}
	out = publicTrial(r)
	if err = s.saveIdempotency(ctx, q, principal, "reconsiderTrialRequest", key, hash, out); err != nil {
		return out, err
	}
	if tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	return out, nil
}

func (s *Service) canRequestTrial(ctx context.Context, q *store.Queries, account store.Account) (bool, error) {
	if len(s.cfg.Operators) == 0 {
		return false, nil
	}
	if err := s.trialEligibility(ctx, q, account); err != nil {
		var domain *Error
		if errors.As(err, &domain) && domain.Status != 503 {
			return false, nil
		}
		return false, err
	}
	_, err := q.CurrentTrial(ctx, account.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, unavailable()
	}
	return false, nil
}
