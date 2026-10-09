package support

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/support/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
)

func TestSupportTelegramRetiredRemainderRecovery(t *testing.T) {
	for _, sending := range []bool{false, true} {
		t.Run(map[bool]string{false: "deferred_429", true: "sending_marker"}[sending], func(t *testing.T) {
			s, _, e, customer, operator := supportTelegramFixture(t)
			ctx := context.Background()
			m, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), strings.Repeat("я", 4000), "owned.bin", []byte{7, 8})
			if err != nil {
				t.Fatal(err)
			}
			create, err := s.ClaimTelegramDelivery(ctx)
			if err != nil || create == nil || s.DeliverTelegram(ctx, *create, func(context.Context, TelegramPart) (TelegramOutcome, error) {
				return TelegramOutcome{Status: "sent", ThreadID: 888}, nil
			}) != nil {
				t.Fatal("topic", err)
			}
			job, err := s.ClaimTelegramDelivery(ctx)
			calls := 0
			if err != nil || job == nil || s.DeliverTelegram(ctx, *job, func(context.Context, TelegramPart) (TelegramOutcome, error) {
				calls++
				if calls == 1 {
					return TelegramOutcome{Status: "sent", MessageID: 61}, nil
				}
				return TelegramOutcome{Status: "retry", Code: "RATE_LIMITED", RetryAfter: time.Hour}, errors.New("owned429")
			}) != nil || calls != 2 {
				t.Fatal("partial deferral", err)
			}
			if sending {
				// A stopped process leaves its committed marker, without an ACK.
				if _, err = e.Pool.Exec(ctx, `UPDATE support_telegram_deliveries SET status='sending',lease=$2,lease_expires_at=clock_timestamp()+interval '60 seconds',parts=jsonb_set(parts,'{1,status}','"sending"') WHERE id=$1`, job.ID, uuid.New()); err != nil {
					t.Fatal(err)
				}
			}
			q := store.New(e.Pool)
			row, err := q.TelegramDeliveryByID(ctx, job.ID)
			if err != nil || row.TopicID == nil || q.RetireTelegramTopic(ctx, *row.TopicID) != nil {
				t.Fatal("retire fixture", err)
			}
			row, err = q.TelegramDeliveryByID(ctx, job.ID)
			var parts []TelegramPart
			expected := "failed"
			if sending {
				expected = "unknown"
			}
			if err != nil || row.Status != expected || row.Code != "TOPIC_RETIRED" || json.Unmarshal(row.Parts, &parts) != nil || parts[0].Status != "sent" || parts[0].ConfirmedMessageID != 61 || sending && parts[1].Status != "unknown" {
				t.Fatal("retirement lost recoverable remainder or ACK", err, row.Status)
			}
			if _, err = q.AddReplacementTelegramTopic(ctx, store.AddReplacementTelegramTopicParams{NewID: uuid.New(), NewThread: 999, OldID: *row.TopicID}); err != nil {
				t.Fatal(err)
			}
			if next, claimErr := s.ClaimTelegramDelivery(ctx); claimErr != nil || next != nil {
				t.Fatal("retired remainder automatically resent", claimErr)
			}
			page, err := s.Support(ctx, operator, customer, true)
			if err != nil || len(page.Messages) != 1 || page.Messages[0].Id != m.Id || !page.Messages[0].TelegramDelivery.RetryCapability {
				t.Fatal("recovery capability missing", err)
			}
			retry, _, err := s.RetryTelegramDelivery(ctx, operator, customer, job.ID, uuid.New(), true, "explicit replacement recovery")
			if err != nil {
				t.Fatal(err)
			}
			claim, err := s.ClaimTelegramDelivery(ctx)
			if err != nil || claim == nil || claim.ID != retry.Id || s.DeliverTelegram(ctx, *claim, func(_ context.Context, p TelegramPart) (TelegramOutcome, error) {
				calls++
				if p.ThreadID != 999 || p.Kind != "document" || !bytes.Equal(p.Bytes, []byte{7, 8}) {
					t.Fatal("old destination or confirmed text reused")
				}
				return TelegramOutcome{Status: "sent", MessageID: 62}, nil
			}) != nil || calls != 3 {
				t.Fatal("remaining file not recovered", err, calls)
			}
		})
	}
}

func TestSupportTelegramRetryChain(t *testing.T) {
	s, _, e, customer, operator := supportTelegramFixture(t)
	ctx := context.Background()
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), strings.Repeat("я", 4000), "owned.bin", []byte{7, 8}); err != nil {
		t.Fatal(err)
	}
	create, err := s.ClaimTelegramDelivery(ctx)
	if err != nil || create == nil || s.DeliverTelegram(ctx, *create, func(context.Context, TelegramPart) (TelegramOutcome, error) {
		return TelegramOutcome{Status: "sent", ThreadID: 888}, nil
	}) != nil {
		t.Fatal("topic", err)
	}
	first, err := s.ClaimTelegramDelivery(ctx)
	if err != nil || first == nil || s.DeliverTelegram(ctx, *first, func(context.Context, TelegramPart) (TelegramOutcome, error) {
		return TelegramOutcome{Status: "failed", Code: "FORBIDDEN"}, nil
	}) != nil {
		t.Fatal("initial failed delivery", err)
	}
	child, _, err := s.RetryTelegramDelivery(ctx, operator, customer, first.ID, uuid.New(), true, "first explicit retry")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimTelegramDelivery(ctx)
	calls := 0
	if err != nil || claimed == nil || claimed.ID != child.Id || s.DeliverTelegram(ctx, *claimed, func(_ context.Context, p TelegramPart) (TelegramOutcome, error) {
		calls++
		if calls == 1 {
			return TelegramOutcome{Status: "sent", MessageID: 61}, nil
		}
		return TelegramOutcome{Status: "unknown"}, errors.New("owned second ACK loss")
	}) != nil || calls != 2 {
		t.Fatal("partial child delivery", err, calls)
	}
	if _, _, err = s.RetryTelegramDelivery(ctx, operator, customer, first.ID, uuid.New(), true, "stale partial ancestor"); err == nil || err.Error() != "REQUEST_STATE_CONFLICT" {
		t.Fatal("stale ancestor ignored confirmed child parts", err)
	}
	last, _, err := s.RetryTelegramDelivery(ctx, operator, customer, child.Id, uuid.New(), true, "latest explicit retry")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = s.ClaimTelegramDelivery(ctx)
	if err != nil || claimed == nil || claimed.ID != last.Id || s.DeliverTelegram(ctx, *claimed, func(_ context.Context, p TelegramPart) (TelegramOutcome, error) {
		calls++
		if p.Kind != "document" || !bytes.Equal(p.Bytes, []byte{7, 8}) {
			t.Fatal("confirmed child text duplicated")
		}
		return TelegramOutcome{Status: "sent", MessageID: 62}, nil
	}) != nil || calls != 3 {
		t.Fatal("latest remaining part", err, calls)
	}
	row, err := store.New(e.Pool).TelegramDeliveryByID(ctx, last.Id)
	var parts []TelegramPart
	if err != nil || json.Unmarshal(row.Parts, &parts) != nil || len(parts) != 2 || parts[0].ConfirmedMessageID != 61 || parts[1].ConfirmedMessageID != 62 {
		t.Fatal("chain ACKs not retained", err)
	}
	for _, ancestor := range []uuid.UUID{first.ID, child.Id} {
		if _, _, err = s.RetryTelegramDelivery(ctx, operator, customer, ancestor, uuid.New(), true, "stale after success"); err == nil || err.Error() != "REQUEST_STATE_CONFLICT" {
			t.Fatal("successful chain was repeated", err)
		}
	}
}

func TestSupportTelegramMediaParts(t *testing.T) {
	t.Run("caption", func(t *testing.T) {
		for _, text := range []string{"", strings.Repeat("я", 1024), strings.Repeat("я", 4000)} {
			m := store.SupportMessage{ID: uuid.New(), Text: text, AttachmentName: pgtype.Text{String: "owned.bin", Valid: true}}
			parts := supportMessageParts(m, -10074001)
			if len([]rune(text)) <= 1024 {
				if len(parts) != 1 || parts[0].Kind != "document" || parts[0].Text != text {
					t.Error("caption not preserved in one document")
				}
			} else if len(parts) != 2 || parts[0].Text != text || parts[0].Kind != "text" || parts[1].Kind != "document" || parts[1].Text != "" {
				t.Error("long text/file split lost content")
			}
		}
	})
	t.Run("stored-and-fallback", func(t *testing.T) {
		s, a, e, customer, _ := supportTelegramFixture(t)
		ctx, _, err := a.ResolveTelegramContext(context.Background(), 731)
		if err != nil {
			t.Fatal(err)
		}
		in := TelegramInput{Source: TelegramSource{BotID: 973, GroupID: -10074001, ChatID: 731, ActorID: 731, MessageID: 7, Action: "message", Digest: sha256.Sum256([]byte("media"))}, Text: "caption", Name: "owned.bin", Bytes: []byte{0, 1, 255}, MediaKind: "document"}
		first, err := s.ReceiveTelegramMessage(ctx, in)
		if err != nil || first.Message == nil || first.Message.Attachment == nil || first.Message.Attachment.SizeBytes != 3 {
			t.Fatal("media not stored", err)
		}
		var actual []byte
		if e.Pool.QueryRow(ctx, "SELECT attachment_bytes FROM support_messages WHERE id=$1", first.Message.Id).Scan(&actual) != nil || !bytes.Equal(actual, in.Bytes) {
			t.Fatal("stored file differs")
		}
		in.Bytes, in.Name, in.TelegramOnly = nil, "", true
		again, err := s.ReceiveTelegramMessage(ctx, in)
		if err != nil || !again.Replay || again.Message == nil || again.Message.Id != first.Message.Id || again.Message.Attachment == nil {
			t.Fatal("download failure changed frozen replay", err)
		}
		for _, kind := range []string{"unknown", "document"} {
			in.Source.MessageID++
			in.MediaKind, in.Text = kind, ""
			fallback, err := s.ReceiveTelegramMessage(ctx, in)
			if err != nil || fallback.Message == nil || fallback.Message.Attachment != nil || fallback.Message.TelegramDelivery == nil || fallback.Message.TelegramDelivery.MediaAvailability != "telegram_only" {
				t.Fatal("Telegram-only source silently lost", err)
			}
		}
		in.Source.MessageID++
		in.Bytes, in.Name, in.TelegramOnly = make([]byte, supportFileMax+1), "oversize.bin", false
		oversize, err := s.ReceiveTelegramMessage(ctx, in)
		if err != nil || oversize.Message == nil || oversize.Message.Attachment != nil || oversize.Message.TelegramDelivery.MediaAvailability != "telegram_only" {
			t.Fatal("oversize copy discarded", err)
		}
		var conversation uuid.UUID
		if e.Pool.QueryRow(ctx, "SELECT id FROM support_conversations WHERE account_id=$1", customer).Scan(&conversation) != nil {
			t.Fatal("conversation absent")
		}
		if _, err = e.Pool.Exec(ctx, `INSERT INTO support_messages(id,conversation_id,sender_account_id,sender_kind,text,created_at,attachment_name,attachment_bytes) SELECT gen_random_uuid(),$1,$2,'customer','quota fixture',clock_timestamp(),'quota.bin',convert_to(repeat('x',10*1024*1024),'UTF8') FROM generate_series(1,5)`, conversation, customer); err != nil {
			t.Fatal(err)
		}
		in.Source.MessageID++
		in.Bytes, in.Name = []byte{4, 5}, "quota.bin"
		quota, err := s.ReceiveTelegramMessage(ctx, in)
		if err != nil || quota.Message == nil || quota.Message.Attachment != nil || quota.Message.TelegramDelivery.MediaAvailability != "telegram_only" {
			t.Fatal("file quota bypassed/lost copy", err)
		}
		var members []redis.Z
		for i := 0; i < 30; i++ {
			members = append(members, redis.Z{Score: float64(e.Clock().UnixMilli()), Member: uuid.NewString()})
		}
		if e.Redis.ZAdd(ctx, s.rateNamespace+":support:"+customer.String(), members...).Err() != nil {
			t.Fatal("rate fixture")
		}
		in.Source.MessageID++
		in.Bytes, in.Name, in.TelegramOnly = nil, "", true
		if _, err = s.ReceiveTelegramMessage(ctx, in); err == nil || err.Error() != "RATE_LIMITED" {
			t.Fatal("media fallback bypassed rate", err)
		}
	})
	t.Run("partial-unknown-confirmed-retry", func(t *testing.T) {
		s, _, e, customer, operator := supportTelegramFixture(t)
		ctx := context.Background()
		message, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), strings.Repeat("я", 4000), "owned.bin", []byte{7, 8})
		if err != nil {
			t.Fatal(err)
		}
		create, err := s.ClaimTelegramDelivery(ctx)
		if err != nil || create == nil {
			t.Fatal("topic missing", err)
		}
		if s.DeliverTelegram(ctx, *create, func(context.Context, TelegramPart) (TelegramOutcome, error) {
			return TelegramOutcome{Status: "sent", ThreadID: 888}, nil
		}) != nil {
			t.Fatal("topic ACK")
		}
		job, err := s.ClaimTelegramDelivery(ctx)
		if err != nil || job == nil {
			t.Fatal("message claim", err)
		}
		calls := 0
		if err = s.DeliverTelegram(ctx, *job, func(_ context.Context, p TelegramPart) (TelegramOutcome, error) {
			calls++
			if calls == 1 {
				if p.Kind != "text" || p.Text != message.Text {
					t.Fatal("full text lost")
				}
				return TelegramOutcome{Status: "sent", MessageID: 51}, nil
			}
			if p.Kind != "document" || !bytes.Equal(p.Bytes, []byte{7, 8}) {
				t.Fatal("file bytes lost")
			}
			return TelegramOutcome{Status: "unknown"}, errors.New("owned lost second ACK")
		}); err != nil || calls != 2 {
			t.Fatal("partial delivery", err, calls)
		}
		key := uuid.New()
		if _, _, err = s.RetryTelegramDelivery(ctx, operator, customer, job.ID, key, false, "owned confirmation"); err == nil {
			t.Fatal("unconfirmed unknown retry accepted")
		}
		retry, created, err := s.RetryTelegramDelivery(ctx, operator, customer, job.ID, key, true, "owned confirmation")
		if err != nil || !created || retry.Id == job.ID || retry.Status != "queued" {
			t.Fatal("confirmed retry not queued", err)
		}
		again, newIntent, err := s.RetryTelegramDelivery(ctx, operator, customer, job.ID, key, true, "owned confirmation")
		if err != nil || newIntent || again.Id != retry.Id {
			t.Fatal("retry key not stable", err)
		}
		claim, err := s.ClaimTelegramDelivery(ctx)
		if err != nil || claim == nil || claim.ID != retry.Id {
			t.Fatal("retry claim", err)
		}
		if err = s.DeliverTelegram(ctx, *claim, func(_ context.Context, p TelegramPart) (TelegramOutcome, error) {
			calls++
			if p.Kind != "document" || !bytes.Equal(p.Bytes, []byte{7, 8}) {
				t.Fatal("confirmed text duplicated")
			}
			return TelegramOutcome{Status: "sent", MessageID: 52}, nil
		}); err != nil || calls != 3 {
			t.Fatal("remaining part not sent", err)
		}
		var parts []TelegramPart
		row, err := store.New(e.Pool).TelegramDeliveryByID(ctx, retry.Id)
		if err != nil || json.Unmarshal(row.Parts, &parts) != nil || len(parts) != 2 || parts[0].ConfirmedMessageID != 51 || parts[1].ConfirmedMessageID != 52 {
			t.Fatal("part ACK history lost", err)
		}
		var messages, audits int
		if e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM support_messages),(SELECT count(*) FROM audit_events WHERE action='support_message')`).Scan(&messages, &audits) != nil || messages != 1 || audits != 1 {
			t.Fatal("retry duplicated domain message")
		}
		if _, _, err = s.RetryTelegramDelivery(ctx, operator, customer, job.ID, uuid.New(), true, "stale successful retry"); err == nil || err.Error() != "REQUEST_STATE_CONFLICT" {
			t.Fatal("successful descendant allowed stale retry", err)
		}
	})
	t.Run("429-bound", func(t *testing.T) {
		for attempts := 1; attempts <= 5; attempts++ {
			out := normalizeTelegramOutcome(TelegramPart{Attempts: attempts}, TelegramOutcome{Status: "retry", Code: "RATE_LIMITED", RetryAfter: time.Second}, errors.New("owned429"))
			if attempts < 5 && out.Status != "retry" || attempts == 5 && (out.Status != "failed" || out.Code != "RETRY_EXHAUSTED") {
				t.Fatal("429 bound lost", attempts)
			}
		}
		if normalizeTelegramOutcome(TelegramPart{}, TelegramOutcome{Status: "sent", MessageID: 0}, nil).Status != "unknown" {
			t.Fatal("invalid ACK accepted")
		}
	})
}
