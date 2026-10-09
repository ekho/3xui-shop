package support

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/support/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

type TelegramDelivery struct{ ID, Lease uuid.UUID }
type TelegramPart struct {
	Kind                string    `json:"kind"`
	ChatID              int64     `json:"chat_id"`
	ThreadID            int64     `json:"thread_id,omitempty"`
	CopyChatID          int64     `json:"copy_chat_id,omitempty"`
	CopyMessageID       int64     `json:"copy_message_id,omitempty"`
	Text                string    `json:"text,omitempty"`
	Name                string    `json:"name,omitempty"`
	Bytes               []byte    `json:"-"`
	MessageID           uuid.UUID `json:"message_id,omitempty"`
	RecipientVersion    int64     `json:"recipient_version,omitempty"`
	RecipientTelegramID int64     `json:"recipient_telegram_id,omitempty"`
	SourceAccountID     uuid.UUID `json:"source_account_id,omitempty"`
	SourceTelegramID    int64     `json:"source_telegram_id,omitempty"`
	SourceVersion       int64     `json:"source_version,omitempty"`
	Status              string    `json:"status"`
	Code                string    `json:"code,omitempty"`
	ConfirmedMessageID  int64     `json:"confirmed_message_id,omitempty"`
	Attempts            int       `json:"attempts,omitempty"`
}
type TelegramOutcome struct {
	Status, Code        string
	MessageID, ThreadID int64
	RetryAfter          time.Duration
}

func (s *Service) ClaimTelegramDelivery(ctx context.Context) (*TelegramDelivery, error) {
	if s.telegramBotID == 0 {
		return nil, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	if err = q.ExpireTelegramSending(ctx); err != nil {
		return nil, unavailable()
	}
	lease := uuid.New()
	row, err := q.ClaimTelegramDelivery(ctx, store.ClaimTelegramDeliveryParams{Lease: &lease, BotID: s.telegramBotID, GroupID: s.telegramGroupID})
	if errors.Is(err, pgx.ErrNoRows) {
		if tx.Commit(ctx) != nil {
			return nil, unavailable()
		}
		return nil, nil
	}
	if err != nil {
		return nil, unavailable()
	}
	if tx.Commit(ctx) != nil {
		return nil, unavailable()
	}
	return &TelegramDelivery{ID: row.ID, Lease: lease}, nil
}
func (s *Service) DeliverTelegram(ctx context.Context, job TelegramDelivery, send func(context.Context, TelegramPart) (TelegramOutcome, error)) error {
	if job.ID == uuid.Nil || job.Lease == uuid.Nil || send == nil {
		return failure(400, "INVALID_INPUT")
	}
	for {
		row, err := store.New(s.pool).TelegramDeliveryByID(ctx, job.ID)
		if err != nil {
			return unavailable()
		}
		if row.Lease == nil || *row.Lease != job.Lease || row.Status != "queued" && row.Status != "sending" {
			return failure(409, "REQUEST_STATE_CONFLICT")
		}
		var parts []TelegramPart
		if json.Unmarshal(row.Parts, &parts) != nil || len(parts) == 0 || len(parts) > 8 {
			return unavailable()
		}
		index := -1
		for i, p := range parts {
			if p.Status == "queued" {
				index = i
				break
			}
		}
		if index < 0 {
			return failure(409, "REQUEST_STATE_CONFLICT")
		}
		// No provider call occurs before this separate transaction commits.
		if err = s.markTelegramSending(ctx, job, row.TopicID, index); err != nil {
			return err
		}
		wireCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = s.sendTelegramPart(wireCtx, job, row.TopicID, index, parts[index], send)
		cancel()
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			store.New(s.pool).UnknownTelegramDelivery(cleanup, store.UnknownTelegramDeliveryParams{ID: job.ID, Lease: &job.Lease})
			return err
		}
		current, err := store.New(s.pool).TelegramDeliveryByID(ctx, job.ID)
		if err != nil {
			return unavailable()
		}
		if current.Status != "sending" {
			return nil
		}
	}
}

func (s *Service) markTelegramSending(ctx context.Context, job TelegramDelivery, topic uuid.UUID, index int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	t, err := q.LockTelegramTopic(ctx, topic)
	if err != nil {
		return unavailable()
	}
	r, err := q.LockTelegramDelivery(ctx, job.ID)
	if err != nil {
		return unavailable()
	}
	var parts []TelegramPart
	if r.Lease == nil || *r.Lease != job.Lease || r.Status != "queued" && r.Status != "sending" || json.Unmarshal(r.Parts, &parts) != nil || index >= len(parts) || parts[index].Status != "queued" {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	if t.BotID != s.telegramBotID || t.GroupID != s.telegramGroupID {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	parts[index].Status = "sending"
	parts[index].Attempts++
	if err = s.saveTelegramDeliveryTx(ctx, tx, job, "sending", "", parts, 0); err != nil {
		return err
	}
	if r.Kind == "topic_create" {
		if t.Status != "pending" {
			return failure(409, "REQUEST_STATE_CONFLICT")
		}
		if err = q.SetTelegramTopicState(ctx, store.SetTelegramTopicStateParams{ID: t.ID, Status: "sending"}); err != nil {
			return unavailable()
		}
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}

func (s *Service) saveTelegramDeliveryTx(ctx context.Context, tx pgx.Tx, job TelegramDelivery, status, code string, parts []TelegramPart, delay int32) error {
	raw, err := json.Marshal(parts)
	if err != nil {
		return unavailable()
	}
	n, err := store.New(tx).SaveTelegramDelivery(ctx, store.SaveTelegramDeliveryParams{ID: job.ID, Status: status, Code: code, Parts: raw, DelaySeconds: delay, ExpectedLease: job.Lease})
	if err != nil {
		return unavailable()
	}
	if n != 1 {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	return nil
}

func (s *Service) sendTelegramPart(ctx context.Context, job TelegramDelivery, topic uuid.UUID, index int, p TelegramPart, send func(context.Context, TelegramPart) (TelegramOutcome, error)) error {
	t, err := store.New(s.pool).TelegramTopicByID(ctx, topic)
	if err != nil {
		return unavailable()
	}
	if t.Kind != "account" || t.AccountID == nil {
		return s.finishTelegramNoWire(ctx, job, index, TelegramOutcome{Status: "skipped", Code: "SOURCE_REVOKED"})
	}
	sourceCtx := ctx
	if p.SourceAccountID != uuid.Nil {
		var a *accounts.Snapshot
		sourceCtx, a, err = s.authority.ResolveTelegramContext(ctx, p.SourceTelegramID)
		if err != nil || a == nil || a.ID != p.SourceAccountID || a.CredentialVersion != p.SourceVersion {
			var ae *accounts.Error
			if err != nil && (!errors.As(err, &ae) || ae.Status >= 500) {
				return err
			}
			return s.finishTelegramNoWire(ctx, job, index, TelegramOutcome{Status: "skipped", Code: "SOURCE_REVOKED"})
		}
	}
	work := func(tx pgx.Tx) error {
		q := store.New(tx)
		c, e := q.LockSupportByAccount(ctx, *t.AccountID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return unavailable()
		}
		if c.SupportBanned {
			return s.finishTelegramPartTx(ctx, tx, job, index, TelegramOutcome{Status: "skipped", Code: "SUPPORT_BANNED"})
		}
		if p.RecipientTelegramID > 0 {
			banned, e := q.TelegramGuestBanned(ctx, p.RecipientTelegramID)
			if e != nil {
				return unavailable()
			}
			if banned {
				return s.finishTelegramPartTx(ctx, tx, job, index, TelegramOutcome{Status: "skipped", Code: "SUPPORT_BANNED"})
			}
		}
		current, e := q.LockTelegramTopic(ctx, t.ID)
		if e != nil {
			return unavailable()
		}
		if current.AccountID == nil || *current.AccountID != *t.AccountID || current.BotID != s.telegramBotID || current.GroupID != s.telegramGroupID || p.Kind == "create_topic" && current.Status != "sending" || p.Kind != "create_topic" && current.Status != "ready" {
			return s.finishTelegramPartTx(ctx, tx, job, index, TelegramOutcome{Status: "skipped", Code: "TOPIC_RETIRED"})
		}
		if p.ChatID == s.telegramGroupID {
			if p.Kind != "create_topic" {
				p.ThreadID = current.ThreadID.Int64
			}
		} else if p.ChatID <= 0 || p.ChatID != p.RecipientTelegramID {
			return s.finishTelegramPartTx(ctx, tx, job, index, TelegramOutcome{Status: "skipped", Code: "NO_RECIPIENT"})
		}
		locked, e := q.LockTelegramDelivery(ctx, job.ID)
		if e != nil {
			return unavailable()
		}
		var parts []TelegramPart
		if locked.Lease == nil || *locked.Lease != job.Lease || locked.Status != "sending" || json.Unmarshal(locked.Parts, &parts) != nil || index >= len(parts) || parts[index].Status != "sending" {
			return failure(409, "REQUEST_STATE_CONFLICT")
		}
		if p.Kind == "document" {
			m, e := q.SupportMessageByID(ctx, p.MessageID)
			if e != nil {
				return unavailable()
			}
			p.Bytes = m.AttachmentBytes
		}
		out, wireError := send(ctx, p)
		out = normalizeTelegramOutcome(parts[index], out, wireError)
		return s.finishTelegramPartTx(ctx, tx, job, index, out)
	}
	if p.RecipientTelegramID > 0 {
		valid, e := s.authority.WithTelegramDelivery(sourceCtx, *t.AccountID, p.RecipientTelegramID, p.RecipientVersion, work)
		if e != nil {
			var ae *accounts.Error
			if errors.As(e, &ae) && ae.Status < 500 {
				return s.finishTelegramNoWire(ctx, job, index, TelegramOutcome{Status: "skipped", Code: "SOURCE_REVOKED"})
			}
			return e
		}
		if !valid {
			return s.finishTelegramNoWire(ctx, job, index, TelegramOutcome{Status: "skipped", Code: "DESTINATION_CHANGED"})
		}
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	a, err := s.authority.Lock(sourceCtx, tx, *t.AccountID)
	if err != nil {
		return err
	}
	if a.TelegramID != nil || a.CredentialVersion != p.RecipientVersion || a.Restricted || a.TelegramLoginDisabled || !accounts.SourceEligible(a) || a.TermsVersion == nil || a.PrivacyVersion == nil {
		err = s.finishTelegramPartTx(ctx, tx, job, index, TelegramOutcome{Status: "skipped", Code: "DESTINATION_CHANGED"})
	} else {
		err = work(tx)
	}
	if err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}

func normalizeTelegramOutcome(p TelegramPart, out TelegramOutcome, wireError error) TelegramOutcome {
	if wireError != nil && out.Status != "failed" && out.Status != "retry" {
		return TelegramOutcome{Status: "unknown", Code: "NETWORK"}
	}
	switch out.Status {
	case "sent":
		if out.Code == "" && out.RetryAfter == 0 && (p.Kind == "create_topic" && out.ThreadID > 1 && out.ThreadID <= 1<<52-1 && out.MessageID == 0 || p.Kind != "create_topic" && out.MessageID > 0 && out.MessageID <= 1<<52-1) {
			return out
		}
	case "failed":
		if out.MessageID == 0 && out.ThreadID == 0 && out.RetryAfter == 0 {
			switch out.Code {
			case "BAD_REQUEST", "FORBIDDEN", "UNAUTHORIZED", "CONFLICT", "THREAD_NOT_FOUND":
				return out
			}
		}
	case "retry":
		if out.Code == "RATE_LIMITED" && out.MessageID == 0 && out.ThreadID == 0 && out.RetryAfter >= time.Second && out.RetryAfter <= 24*time.Hour {
			if p.Attempts < 5 {
				return out
			}
			return TelegramOutcome{Status: "failed", Code: "RETRY_EXHAUSTED"}
		}
	case "unknown":
		return TelegramOutcome{Status: "unknown", Code: "ACK_UNKNOWN"}
	}
	return TelegramOutcome{Status: "unknown", Code: "INVALID_RESPONSE"}
}

func (s *Service) finishTelegramPartTx(ctx context.Context, tx pgx.Tx, job TelegramDelivery, index int, out TelegramOutcome) error {
	q := store.New(tx)
	lookup, err := q.TelegramDeliveryByID(ctx, job.ID)
	if err != nil {
		return unavailable()
	}
	if _, err = q.LockTelegramTopic(ctx, lookup.TopicID); err != nil {
		return unavailable()
	}
	r, err := q.LockTelegramDelivery(ctx, job.ID)
	if err != nil {
		return unavailable()
	}
	var parts []TelegramPart
	if r.Lease == nil || *r.Lease != job.Lease || r.Status != "sending" || json.Unmarshal(r.Parts, &parts) != nil || index >= len(parts) || parts[index].Status != "sending" {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	p := &parts[index]
	p.Status, p.Code = out.Status, out.Code
	p.ConfirmedMessageID = out.MessageID
	status, code := out.Status, out.Code
	delay := int32(0)
	if out.Status == "sent" {
		for _, part := range parts {
			if part.Status == "queued" {
				status = "sending"
				break
			}
		}
	}
	if out.Status == "retry" {
		p.Status = "queued"
		status = "queued"
		delay = int32((out.RetryAfter + time.Second - 1) / time.Second)
	}
	if r.Kind == "topic_create" {
		topicState := out.Status
		thread := telegramInt(out.ThreadID)
		if topicState == "sent" {
			topicState = "ready"
		}
		if topicState == "retry" {
			topicState = "pending"
		}
		if topicState == "skipped" {
			topicState = "failed"
		}
		if err = q.SetTelegramTopicState(ctx, store.SetTelegramTopicStateParams{ID: r.TopicID, Status: topicState, ThreadID: thread}); err != nil {
			return unavailable()
		}
	}
	return s.saveTelegramDeliveryTx(ctx, tx, job, status, code, parts, delay)
}
func (s *Service) finishTelegramNoWire(ctx context.Context, job TelegramDelivery, index int, out TelegramOutcome) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	if err = s.finishTelegramPartTx(ctx, tx, job, index, out); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}
