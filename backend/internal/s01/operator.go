package s01

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Every web-operator mutation takes account locks in UUID order, then the role.
// The CLI revoker takes its account lock before deleting that same role.
func (s *Service) lockOperatorPair(ctx context.Context, tx pgx.Tx, actor, target uuid.UUID) (store.Account, error) {
	var empty store.Account
	if actor == uuid.Nil || target == uuid.Nil {
		return empty, failure(400, "INVALID_INPUT")
	}
	first, second := actor, target
	if bytes.Compare(actor[:], target[:]) > 0 {
		first, second = target, actor
	}
	ids := []uuid.UUID{first}
	if second != first {
		ids = append(ids, second)
	}
	q := store.New(tx)
	var targetAccount store.Account
	for _, id := range ids {
		a, err := q.LockAccount(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, failure(404, "INVALID_INPUT")
		}
		if err != nil {
			return empty, unavailable()
		}
		if id == actor {
			if a.Restricted {
				return empty, failure(403, "ACCOUNT_RESTRICTED")
			}
			if a.Kind != "web" || !a.VerifiedAt.Valid || !a.EmailKey.Valid || !a.PasswordHash.Valid {
				return empty, failure(403, "INVALID_CREDENTIALS")
			}
		}
		if id == target {
			targetAccount = a
		}
	}
	if _, err := q.LockOperatorRole(ctx, actor); errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(403, "INVALID_CREDENTIALS")
	} else if err != nil {
		return empty, unavailable()
	}
	return targetAccount, nil
}

func operatorClient(a store.Account) wire.OperatorClient {
	out := wire.OperatorClient{
		AccountId: a.ID, DisplayName: a.DisplayName.String,
		HadSubscription: a.HadSubscription, Kind: wire.OperatorClientKind(a.Kind),
		Locale: wire.OperatorClientLocale(a.Locale), Restricted: a.Restricted,
		VpnBanned: a.VpnBanned,
	}
	if a.CreatedAt.Valid {
		out.CreatedAt = &a.CreatedAt.Time
	}
	if a.EmailKey.Valid {
		email := openapi_types.Email(a.EmailKey.String)
		out.Email = &email
	}
	if a.TelegramID.Valid {
		id := strconv.FormatInt(a.TelegramID.Int64, 10)
		out.TelegramId = &id
	}
	return out
}

func operatorTrial(r store.OperatorTrialPageRow) wire.OperatorTrialRequest {
	out := wire.OperatorTrialRequest{
		RequestId: r.ID, Status: wire.OperatorTrialRequestStatus(r.Status),
		Comment: r.Comment, CreatedAt: r.CreatedAt.Time,
		OperationId: r.OperationID, PreviousRequestId: r.PreviousRequestID,
		OperatorAccountId: r.OperatorAccountID,
	}
	if r.DecidedAt.Valid {
		out.DecidedAt = &r.DecidedAt.Time
	}
	if r.Reason.Valid {
		out.Reason = &r.Reason.String
	}
	if r.OperatorTgID.Valid {
		id := strconv.FormatInt(r.OperatorTgID.Int64, 10)
		out.OperatorTgId = &id
	}
	if r.OperationID != nil && r.OperationStatus.Valid && r.OperationCreatedAt.Valid {
		out.Operation = &wire.OperatorOperation{OperationId: *r.OperationID, Status: wire.OperatorOperationStatus(r.OperationStatus.String), CreatedAt: r.OperationCreatedAt.Time}
	}
	return out
}
func operatorAudit(e store.AuditEvent) wire.OperatorAuditEvent {
	out := wire.OperatorAuditEvent{Id: e.ID, CreatedAt: e.CreatedAt.Time, Action: e.Action,
		RequestId: e.RequestID, OperationId: e.OperationID,
		OperatorAccountId: e.OperatorAccountID, SupportMessageId: e.SupportMessageID}
	if e.OperatorTgID.Valid {
		id := strconv.FormatInt(e.OperatorTgID.Int64, 10)
		out.OperatorTgId = &id
	}
	if e.Reason.Valid {
		out.Reason = &e.Reason.String
	}
	return out
}
func operatorTrialRows(rows []store.OperatorTrialPageRow) ([]wire.OperatorTrialRequest, bool) {
	more := len(rows) > 50
	if more {
		rows = rows[:50]
	}
	out := make([]wire.OperatorTrialRequest, 0, len(rows))
	for _, row := range rows {
		out = append(out, operatorTrial(row))
	}
	return out, more
}
func operatorAuditRows(rows []store.AuditEvent) ([]wire.OperatorAuditEvent, bool) {
	more := len(rows) > 50
	if more {
		rows = rows[:50]
	}
	out := make([]wire.OperatorAuditEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, operatorAudit(row))
	}
	return out, more
}
func legacySnapshot(r store.LegacyApprovalSnapshot) *wire.OperatorLegacyApproval {
	out := &wire.OperatorLegacyApproval{SourceLegacyUserId: strconv.FormatInt(r.SourceLegacyUserID, 10), SourceTgId: strconv.FormatInt(r.SourceTgID, 10), Status: wire.OperatorLegacyApprovalStatus(r.Status)}
	if r.RequestedAt.Valid {
		out.RequestedAt = &r.RequestedAt.Time
	}
	if r.DecidedAt.Valid {
		out.DecidedAt = &r.DecidedAt.Time
	}
	if r.DecidedBy.Valid {
		id := strconv.FormatInt(r.DecidedBy.Int64, 10)
		out.DecidedBy = &id
	}
	return out
}
func legacyEvent(r store.LegacyApprovalEvent) wire.OperatorLegacyApprovalEvent {
	out := wire.OperatorLegacyApprovalEvent{SourceId: strconv.FormatInt(r.SourceID, 10), TargetTgId: strconv.FormatInt(r.TargetTgID, 10), CreatedAt: r.CreatedAt.Time, Action: wire.OperatorLegacyApprovalEventAction(r.Action)}
	if r.Source.Valid {
		out.Source = &r.Source.String
	}
	if r.ActorType.Valid {
		out.ActorType = &r.ActorType.String
	}
	if r.ActorID.Valid {
		id := strconv.FormatInt(r.ActorID.Int64, 10)
		out.ActorId = &id
	}
	if r.ActorName.Valid {
		out.ActorName = &r.ActorName.String
	}
	return out
}
func legacyEventRows(rows []store.LegacyApprovalEvent) ([]wire.OperatorLegacyApprovalEvent, bool) {
	more := len(rows) > 50
	if more {
		rows = rows[:50]
	}
	out := make([]wire.OperatorLegacyApprovalEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, legacyEvent(r))
	}
	return out, more
}

func (s *Service) SearchOperatorClients(ctx context.Context, actor uuid.UUID, in wire.OperatorSearchInput) (wire.OperatorSearchResult, error) {
	out := wire.OperatorSearchResult{Clients: []wire.OperatorClient{}, Page: in.Page, PerPage: in.PerPage}
	if err := s.RequireSupportOperator(ctx, actor); err != nil {
		return out, err
	}
	if !utf8.ValidString(in.Q) || strings.ContainsRune(in.Q, '\x00') || utf8.RuneCountInString(in.Q) > 256 ||
		in.Page < 1 || in.Page > math.MaxInt32 || in.PerPage < 1 || in.PerPage > 50 {
		return out, failure(400, "INVALID_INPUT")
	}
	q := store.New(s.pool)
	count, err := q.CountOperatorClients(ctx, in.Q)
	if err != nil {
		return out, unavailable()
	}
	rows, err := q.SearchOperatorClients(ctx, store.SearchOperatorClientsParams{Query: in.Q, PageLimit: int32(in.PerPage), PageOffset: int64(in.Page-1) * int64(in.PerPage)})
	if err != nil {
		return out, unavailable()
	}
	out.Total = count
	for _, a := range rows {
		out.Clients = append(out.Clients, operatorClient(a))
	}
	return out, nil
}

func (s *Service) operatorClientAccount(ctx context.Context, actor, target uuid.UUID) (store.Account, error) {
	var empty store.Account
	if err := s.RequireSupportOperator(ctx, actor); err != nil {
		return empty, err
	}
	if target == uuid.Nil {
		return empty, failure(400, "INVALID_INPUT")
	}
	a, err := store.New(s.pool).AccountByID(ctx, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return empty, unavailable()
	}
	return a, nil
}

func (s *Service) OperatorClientHistory(ctx context.Context, actor, target uuid.UUID, in wire.OperatorHistoryInput) (wire.OperatorHistoryResult, error) {
	out := wire.OperatorHistoryResult{Kind: wire.OperatorHistoryResultKind(in.Kind), TrialRequests: []wire.OperatorTrialRequest{}, AuditEvents: []wire.OperatorAuditEvent{}, LegacyEvents: []wire.OperatorLegacyApprovalEvent{}}
	if _, err := s.operatorClientAccount(ctx, actor, target); err != nil {
		return out, err
	}
	if in.Kind != "trials" && in.Kind != "audit" && in.Kind != "legacy" {
		return out, failure(400, "INVALID_INPUT")
	}
	if in.Kind == "legacy" {
		if in.BeforeId != nil || (in.BeforeCreatedAt == nil) != (in.BeforeSourceId == nil) {
			return out, failure(400, "INVALID_INPUT")
		}
		var before pgtype.Timestamptz
		var sourceID int64
		if in.BeforeCreatedAt != nil {
			before = stamp(*in.BeforeCreatedAt)
			var err error
			sourceID, err = parseTelegramID(*in.BeforeSourceId)
			if err != nil {
				return out, err
			}
		}
		rows, err := store.New(s.pool).LegacyApprovalPage(ctx, store.LegacyApprovalPageParams{AccountID: target, BeforeCreatedAt: before, BeforeSourceID: sourceID})
		if err != nil {
			return out, unavailable()
		}
		out.LegacyEvents, out.HasMore = legacyEventRows(rows)
		return out, nil
	}
	if in.BeforeSourceId != nil || (in.BeforeCreatedAt == nil) != (in.BeforeId == nil) {
		return out, failure(400, "INVALID_INPUT")
	}
	var before pgtype.Timestamptz
	var id uuid.UUID
	if in.BeforeCreatedAt != nil {
		before = stamp(*in.BeforeCreatedAt)
		id = *in.BeforeId
		if id == uuid.Nil {
			return out, failure(400, "INVALID_INPUT")
		}
	}
	q := store.New(s.pool)
	if in.Kind == "trials" {
		rows, err := q.OperatorTrialPage(ctx, store.OperatorTrialPageParams{AccountID: target, BeforeCreatedAt: before, BeforeID: id})
		if err != nil {
			return out, unavailable()
		}
		out.TrialRequests, out.HasMore = operatorTrialRows(rows)
	} else {
		rows, err := q.OperatorAuditPage(ctx, store.OperatorAuditPageParams{AccountID: target, BeforeCreatedAt: before, BeforeID: id})
		if err != nil {
			return out, unavailable()
		}
		out.AuditEvents, out.HasMore = operatorAuditRows(rows)
	}
	return out, nil
}

func (s *Service) OperatorClient(ctx context.Context, actor, target uuid.UUID) (wire.OperatorClientCard, error) {
	var out wire.OperatorClientCard
	a, err := s.operatorClientAccount(ctx, actor, target)
	if err != nil {
		return out, err
	}
	q := store.New(s.pool)
	legacy, err := q.LegacyApprovalByAccount(ctx, target)
	if err == nil {
		out.LegacyApproval = legacySnapshot(legacy)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, unavailable()
	}
	legacyRows, err := q.LegacyApprovalPage(ctx, store.LegacyApprovalPageParams{AccountID: target})
	if err != nil {
		return out, unavailable()
	}
	out.LegacyEvents, out.LegacyHasMore = legacyEventRows(legacyRows)
	trials, err := q.OperatorTrialPage(ctx, store.OperatorTrialPageParams{AccountID: target})
	if err != nil {
		return out, unavailable()
	}
	audit, err := q.OperatorAuditPage(ctx, store.OperatorAuditPageParams{AccountID: target})
	if err != nil {
		return out, unavailable()
	}
	out.Client = operatorClient(a)
	out.TrialRequests, out.TrialHasMore = operatorTrialRows(trials)
	out.AuditEvents, out.AuditHasMore = operatorAuditRows(audit)
	if s.cfg.PanelID != "" {
		out.Server = &wire.OperatorServer{PanelId: s.cfg.PanelID, Enabled: s.cfg.TrialEnabled}
	}
	support, err := q.SupportByAccount(ctx, target)
	if err == nil {
		out.Support = publicSupportConversation(support)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, unavailable()
	}
	if a.Restricted {
		out.Subscription, err = s.restrictedOperatorSubscription(ctx, a)
	} else {
		out.Subscription, err = s.Subscription(ctx, target)
	}
	if err != nil {
		return out, err
	}
	return out, nil
}

// Restricted customers cannot call the cabinet Subscription endpoint. The
// operator card still shows the last stored observation, explicitly stale.
func (s *Service) restrictedOperatorSubscription(ctx context.Context, a store.Account) (wire.Subscription, error) {
	out := wire.Subscription{Status: "none", DataStale: true}
	available := false
	out.ConnectionAvailable = &available
	op, err := store.New(s.pool).AccountOperation(ctx, a.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, unavailable()
	}
	out.Devices = op.Devices
	out.TrafficLimitBytes = op.TrafficGb * 1024 * 1024 * 1024
	if op.FirstStartedAt.Valid {
		expiry := op.FirstStartedAt.Time.Add(time.Duration(op.PeriodDays) * 24 * time.Hour)
		out.ExpiresAt = &expiry
	}
	cachedTraffic(&out, op)
	switch op.Status {
	case "needs_review":
		out.Status = "needs_review"
	case "applied":
		if !cachedProfile(&out, op, a.VpnBanned, s.now()) {
			out.Status = "active"
			if a.VpnBanned {
				out.Status = "banned"
			} else if out.ExpiresAt != nil && !s.now().Before(*out.ExpiresAt) {
				out.Status = "expired"
			}
		}
	default:
		out.Status = "provisioning"
	}
	return out, nil
}

func (s *Service) OperatorClientKey(ctx context.Context, actor, target uuid.UUID) (wire.SubscriptionKey, error) {
	var out wire.SubscriptionKey
	if _, err := s.operatorClientAccount(ctx, actor, target); err != nil {
		return out, err
	}
	return s.SubscriptionKey(ctx, target)
}

func (s *Service) ChangeOperatorRole(ctx context.Context, target uuid.UUID, grant bool) error {
	if target == uuid.Nil {
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	a, err := q.LockAccount(ctx, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return unavailable()
	}
	if grant && (a.Kind != "web" || !a.VerifiedAt.Valid || !a.EmailKey.Valid || !a.PasswordHash.Valid || a.Restricted) {
		return failure(403, "INVALID_CREDENTIALS")
	}
	var changed int64
	if grant {
		changed, err = q.GrantOperator(ctx, store.GrantOperatorParams{AccountID: target, GrantedAt: stamp(s.now())})
	} else {
		changed, err = q.RevokeOperator(ctx, target)
	}
	if err != nil {
		return unavailable()
	}
	if changed > 0 {
		action := "operator_revoked"
		if grant {
			action = "operator_granted"
		}
		if err = q.AddOperatorAudit(ctx, store.AddOperatorAuditParams{ID: uuid.New(), CreatedAt: stamp(s.now()), Action: action, AccountID: target}); err != nil {
			return unavailable()
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
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

func (s *Service) DecideOperatorTrial(ctx context.Context, actor, id, key uuid.UUID, in wire.OperatorDecisionInput) (wire.OperatorDecisionResult, bool, error) {
	var out wire.OperatorDecisionResult
	if err := s.RequireSupportOperator(ctx, actor); err != nil {
		return out, false, err
	}
	if id == uuid.Nil || key == uuid.Nil || (in.Decision != "approve" && in.Decision != "reject") ||
		!validText(in.Reason, 0, 1000) || (in.Decision == "reject" && !validText(in.Reason, 1, 1000)) {
		return out, false, failure(400, "INVALID_INPUT")
	}
	principal := "operator-account:" + actor.String()
	hash := bodyHash(struct {
		RequestID uuid.UUID
		Input     wire.OperatorDecisionInput
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
	if prior, found, replayErr := replay[wire.OperatorDecisionResult](ctx, q, principal, "decideTrialRequest", key, hash); found || replayErr != nil {
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
	out = wire.OperatorDecisionResult{Request: publicTrial(r), OperationId: r.OperationID}
	if err = s.saveIdempotency(ctx, q, principal, "decideTrialRequest", key, hash, out); err != nil {
		return out, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, false, unavailable()
	}
	return out, created, nil
}

func (s *Service) ReconsiderOperatorTrial(ctx context.Context, actor, id, key uuid.UUID, reason string) (wire.TrialRequest, bool, error) {
	var out wire.TrialRequest
	if err := s.RequireSupportOperator(ctx, actor); err != nil {
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
	if prior, found, replayErr := replay[wire.TrialRequest](ctx, q, principal, "reconsiderTrialRequest", key, hash); found || replayErr != nil {
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

func (s *Service) ReconcileOperatorTrial(ctx context.Context, actor, id, key uuid.UUID, reason string) (wire.ReconcileResult, bool, error) {
	var out wire.ReconcileResult
	if err := s.RequireSupportOperator(ctx, actor); err != nil {
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
	op, err := q.OperationByID(ctx, id)
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
	if prior, found, replayErr := replay[wire.ReconcileResult](ctx, q, principal, "reconcileTrialOperation", key, hash); found || replayErr != nil {
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

func newOperatorSubID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", unavailable()
	}
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	for i, b := range random {
		random[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(random), nil
}

func (s *Service) CreateTelegramTrial(ctx context.Context, actor, key uuid.UUID, in wire.OperatorTelegramTrialInput) (wire.OperatorTelegramTrialResult, bool, error) {
	var out wire.OperatorTelegramTrialResult
	if err := s.RequireSupportOperator(ctx, actor); err != nil {
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
	if prior, found, replayErr := replay[wire.OperatorTelegramTrialResult](ctx, q, principal, "createTelegramTrial", key, hash); found || replayErr != nil {
		return prior, false, replayErr
	}
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('telegram-account:'||$1::text,0))`, in.TelegramId).Scan(&locked); err != nil {
		return out, false, unavailable()
	}
	if !locked {
		return out, false, failure(409, "REQUEST_STATE_CONFLICT")
	}
	if _, err = q.AccountByTelegramID(ctx, pgtype.Int8{Int64: telegramID, Valid: true}); err == nil {
		return out, false, failure(409, "TRIAL_ALREADY_USED")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, false, unavailable()
	}
	if !s.cfg.TrialEnabled {
		return out, false, failure(403, "TRIAL_DISABLED")
	}
	if s.cfg.PanelID == "" {
		return out, false, unavailable()
	}
	sub, err := newOperatorSubID()
	if err != nil {
		return out, false, err
	}
	id := uuid.New()
	if err = q.AddTelegramAccount(ctx, store.AddTelegramAccountParams{ID: id, DisplayName: pgtype.Text{String: in.DisplayName, Valid: true}, TelegramID: pgtype.Int8{Int64: telegramID, Valid: true}, Locale: string(in.Locale), VpnID: uuid.New(), SubID: sub, PanelKey: "acct_" + strings.ReplaceAll(id.String(), "-", "")}); err != nil {
		return out, false, unavailable()
	}
	a, err := q.AccountByID(ctx, id)
	if err != nil {
		return out, false, unavailable()
	}
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
	out = wire.OperatorTelegramTrialResult{Client: operatorClient(a), Request: publicTrial(r), OperationId: *r.OperationID}
	if err = s.saveIdempotency(ctx, q, principal, "createTelegramTrial", key, hash, out); err != nil {
		return out, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, false, unavailable()
	}
	return out, true, nil
}
