package support

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/support/internal/store"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type TelegramSource struct {
	BotID, GroupID, ChatID, MessageID, UpdateID, ActorID, ThreadID int64
	Action, CallbackID                                             string
	Digest                                                         [32]byte
}
type TelegramInput struct {
	Source                TelegramSource
	Text, Name, MediaKind string
	Bytes                 []byte
	TelegramOnly          bool
	receiptID             uuid.UUID
}
type TelegramReceiptResult struct {
	ID      uuid.UUID
	Message *SupportMessage
	Replay  bool
}

func (s *Service) ConfigureTelegram(botID, groupID int64) error {
	if botID <= 0 || botID > 1<<52-1 || groupID >= 0 || groupID < -(1<<52-1) || s.telegramBotID != 0 && (s.telegramBotID != botID || s.telegramGroupID != groupID) {
		return failure(400, "INVALID_INPUT")
	}
	// App initializes this owner before starting HTTP and either polling loop.
	s.telegramBotID, s.telegramGroupID = botID, groupID
	return nil
}

func telegramInt(v int64) pgtype.Int8 { return pgtype.Int8{Int64: v, Valid: v != 0} }
func telegramReceiptID(in TelegramSource) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("support:%d:%d:%d:%s:%s", in.BotID, in.ChatID, in.MessageID, in.Action, in.CallbackID)))
}
func telegramMessageDigest(in TelegramInput) []byte {
	// Download availability/bytes and display names are not provider identity.
	return bodyHash(struct {
		Digest               [32]byte
		Actor, Group, Thread int64
		Text, Media          string
	}{in.Source.Digest, in.Source.ActorID, in.Source.GroupID, in.Source.ThreadID, in.Text, in.MediaKind})
}
func (s *Service) validTelegramSource(in TelegramSource) bool {
	return s.telegramBotID != 0 && in.BotID == s.telegramBotID && in.GroupID == s.telegramGroupID && in.ActorID > 0 && in.ActorID <= 1<<52-1 && in.MessageID > 0 && in.MessageID <= 1<<52-1 && in.UpdateID >= 0 && in.UpdateID <= 1<<52-1 && in.Digest != [32]byte{} &&
		(in.ChatID == in.ActorID && in.ThreadID == 0 || in.ChatID == s.telegramGroupID && in.ThreadID > 1 && in.ThreadID <= 1<<52-1)
}

func (s *Service) ReceiveTelegramMessage(ctx context.Context, in TelegramInput) (TelegramReceiptResult, error) {
	var out TelegramReceiptResult
	if !s.validTelegramSource(in.Source) || in.Source.Action != "message" || in.Source.CallbackID != "" || !validSupportText(in.Text) || in.Text == "" && len(in.Bytes) == 0 || len(in.Bytes) > supportFileMax || len(in.Bytes) == 0 && in.Name != "" || len(in.Bytes) > 0 && !validSupportName(in.Name) {
		return out, failure(400, "INVALID_INPUT")
	}
	actor, ok := accounts.TelegramActor(ctx)
	if !ok || actor.TelegramID == nil || *actor.TelegramID != in.Source.ActorID {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	target := actor.ID
	operator := in.Source.ChatID == s.telegramGroupID
	var topic store.SupportTelegramTopic
	if operator {
		topic, err = q.TelegramTopicByThread(ctx, store.TelegramTopicByThreadParams{BotID: s.telegramBotID, GroupID: s.telegramGroupID, ThreadID: telegramInt(in.Source.ThreadID)})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && (topic.Kind != "account" || topic.AccountID == nil) {
			return out, failure(404, "INVALID_INPUT")
		}
		if err != nil {
			return out, unavailable()
		}
		target = *topic.AccountID
	}
	c, err := s.lockSupport(ctx, tx, actor.ID, target, operator)
	if err != nil {
		return out, err
	}
	a, err := s.authority.LookupTx(ctx, tx, target)
	if err != nil {
		return out, err
	}
	if c.SupportBanned || a.Restricted || a.TelegramLoginDisabled {
		return out, failure(403, "ACCOUNT_RESTRICTED")
	}
	if a.TelegramID != nil {
		banned, e := q.TelegramGuestBanned(ctx, *a.TelegramID)
		if e != nil {
			return out, unavailable()
		}
		if banned {
			return out, failure(403, "ACCOUNT_RESTRICTED")
		}
	}
	if operator {
		current, e := q.LockTelegramTopic(ctx, topic.ID)
		if e != nil {
			return out, unavailable()
		}
		if current.Status != "ready" || current.AccountID == nil || *current.AccountID != target || current.ThreadID != telegramInt(in.Source.ThreadID) {
			return out, failure(409, "REQUEST_STATE_CONFLICT")
		}
		ctx = auditreports.WithTelegramActor(ctx, actor.ID, in.Source.ActorID)
	}
	out.ID = telegramReceiptID(in.Source)
	hash := telegramMessageDigest(in)
	prior, e := q.TelegramReceipt(ctx, out.ID)
	if e == nil {
		if !bytes.Equal(prior.Digest, hash) || prior.ActorAccountID == nil || *prior.ActorAccountID != actor.ID || prior.TargetAccountID == nil || *prior.TargetAccountID != target {
			return out, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		if json.Unmarshal(prior.Result, &out) != nil {
			return out, unavailable()
		}
		out.Replay = true
		return out, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return out, unavailable()
	}
	in.receiptID = out.ID
	if e = q.AddTelegramReceipt(ctx, store.AddTelegramReceiptParams{ID: out.ID, BotID: in.Source.BotID, GroupID: in.Source.GroupID, ChatID: in.Source.ChatID, MessageID: in.Source.MessageID, UpdateID: in.Source.UpdateID, ActorTgID: telegramInt(in.Source.ActorID), ThreadID: telegramInt(in.Source.ThreadID), Action: in.Source.Action, CallbackID: in.Source.CallbackID, Digest: hash, ActorAccountID: &actor.ID, TargetAccountID: &target, OwnerKey: out.ID, Result: []byte("{}")}); e != nil {
		return out, unavailable()
	}
	message, _, e := s.createSupportMessageTx(ctx, tx, actor.ID, target, operator, out.ID, in.Text, in.Name, in.Bytes, &in)
	if e != nil {
		return out, e
	}
	out.Message = &message
	topic, e = q.AccountTelegramTopic(ctx, store.AccountTelegramTopicParams{BotID: s.telegramBotID, GroupID: s.telegramGroupID, AccountID: &target})
	if e != nil {
		return out, unavailable()
	}
	raw, e := json.Marshal(out)
	if e != nil {
		return out, unavailable()
	}
	if e = q.CompleteTelegramReceipt(ctx, store.CompleteTelegramReceiptParams{ID: out.ID, Result: raw, SupportMessageID: &message.Id, TopicID: &topic.ID}); e != nil {
		return out, unavailable()
	}
	if tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	return out, nil
}

func (s *Service) accountTelegramTopicTx(ctx context.Context, tx pgx.Tx, account uuid.UUID) (store.SupportTelegramTopic, error) {
	q := store.New(tx)
	t, err := q.AccountTelegramTopic(ctx, store.AccountTelegramTopicParams{BotID: s.telegramBotID, GroupID: s.telegramGroupID, AccountID: &account})
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return t, unavailable()
	}
	t, err = q.AddTelegramTopic(ctx, store.AddTelegramTopicParams{ID: uuid.New(), BotID: s.telegramBotID, GroupID: s.telegramGroupID, Kind: "account", AccountID: &account})
	if err != nil {
		return t, unavailable()
	}
	a, err := s.authority.LookupTx(ctx, tx, account)
	if err != nil {
		return t, err
	}
	p := TelegramPart{Kind: "create_topic", ChatID: s.telegramGroupID, Text: "Account " + account.String(), Status: "queued", RecipientVersion: a.CredentialVersion}
	if a.TelegramID != nil {
		p.RecipientTelegramID = *a.TelegramID
	}
	_, err = s.addTelegramDeliveryTx(ctx, tx, t.ID, nil, nil, "topic_create", []TelegramPart{p}, nil)
	return t, err
}

func (s *Service) addTelegramDeliveryTx(ctx context.Context, tx pgx.Tx, topic uuid.UUID, message, receipt *uuid.UUID, kind string, parts []TelegramPart, prior *uuid.UUID) (uuid.UUID, error) {
	id := uuid.New()
	raw, err := json.Marshal(parts)
	if err != nil {
		return uuid.Nil, unavailable()
	}
	if err = store.New(tx).AddTelegramDelivery(ctx, store.AddTelegramDeliveryParams{ID: id, TopicID: topic, MessageID: message, ReceiptID: receipt, Kind: kind, Parts: raw, PriorDeliveryID: prior}); err != nil {
		return uuid.Nil, unavailable()
	}
	return id, nil
}

func (s *Service) enqueueSupportMessageTx(ctx context.Context, tx pgx.Tx, target uuid.UUID, m store.SupportMessage, operator bool, source *TelegramInput) error {
	if s.telegramBotID == 0 {
		return nil
	}
	topic, err := s.accountTelegramTopicTx(ctx, tx, target)
	if err != nil {
		return err
	}
	a, err := s.authority.LookupTx(ctx, tx, target)
	if err != nil {
		return err
	}
	var parts []TelegramPart
	var receipt *uuid.UUID
	if source != nil {
		receipt = &source.receiptID
		chat := s.telegramGroupID
		if source.Source.ChatID == s.telegramGroupID {
			chat = 0
			if a.TelegramID != nil {
				chat = *a.TelegramID
			}
		}
		parts = []TelegramPart{{Kind: "copy", ChatID: chat, CopyChatID: source.Source.ChatID, CopyMessageID: source.Source.MessageID}}
	} else {
		parts = supportMessageParts(m, s.telegramGroupID)
		if operator && a.TelegramID != nil {
			parts = append(parts, supportMessageParts(m, *a.TelegramID)...)
		}
	}
	for i := range parts {
		p := &parts[i]
		p.Status = "queued"
		p.RecipientVersion = a.CredentialVersion
		if a.TelegramID != nil {
			p.RecipientTelegramID = *a.TelegramID
		}
		if proof, ok := accounts.TelegramActor(ctx); ok {
			p.SourceAccountID = proof.ID
			p.SourceTelegramID = *proof.TelegramID
			p.SourceVersion = proof.CredentialVersion
		}
	}
	_, err = s.addTelegramDeliveryTx(ctx, tx, topic.ID, &m.ID, receipt, "message", parts, nil)
	return err
}

func supportMessageParts(m store.SupportMessage, chat int64) []TelegramPart {
	if !m.AttachmentName.Valid {
		return []TelegramPart{{Kind: "text", ChatID: chat, Text: m.Text}}
	}
	// Caption handling is expanded with the media cases in Task2.
	return []TelegramPart{{Kind: "text", ChatID: chat, Text: m.Text}, {Kind: "document", ChatID: chat, Name: m.AttachmentName.String, MessageID: m.ID}}
}

func telegramDeliveryStatus(id uuid.UUID, status, code, topicStatus string) *TelegramDeliveryStatus {
	if status == "queued" && topicStatus == "unknown" {
		status, code = "unknown", "TOPIC_UNKNOWN"
	}
	if status == "queued" && topicStatus == "failed" {
		status, code = "failed", "TOPIC_FAILED"
	}
	if status == "queued" && topicStatus == "retired" {
		status, code = "skipped", "TOPIC_RETIRED"
	}
	return &TelegramDeliveryStatus{Id: id, Status: status, Code: code, RetryCapability: (status == "unknown" || status == "failed") && topicStatus == "ready", MediaAvailability: "stored"}
}
func (s *Service) messageTelegramStatusTx(ctx context.Context, tx pgx.Tx, message uuid.UUID) (*TelegramDeliveryStatus, error) {
	r, err := store.New(tx).LatestMessageTelegramDelivery(ctx, &message)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, unavailable()
	}
	return telegramDeliveryStatus(r.ID, r.Status, r.Code, r.TopicStatus), nil
}
