package platform

import (
	"context"
	"crypto/sha256"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const supportFileMax = 10 * 1024 * 1024
const supportConversationMax = 50 * 1024 * 1024

var supportMessageLimit = redis.NewScript(`
local key=KEYS[1]; local now=tonumber(ARGV[1]); local member=ARGV[2]
redis.call('ZREMRANGEBYSCORE',key,'-inf',now-900000)
if redis.call('ZSCORE',key,member) then return 0 end
if redis.call('ZCARD',key)>=30 then
 local first=redis.call('ZRANGE',key,0,0,'WITHSCORES')
 return math.max(1,math.ceil((900000-now+tonumber(first[2]))/1000))
end
redis.call('ZADD',key,now,member); redis.call('PEXPIRE',key,900000)
return 0`)

func (s *Service) limitSupportMessage(ctx context.Context, actor, key uuid.UUID) error {
	limited, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	retry, err := supportMessageLimit.Run(limited, s.limiter, []string{s.cfg.RateNamespace + ":support:" + actor.String()}, s.now().UnixMilli(), key.String()).Int()
	if err != nil {
		return unavailable()
	}
	if retry > 0 {
		return &Error{Status: 429, Code: "RATE_LIMITED", RetryAfter: retry}
	}
	return nil
}

func validSupportName(name string) bool {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 128 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return name != "." && name != ".."
}
func validSupportText(text string) bool {
	return utf8.ValidString(text) && !strings.ContainsRune(text, '\x00') && utf8.RuneCountInString(text) <= 4000
}

func (s *Service) supportAccess(ctx context.Context, actor, target uuid.UUID, operator bool) error {
	if actor == uuid.Nil || target == uuid.Nil {
		return failure(400, "INVALID_INPUT")
	}
	if !operator && actor != target {
		return failure(404, "INVALID_INPUT")
	}
	q := store.New(s.pool)
	a, err := q.AccountByID(ctx, actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return unavailable()
	}
	if a.Restricted {
		return failure(403, "ACCOUNT_RESTRICTED")
	}
	if operator {
		if a.Kind != "web" || !a.VerifiedAt.Valid {
			return failure(403, "INVALID_CREDENTIALS")
		}
		allowed, err := q.OperatorExists(ctx, actor)
		if err != nil {
			return unavailable()
		}
		if !allowed {
			return failure(403, "INVALID_CREDENTIALS")
		}
		if _, err = q.AccountByID(ctx, target); errors.Is(err, pgx.ErrNoRows) {
			return failure(404, "INVALID_INPUT")
		} else if err != nil {
			return unavailable()
		}
	}
	return nil
}

// Account rows are locked in UUID order before the entitlement and conversation.
func (s *Service) lockSupport(ctx context.Context, tx pgx.Tx, actor, target uuid.UUID, operator bool) (store.SupportConversation, error) {
	var empty store.SupportConversation
	if actor == uuid.Nil || target == uuid.Nil {
		return empty, failure(400, "INVALID_INPUT")
	}
	if !operator && actor != target {
		return empty, failure(404, "INVALID_INPUT")
	}
	q := store.New(tx)
	if operator {
		if _, err := s.lockOperatorPair(ctx, tx, actor, target); err != nil {
			return empty, err
		}
	} else {
		a, err := q.LockAccount(ctx, actor)
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, failure(404, "INVALID_INPUT")
		}
		if err != nil {
			return empty, unavailable()
		}
		if a.Restricted {
			return empty, failure(403, "ACCOUNT_RESTRICTED")
		}
	}
	conv, err := q.LockSupportByAccount(ctx, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, nil
	}
	if err != nil {
		return empty, unavailable()
	}
	return conv, nil
}

func publicSupportConversation(c store.SupportConversation) *wire.SupportConversation {
	return &wire.SupportConversation{Id: c.ID, Status: wire.SupportConversationStatus(c.Status), SupportBanned: c.SupportBanned, CreatedAt: c.CreatedAt.Time, UpdatedAt: c.UpdatedAt.Time, CustomerReceivedSequence: c.CustomerReceivedSequence, OperatorReceivedSequence: c.OperatorReceivedSequence}
}
func supportDelivery(sender string, seq int64, c store.SupportConversation) wire.SupportMessageDelivery {
	if sender == "customer" && c.OperatorReceivedSequence >= seq || sender == "operator" && c.CustomerReceivedSequence >= seq {
		return wire.Delivered
	}
	return wire.Stored
}
func publicSupportMessage(m store.SupportMessage, c store.SupportConversation) wire.SupportMessage {
	out := wire.SupportMessage{Id: m.ID, Sequence: m.Sequence, Sender: wire.SupportMessageSender(m.SenderKind), Text: m.Text, CreatedAt: m.CreatedAt.Time, Delivery: supportDelivery(m.SenderKind, m.Sequence, c)}
	if m.AttachmentName.Valid {
		out.Attachment = &wire.SupportAttachment{Name: m.AttachmentName.String, SizeBytes: int64(len(m.AttachmentBytes))}
	}
	return out
}
func publicSupportPage(rows []store.SupportPageRow, c store.SupportConversation) wire.SupportResult {
	out := wire.SupportResult{Conversation: publicSupportConversation(c), Messages: []wire.SupportMessage{}, HasMore: len(rows) > 50}
	if len(rows) > 50 {
		rows = rows[:50]
	}
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		m := wire.SupportMessage{Id: r.ID, Sequence: r.Sequence, Sender: wire.SupportMessageSender(r.SenderKind), Text: r.Text, CreatedAt: r.CreatedAt.Time, Delivery: supportDelivery(r.SenderKind, r.Sequence, c)}
		if r.AttachmentName.Valid {
			m.Attachment = &wire.SupportAttachment{Name: r.AttachmentName.String, SizeBytes: r.AttachmentSize}
		}
		out.Messages = append(out.Messages, m)
	}
	if len(out.Messages) > 0 {
		oldest := out.Messages[0].Sequence
		out.OldestSequence = &oldest
	}
	return out
}
func (s *Service) supportPage(ctx context.Context, actor, target uuid.UUID, operator bool, before int64) (wire.SupportResult, error) {
	out := wire.SupportResult{Messages: []wire.SupportMessage{}}
	if err := s.supportAccess(ctx, actor, target, operator); err != nil {
		return out, err
	}
	q := store.New(s.pool)
	c, err := q.SupportByAccount(ctx, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, unavailable()
	}
	rows, err := q.SupportPage(ctx, store.SupportPageParams{ConversationID: c.ID, BeforeSequence: before})
	if err != nil {
		return out, unavailable()
	}
	return publicSupportPage(rows, c), nil
}
func (s *Service) Support(ctx context.Context, actor, target uuid.UUID, operator bool) (wire.SupportResult, error) {
	return s.supportPage(ctx, actor, target, operator, 0)
}
func (s *Service) RequireSupportOperator(ctx context.Context, actor uuid.UUID) error {
	if actor == uuid.Nil {
		return failure(401, "INVALID_CREDENTIALS")
	}
	q := store.New(s.pool)
	account, err := q.AccountByID(ctx, actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return unavailable()
	}
	if account.Restricted {
		return failure(403, "ACCOUNT_RESTRICTED")
	}
	if account.Kind != "web" || !account.VerifiedAt.Valid {
		return failure(403, "INVALID_CREDENTIALS")
	}
	allowed, err := q.OperatorExists(ctx, actor)
	if err != nil {
		return unavailable()
	}
	if !allowed {
		return failure(403, "INVALID_CREDENTIALS")
	}
	return nil
}
func (s *Service) SupportHistory(ctx context.Context, actor, target uuid.UUID, operator bool, before int64) (wire.SupportResult, error) {
	if before < 1 {
		return wire.SupportResult{}, failure(400, "INVALID_INPUT")
	}
	return s.supportPage(ctx, actor, target, operator, before)
}

func (s *Service) supportAudit(ctx context.Context, q *store.Queries, action string, target, actor uuid.UUID, operator bool, messageID *uuid.UUID, reason string) error {
	var operatorID *uuid.UUID
	if operator {
		operatorID = &actor
	}
	var why pgtype.Text
	if reason != "" {
		why = pgtype.Text{String: reason, Valid: true}
	}
	if q.AddSupportAudit(ctx, store.AddSupportAuditParams{ID: uuid.New(), CreatedAt: stamp(s.now()), Action: action, AccountID: target, OperatorAccountID: operatorID, Reason: why, SupportMessageID: messageID}) != nil {
		return unavailable()
	}
	return nil
}

func (s *Service) CreateSupportMessage(ctx context.Context, actor, target uuid.UUID, operator bool, key uuid.UUID, text, name string, file []byte) (wire.SupportMessage, bool, error) {
	var out wire.SupportMessage
	if key == uuid.Nil || !validSupportText(text) || (text == "" && len(file) == 0) || (len(file) == 0 && name != "") || (len(file) > 0 && !validSupportName(name)) {
		return out, false, failure(400, "INVALID_INPUT")
	}
	if len(file) > supportFileMax {
		return out, false, failure(413, "INVALID_INPUT")
	}
	principal := "account:" + actor.String()
	hash := bodyHash(struct {
		Target     uuid.UUID
		Operator   bool
		Text, Name string
		FileHash   [32]byte
		FileSize   int
	}{target, operator, text, name, sha256.Sum256(file), len(file)})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, false, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	c, err := s.lockSupport(ctx, tx, actor, target, operator)
	if err != nil {
		return out, false, err
	}
	if q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "createSupportMessage", Key: key}) != nil {
		return out, false, unavailable()
	}
	if prior, found, err := replay[wire.SupportMessage](ctx, q, principal, "createSupportMessage", key, hash); found || err != nil {
		return prior, false, err
	}
	if c.ID == uuid.Nil {
		c, err = q.CreateSupportConversation(ctx, store.CreateSupportConversationParams{ID: uuid.New(), AccountID: target, CreatedAt: stamp(s.now())})
		if err != nil {
			return out, false, unavailable()
		}
	}
	if !operator && c.SupportBanned {
		return out, false, failure(403, "ACCOUNT_RESTRICTED")
	}
	used, err := q.SupportFileBytes(ctx, c.ID)
	if err != nil {
		return out, false, unavailable()
	}
	if used > supportConversationMax-int64(len(file)) {
		return out, false, failure(413, "INVALID_INPUT")
	}
	if err := s.limitSupportMessage(ctx, actor, key); err != nil {
		return out, false, err
	}
	var attachment pgtype.Text
	if len(file) > 0 {
		attachment = pgtype.Text{String: name, Valid: true}
	}
	kind := "customer"
	if operator {
		kind = "operator"
	}
	m, err := q.AddSupportMessage(ctx, store.AddSupportMessageParams{ID: uuid.New(), ConversationID: c.ID, SenderAccountID: actor, SenderKind: kind, Text: text, CreatedAt: stamp(s.now()), AttachmentName: attachment, AttachmentBytes: file})
	if err != nil {
		return out, false, unavailable()
	}
	if err = q.UpdateSupportState(ctx, store.UpdateSupportStateParams{ID: c.ID, Status: "open", UpdatedAt: stamp(s.now())}); err != nil {
		return out, false, unavailable()
	}
	out = publicSupportMessage(m, c)
	if err = s.supportAudit(ctx, q, "support_message", target, actor, operator, &m.ID, ""); err != nil {
		return out, false, err
	}
	if err = s.saveIdempotency(ctx, q, principal, "createSupportMessage", key, hash, out); err != nil {
		return out, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, false, unavailable()
	}
	return out, true, nil
}

func (s *Service) AcknowledgeSupport(ctx context.Context, actor, target uuid.UUID, operator bool, sequence int64) error {
	if sequence < 0 {
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	c, err := s.lockSupport(ctx, tx, actor, target, operator)
	if err != nil {
		return err
	}
	if c.ID == uuid.Nil {
		return failure(404, "INVALID_INPUT")
	}
	q := store.New(tx)
	if sequence > 0 {
		sender, err := q.SupportMessageBySequence(ctx, store.SupportMessageBySequenceParams{ConversationID: c.ID, Sequence: sequence})
		if errors.Is(err, pgx.ErrNoRows) {
			return failure(409, "REQUEST_STATE_CONFLICT")
		}
		if err != nil {
			return unavailable()
		}
		if sender == "customer" && !operator || sender == "operator" && operator {
			return failure(409, "REQUEST_STATE_CONFLICT")
		}
	}
	if operator {
		err = q.AckSupportOperator(ctx, store.AckSupportOperatorParams{ID: c.ID, OperatorReceivedSequence: sequence})
	} else {
		err = q.AckSupportCustomer(ctx, store.AckSupportCustomerParams{ID: c.ID, CustomerReceivedSequence: sequence})
	}
	if err != nil {
		return unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
}

func (s *Service) SetSupportState(ctx context.Context, actor, target uuid.UUID, operator bool, state string) error {
	if state != "open" && state != "closed" {
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	c, err := s.lockSupport(ctx, tx, actor, target, operator)
	if err != nil {
		return err
	}
	if c.ID == uuid.Nil {
		return failure(404, "INVALID_INPUT")
	}
	if !operator && c.SupportBanned {
		return failure(403, "ACCOUNT_RESTRICTED")
	}
	q := store.New(tx)
	if err := q.UpdateSupportState(ctx, store.UpdateSupportStateParams{ID: c.ID, Status: state, UpdatedAt: stamp(s.now())}); err != nil {
		return unavailable()
	}
	if err := s.supportAudit(ctx, q, "support_state_"+state, target, actor, operator, nil, ""); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
}

func (s *Service) SetSupportBan(ctx context.Context, actor, target uuid.UUID, banned bool, reason string) error {
	if !validText(reason, 1, 1000) || strings.TrimSpace(reason) == "" {
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	c, err := s.lockSupport(ctx, tx, actor, target, true)
	if err != nil {
		return err
	}
	q := store.New(tx)
	if c.ID == uuid.Nil {
		c, err = q.CreateSupportConversation(ctx, store.CreateSupportConversationParams{ID: uuid.New(), AccountID: target, CreatedAt: stamp(s.now())})
		if err != nil {
			return unavailable()
		}
	}
	if q.UpdateSupportBan(ctx, store.UpdateSupportBanParams{ID: c.ID, SupportBanned: banned, UpdatedAt: stamp(s.now())}) != nil {
		return unavailable()
	}
	action := "support_unbanned"
	if banned {
		action = "support_banned"
	}
	if err := s.supportAudit(ctx, q, action, target, actor, true, nil, reason); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
}

func (s *Service) SupportAttachment(ctx context.Context, actor, message uuid.UUID) (string, []byte, error) {
	if message == uuid.Nil {
		return "", nil, failure(400, "INVALID_INPUT")
	}
	q := store.New(s.pool)
	lookup, err := q.SupportAttachmentLookup(ctx, message)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return "", nil, unavailable()
	}
	if !lookup.AttachmentName.Valid {
		return "", nil, failure(404, "INVALID_INPUT")
	}
	operator := actor != lookup.AccountID
	if err := s.supportAccess(ctx, actor, lookup.AccountID, operator); err != nil {
		if operator {
			return "", nil, failure(404, "INVALID_INPUT")
		}
		return "", nil, err
	}
	m, err := q.SupportMessageByID(ctx, message)
	if err != nil {
		return "", nil, unavailable()
	}
	return lookup.AttachmentName.String, m.AttachmentBytes, nil
}
