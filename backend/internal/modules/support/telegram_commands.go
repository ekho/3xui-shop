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
	"github.com/jackc/pgx/v5/pgconn"
	"strings"
	"unicode/utf8"
)

type TelegramCommandInput struct {
	Action, Reason                             string
	Days                                       int
	DeliveryID, TrialRequestID, ConfirmationID uuid.UUID
	ThreadID                                   int64
	Confirmed, GeneratedReason                 bool
}
type TelegramCommandResult struct {
	Status, Code                              string
	OperationID, RequestID, DeliveryID        uuid.UUID
	Links                                     []TelegramCommandLink
	More                                      bool
	ReplacementTopicID                        uuid.UUID
	TopicID                                   uuid.UUID
	TopicStatus, DeliveryStatus, DeliveryCode string
	Topics                                    []uuid.UUID
}
type TelegramCommandLink struct{ AccountID, RequestID uuid.UUID }
type TelegramCommandReceipt struct {
	ID, OwnerKey, TopicID, ActorAccountID uuid.UUID
	TargetAccountID                       *uuid.UUID
	GuestTelegramID, SourceVersion        int64
	Input                                 TelegramCommandInput
	Result                                *TelegramCommandResult
	AwaitingConfirmation                  bool
}

func telegramCommandInput(in TelegramCommandInput) bool {
	if !validText(in.Reason, 1, 1000) || in.ConfirmationID != uuid.Nil || in.Confirmed {
		return false
	}
	if in.Action != "comp" && in.Days != 0 || in.Action != "retry" && in.DeliveryID != uuid.Nil || in.Action != "bind" && in.ThreadID != 0 {
		return false
	}
	if in.Action != "approve" && in.Action != "reject" && in.Action != "reconsider" && in.TrialRequestID != uuid.Nil {
		return false
	}
	switch in.Action {
	case "comp":
		return in.Days >= 1 && in.Days <= 365
	case "approve", "reject", "reconsider":
		return in.TrialRequestID != uuid.Nil
	case "retry":
		return in.DeliveryID != uuid.Nil
	case "bind":
		return in.ThreadID > 1 && in.ThreadID <= 1<<52-1
	case "close", "reopen", "ban", "unban", "info", "pending", "reset":
		return true
	}
	return false
}

func (s *Service) lockCommandTopicTx(ctx context.Context, tx pgx.Tx, actor uuid.UUID, topic store.SupportTelegramTopic) (store.SupportTelegramTopic, error) {
	if topic.ID == uuid.Nil {
		_, err := s.authority.LockOperatorPair(ctx, tx, actor, actor)
		return topic, err
	}
	if topic.AccountID != nil {
		if _, err := s.lockSupport(ctx, tx, actor, *topic.AccountID, true); err != nil {
			return topic, err
		}
	} else if topic.Kind == "guest" && topic.GuestTgID.Valid {
		if err := s.authority.LockSupportGuestOperatorTx(ctx, tx, actor, topic.GuestTgID.Int64); err != nil {
			return topic, err
		}
	} else {
		if _, err := s.authority.LockOperatorPair(ctx, tx, actor, actor); err != nil {
			return topic, err
		}
	}
	current, err := store.New(tx).LockTelegramTopic(ctx, topic.ID)
	if err != nil {
		return topic, unavailable()
	}
	if current.BotID != s.telegramBotID || current.GroupID != s.telegramGroupID || current.Status == "retired" || current.Kind != topic.Kind || !sameUUID(current.AccountID, topic.AccountID) || current.GuestTgID != topic.GuestTgID {
		return topic, failure(409, "REQUEST_STATE_CONFLICT")
	}
	return current, nil
}

func (s *Service) BeginTelegramCommand(ctx context.Context, source TelegramSource, topicID uuid.UUID, in TelegramCommandInput) (TelegramCommandReceipt, error) {
	var out TelegramCommandReceipt
	if in.ConfirmationID != uuid.Nil {
		return s.confirmTelegramCommand(ctx, source, in)
	}
	if in.Reason == "" {
		in.Reason = fmt.Sprintf("Telegram /%s (generated)", in.Action)
		if in.Action == "comp" {
			in.Reason = fmt.Sprintf("Telegram /comp %d (generated)", in.Days)
		}
		in.GeneratedReason = true
	}
	validationSource := source
	if source.ThreadID <= 1 {
		validationSource.ThreadID = 2
	}
	if source.ThreadID < 0 || !s.validTelegramSource(validationSource) || source.ChatID != s.telegramGroupID || source.Action != "command" || source.CallbackID != "" || !telegramCommandInput(in) {
		return out, failure(400, "INVALID_INPUT")
	}
	actor, ok := accounts.TelegramActor(ctx)
	if !ok || actor.TelegramID == nil || *actor.TelegramID != source.ActorID {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	var topic store.SupportTelegramTopic
	if topicID == uuid.Nil && source.ThreadID <= 1 {
		if in.Action != "info" && in.Action != "pending" {
			return out, failure(409, "REQUEST_STATE_CONFLICT")
		}
	} else if topicID == uuid.Nil {
		topic, err = q.TelegramTopicByThread(ctx, store.TelegramTopicByThreadParams{BotID: s.telegramBotID, GroupID: s.telegramGroupID, ThreadID: telegramInt(source.ThreadID)})
	} else {
		topic, err = q.TelegramTopicByID(ctx, topicID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, unavailable()
	}
	out = TelegramCommandReceipt{ID: telegramReceiptID(source), OwnerKey: telegramReceiptID(source), TopicID: topic.ID, ActorAccountID: actor.ID, SourceVersion: actor.CredentialVersion, TargetAccountID: topic.AccountID, GuestTelegramID: topic.GuestTgID.Int64, Input: in, AwaitingConfirmation: in.Action == "retry" || in.Action == "bind"}
	hash := bodyHash(struct {
		Digest [32]byte
		Actor  int64
		Topic  uuid.UUID
		Input  TelegramCommandInput
	}{source.Digest, source.ActorID, topic.ID, in})
	prior, priorErr := q.TelegramReceipt(ctx, out.ID)

	if priorErr == nil {
		var saved TelegramCommandReceipt
		if json.Unmarshal(prior.Result, &saved) != nil {
			return out, unavailable()
		}
		if saved.Input.Action == "bind" && saved.Result != nil && saved.Result.Status == "completed" && saved.Result.ReplacementTopicID != uuid.Nil {
			topic, err = q.TelegramTopicByID(ctx, saved.Result.ReplacementTopicID)
			if err != nil {
				return out, unavailable()
			}
		}
	}
	topic, err = s.lockCommandTopicTx(ctx, tx, actor.ID, topic)
	if err != nil {
		return out, err
	}
	if priorErr == nil {
		if !bytes.Equal(prior.Digest, hash) || prior.ActorAccountID == nil || *prior.ActorAccountID != actor.ID {
			return out, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		if json.Unmarshal(prior.Result, &out) != nil {
			return out, unavailable()
		}
		if out.SourceVersion != actor.CredentialVersion {
			return out, failure(403, "INVALID_CREDENTIALS")
		}
		return out, nil
	}
	if !errors.Is(priorErr, pgx.ErrNoRows) {
		return out, unavailable()
	}
	if in.Action == "bind" {
		if topicID == uuid.Nil || topic.Kind == "orphan" || topic.Status != "unknown" && topic.Status != "failed" || source.ThreadID > 1 && source.ThreadID != in.ThreadID {
			return out, failure(409, "REQUEST_STATE_CONFLICT")
		}
	} else if topic.ID != uuid.Nil && !(in.Action == "info" && source.ThreadID <= 1) && (topic.Status != "ready" || topic.ThreadID != telegramInt(source.ThreadID)) {
		return out, failure(409, "REQUEST_STATE_CONFLICT")
	}
	if topic.AccountID == nil {
		if topic.Kind == "orphan" && in.Action != "info" && in.Action != "pending" {
			return out, failure(409, "REQUEST_STATE_CONFLICT")
		}
		switch in.Action {
		case "comp", "reset", "approve", "reject", "reconsider", "retry":
			return out, failure(409, "REQUEST_STATE_CONFLICT")
		}
	}
	ctx = auditreports.WithTelegramActor(ctx, actor.ID, source.ActorID)
	owned := false
	switch in.Action {
	case "info":
		out.Result = &TelegramCommandResult{Status: "completed"}
		if topic.ID == uuid.Nil {
			out.Result.Topics, err = q.PendingTelegramTopics(ctx, store.PendingTelegramTopicsParams{BotID: s.telegramBotID, GroupID: s.telegramGroupID})
			if err != nil {
				return out, unavailable()
			}
			if len(out.Result.Topics) > 20 {
				out.Result.Topics = out.Result.Topics[:20]
				out.Result.More = true
			}
		} else {
			out.Result.TopicID, out.Result.TopicStatus = topic.ID, topic.Status
			delivery, e := q.LatestTopicTelegramDelivery(ctx, &topic.ID)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return out, unavailable()
			}
			if e == nil {
				out.Result.DeliveryID = delivery.ID
				out.Result.DeliveryStatus = delivery.Status
				out.Result.DeliveryCode = delivery.Code
			}
		}
		if topic.AccountID != nil {
			out.Result.Links = []TelegramCommandLink{{AccountID: *topic.AccountID}}
			c, e := q.LockSupportByAccount(ctx, *topic.AccountID)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return out, unavailable()
			}
			if c.SupportBanned || topic.SupportBanned {
				out.Result.Code = "SUPPORT_BANNED"
			}
			a, e := s.authority.LookupTx(ctx, tx, *topic.AccountID)
			if e != nil {
				return out, e
			}
			if a.TelegramID != nil {
				banned, e := q.TelegramGuestBanned(ctx, *a.TelegramID)
				if e != nil {
					return out, unavailable()
				}
				if banned {
					if out.Result.Code == "" {
						out.Result.Code = "GUEST_BANNED"
					} else {
						out.Result.Code += "_AND_GUEST_BANNED"
					}
				}
			}
		} else if topic.Kind == "guest" {
			banned, e := q.TelegramGuestBanned(ctx, topic.GuestTgID.Int64)
			if e != nil {
				return out, unavailable()
			}
			if banned {
				out.Result.Code = "GUEST_BANNED"
			}
		}
	case "ban", "unban":
		banned := in.Action == "ban"
		if topic.AccountID != nil {
			err = s.setSupportBanTx(ctx, tx, actor.ID, *topic.AccountID, banned, in.Reason)
		} else if topic.Kind == "guest" {
			err = q.SetTelegramGuestBan(ctx, store.SetTelegramGuestBanParams{TelegramID: topic.GuestTgID.Int64, Banned: banned})
			if err == nil {
				err = q.SetTelegramTopicBan(ctx, store.SetTelegramTopicBanParams{ID: topic.ID, SupportBanned: banned})
			}
		} else {
			return out, failure(409, "REQUEST_STATE_CONFLICT")
		}
		owned = true
	case "close", "reopen":
		closed := in.Action == "close"
		if topic.AccountID != nil {
			state := "open"
			if closed {
				state = "closed"
			}
			err = s.setSupportStateTx(ctx, tx, actor.ID, *topic.AccountID, true, state)
		}
		if err == nil {
			err = q.SetTelegramTopicClosed(ctx, store.SetTelegramTopicClosedParams{ID: topic.ID, Closed: closed})
		}
		owned = true
	}
	if err != nil {
		return out, err
	}
	if owned {
		out.Result = &TelegramCommandResult{Status: "completed"}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, unavailable()
	}
	var mappedTopic *uuid.UUID
	if topic.ID != uuid.Nil {
		mappedTopic = &topic.ID
	}
	if q.AddTelegramReceipt(ctx, store.AddTelegramReceiptParams{ID: out.ID, BotID: source.BotID, GroupID: source.GroupID, ChatID: source.ChatID, MessageID: source.MessageID, UpdateID: source.UpdateID, ActorTgID: telegramInt(source.ActorID), ThreadID: telegramInt(source.ThreadID), Action: source.Action, Digest: hash, ActorAccountID: &actor.ID, TargetAccountID: topic.AccountID, TopicID: mappedTopic, OwnerKey: out.OwnerKey, Result: raw}) != nil {
		return out, unavailable()
	}
	if err = s.commandAuditTx(ctx, tx, source, topic, out.ID, in.Reason, in.Action); err != nil {
		return out, err
	}
	if out.Result != nil {
		if err = s.commandReplyTx(ctx, tx, out, topic, telegramCommandText(*out.Result)); err != nil {
			return out, err
		}
	}
	if out.AwaitingConfirmation {
		prompt := "Confirm /" + in.Action + ". Delivery may be duplicated."
		if in.Action == "bind" {
			prompt += "\nTopic: " + topic.ID.String() + fmt.Sprintf("\nThread: %d", in.ThreadID) + "\nConfirm that this thread belongs to the selected topic."
		}
		if err = s.commandReplyTx(ctx, tx, out, topic, prompt); err != nil {
			return out, err
		}
	}
	if in.Action == "close" || in.Action == "reopen" {
		parts, err := s.commandPartsTx(ctx, tx, out, topic, in.Action+"_topic", "")
		if err != nil {
			return out, err
		}
		if _, err = s.addTelegramDeliveryTx(ctx, tx, topic.ID, nil, &out.ID, "topic_action", parts, nil); err != nil {
			return out, err
		}
	}
	if tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	return out, nil
}

func (s *Service) commandPartsTx(ctx context.Context, tx pgx.Tx, receipt TelegramCommandReceipt, topic store.SupportTelegramTopic, kind, text string) ([]TelegramPart, error) {
	actor, ok := accounts.TelegramActor(ctx)
	if !ok || actor.TelegramID == nil || actor.ID != receipt.ActorAccountID || actor.CredentialVersion != receipt.SourceVersion {
		return nil, failure(403, "INVALID_CREDENTIALS")
	}
	p := TelegramPart{Kind: kind, ChatID: s.telegramGroupID, ThreadID: topic.ThreadID.Int64, Text: text, Status: "queued", SourceAccountID: actor.ID, SourceTelegramID: *actor.TelegramID, SourceVersion: actor.CredentialVersion, SourceOperator: true}
	if topic.AccountID != nil {
		a, err := s.authority.LookupTx(ctx, tx, *topic.AccountID)
		if err != nil {
			return nil, err
		}
		p.RecipientVersion = a.CredentialVersion
		if a.TelegramID != nil {
			p.RecipientTelegramID = *a.TelegramID
		}
	} else if topic.GuestTgID.Valid {
		p.RecipientTelegramID = topic.GuestTgID.Int64
	}
	return []TelegramPart{p}, nil
}
func (s *Service) commandReplyTx(ctx context.Context, tx pgx.Tx, receipt TelegramCommandReceipt, topic store.SupportTelegramTopic, text string) error {
	parts, err := s.commandPartsTx(ctx, tx, receipt, topic, "control", text)
	if err != nil {
		return err
	}
	source, err := store.New(tx).TelegramReceipt(ctx, receipt.ID)
	if err != nil {
		return unavailable()
	}
	parts[0].ThreadID = source.ThreadID.Int64
	if parts[0].ThreadID <= 1 {
		parts[0].Kind, parts[0].ThreadID = "general", 0
	}
	if receipt.AwaitingConfirmation {
		parts[0].Kind, parts[0].ConfirmationID = "confirmation", receipt.ID
		if receipt.TargetAccountID != nil {
			parts[0].Links = []TelegramCommandLink{{AccountID: *receipt.TargetAccountID}}
		}
	}
	if receipt.Result != nil {
		parts[0].Links = receipt.Result.Links
	}
	_, err = s.addTelegramDeliveryTx(ctx, tx, uuid.Nil, nil, &receipt.ID, "ack", parts, nil)
	return err
}
func (s *Service) commandAuditTx(ctx context.Context, tx pgx.Tx, source TelegramSource, topic store.SupportTelegramTopic, receipt uuid.UUID, reason, action string) error {
	if topic.AccountID != nil {
		actor, ok := accounts.TelegramActor(ctx)
		if !ok {
			return failure(403, "INVALID_CREDENTIALS")
		}
		return s.supportAudit(ctx, tx, "support_telegram_command_"+action, *topic.AccountID, actor.ID, true, nil, reason)
	}
	actor, ok := accounts.TelegramActor(ctx)
	if !ok {
		return failure(403, "INVALID_CREDENTIALS")
	}
	kind := "command"
	if action == "ban" {
		kind = "guest_banned"
	}
	if action == "unban" {
		kind = "guest_unbanned"
	}
	var thread *int64
	var topicID *uuid.UUID
	if source.ThreadID > 1 {
		thread = &source.ThreadID
	}
	if topic.ID != uuid.Nil {
		topicID = &topic.ID
	}
	return auditreports.RecordSupportTelegramTx(ctx, tx, auditreports.SupportTelegramEvent{ID: uuid.New(), BotID: source.BotID, GroupID: source.GroupID, ChatID: source.ChatID, MessageID: source.MessageID, UpdateID: source.UpdateID, ActorTgID: &source.ActorID, ActorAccountID: &actor.ID, ThreadID: thread, TopicID: topicID, ReceiptID: &receipt, Kind: kind, Outcome: "completed", Reason: &reason})
}

func (s *Service) CompleteTelegramCommand(ctx context.Context, id uuid.UUID, in TelegramCommandResult) error {
	if id == uuid.Nil || !validTelegramCommandResult(in) {
		return failure(400, "INVALID_INPUT")
	}
	q := store.New(s.pool)
	initial, err := q.TelegramReceipt(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return unavailable()
	}
	var receipt TelegramCommandReceipt
	if json.Unmarshal(initial.Result, &receipt) != nil || receipt.ID != id {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	actor, ok := accounts.TelegramActor(ctx)
	if !ok || actor.ID != receipt.ActorAccountID || actor.CredentialVersion != receipt.SourceVersion {
		return failure(403, "INVALID_CREDENTIALS")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q = store.New(tx)
	var topic store.SupportTelegramTopic
	if receipt.TopicID != uuid.Nil {
		topic, err = q.TelegramTopicByID(ctx, receipt.TopicID)
		if err != nil {
			return unavailable()
		}
	}
	topic, err = s.lockCommandTopicTx(ctx, tx, actor.ID, topic)
	if err != nil {
		return err
	}
	row, err := q.LockTelegramReceipt(ctx, id)
	if err != nil {
		return unavailable()
	}
	if row.BotID != s.telegramBotID || row.GroupID != s.telegramGroupID || row.ActorAccountID == nil || *row.ActorAccountID != actor.ID || json.Unmarshal(row.Result, &receipt) != nil || !sameUUID(receipt.TargetAccountID, topic.AccountID) || receipt.AwaitingConfirmation {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	if receipt.Result != nil {
		if !bytes.Equal(bodyHash(*receipt.Result), bodyHash(in)) {
			return failure(409, "IDEMPOTENCY_CONFLICT")
		}
		return nil
	}
	receipt.Result = &in
	raw, err := json.Marshal(receipt)
	if err != nil {
		return unavailable()
	}
	if q.CompleteTelegramReceipt(ctx, store.CompleteTelegramReceiptParams{ID: id, Result: raw, TopicID: row.TopicID}) != nil {
		return unavailable()
	}
	if err = s.commandReplyTx(ctx, tx, receipt, topic, telegramCommandText(in)); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}
func telegramCommandText(in TelegramCommandResult) string {
	text := "Command " + in.Status
	if in.Code != "" {
		text += " (" + in.Code + ")"
	}
	if in.OperationID != uuid.Nil {
		text += "\nOperation: " + in.OperationID.String()
	}
	if in.RequestID != uuid.Nil {
		text += "\nRequest: " + in.RequestID.String()
	}
	if in.DeliveryID != uuid.Nil {
		text += "\nDelivery: " + in.DeliveryID.String()
		if in.DeliveryStatus != "" {
			text += " (" + in.DeliveryStatus + ")"
		}
		if in.DeliveryCode != "" {
			text += " (" + in.DeliveryCode + ")"
		}
	}
	if in.TopicID != uuid.Nil {
		text += "\nTopic: " + in.TopicID.String() + " (" + in.TopicStatus + ")"
	}
	for _, id := range in.Topics {
		text += "\nTopic: " + id.String()
	}
	if len(in.Links) > 0 {
		text += fmt.Sprintf("\nProtected links: %d", len(in.Links))
	}
	if in.More {
		text += "\nMore results are available in the cabinet."
	}
	return text
}
func validTelegramCommandResult(in TelegramCommandResult) bool {
	switch in.Status {
	case "completed", "queued", "pending", "applied", "rejected", "failed", "cancelled", "needs_review":
	default:
		return false
	}
	if len(in.Code) > 48 || len(in.DeliveryCode) > 48 || len(in.Topics) > 20 {
		return false
	}
	if len(in.Links) > 50 {
		return false
	}
	for _, link := range in.Links {
		if link.AccountID == uuid.Nil {
			return false
		}
	}
	for _, id := range in.Topics {
		if id == uuid.Nil {
			return false
		}
	}
	switch in.TopicStatus {
	case "", "pending", "sending", "ready", "failed", "unknown", "retired":
	default:
		return false
	}
	switch in.DeliveryStatus {
	case "", "queued", "sending", "sent", "failed", "unknown", "skipped":
	default:
		return false
	}
	for _, r := range in.Code + in.DeliveryCode {
		if r != '_' && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func (s *Service) ObserveTelegramTopic(ctx context.Context, source TelegramSource, closed bool) error {
	if s.telegramBotID == 0 || source.BotID != s.telegramBotID || source.GroupID != s.telegramGroupID || source.ChatID != s.telegramGroupID || source.MessageID <= 0 || source.MessageID > 1<<52-1 || source.UpdateID < 0 || source.UpdateID > 1<<52-1 || source.ActorID < 0 || source.ActorID > 1<<52-1 || source.ThreadID <= 1 || source.ThreadID > 1<<52-1 || source.Digest == [32]byte{} || source.CallbackID != "" || closed && source.Action != "topic_closed" || !closed && source.Action != "topic_reopened" {
		return failure(400, "INVALID_INPUT")
	}
	// Native provider facts never grant the observed human an operator role.
	if _, hasPrincipal := accounts.TelegramActor(ctx); hasPrincipal {
		return failure(400, "INVALID_INPUT")
	}
	var observedActor *accounts.Snapshot
	if source.ActorID > 0 {
		a, err := s.authority.LookupTelegram(ctx, source.ActorID)
		if err == nil {
			observedActor = &a
		} else if !errors.Is(err, accounts.ErrNotFound) {
			return err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	topic, err := q.TelegramTopicByThread(ctx, store.TelegramTopicByThreadParams{BotID: s.telegramBotID, GroupID: s.telegramGroupID, ThreadID: telegramInt(source.ThreadID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return unavailable()
	}
	var conversation store.SupportConversation
	if topic.AccountID != nil {
		if _, err = s.authority.Lock(ctx, tx, *topic.AccountID); err != nil {
			return err
		}
		conversation, err = q.LockSupportByAccount(ctx, *topic.AccountID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return unavailable()
		}
	}
	topic, err = q.LockTelegramTopic(ctx, topic.ID)
	if err != nil {
		return unavailable()
	}
	if topic.BotID != s.telegramBotID || topic.GroupID != s.telegramGroupID || topic.Status != "ready" || topic.ThreadID != telegramInt(source.ThreadID) {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	id := telegramReceiptID(source)
	hash := bodyHash(struct {
		Digest [32]byte
		Actor  int64
		Closed bool
	}{source.Digest, source.ActorID, closed})
	prior, err := q.TelegramReceipt(ctx, id)
	if err == nil {
		if !bytes.Equal(prior.Digest, hash) {
			return failure(409, "IDEMPOTENCY_CONFLICT")
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return unavailable()
	}
	var actorID *uuid.UUID
	if observedActor != nil {
		current, err := s.authority.LookupTx(ctx, tx, observedActor.ID)
		if err != nil && !errors.Is(err, accounts.ErrNotFound) {
			return err
		}
		if err == nil && current.TelegramID != nil && *current.TelegramID == source.ActorID {
			actorID = &current.ID
		}
	}
	if q.SetTelegramTopicClosed(ctx, store.SetTelegramTopicClosedParams{ID: topic.ID, Closed: closed}) != nil {
		return unavailable()
	}
	if conversation.ID != uuid.Nil {
		state := "open"
		if closed {
			state = "closed"
		}
		if q.UpdateSupportState(ctx, store.UpdateSupportStateParams{ID: conversation.ID, Status: state, UpdatedAt: stamp(s.now())}) != nil {
			return unavailable()
		}
	}
	if q.AddTelegramReceipt(ctx, store.AddTelegramReceiptParams{ID: id, BotID: source.BotID, GroupID: source.GroupID, ChatID: source.ChatID, MessageID: source.MessageID, UpdateID: source.UpdateID, ActorTgID: telegramInt(source.ActorID), ThreadID: telegramInt(source.ThreadID), Action: source.Action, Digest: hash, ActorAccountID: actorID, TargetAccountID: topic.AccountID, TopicID: &topic.ID, OwnerKey: id, Result: []byte("{}")}) != nil {
		return unavailable()
	}
	event := auditreports.SupportTelegramEvent{ID: uuid.New(), BotID: source.BotID, GroupID: source.GroupID, ChatID: source.ChatID, MessageID: source.MessageID, UpdateID: source.UpdateID, ThreadID: &source.ThreadID, ActorAccountID: actorID, TargetAccountID: topic.AccountID, TopicID: &topic.ID, ReceiptID: &id, Kind: source.Action, Outcome: "observed"}
	if source.ActorID > 0 {
		event.ActorTgID = &source.ActorID
	}
	if auditreports.RecordSupportTelegramTx(ctx, tx, event) != nil {
		return unavailable()
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) BindTelegramTopic(ctx context.Context, id uuid.UUID, thread int64, confirmed bool) error {
	if id == uuid.Nil || thread <= 1 || thread > 1<<52-1 || !confirmed {
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	row, err := q.TelegramReceipt(ctx, id)
	if err != nil || row.TopicID == nil {
		return failure(404, "INVALID_INPUT")
	}
	var receipt TelegramCommandReceipt
	actor, ok := accounts.TelegramActor(ctx)
	if !ok || json.Unmarshal(row.Result, &receipt) != nil || receipt.ActorAccountID != actor.ID || receipt.SourceVersion != actor.CredentialVersion {
		return failure(403, "INVALID_CREDENTIALS")
	}
	topic, err := q.TelegramTopicByID(ctx, *row.TopicID)
	if err != nil {
		return unavailable()
	}

	if receipt.Input.Action == "bind" && receipt.Result != nil && receipt.Result.Status == "completed" && receipt.Result.ReplacementTopicID != uuid.Nil {
		topic, err = q.TelegramTopicByID(ctx, receipt.Result.ReplacementTopicID)
		if err != nil {
			return unavailable()
		}
	}
	topic, err = s.lockCommandTopicTx(ctx, tx, actor.ID, topic)
	if err != nil {
		return err
	}
	row, err = q.LockTelegramReceipt(ctx, id)
	if err != nil {
		return unavailable()
	}
	if json.Unmarshal(row.Result, &receipt) != nil || row.BotID != s.telegramBotID || row.GroupID != s.telegramGroupID || receipt.Input.Action != "bind" || receipt.Input.ThreadID != thread || !receipt.Input.Confirmed || receipt.AwaitingConfirmation || !sameUUID(receipt.TargetAccountID, topic.AccountID) {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	if receipt.Result != nil {
		if receipt.Result.Status != "completed" || topic.Status != "ready" || topic.ThreadID != telegramInt(thread) {
			return failure(409, "REQUEST_STATE_CONFLICT")
		}
		return nil
	}
	if topic.Kind == "orphan" || topic.Status != "unknown" && topic.Status != "failed" {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	inUse, err := q.TelegramThreadInUse(ctx, store.TelegramThreadInUseParams{BotID: s.telegramBotID, GroupID: s.telegramGroupID, ThreadID: telegramInt(thread), ID: topic.ID})
	if err != nil {
		return unavailable()
	}
	if inUse {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	bound := topic
	if topic.ThreadID.Valid {
		if topic.ThreadID.Int64 == thread {
			return failure(409, "REQUEST_STATE_CONFLICT")
		}
		if q.RetireTelegramTopic(ctx, topic.ID) != nil {
			return unavailable()
		}
		bound, err = q.AddReplacementTelegramTopic(ctx, store.AddReplacementTelegramTopicParams{NewID: uuid.New(), NewThread: thread, OldID: topic.ID})
	} else {
		err = q.SetTelegramTopicState(ctx, store.SetTelegramTopicStateParams{ID: topic.ID, Status: "ready", ThreadID: telegramInt(thread)})
	}
	if err != nil {
		var unique *pgconn.PgError
		if errors.As(err, &unique) && unique.Code == "23505" {
			return failure(409, "REQUEST_STATE_CONFLICT")
		}
		return unavailable()
	}
	receipt.Result = &TelegramCommandResult{Status: "completed"}
	if bound.ID != topic.ID {
		receipt.Result.ReplacementTopicID = bound.ID
	}
	raw, err := json.Marshal(receipt)
	if err != nil || q.CompleteTelegramReceipt(ctx, store.CompleteTelegramReceiptParams{ID: id, Result: raw, TopicID: row.TopicID}) != nil {
		return unavailable()
	}
	ctx = auditreports.WithTelegramActor(ctx, actor.ID, row.ActorTgID.Int64)
	source := TelegramSource{BotID: row.BotID, GroupID: row.GroupID, ChatID: row.ChatID, MessageID: row.MessageID, UpdateID: row.UpdateID, ActorID: row.ActorTgID.Int64, ThreadID: row.ThreadID.Int64}
	if err = s.commandAuditTx(ctx, tx, source, topic, id, receipt.Input.Reason, "bind"); err != nil {
		return err
	}
	if err = s.commandReplyTx(ctx, tx, receipt, bound, "Topic binding completed"); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}

func (s *Service) confirmTelegramCommand(ctx context.Context, source TelegramSource, in TelegramCommandInput) (TelegramCommandReceipt, error) {
	var out TelegramCommandReceipt
	check := source
	if check.ThreadID <= 1 {
		check.ThreadID = 2
	}
	if source.ThreadID < 0 || !s.validTelegramSource(check) || source.ChatID != s.telegramGroupID || source.Action != in.Action || in.Action != "confirm" && in.Action != "cancel" || in.Confirmed != (in.Action == "confirm") || len(source.CallbackID) < 1 || len(source.CallbackID) > 128 || !utf8.ValidString(source.CallbackID) || strings.ContainsRune(source.CallbackID, '\x00') || in.Reason != "" || in.Days != 0 || in.DeliveryID != uuid.Nil || in.TrialRequestID != uuid.Nil || in.ThreadID != 0 || in.GeneratedReason {
		return out, failure(400, "INVALID_INPUT")
	}
	actor, ok := accounts.TelegramActor(ctx)
	if !ok || actor.TelegramID == nil || *actor.TelegramID != source.ActorID {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	row, err := q.TelegramReceipt(ctx, in.ConfirmationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, unavailable()
	}
	if json.Unmarshal(row.Result, &out) != nil || out.ID != in.ConfirmationID || out.ActorAccountID != actor.ID || out.SourceVersion != actor.CredentialVersion || row.ActorTgID != telegramInt(source.ActorID) || row.BotID != source.BotID || row.GroupID != source.GroupID {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	var topic store.SupportTelegramTopic
	if row.TopicID != nil {
		topic, err = q.TelegramTopicByID(ctx, *row.TopicID)
		if err != nil {
			return out, unavailable()
		}
	}

	if out.Input.Action == "bind" && out.Result != nil && out.Result.Status == "completed" && out.Result.ReplacementTopicID != uuid.Nil {
		topic, err = q.TelegramTopicByID(ctx, out.Result.ReplacementTopicID)
		if err != nil {
			return out, unavailable()
		}
	}
	topic, err = s.lockCommandTopicTx(ctx, tx, actor.ID, topic)
	if err != nil {
		return out, err
	}
	row, err = q.LockTelegramReceipt(ctx, in.ConfirmationID)
	if err != nil || json.Unmarshal(row.Result, &out) != nil {
		return out, unavailable()
	}
	if !sameUUID(out.TargetAccountID, topic.AccountID) || (row.ThreadID.Int64 > 1 || source.ThreadID > 1) && row.ThreadID.Int64 != source.ThreadID {
		return out, failure(409, "REQUEST_STATE_CONFLICT")
	}
	callback := telegramReceiptID(source)
	hash := bodyHash(struct {
		Digest [32]byte
		Actor  int64
		Input  TelegramCommandInput
	}{source.Digest, source.ActorID, in})
	prior, priorErr := q.TelegramReceipt(ctx, callback)
	if priorErr == nil {
		if !bytes.Equal(prior.Digest, hash) || json.Unmarshal(prior.Result, &out) != nil {
			return out, failure(409, "IDEMPOTENCY_CONFLICT")
		}
		return out, nil
	}
	if !errors.Is(priorErr, pgx.ErrNoRows) {
		return out, unavailable()
	}
	if !out.AwaitingConfirmation || out.Result != nil {
		return out, failure(409, "REQUEST_STATE_CONFLICT")
	}
	delivered, err := q.TelegramConfirmationDelivered(ctx, store.TelegramConfirmationDeliveredParams{ReceiptID: &in.ConfirmationID, Column2: source.MessageID})
	if err != nil {
		return out, unavailable()
	}
	if !delivered {
		return out, failure(409, "REQUEST_STATE_CONFLICT")
	}
	out.AwaitingConfirmation = false
	out.Input.Confirmed = in.Confirmed
	if !in.Confirmed {
		out.Result = &TelegramCommandResult{Status: "cancelled"}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, unavailable()
	}
	if q.CompleteTelegramReceipt(ctx, store.CompleteTelegramReceiptParams{ID: out.ID, Result: raw, TopicID: row.TopicID}) != nil {
		return out, unavailable()
	}
	if q.AddTelegramReceipt(ctx, store.AddTelegramReceiptParams{ID: callback, BotID: source.BotID, GroupID: source.GroupID, ChatID: source.ChatID, MessageID: source.MessageID, UpdateID: source.UpdateID, ActorTgID: telegramInt(source.ActorID), ThreadID: telegramInt(source.ThreadID), Action: source.Action, CallbackID: source.CallbackID, Digest: hash, ActorAccountID: &actor.ID, TargetAccountID: out.TargetAccountID, TopicID: row.TopicID, OwnerKey: callback, Result: raw}) != nil {
		return out, unavailable()
	}
	ctx = auditreports.WithTelegramActor(ctx, actor.ID, source.ActorID)
	if err = s.commandAuditTx(ctx, tx, source, topic, out.ID, out.Input.Reason, in.Action); err != nil {
		return out, err
	}
	if !in.Confirmed {
		if err = s.commandReplyTx(ctx, tx, out, topic, "Command cancelled"); err != nil {
			return out, err
		}
	}
	if tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	return out, nil
}

func (s *Service) RetryTelegramDelivery(ctx context.Context, actor, target, delivery, key uuid.UUID, confirmed bool, reason string) (TelegramDeliveryStatus, bool, error) {
	var out TelegramDeliveryStatus
	if !confirmed || key == uuid.Nil || delivery == uuid.Nil || !validText(reason, 1, 1000) {
		return out, false, failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, false, unavailable()
	}
	defer tx.Rollback(ctx)
	c, err := s.lockSupport(ctx, tx, actor, target, true)
	if err != nil {
		return out, false, err
	}
	q := store.New(tx)
	principal := "account:" + actor.String()
	hash := bodyHash(struct {
		Target, Delivery uuid.UUID
		Reason           string
	}{target, delivery, reason})
	if q.LockIdempotency(ctx, store.LockIdempotencyParams{Principal: principal, Operation: "retrySupportTelegramDelivery", Key: key}) != nil {
		return out, false, unavailable()
	}
	if prior, found, e := replay[TelegramDeliveryStatus](ctx, q, principal, "retrySupportTelegramDelivery", key, hash); found || e != nil {
		return prior, false, e
	}
	row, err := q.TelegramDeliveryByID(ctx, delivery)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return out, false, unavailable()
	}
	if row.Kind != "message" || row.MessageID == nil || row.TopicID == nil {
		return out, false, failure(409, "REQUEST_STATE_CONFLICT")
	}
	topic, err := q.LockTelegramTopic(ctx, *row.TopicID)
	if err != nil {
		return out, false, unavailable()
	}
	if topic.Kind != "account" || topic.AccountID == nil || *topic.AccountID != target || topic.BotID != s.telegramBotID || topic.GroupID != s.telegramGroupID {
		return out, false, failure(404, "INVALID_INPUT")
	}
	if topic.Status == "retired" {
		topic, err = q.AccountTelegramTopic(ctx, store.AccountTelegramTopicParams{BotID: s.telegramBotID, GroupID: s.telegramGroupID, AccountID: &target})
		if err != nil {
			return out, false, failure(409, "REQUEST_STATE_CONFLICT")
		}
		topic, err = q.LockTelegramTopic(ctx, topic.ID)
		if err != nil {
			return out, false, unavailable()
		}
	}
	if topic.Status != "ready" || c.SupportBanned || topic.SupportBanned || c.ID == uuid.Nil {
		return out, false, failure(409, "REQUEST_STATE_CONFLICT")
	}
	row, err = q.LockTelegramDelivery(ctx, delivery)
	if err != nil {
		return out, false, unavailable()
	}
	if row.Status != "failed" && row.Status != "unknown" {
		return out, false, failure(409, "REQUEST_STATE_CONFLICT")
	}
	// The conversation lock serializes new attempts. Only its latest intent
	// contains every confirmed part from the entire retry chain.
	latest, err := q.LatestMessageTelegramDelivery(ctx, row.MessageID)
	if err != nil {
		return out, false, unavailable()
	}
	if latest.ID != row.ID {
		return out, false, failure(409, "REQUEST_STATE_CONFLICT")
	}
	active, err := q.ActiveMessageTelegramDelivery(ctx, row.MessageID)
	if err != nil {
		return out, false, unavailable()
	}
	if active {
		return out, false, failure(409, "REQUEST_STATE_CONFLICT")
	}
	m, err := q.SupportMessageByID(ctx, *row.MessageID)
	if err != nil {
		return out, false, unavailable()
	}
	if m.ConversationID != c.ID {
		return out, false, failure(404, "INVALID_INPUT")
	}
	a, err := s.authority.LookupTx(ctx, tx, target)
	if err != nil {
		return out, false, err
	}
	if a.Restricted || a.TelegramLoginDisabled || !accounts.SourceEligible(a) {
		return out, false, failure(403, "ACCOUNT_RESTRICTED")
	}
	tg := int64(0)
	if a.TelegramID != nil {
		tg = *a.TelegramID
		banned, e := q.TelegramGuestBanned(ctx, tg)
		if e != nil {
			return out, false, unavailable()
		}
		if banned {
			return out, false, failure(403, "ACCOUNT_RESTRICTED")
		}
	}
	var parts []TelegramPart
	if json.Unmarshal(row.Parts, &parts) != nil || len(parts) == 0 || len(parts) > 8 {
		return out, false, unavailable()
	}
	remaining := 0
	for i := range parts {
		p := &parts[i]
		if p.Status == "sent" {
			continue
		}
		if p.RecipientVersion != a.CredentialVersion || p.RecipientTelegramID != tg || p.ChatID != s.telegramGroupID && p.ChatID != tg {
			return out, false, failure(409, "REQUEST_STATE_CONFLICT")
		}
		p.Status, p.Code, p.Attempts, p.ConfirmedMessageID = "queued", "", 0, 0
		if proof, ok := accounts.TelegramActor(ctx); ok && proof.TelegramID != nil {
			p.SourceAccountID, p.SourceTelegramID, p.SourceVersion = proof.ID, *proof.TelegramID, proof.CredentialVersion
		}
		remaining++
	}
	if remaining == 0 {
		return out, false, failure(409, "REQUEST_STATE_CONFLICT")
	}
	id, err := s.addTelegramDeliveryTx(ctx, tx, topic.ID, row.MessageID, row.ReceiptID, "message", parts, &delivery)
	if err != nil {
		return out, false, err
	}
	out = *telegramDeliveryStatus(id, "queued", "", topic.Status, m.TelegramOnly.Bool)
	if proof, ok := accounts.TelegramActor(ctx); ok {
		ctx = auditreports.WithTelegramActor(ctx, actor, *proof.TelegramID)
	}
	if err = s.supportAudit(ctx, tx, "support_telegram_retry", target, actor, true, row.MessageID, reason); err != nil {
		return out, false, err
	}
	if err = s.saveIdempotency(ctx, q, principal, "retrySupportTelegramDelivery", key, hash, out); err != nil {
		return out, false, err
	}
	if tx.Commit(ctx) != nil {
		return out, false, unavailable()
	}
	return out, true, nil
}
