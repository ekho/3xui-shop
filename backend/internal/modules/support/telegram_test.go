package support

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func TestSupportTelegramGeneralGuard(t *testing.T) {
	s := &Service{telegramBotID: 973, telegramGroupID: -10074001}
	in := TelegramSource{BotID: 973, GroupID: -10074001, ChatID: -10074001, MessageID: 12, UpdateID: 1, ActorID: 732, ThreadID: 1, Action: "message", Digest: [32]byte{1}}
	if s.validTelegramSource(in) {
		t.Error("General accepted as a personal support topic")
	}
	if out := normalizeTelegramOutcome(TelegramPart{Kind: "create_topic"}, TelegramOutcome{Status: "sent", ThreadID: 1}, nil); out.Status != "unknown" {
		t.Error("General ACK accepted as a private topic")
	}
}

func supportTelegramFixture(t *testing.T) (*Service, *accounts.Service, *testkit.Env, uuid.UUID, uuid.UUID) {
	t.Helper()
	e := testkit.Open(t)
	ctx := context.Background()
	a := accounts.New(e.Pool, e.Redis, nil, accounts.Config{TermsVersion: "1", PrivacyVersion: "1", RateNamespace: uuid.NewString(), Now: e.Clock})
	customer, _, err := a.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 731, DisplayName: "Support customer", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id)
VALUES($1,'support-operator@example.test','ru','owned fixture hash',$2,$3,'aaaaaaaaaaaaaaaa',$4,'1','1',732)`, actor, e.Clock(), uuid.New(), "acct_"+actor.String()); err != nil {
		t.Fatal(err)
	}
	if err = a.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	n := notifications.New(e.Pool, nil, nil, nil, a.WithTelegramDelivery)
	s := New(e.Pool, e.Redis, a, uuid.NewString(), e.Clock, n)
	if err = s.ConfigureTelegram(973, -10074001); err != nil {
		t.Fatal(err)
	}
	return s, a, e, customer.Account.ID, actor
}

// Removing the receipt/source comparison or enqueue/audit in the same Tx must
// lose this one-message result, duplicate it, or leave a partial rollback.
func TestSupportTelegramTextReceipt(t *testing.T) {
	for _, rejectAudit := range []bool{false, true} {
		t.Run(map[bool]string{false: "flow", true: "audit_rollback"}[rejectAudit], func(t *testing.T) {
			s, a, e, customer, operator := supportTelegramFixture(t)
			ctx, who, err := a.ResolveTelegramContext(context.Background(), 731)
			if err != nil || who == nil {
				t.Fatal("customer source", err)
			}
			in := TelegramInput{Source: TelegramSource{BotID: 973, GroupID: -10074001, ChatID: 731, MessageID: 4, UpdateID: 999, ActorID: 731, Action: "message", Digest: sha256.Sum256([]byte("literal Telegram source"))}, Text: "<b>plain support text</b>"}
			if rejectAudit {
				if _, err = e.Pool.Exec(ctx, `CREATE FUNCTION reject_support_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='support_message' THEN RAISE EXCEPTION 'controlled audit rejection'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_support BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_support_audit()`); err != nil {
					t.Fatal(err)
				}
			}
			first, err := s.ReceiveTelegramMessage(ctx, in)
			if rejectAudit {
				if err == nil {
					t.Fatal("audit rejection committed relay")
				}
				var messages, receipts, jobs, topics int
				if err = e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM support_messages),(SELECT count(*) FROM support_telegram_receipts),(SELECT count(*) FROM support_telegram_deliveries),(SELECT count(*) FROM support_telegram_topics)`).Scan(&messages, &receipts, &jobs, &topics); err != nil || messages != 0 || receipts != 0 || jobs != 0 || topics != 0 {
					t.Fatal("partial relay after audit failure", err, messages, receipts, jobs, topics)
				}
				return
			}
			if err != nil || first.Message == nil || first.Replay || first.ID == uuid.Nil || first.Message.Text != "<b>plain support text</b>" {
				t.Fatal("support text not received", err)
			}
			if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET display_name='renamed' WHERE id=$1", customer); err != nil {
				t.Fatal(err)
			}
			in.Source.UpdateID = 1 // Telegram may restart its sequence after a week.
			again, err := s.ReceiveTelegramMessage(ctx, in)
			if err != nil || !again.Replay || again.ID != first.ID || again.Message == nil || again.Message.Id != first.Message.Id {
				t.Fatal("logical replay depends on update ID/name", err)
			}
			changed := in
			changed.Text = "different content"
			if _, err = s.ReceiveTelegramMessage(ctx, changed); err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" {
				t.Fatal("conflicting source accepted", err)
			}
			var messages, audits, receipts, jobs int
			if err = e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM support_messages),(SELECT count(*) FROM audit_events WHERE action='support_message'),(SELECT count(*) FROM support_telegram_receipts),(SELECT count(*) FROM support_telegram_deliveries)`).Scan(&messages, &audits, &receipts, &jobs); err != nil || messages != 1 || audits != 1 || receipts != 1 || jobs != 2 {
				t.Fatal("relay duplicate or missing durable effects", err, messages, audits, receipts, jobs)
			}
			creation, err := s.ClaimTelegramDelivery(ctx)
			if err != nil || creation == nil {
				t.Fatal("missing topic creation", err)
			}
			if err = s.DeliverTelegram(ctx, *creation, func(context.Context, TelegramPart) (TelegramOutcome, error) {
				return TelegramOutcome{Status: "sent", ThreadID: 888}, nil
			}); err != nil {
				t.Fatal(err)
			}
			opCtx, who, err := a.ResolveTelegramContext(context.Background(), 732)
			if err != nil || who == nil {
				t.Fatal("operator source", err)
			}
			reply := TelegramInput{Source: TelegramSource{BotID: 973, GroupID: -10074001, ChatID: -10074001, MessageID: 5, UpdateID: 2, ActorID: 732, ThreadID: 888, Action: "message", Digest: sha256.Sum256([]byte("literal operator source"))}, Text: "operator answer"}
			answer, err := s.ReceiveTelegramMessage(opCtx, reply)
			if err != nil || answer.Message == nil || answer.Message.Sender != "operator" {
				t.Fatal("forum reply not stored", err)
			}
			var actorID uuid.UUID
			var actorTG int64
			if err = e.Pool.QueryRow(ctx, "SELECT operator_account_id,operator_tg_id FROM audit_events WHERE support_message_id=$1", answer.Message.Id).Scan(&actorID, &actorTG); err != nil || actorID != operator || actorTG != 732 {
				t.Fatal("actual forum actor lost", err, actorTG)
			}
			if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET credential_version=credential_version+1 WHERE id=$1", operator); err != nil {
				t.Fatal(err)
			}
			reply.Source.MessageID++
			if _, err = s.ReceiveTelegramMessage(opCtx, reply); err == nil {
				t.Fatal("revoked operator source stored another reply")
			}
			if err = s.SetSupportBan(context.Background(), operator, customer, true, "owned abuse fixture"); err != nil {
				t.Fatal(err)
			}
			in.Source.MessageID++
			if _, err = s.ReceiveTelegramMessage(ctx, in); err == nil || err.Error() != "ACCOUNT_RESTRICTED" {
				t.Fatal("support ban bypassed", err)
			}
			if _, err = e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", customer); err != nil {
				t.Fatal(err)
			}
			in.Source.MessageID++
			if _, err = s.ReceiveTelegramMessage(ctx, in); err == nil || err.Error() != "ACCOUNT_RESTRICTED" {
				t.Fatal("account restriction bypassed", err)
			}
		})
	}
}

// A lost ACK or expired persisted sending marker must never trigger another
// automatic wire call; the callback reads the committed marker independently.
func TestSupportTelegramUnknownDelivery(t *testing.T) {
	s, _, e, customer, _ := supportTelegramFixture(t)
	ctx := context.Background()
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "web text", "", nil); err != nil {
		t.Fatal(err)
	}
	create, err := s.ClaimTelegramDelivery(ctx)
	if err != nil || create == nil {
		t.Fatal("no topic delivery", err)
	}
	err = s.DeliverTelegram(ctx, *create, func(ctx context.Context, p TelegramPart) (TelegramOutcome, error) {
		if p.Kind != "create_topic" || p.ChatID != -10074001 {
			t.Fatal("wrong topic boundary", p.Kind, p.ChatID)
		}
		var marker string
		if err := e.Pool.QueryRow(ctx, "SELECT parts->0->>'status' FROM support_telegram_deliveries WHERE id=$1", create.ID).Scan(&marker); err != nil || marker != "sending" {
			t.Fatal("topic wire preceded committed marker", err, marker)
		}
		return TelegramOutcome{Status: "sent", ThreadID: 888}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimTelegramDelivery(ctx)
	if err != nil || job == nil {
		t.Fatal("no message delivery", err)
	}
	err = s.DeliverTelegram(ctx, *job, func(ctx context.Context, p TelegramPart) (TelegramOutcome, error) {
		if p.Kind != "text" || p.ChatID != -10074001 || p.ThreadID != 888 || p.Text != "web text" {
			t.Fatal("wrong recipient/content", p.Kind, p.ChatID, p.ThreadID)
		}
		var marker string
		if err := e.Pool.QueryRow(ctx, "SELECT parts->0->>'status' FROM support_telegram_deliveries WHERE id=$1", job.ID).Scan(&marker); err != nil || marker != "sending" {
			t.Fatal("wire preceded committed marker", err, marker)
		}
		return TelegramOutcome{Status: "unknown", Code: "NETWORK"}, errors.New("owned lost ACK")
	})
	if err != nil {
		t.Fatal("unknown result not retained", err)
	}
	var state string
	if err = e.Pool.QueryRow(ctx, "SELECT status FROM support_telegram_deliveries WHERE id=$1", job.ID).Scan(&state); err != nil || state != "unknown" {
		t.Fatal("lost ACK not unknown", err, state)
	}
	if next, err := s.ClaimTelegramDelivery(ctx); err != nil || next != nil {
		t.Fatal("unknown retried automatically", err)
	}
	if _, _, err = s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "crash text", "", nil); err != nil {
		t.Fatal(err)
	}
	crash, err := s.ClaimTelegramDelivery(ctx)
	if err != nil || crash == nil {
		t.Fatal("crash claim", err)
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE support_telegram_deliveries SET status='sending',parts=jsonb_set(parts,'{0,status}','"sending"'),lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, crash.ID); err != nil {
		t.Fatal(err)
	}
	if next, err := s.ClaimTelegramDelivery(ctx); err != nil || next != nil {
		t.Fatal("expired marker retried", err)
	}
	if err = e.Pool.QueryRow(ctx, "SELECT status FROM support_telegram_deliveries WHERE id=$1", crash.ID).Scan(&state); err != nil || state != "unknown" {
		t.Fatal("crash uncertainty lost", err, state)
	}
}

func TestSupportTelegramConfigIsolation(t *testing.T) {
	s, authority, e, customer, _ := supportTelegramFixture(t)
	ctx := context.Background()
	old := New(e.Pool, e.Redis, authority, uuid.NewString(), e.Clock, nil)
	if old.ConfigureTelegram(974, -10074002) != nil {
		t.Fatal("owned old config rejected")
	}
	if _, _, err := old.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "older configured destination", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "current configured destination", "", nil); err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimTelegramDelivery(ctx)
	if err != nil || job == nil {
		t.Fatal("current delivery unavailable", err)
	}
	var bot, group int64
	if err = e.Pool.QueryRow(ctx, `SELECT t.bot_id,t.group_id FROM support_telegram_topics t JOIN support_telegram_deliveries d ON d.topic_id=t.id WHERE d.id=$1`, job.ID).Scan(&bot, &group); err != nil || bot != 973 || group != -10074001 {
		t.Fatal("old configuration blocked or captured current poller", err)
	}
}
