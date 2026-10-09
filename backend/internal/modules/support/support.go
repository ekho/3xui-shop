package support

import (
	"context"
	"crypto/sha256"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/support/internal/store"
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
	retry, err := supportMessageLimit.Run(limited, s.limiter, []string{s.rateNamespace + ":support:" + actor.String()}, s.now().UnixMilli(), key.String()).Int()
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
	a, err := s.accountByID(ctx, actor)
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
		if err := s.authority.RequireOperator(ctx, actor); err != nil {
			return err
		}
		if _, err = s.accountByID(ctx, target); errors.Is(err, pgx.ErrNoRows) {
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
		if _, err := s.authority.LockOperatorPair(ctx, tx, actor, target); err != nil {
			return empty, err
		}
	} else {
		a, err := s.lockAccount(ctx, tx, actor)
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, failure(404, "INVALID_INPUT")
		}
		if err != nil {
			var sourceError *accounts.Error
			if errors.As(err, &sourceError) {
				return empty, err
			}
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

func publicSupportConversation(c store.SupportConversation) *SupportConversation {
	return &SupportConversation{Id: c.ID, Status: c.Status, SupportBanned: c.SupportBanned, CreatedAt: c.CreatedAt.Time, UpdatedAt: c.UpdatedAt.Time, CustomerReceivedSequence: c.CustomerReceivedSequence, OperatorReceivedSequence: c.OperatorReceivedSequence}
}
func supportDelivery(sender string, seq int64, c store.SupportConversation) string {
	if sender == "customer" && c.OperatorReceivedSequence >= seq || sender == "operator" && c.CustomerReceivedSequence >= seq {
		return "delivered"
	}
	return "stored"
}
func publicSupportMessage(m store.SupportMessage, c store.SupportConversation) SupportMessage {
	out := SupportMessage{Id: m.ID, Sequence: m.Sequence, Sender: m.SenderKind, Text: m.Text, CreatedAt: m.CreatedAt.Time, Delivery: supportDelivery(m.SenderKind, m.Sequence, c)}
	if m.AttachmentName.Valid {
		out.Attachment = &SupportAttachment{Name: m.AttachmentName.String, SizeBytes: int64(len(m.AttachmentBytes))}
	}
	return out
}
func publicSupportPage(rows []store.SupportPageRow, c store.SupportConversation) SupportResult {
	out := SupportResult{Conversation: publicSupportConversation(c), Messages: []SupportMessage{}, HasMore: len(rows) > 50}
	if len(rows) > 50 {
		rows = rows[:50]
	}
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		m := SupportMessage{Id: r.ID, Sequence: r.Sequence, Sender: r.SenderKind, Text: r.Text, CreatedAt: r.CreatedAt.Time, Delivery: supportDelivery(r.SenderKind, r.Sequence, c)}
		if r.AttachmentName.Valid {
			m.Attachment = &SupportAttachment{Name: r.AttachmentName.String, SizeBytes: r.AttachmentSize}
		}
		out.Messages = append(out.Messages, m)
	}
	if len(out.Messages) > 0 {
		oldest := out.Messages[0].Sequence
		out.OldestSequence = &oldest
	}
	return out
}
func (s *Service) supportPage(ctx context.Context, actor, target uuid.UUID, operator bool, before int64) (SupportResult, error) {
	out := SupportResult{Messages: []SupportMessage{}}
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
func (s *Service) Support(ctx context.Context, actor, target uuid.UUID, operator bool) (SupportResult, error) {
	return s.supportPage(ctx, actor, target, operator, 0)
}

func (s *Service) SupportHistory(ctx context.Context, actor, target uuid.UUID, operator bool, before int64) (SupportResult, error) {
	if before < 1 {
		return SupportResult{}, failure(400, "INVALID_INPUT")
	}
	return s.supportPage(ctx, actor, target, operator, before)
}

func (s *Service) supportAudit(ctx context.Context, tx pgx.Tx, action string, target, actor uuid.UUID, operator bool, messageID *uuid.UUID, reason string) error {
	var operatorID *uuid.UUID
	if operator {
		operatorID = &actor
	}
	var why *string
	if reason != "" {
		why = &reason
	}
	if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: action, AccountID: target, OperatorAccountID: operatorID, Reason: why, SupportMessageID: messageID}) != nil {
		return unavailable()
	}
	return nil
}

func (s *Service) CreateSupportMessage(ctx context.Context, actor, target uuid.UUID, operator bool, key uuid.UUID, text, name string, file []byte) (SupportMessage, bool, error) {
	var out SupportMessage
	if key == uuid.Nil || !validSupportText(text) || (text == "" && len(file) == 0) || (len(file) == 0 && name != "") || (len(file) > 0 && !validSupportName(name)) {
		return out, false, failure(400, "INVALID_INPUT")
	}
	if len(file) > supportFileMax {
		return out, false, failure(413, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, false, unavailable()
	}
	defer tx.Rollback(ctx)
	out, created, err := s.createSupportMessageTx(ctx, tx, actor, target, operator, key, text, name, file, nil)
	if err != nil {
		return out, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, false, unavailable()
	}
	return out, created, nil
}

func (s *Service) createSupportMessageTx(ctx context.Context, tx pgx.Tx, actor, target uuid.UUID, operator bool, key uuid.UUID, text, name string, file []byte, source *TelegramInput) (SupportMessage, bool, error) {
	var out SupportMessage
	principal := "account:" + actor.String()
	hash := bodyHash(struct {
		Target     uuid.UUID
		Operator   bool
		Text, Name string
		FileHash   [32]byte
		FileSize   int
	}{target, operator, text, name, sha256.Sum256(file), len(file)})
	q := store.New(tx)
	c, err := s.lockSupport(ctx, tx, actor, target, operator)
	if err != nil {
		return out, false, err
	}
	if q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "createSupportMessage", Key: key}) != nil {
		return out, false, unavailable()
	}
	if prior, found, err := replay[SupportMessage](ctx, q, principal, "createSupportMessage", key, hash); found || err != nil {
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
	if err = s.enqueueSupportMessageTx(ctx, tx, target, m, operator, source); err != nil {
		return out, false, err
	}
	if s.telegramBotID != 0 {
		out.TelegramDelivery, err = s.messageTelegramStatusTx(ctx, tx, m.ID)
		if err != nil {
			return out, false, err
		}
	}
	if operator {
		a, err := s.authority.LookupTx(ctx, tx, target)
		if err != nil {
			return out, false, unavailable()
		}
		if a.TelegramID != nil && !a.TelegramLoginDisabled {
			notice := notifications.ClientNotice{AccountID: target, TelegramID: *a.TelegramID, CredentialVersion: a.CredentialVersion, Locale: a.Locale, EventKey: "support:" + m.ID.String(), Route: "support"}
			if s.notifications.EnqueueClientTx(ctx, tx, notice, s.now()) != nil {
				return out, false, unavailable()
			}
		}
	}
	if err = s.supportAudit(ctx, tx, "support_message", target, actor, operator, &m.ID, ""); err != nil {
		return out, false, err
	}
	if err = s.saveIdempotency(ctx, q, principal, "createSupportMessage", key, hash, out); err != nil {
		return out, false, err
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
	if err := s.supportAudit(ctx, tx, "support_state_"+state, target, actor, operator, nil, ""); err != nil {
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
	if err := s.supportAudit(ctx, tx, action, target, actor, true, nil, reason); err != nil {
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

func (s *Service) Conversation(ctx context.Context, actor, target uuid.UUID, operator bool) (*SupportConversation, error) {
	if err := s.supportAccess(ctx, actor, target, operator); err != nil {
		return nil, err
	}
	conversation, err := store.New(s.pool).SupportByAccount(ctx, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, unavailable()
	}
	return publicSupportConversation(conversation), nil
}
