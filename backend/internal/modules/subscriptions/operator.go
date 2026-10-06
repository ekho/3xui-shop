package subscriptions

import (
	"context"
	"errors"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/subscriptions/internal/store"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func operatorClient(a accounts.Snapshot) OperatorClient {
	out := OperatorClient{
		AccountId: a.ID, DisplayName: stringValue(a.DisplayName),
		HadSubscription: a.HadSubscription, Kind: OperatorClientKind(a.Kind),
		Locale: OperatorClientLocale(a.Locale), Restricted: a.Restricted,
		VpnBanned: a.VpnBanned,
	}
	if a.CreatedAt != nil {
		out.CreatedAt = &(*a.CreatedAt)
	}
	if a.EmailKey != nil {
		email := string(stringValue(a.EmailKey))
		out.Email = &email
	}
	if a.TelegramID != nil {
		id := strconv.FormatInt((*a.TelegramID), 10)
		out.TelegramId = &id
	}
	return out
}

func (s *Service) DecideOperatorTrial(ctx context.Context, actor, id, key uuid.UUID, in OperatorDecisionInput) (OperatorDecisionResult, bool, error) {
	var out OperatorDecisionResult
	if err := s.requireOperator(ctx, actor); err != nil {
		return out, false, err
	}
	if id == uuid.Nil || key == uuid.Nil || (in.Decision != "approve" && in.Decision != "reject") ||
		!validText(in.Reason, 0, 1000) || (in.Decision == "reject" && !validText(in.Reason, 1, 1000)) {
		return out, false, failure(400, "INVALID_INPUT")
	}
	principal := "operator-account:" + actor.String()
	hash := bodyHash(struct {
		RequestID uuid.UUID
		Input     OperatorDecisionInput
	}{id, in})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, false, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	r, err := q.TrialByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, false, unavailable()
	}
	a, err := s.lockOperatorPair(ctx, tx, actor, r.AccountID)
	if err != nil {
		return out, false, err
	}
	r, err = q.LockTrial(ctx, id)
	if err != nil {
		return out, false, unavailable()
	}
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "decideTrialRequest", Key: key}); err != nil {
		return out, false, unavailable()
	}
	if prior, found, replayErr := replay[OperatorDecisionResult](ctx, q, principal, "decideTrialRequest", key, hash); found || replayErr != nil {
		return prior, false, replayErr
	}
	desired := "approved"
	if in.Decision == "reject" {
		desired = "rejected"
	}
	if r.Status != "pending" && r.Status != desired {
		return out, false, &Error{Status: 409, Code: "REQUEST_STATE_CONFLICT", Details: map[string]any{"current_request_status": r.Status, "operation_id": r.OperationID}}
	}
	created := r.Status == "pending"
	if created {
		r, err = s.decideTrialLocked(ctx, tx, q, a, r, trialActor{accountID: &actor}, string(in.Decision), in.Reason)
		if err != nil {
			return out, false, err
		}
	}
	out = OperatorDecisionResult{Request: publicTrial(r), OperationId: r.OperationID}
	if err = s.saveIdempotency(ctx, q, principal, "decideTrialRequest", key, hash, out); err != nil {
		return out, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, false, unavailable()
	}
	return out, created, nil
}

func (s *Service) ReconsiderOperatorTrial(ctx context.Context, actor, id, key uuid.UUID, reason string) (TrialRequest, bool, error) {
	var out TrialRequest
	if err := s.requireOperator(ctx, actor); err != nil {
		return out, false, err
	}
	if id == uuid.Nil || key == uuid.Nil || !validText(reason, 1, 1000) {
		return out, false, failure(400, "INVALID_INPUT")
	}
	principal := "operator-account:" + actor.String()
	hash := bodyHash(struct {
		RequestID uuid.UUID
		Reason    string
	}{id, reason})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, false, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	old, err := q.TrialByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, false, unavailable()
	}
	a, err := s.lockOperatorPair(ctx, tx, actor, old.AccountID)
	if err != nil {
		return out, false, err
	}
	old, err = q.LockTrial(ctx, id)
	if err != nil {
		return out, false, unavailable()
	}
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "reconsiderTrialRequest", Key: key}); err != nil {
		return out, false, unavailable()
	}
	if prior, found, replayErr := replay[TrialRequest](ctx, q, principal, "reconsiderTrialRequest", key, hash); found || replayErr != nil {
		return prior, false, replayErr
	}
	r, err := s.reconsiderTrialLocked(ctx, q, a, old, trialActor{accountID: &actor}, reason)
	if err != nil {
		return out, false, err
	}
	out = publicTrial(r)
	if err = s.saveIdempotency(ctx, q, principal, "reconsiderTrialRequest", key, hash, out); err != nil {
		return out, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, false, unavailable()
	}
	return out, true, nil
}

func (s *Service) ReconcileOperatorTrial(ctx context.Context, actor, id, key uuid.UUID, reason string) (ReconcileResult, bool, error) {
	var out ReconcileResult
	if err := s.requireOperator(ctx, actor); err != nil {
		return out, false, err
	}
	if id == uuid.Nil || key == uuid.Nil || !validText(reason, 1, 1000) {
		return out, false, failure(400, "INVALID_INPUT")
	}
	principal := "operator-account:" + actor.String()
	hash := bodyHash(struct {
		OperationID uuid.UUID
		Reason      string
	}{id, reason})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, false, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	op, err := s.vpn.TrialStateTx(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, false, unavailable()
	}
	a, err := s.lockOperatorPair(ctx, tx, actor, op.AccountID)
	if err != nil {
		return out, false, err
	}
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "reconcileTrialOperation", Key: key}); err != nil {
		return out, false, unavailable()
	}
	if prior, found, replayErr := replay[ReconcileResult](ctx, q, principal, "reconcileTrialOperation", key, hash); found || replayErr != nil {
		return prior, false, replayErr
	}
	out, err = s.reconcileOperationLocked(ctx, tx, q, a, op, trialActor{accountID: &actor}, reason)
	if err != nil {
		return out, false, err
	}
	if err = s.saveIdempotency(ctx, q, principal, "reconcileTrialOperation", key, hash, out); err != nil {
		return out, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, false, unavailable()
	}
	return out, true, nil
}

func (s *Service) CreateTelegramTrial(ctx context.Context, actor, key uuid.UUID, in OperatorTelegramTrialInput) (OperatorTelegramTrialResult, bool, error) {
	var out OperatorTelegramTrialResult
	if err := s.requireOperator(ctx, actor); err != nil {
		return out, false, err
	}
	telegramID, err := parseTelegramID(in.TelegramId)
	if err != nil || key == uuid.Nil || !validOperatorName(in.DisplayName) || (in.Locale != "ru" && in.Locale != "en") {
		return out, false, failure(400, "INVALID_INPUT")
	}
	principal := "operator-account:" + actor.String()
	hash := bodyHash(in)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, false, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	// New target has no row to lock. The role account is locked before the
	// identity-specific advisory lock and insert; uniqueness remains in PG.
	if _, err = s.lockOperatorPair(ctx, tx, actor, actor); err != nil {
		return out, false, err
	}
	if err = q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "createTelegramTrial", Key: key}); err != nil {
		return out, false, unavailable()
	}
	if prior, found, replayErr := replay[OperatorTelegramTrialResult](ctx, q, principal, "createTelegramTrial", key, hash); found || replayErr != nil {
		return prior, false, replayErr
	}
	if err = s.accounts.CheckTelegramAvailable(ctx, tx, telegramID); err != nil {
		return out, false, accountError(err)
	}
	if !s.config().TrialEnabled {
		return out, false, failure(403, "TRIAL_DISABLED")
	}
	if s.config().PanelID == "" {
		return out, false, unavailable()
	}
	identity, err := s.accounts.CreateTelegram(ctx, tx, accounts.TelegramInput{TelegramID: telegramID, DisplayName: in.DisplayName, Locale: string(in.Locale)})
	if err != nil {
		return out, false, accountError(err)
	}
	a := identity
	id := a.ID
	r, err := q.AddTrial(ctx, store.AddTrialParams{ID: uuid.New(), AccountID: id, Comment: "", CreatedAt: stamp(s.now())})
	if err != nil {
		return out, false, unavailable()
	}
	r, err = s.decideTrialLocked(ctx, tx, q, a, r, trialActor{accountID: &actor}, "approve", "")
	if err != nil {
		return out, false, err
	}
	if r.OperationID == nil {
		return out, false, unavailable()
	}
	out = OperatorTelegramTrialResult{Client: operatorClient(a), Request: publicTrial(r), OperationId: *r.OperationID}
	if err = s.saveIdempotency(ctx, q, principal, "createTelegramTrial", key, hash, out); err != nil {
		return out, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, false, unavailable()
	}
	return out, true, nil
}

func validOperatorName(name string) bool {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 128 || strings.TrimSpace(name) == "" || strings.ContainsRune(name, '\x00') {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func parseTelegramID(value string) (int64, error) {
	if value == "" || len(value) > 19 || value[0] < '1' || value[0] > '9' {
		return 0, failure(400, "INVALID_INPUT")
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, failure(400, "INVALID_INPUT")
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, failure(400, "INVALID_INPUT")
	}
	return id, nil
}
