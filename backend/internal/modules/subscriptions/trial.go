package subscriptions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/notifications"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/subscriptions/internal/store"

	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"math"
	"time"

	"example.com/cabinet/backend/internal/modules/vpn"
)

func (s *Service) operatorAllowed(actor int64) bool { return s.accounts.OperatorAllowed(actor) }

// TrialMode preserves the account's original registration policy across linking.
func (s *Service) TrialMode(a accounts.Snapshot) string {
	if a.SourceKind == "telegram" && a.LegacyUserID == nil {
		return "activate"
	}
	return "request"
}

func (s *Service) automaticTrialSource(a accounts.Snapshot) error {
	if s.TrialMode(a) != "activate" {
		return failure(403, "TRIAL_APPROVAL_REQUIRED")
	}
	if a.Restricted {
		return failure(403, "ACCOUNT_RESTRICTED")
	}
	if a.VpnBanned || !accounts.SourceEligible(a) || a.TelegramID == nil || a.TelegramLoginDisabled || a.PolicyAcceptedAt == nil || stringValue(a.TermsVersion) == "" || stringValue(a.PrivacyVersion) == "" {
		return failure(403, "TRIAL_UNAVAILABLE")
	}
	return nil
}

// ActivateTelegramTrial shares the existing grant and provision pipeline.
func (s *Service) ActivateTelegramTrial(ctx context.Context, accountID, key uuid.UUID) (TrialRequest, bool, error) {
	var out TrialRequest
	if accountID == uuid.Nil || key == uuid.Nil {
		return out, false, failure(400, "INVALID_INPUT")
	}
	principal, operation := "account:"+accountID.String(), "activateTelegramTrial"
	hash := bodyHash(struct{}{})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, false, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: operation, Key: key}) != nil {
		return out, false, unavailable()
	}
	a, err := s.lockAccount(ctx, tx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, false, unavailable()
	}
	if err = s.automaticTrialSource(a); err != nil {
		return out, false, err
	}
	if prior, found, e := replay[TrialRequest](ctx, q, principal, operation, key, hash); found || e != nil {
		return prior, false, e
	}
	if err = s.trialEligibility(ctx, q, a); err != nil {
		return out, false, err
	}
	current, err := q.CurrentTrial(ctx, accountID)
	if err == nil {
		if current.Status == "rejected" {
			return out, false, failure(409, "TRIAL_RECONSIDERATION_REQUIRED")
		}
		return out, false, failure(409, "REQUEST_STATE_CONFLICT")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, false, unavailable()
	}
	current, err = q.AddTrial(ctx, store.AddTrialParams{ID: uuid.New(), AccountID: accountID, CreatedAt: stamp(s.now())})
	if err != nil {
		return out, false, unavailable()
	}
	current, err = s.decideTrialLocked(ctx, tx, q, a, current, trialActor{automatic: true}, "approve", "")
	if err != nil {
		return out, false, err
	}
	out = publicTrial(current)
	if err = s.saveIdempotency(ctx, q, principal, operation, key, hash, out); err != nil {
		return out, false, err
	}
	if tx.Commit(ctx) != nil {
		return out, false, unavailable()
	}
	return out, true, nil
}

func publicTrial(r store.TrialRequest) TrialRequest {
	var decided *time.Time
	if r.DecidedAt.Valid {
		decided = &r.DecidedAt.Time
	}
	return TrialRequest{RequestId: r.ID, Status: TrialRequestStatus(r.Status), CreatedAt: r.CreatedAt.Time, DecidedAt: decided, OperationId: r.OperationID, PreviousRequestId: r.PreviousRequestID}
}
func (s *Service) trialEligibility(ctx context.Context, q *store.Queries, a accounts.Snapshot) error {
	if a.Kind == "web" && (!(a.VerifiedAt != nil) || !(a.EmailKey != nil) || !a.PasswordSet) {
		return failure(403, "EMAIL_VERIFICATION_REQUIRED")
	}
	if !accounts.SourceEligible(a) {
		return failure(403, "INVALID_CREDENTIALS")
	}
	if a.Restricted {
		return failure(403, "ACCOUNT_RESTRICTED")
	}
	grant, err := q.HasGrant(ctx, a.ID)
	if err != nil {
		return unavailable()
	}
	if grant || a.HadSubscription || (a.AssignedPanelID != nil) {
		return failure(409, "TRIAL_ALREADY_USED")
	}
	if !s.config().TrialEnabled {
		return failure(403, "TRIAL_DISABLED")
	}
	return nil
}
func (s *Service) audit(ctx context.Context, tx pgx.Tx, action string, account uuid.UUID, requestID, operationID *uuid.UUID, actor int64, reason string) error {
	var operator *int64
	if actor > 0 {
		operator = &actor
	}
	var text *string
	if reason != "" {
		text = &reason
	}
	if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: action, AccountID: account, RequestID: requestID, OperationID: operationID, OperatorTgID: operator, Reason: text}) != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) payload(ctx context.Context, tx pgx.Tx, a accounts.Snapshot, r store.TrialRequest, actor int64, status string) (TelegramPayload, error) {
	var target *int64
	if actor > 0 {
		message, err := s.notifications.LatestTelegramMessageTx(ctx, tx, r.ID, actor)
		if err != nil {
			return TelegramPayload{}, unavailable()
		}
		target = message
	}
	var email *string
	if a.EmailKey != nil {
		value := string(stringValue(a.EmailKey))
		email = &value
	}
	out := TelegramPayload{RequestId: r.ID, OperationId: r.OperationID, TargetMessageId: target, Email: email, Comment: r.Comment, CreatedAt: r.CreatedAt.Time, Status: TelegramPayloadStatus(status)}
	if a.Kind == "telegram" {
		name := stringValue(a.DisplayName)
		id := fmt.Sprintf("%d", (*a.TelegramID))
		out.DisplayName = &name
		out.TelegramId = &id
	}
	return out, nil
}
func (s *Service) notify(ctx context.Context, tx pgx.Tx, a accounts.Snapshot, r store.TrialRequest, kind, status string) error {
	if kind != "approval_card" && a.TelegramID != nil && !a.TelegramLoginDisabled {
		notice := notifications.ClientNotice{AccountID: a.ID, TelegramID: *a.TelegramID, CredentialVersion: a.CredentialVersion, Locale: a.Locale, EventKey: "trial:" + r.ID.String() + ":" + kind + ":" + status, Route: "cabinet"}
		if s.notifications.EnqueueClientTx(ctx, tx, notice, s.now()) != nil {
			return unavailable()
		}
	}
	seen := map[int64]bool{}
	if len(s.config().Operators) == 0 {
		return nil
	}
	for _, actor := range s.config().Operators {
		if actor <= 0 {
			return unavailable()
		}
		if seen[actor] {
			continue
		}
		seen[actor] = true
		payload, err := s.payload(ctx, tx, a, r, actor, status)
		if err != nil {
			return err
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return unavailable()
		}
		if s.notifications.EnqueueTelegramTx(ctx, tx, r.ID, r.OperationID, actor, kind, data, s.now()) != nil {
			return unavailable()
		}
	}
	return nil
}
func (s *Service) CreateTrialRequest(ctx context.Context, accountID, key uuid.UUID, in TrialRequestInput) (TrialRequest, bool, error) {
	out := TrialRequest{}
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
	a, err := s.lockAccount(ctx, tx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, false, unavailable()
	}
	if prior, found, e := replay[TrialRequest](ctx, q, principal, "createTrialRequest", key, hash); found || e != nil {
		return prior, false, e
	}
	if err = s.trialEligibility(ctx, q, a); err != nil {
		return out, false, err
	}
	if len(s.config().Operators) == 0 {
		available, err := s.accounts.AnyWebOperator(ctx, tx)
		if err != nil {
			return out, false, unavailable()
		}
		if !available {
			return out, false, unavailable()
		}
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
		if s.audit(ctx, tx, "trial_requested", accountID, &current.ID, nil, 0, "") != nil || s.notify(ctx, tx, a, current, "approval_card", "pending") != nil {
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
func (s *Service) CurrentTrialRequest(ctx context.Context, accountID uuid.UUID) (CurrentTrialRequest, error) {
	r, err := store.New(s.pool).CurrentTrial(ctx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CurrentTrialRequest{Request: nil}, nil
	}
	if err != nil {
		return CurrentTrialRequest{}, unavailable()
	}
	out := publicTrial(r)
	return CurrentTrialRequest{Request: &out}, nil
}
func (s *Service) decisionResult(ctx context.Context, tx pgx.Tx, a accounts.Snapshot, r store.TrialRequest, actor int64) (DecisionResult, error) {
	card, err := s.payload(ctx, tx, a, r, actor, r.Status)
	if err != nil {
		return DecisionResult{}, err
	}
	state, err := s.notifications.LatestTelegramStateTx(ctx, tx, r.ID, actor)
	if err != nil {
		return DecisionResult{}, unavailable()
	}
	return DecisionResult{Request: publicTrial(r), OperationId: r.OperationID, Card: card, DeliveryState: DecisionResultDeliveryState(state)}, nil
}

type trialActor struct {
	telegramID int64
	accountID  *uuid.UUID
	automatic  bool
}

func (s *Service) trialActorAudit(ctx context.Context, tx pgx.Tx, action string, account, request uuid.UUID, operation *uuid.UUID, actor trialActor, reason string) error {
	if actor.accountID == nil {
		return s.audit(ctx, tx, action, account, &request, operation, actor.telegramID, reason)
	}
	var why *string
	if reason != "" {
		why = &reason
	}
	if err := auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: action, AccountID: account, RequestID: &request, OperationID: operation, OperatorAccountID: actor.accountID, Reason: why}); err != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) decideTrialLocked(ctx context.Context, tx pgx.Tx, q *store.Queries, a accounts.Snapshot, r store.TrialRequest, actor trialActor, decision, reason string) (store.TrialRequest, error) {
	if r.Status != "pending" {
		return r, nil
	}
	desired := "approved"
	if decision == "reject" {
		desired = "rejected"
	}
	var operation *uuid.UUID
	if decision == "approve" {
		if active, err := s.vpn.UnresolvedAccessTx(ctx, tx, a.ID); err != nil {
			return r, unavailable()
		} else if active {
			return r, failure(409, "ACCESS_OPERATION_CONFLICT")
		}
		if err := s.trialEligibility(ctx, q, a); err != nil {
			return r, err
		}
		c := s.config()
		if c.PanelID == "" || c.TrialPeriodDays <= 0 || c.TrialPeriodDays > math.MaxInt64/int64(24*time.Hour) || c.TrialTrafficGB < 0 || c.TrialTrafficGB > math.MaxInt64/(1024*1024*1024) || c.TrialDevices < 0 || c.TrialDevices == math.MaxInt64 {
			return r, unavailable()
		}
		op := uuid.New()
		operation = &op
		if err := s.vpn.ReserveTrialTx(ctx, tx, vpn.TrialReservation{ID: op, AccountID: a.ID, RequestID: r.ID, PeriodDays: c.TrialPeriodDays, TrafficGb: c.TrialTrafficGB, Devices: c.TrialDevices, PanelID: c.PanelID, CreatedAt: s.now()}); err != nil {
			return r, unavailable()
		}
		if err := q.ReserveGrant(ctx, store.ReserveGrantParams{AccountID: a.ID, RequestID: r.ID, OperationID: op, CreatedAt: stamp(s.now())}); err != nil {
			return r, unavailable()
		}
	} else if reason == "" {
		reason = "support_declined"
	}
	var err error
	why := pgtype.Text{String: reason, Valid: reason != ""}
	if actor.automatic {
		r, err = q.DecideTrialAutomatic(ctx, store.DecideTrialAutomaticParams{ID: r.ID, DecidedAt: stamp(s.now()), OperationID: operation})
	} else if actor.accountID == nil {
		r, err = q.DecideTrial(ctx, store.DecideTrialParams{ID: r.ID, Status: desired, DecidedAt: stamp(s.now()), OperatorTgID: pgtype.Int8{Int64: actor.telegramID, Valid: true}, Reason: why, OperationID: operation})
	} else {
		r, err = q.DecideTrialWeb(ctx, store.DecideTrialWebParams{ID: r.ID, Status: desired, DecidedAt: stamp(s.now()), OperatorAccountID: actor.accountID, Reason: why, OperationID: operation})
	}
	if err != nil {
		return r, unavailable()
	}
	action := "trial_" + desired
	if actor.automatic {
		action = "trial_activated_telegram"
	}
	if err = s.trialActorAudit(ctx, tx, action, a.ID, r.ID, operation, actor, reason); err != nil {
		return r, err
	}
	if err = s.notify(ctx, tx, a, r, "request_decided", desired); err != nil {
		return r, err
	}
	return r, nil
}
func (s *Service) DecideTrialRequest(ctx context.Context, id uuid.UUID, in DecisionInput) (DecisionResult, error) {
	out := DecisionResult{}
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
		Input     DecisionInput
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
	a, err := s.lockAccount(ctx, tx, r.AccountID)
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
		r, err = s.decideTrialLocked(ctx, tx, q, a, r, trialActor{telegramID: in.OperatorTgId}, string(in.Decision), reason)
		if err != nil {
			return out, err
		}
	}
	out, err = s.decisionResult(ctx, tx, a, r, in.OperatorTgId)
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
func (s *Service) ReconsiderTrialRequest(ctx context.Context, id, key uuid.UUID, in ReconsiderInput) (TrialRequest, error) {
	out := TrialRequest{}
	if !s.operatorAllowed(in.OperatorTgId) {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	if id == uuid.Nil || key == uuid.Nil || !validText(in.Reason, 1, 1000) {
		return out, failure(400, "INVALID_INPUT")
	}
	principal := fmt.Sprintf("operator:%d", in.OperatorTgId)
	hash := bodyHash(struct {
		RequestID uuid.UUID
		Input     ReconsiderInput
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
	a, err := s.lockAccount(ctx, tx, old.AccountID)
	if err != nil {
		return out, unavailable()
	}
	old, err = q.LockTrial(ctx, id)
	if err != nil {
		return out, unavailable()
	}
	if prior, found, e := replay[TrialRequest](ctx, q, principal, "reconsiderTrialRequest", key, hash); found || e != nil {
		return prior, e
	}
	r, err := s.reconsiderTrialLocked(ctx, tx, q, a, old, trialActor{telegramID: in.OperatorTgId}, in.Reason)
	if err != nil {
		return out, err
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

func (s *Service) reconsiderTrialLocked(ctx context.Context, tx pgx.Tx, q *store.Queries, a accounts.Snapshot, old store.TrialRequest, actor trialActor, reason string) (store.TrialRequest, error) {
	if err := s.trialEligibility(ctx, q, a); err != nil {
		return old, err
	}
	current, err := q.CurrentTrial(ctx, a.ID)
	if err != nil {
		return old, unavailable()
	}
	if old.Status != "rejected" || current.ID != old.ID {
		return old, failure(409, "REQUEST_STATE_CONFLICT")
	}
	r, err := q.AddTrial(ctx, store.AddTrialParams{ID: uuid.New(), AccountID: a.ID, Comment: old.Comment, CreatedAt: stamp(s.now()), PreviousRequestID: &old.ID})
	if err != nil {
		return old, unavailable()
	}
	if err = s.trialActorAudit(ctx, tx, "trial_reconsidered", a.ID, r.ID, nil, actor, reason); err != nil {
		return old, err
	}
	if err = s.notify(ctx, tx, a, r, "approval_card", "pending"); err != nil {
		return old, err
	}
	return r, nil
}

func (s *Service) canRequestTrial(ctx context.Context, q *store.Queries, account accounts.Snapshot) (bool, error) {
	if s.TrialMode(account) == "activate" {
		if err := s.automaticTrialSource(account); err != nil {
			return false, nil
		}
	} else if len(s.config().Operators) == 0 {
		available, err := s.accounts.AnyWebOperator(ctx, nil)
		if err != nil {
			return false, unavailable()
		}
		if !available {
			return false, nil
		}
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
