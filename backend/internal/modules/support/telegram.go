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
	"unicode/utf8"
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
	media := telegramMedia(in.MediaKind)
	if len(in.Bytes) > supportFileMax && media {
		in.Bytes, in.Name, in.TelegramOnly = nil, "", true
	}
	if !s.validTelegramSource(in.Source) || in.Source.Action != "message" || in.Source.CallbackID != "" || !validSupportText(in.Text) || in.MediaKind != "" && !media || in.TelegramOnly && (!media || len(in.Bytes) > 0) || in.Text == "" && len(in.Bytes) == 0 && !in.TelegramOnly || len(in.Bytes) > supportFileMax || len(in.Bytes) == 0 && in.Name != "" || len(in.Bytes) > 0 && !validSupportName(in.Name) {
		return out, failure(400, "INVALID_INPUT")
	}
	actor, ok := accounts.TelegramActor(ctx)
	if ok && (actor.TelegramID == nil || *actor.TelegramID != in.Source.ActorID) || !ok && in.Source.ChatID == s.telegramGroupID {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if !ok {
		out, err = s.receiveGuestTelegramTx(ctx, tx, in, nil, store.SupportTelegramTopic{})
		if err != nil {
			return out, err
		}
		if tx.Commit(ctx) != nil {
			return out, unavailable()
		}
		return out, nil
	}
	target := actor.ID
	operator := in.Source.ChatID == s.telegramGroupID
	var topic store.SupportTelegramTopic
	if operator {
		topic, err = q.TelegramTopicByThread(ctx, store.TelegramTopicByThreadParams{BotID: s.telegramBotID, GroupID: s.telegramGroupID, ThreadID: telegramInt(in.Source.ThreadID)})
		if err == nil && topic.Kind == "guest" {
			out, err = s.receiveGuestTelegramTx(ctx, tx, in, &actor, topic)
			if err != nil {
				return out, err
			}
			if tx.Commit(ctx) != nil {
				return out, unavailable()
			}
			return out, nil
		}
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
	currentTopic, topicErr := q.AccountTelegramTopic(ctx, store.AccountTelegramTopicParams{BotID: s.telegramBotID, GroupID: s.telegramGroupID, AccountID: &target})
	if topicErr != nil && !errors.Is(topicErr, pgx.ErrNoRows) {
		return out, unavailable()
	}
	if topicErr == nil && currentTopic.SupportBanned {
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

func guestIdentityError(err error) error {
	if errors.Is(err, accounts.ErrTelegramExists) {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	return err
}

func (s *Service) receiveGuestTelegramTx(ctx context.Context, tx pgx.Tx, in TelegramInput, actor *accounts.Snapshot, topic store.SupportTelegramTopic) (TelegramReceiptResult, error) {
	var out TelegramReceiptResult
	guest := in.Source.ActorID
	if actor != nil {
		if topic.Kind != "guest" || !topic.GuestTgID.Valid {
			return out, failure(404, "INVALID_INPUT")
		}
		guest = topic.GuestTgID.Int64
		if err := s.authority.LockSupportGuestOperatorTx(ctx, tx, actor.ID, guest); err != nil {
			return out, err
		}
	}
	if err := s.authority.CheckTelegramAvailable(ctx, tx, guest); err != nil {
		return out, guestIdentityError(err)
	}
	q := store.New(tx)
	banned, err := q.TelegramGuestBanned(ctx, guest)
	if err != nil {
		return out, unavailable()
	}
	if banned {
		return out, failure(403, "ACCOUNT_RESTRICTED")
	}
	if topic.ID == uuid.Nil {
		topic, err = q.GuestTelegramTopic(ctx, store.GuestTelegramTopicParams{BotID: s.telegramBotID, GroupID: s.telegramGroupID, GuestTgID: telegramInt(guest)})
		if errors.Is(err, pgx.ErrNoRows) {
			topic, err = q.AddTelegramTopic(ctx, store.AddTelegramTopicParams{ID: uuid.New(), BotID: s.telegramBotID, GroupID: s.telegramGroupID, Kind: "guest", GuestTgID: telegramInt(guest)})
			if err == nil {
				_, err = s.addTelegramDeliveryTx(ctx, tx, topic.ID, nil, nil, "topic_create", []TelegramPart{{Kind: "create_topic", ChatID: s.telegramGroupID, Text: fmt.Sprintf("Guest %d", guest), RecipientTelegramID: guest, Status: "queued"}}, nil)
			}
		}
		if err != nil {
			return out, unavailable()
		}
	}
	topic, err = q.LockTelegramTopic(ctx, topic.ID)
	if err != nil {
		return out, unavailable()
	}
	if topic.Kind != "guest" || topic.GuestTgID != telegramInt(guest) || topic.BotID != s.telegramBotID || topic.GroupID != s.telegramGroupID || topic.Status == "retired" || actor != nil && (topic.Status != "ready" || topic.ThreadID != telegramInt(in.Source.ThreadID)) {
		return out, failure(409, "REQUEST_STATE_CONFLICT")
	}
	out.ID = telegramReceiptID(in.Source)
	hash := telegramMessageDigest(in)
	var actorID *uuid.UUID
	if actor != nil {
		actorID = &actor.ID
	}
	prior, err := q.TelegramReceipt(ctx, out.ID)
	if err == nil {
		if !bytes.Equal(prior.Digest, hash) || prior.ActorTgID != telegramInt(in.Source.ActorID) || prior.TargetAccountID != nil || prior.TopicID == nil || *prior.TopicID != topic.ID || !sameUUID(prior.ActorAccountID, actorID) {
			return out, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		if json.Unmarshal(prior.Result, &out) != nil {
			return out, unavailable()
		}
		out.Replay = true
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, unavailable()
	}
	rateActor := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("support-guest:%d", guest)))
	if err = s.limitSupportMessage(ctx, rateActor, out.ID); err != nil {
		return out, err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, unavailable()
	}
	if err = q.AddTelegramReceipt(ctx, store.AddTelegramReceiptParams{ID: out.ID, BotID: in.Source.BotID, GroupID: in.Source.GroupID, ChatID: in.Source.ChatID, MessageID: in.Source.MessageID, UpdateID: in.Source.UpdateID, ActorTgID: telegramInt(in.Source.ActorID), ThreadID: telegramInt(in.Source.ThreadID), Action: in.Source.Action, CallbackID: in.Source.CallbackID, Digest: hash, ActorAccountID: actorID, TopicID: &topic.ID, OwnerKey: out.ID, Result: raw}); err != nil {
		return out, unavailable()
	}
	p := TelegramPart{Kind: "copy", ChatID: s.telegramGroupID, CopyChatID: in.Source.ChatID, CopyMessageID: in.Source.MessageID, RecipientTelegramID: guest, Status: "queued"}
	if actor != nil {
		p.ChatID = guest
		p.SourceAccountID = actor.ID
		p.SourceTelegramID = in.Source.ActorID
		p.SourceVersion = actor.CredentialVersion
	}
	parts := []TelegramPart{p}
	if topic.Closed {
		reopen := p
		reopen.Kind = "reopen_topic"
		reopen.ChatID = s.telegramGroupID
		reopen.CopyChatID = 0
		reopen.CopyMessageID = 0
		parts = append([]TelegramPart{reopen}, parts...)
	}
	if _, err = s.addTelegramDeliveryTx(ctx, tx, topic.ID, nil, &out.ID, "message", parts, nil); err != nil {
		return out, err
	}
	if err = q.SetTelegramTopicClosed(ctx, store.SetTelegramTopicClosedParams{ID: topic.ID, Closed: false}); err != nil {
		return out, unavailable()
	}
	tg := in.Source.ActorID
	event := auditreports.SupportTelegramEvent{ID: uuid.New(), BotID: s.telegramBotID, GroupID: s.telegramGroupID, ChatID: in.Source.ChatID, MessageID: in.Source.MessageID, UpdateID: in.Source.UpdateID, ActorTgID: &tg, ActorAccountID: actorID, TopicID: &topic.ID, ReceiptID: &out.ID, Kind: "message", Outcome: "queued"}
	if in.Source.ThreadID > 1 {
		event.ThreadID = &in.Source.ThreadID
	}
	if auditreports.RecordSupportTelegramTx(ctx, tx, event) != nil {
		return out, unavailable()
	}
	return out, nil
}

func sameUUID(a, b *uuid.UUID) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func (s *Service) accountTelegramTopicTx(ctx context.Context, tx pgx.Tx, account uuid.UUID) (store.SupportTelegramTopic, error) {
	q := store.New(tx)
	t, err := q.AccountTelegramTopic(ctx, store.AccountTelegramTopicParams{BotID: s.telegramBotID, GroupID: s.telegramGroupID, AccountID: &account})
	if err == nil {
		if t.Status != "pending" {
			return t, nil
		}
		hasCreator, e := q.HasTelegramTopicCreator(ctx, &t.ID)
		if e != nil {
			return t, unavailable()
		}
		if hasCreator {
			return t, nil
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		t, err = q.AddTelegramTopic(ctx, store.AddTelegramTopicParams{ID: uuid.New(), BotID: s.telegramBotID, GroupID: s.telegramGroupID, Kind: "account", AccountID: &account})
		if err != nil {
			return t, unavailable()
		}
	} else {
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
	var topicID *uuid.UUID
	if topic != uuid.Nil {
		topicID = &topic
	}
	if err = store.New(tx).AddTelegramDelivery(ctx, store.AddTelegramDeliveryParams{ID: id, TopicID: topicID, MessageID: message, ReceiptID: receipt, Kind: kind, Parts: raw, PriorDeliveryID: prior}); err != nil {
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
	if topic.Closed {
		parts = append([]TelegramPart{{Kind: "reopen_topic", ChatID: s.telegramGroupID}}, parts...)
		if q := store.New(tx); q.SetTelegramTopicClosed(ctx, store.SetTelegramTopicClosedParams{ID: topic.ID, Closed: false}) != nil {
			return unavailable()
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
	document := TelegramPart{Kind: "document", ChatID: chat, Name: m.AttachmentName.String, MessageID: m.ID}
	if utf8.RuneCountInString(m.Text) <= 1024 {
		document.Text = m.Text
		return []TelegramPart{document}
	}
	return []TelegramPart{{Kind: "text", ChatID: chat, Text: m.Text}, document}
}

func telegramMedia(kind string) bool {
	switch kind {
	case "photo", "document", "video", "animation", "audio", "voice", "video_note", "sticker", "unknown":
		return true
	}
	return false
}

func telegramDeliveryStatus(id uuid.UUID, status, code, topicStatus string, telegramOnly bool) *TelegramDeliveryStatus {
	if status == "queued" && topicStatus == "unknown" {
		status, code = "unknown", "TOPIC_UNKNOWN"
	}
	if status == "queued" && topicStatus == "failed" {
		status, code = "failed", "TOPIC_FAILED"
	}
	if status == "queued" && topicStatus == "retired" {
		status, code = "skipped", "TOPIC_RETIRED"
	}
	media := "stored"
	if telegramOnly {
		media = "telegram_only"
	}
	return &TelegramDeliveryStatus{Id: id, Status: status, Code: code, RetryCapability: (status == "unknown" || status == "failed") && topicStatus == "ready", MediaAvailability: media}
}
func (s *Service) messageTelegramStatusTx(ctx context.Context, tx pgx.Tx, message uuid.UUID, telegramOnly bool) (*TelegramDeliveryStatus, error) {
	r, err := store.New(tx).LatestMessageTelegramDelivery(ctx, &message)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, unavailable()
	}
	return telegramDeliveryStatus(r.ID, r.Status, r.Code, r.TopicStatus, telegramOnly), nil
}
