package support

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/google/uuid"
)

func supportCommandSource(message int64, thread int64) TelegramSource {
	return TelegramSource{BotID: 973, GroupID: -10074001, ChatID: -10074001, ActorID: 732, MessageID: message, ThreadID: thread, Action: "command", Digest: sha256.Sum256([]byte("owned command"))}
}

func TestSupportTelegramGuestIsolation(t *testing.T) {
	s, a, e, _, operator := supportTelegramFixture(t)
	ctx := context.Background()
	in := TelegramInput{Source: TelegramSource{BotID: 973, GroupID: -10074001, ChatID: 913, ActorID: 913, MessageID: 17, Action: "message", Digest: sha256.Sum256([]byte("owned guest"))}, Text: "guest contact"}
	guestCtx, who, err := a.ResolveTelegramContext(ctx, 913)
	if err != nil || who != nil {
		t.Fatal("legitimate guest failed", err)
	}
	receipt, err := s.ReceiveTelegramMessage(guestCtx, in)
	if err != nil || receipt.ID == uuid.Nil || receipt.Message != nil {
		t.Fatal("guest contact did not create independent receipt", err)
	}
	var counts []int
	var accountsN, credentials, trials, operations, common int
	if e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts),(SELECT count(*) FROM credential_challenges),(SELECT count(*) FROM trial_requests),(SELECT count(*) FROM trial_operations),(SELECT count(*) FROM support_messages)`).Scan(&accountsN, &credentials, &trials, &operations, &common) != nil {
		t.Fatal("guest facts read")
	}
	counts = []int{accountsN, credentials, trials, operations, common}
	if accountsN != 2 || credentials != 0 || trials != 0 || operations != 0 || common != 0 {
		t.Fatal("guest created identity or access", counts)
	}
	again, err := s.ReceiveTelegramMessage(guestCtx, in)
	if err != nil || !again.Replay || again.ID != receipt.ID {
		t.Fatal("guest receipt replay lost", err)
	}
	create, err := s.ClaimTelegramDelivery(ctx)
	if err != nil || create == nil {
		t.Fatal("guest topic missing", err)
	}
	if err = s.DeliverTelegram(ctx, *create, func(_ context.Context, p TelegramPart) (TelegramOutcome, error) {
		if p.Kind != "create_topic" {
			t.Fatal("guest create kind")
		}
		return TelegramOutcome{Status: "sent", ThreadID: 888}, nil
	}); err != nil {
		t.Fatal("guest topic not created", err)
	}
	var topic uuid.UUID
	if e.Pool.QueryRow(ctx, `SELECT id FROM support_telegram_topics WHERE guest_tg_id=913 AND kind='guest' AND thread_id=888`).Scan(&topic) != nil {
		t.Fatal("guest topic projection lost")
	}
	opCtx, _, err := a.ResolveTelegramContext(ctx, 732)
	if err != nil {
		t.Fatal(err)
	}
	ban, err := s.BeginTelegramCommand(opCtx, supportCommandSource(18, 888), topic, TelegramCommandInput{Action: "ban", Reason: "owned guest abuse"})
	if err != nil || ban.ActorAccountID != operator || ban.GuestTelegramID != 913 {
		t.Fatal("guest moderation missing", err)
	}
	in.Source.MessageID++
	if _, err = s.ReceiveTelegramMessage(guestCtx, in); err == nil || err.Error() != "ACCOUNT_RESTRICTED" {
		t.Fatal("guest ban bypassed", err)
	}
	registered, _, err := a.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 913, DisplayName: "now registered", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	registeredCtx, _, err := a.ResolveTelegramContext(ctx, 913)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReceiveTelegramMessage(registeredCtx, in); err == nil || err.Error() != "ACCOUNT_RESTRICTED" {
		t.Fatal("guest ban disappeared at signup", err)
	}
	oldJob, err := s.ClaimTelegramDelivery(ctx)
	if err != nil || oldJob == nil {
		t.Fatal("old guest relay lost", err)
	}
	if err = s.DeliverTelegram(ctx, *oldJob, func(context.Context, TelegramPart) (TelegramOutcome, error) {
		t.Fatal("registered identity received old guest job")
		return TelegramOutcome{}, nil
	}); err != nil {
		t.Fatal("guest destination change not terminal", err)
	}
	if _, err = s.BeginTelegramCommand(opCtx, supportCommandSource(19, 888), topic, TelegramCommandInput{Action: "unban", Reason: "owned support reconsideration"}); err != nil {
		t.Fatal("retained guest ban cannot be cleared", err)
	}
	var reassigned bool
	if e.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM support_telegram_topics WHERE id=$1 AND (kind<>'guest' OR account_id IS NOT NULL)) OR EXISTS(SELECT 1 FROM support_messages m JOIN support_conversations c ON c.id=m.conversation_id WHERE c.account_id=$2)`, topic, registered.Account.ID).Scan(&reassigned) != nil || reassigned {
		t.Fatal("old guest history assigned to new account")
	}
	if _, err = e.Pool.Exec(ctx, `INSERT INTO telegram_identity_reservations(telegram_id,account_id,retired_at) VALUES(914,$1,clock_timestamp())`, registered.Account.ID); err != nil {
		t.Fatal(err)
	}
	in.Source.ChatID, in.Source.ActorID = 914, 914
	if _, err = s.ReceiveTelegramMessage(ctx, in); err == nil {
		t.Fatal("retired identity fell back to guest")
	}
}

func TestSupportTelegramCommands(t *testing.T) {
	t.Run("bounded-general-info", func(t *testing.T) {
		s, a, e, _, _ := supportTelegramFixture(t)
		ctx := context.Background()
		if _, err := e.Pool.Exec(ctx, `INSERT INTO support_telegram_topics(id,bot_id,group_id,kind,guest_tg_id,status) SELECT gen_random_uuid(),973,-10074001,'guest',1000+n,'unknown' FROM generate_series(1,21)n`); err != nil {
			t.Fatal(err)
		}
		op, _, err := a.ResolveTelegramContext(ctx, 732)
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.BeginTelegramCommand(op, supportCommandSource(88, 0), uuid.Nil, TelegramCommandInput{Action: "info"})
		if err != nil || r.Result == nil || len(r.Result.Topics) != 20 || !r.Result.More {
			t.Fatal("General lacks bounded recovery IDs", err)
		}
		selected, err := s.BeginTelegramCommand(op, supportCommandSource(89, 0), r.Result.Topics[0], TelegramCommandInput{Action: "info"})
		if err != nil || selected.Result == nil || selected.Result.TopicID != r.Result.Topics[0] || selected.Result.TopicStatus != "unknown" {
			t.Fatal("selected General topic metadata missing", err)
		}
	})
	t.Run("automatic-reopen", func(t *testing.T) {
		s, a, e, customer, _ := supportTelegramFixture(t)
		ctx := context.Background()
		if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "first", "", nil); err != nil {
			t.Fatal(err)
		}
		job, err := s.ClaimTelegramDelivery(ctx)
		if err != nil || job == nil {
			t.Fatal(err)
		}
		if err = s.DeliverTelegram(ctx, *job, func(context.Context, TelegramPart) (TelegramOutcome, error) {
			return TelegramOutcome{Status: "sent", ThreadID: 888}, nil
		}); err != nil {
			t.Fatal(err)
		}
		job, err = s.ClaimTelegramDelivery(ctx)
		if err != nil || job == nil {
			t.Fatal(err)
		}
		if err = s.DeliverTelegram(ctx, *job, func(context.Context, TelegramPart) (TelegramOutcome, error) {
			return TelegramOutcome{Status: "sent", MessageID: 90}, nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err = e.Pool.Exec(ctx, `UPDATE support_telegram_topics SET closed=true`); err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "new after close", "", nil); err != nil {
			t.Fatal(err)
		}
		job, err = s.ClaimTelegramDelivery(ctx)
		if err != nil || job == nil {
			t.Fatal(err)
		}
		var kinds []string
		if err = s.DeliverTelegram(ctx, *job, func(_ context.Context, p TelegramPart) (TelegramOutcome, error) {
			kinds = append(kinds, p.Kind)
			if p.Kind == "reopen_topic" {
				return TelegramOutcome{Status: "sent", Acknowledged: true}, nil
			}
			return TelegramOutcome{Status: "sent", MessageID: 91}, nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(kinds) != 2 || kinds[0] != "reopen_topic" || kinds[1] != "text" {
			t.Fatal("closed topic did not reopen before message", kinds)
		}
		// Source resolution remains independent of this forum state.
		if _, _, err = a.ResolveTelegramContext(ctx, 731); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("missing-thread-proof", func(t *testing.T) {
		s, _, e, customer, _ := supportTelegramFixture(t)
		ctx := context.Background()
		if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "known topic", "", nil); err != nil {
			t.Fatal(err)
		}
		job, err := s.ClaimTelegramDelivery(ctx)
		if err != nil || job == nil {
			t.Fatal(err)
		}
		if err = s.DeliverTelegram(ctx, *job, func(context.Context, TelegramPart) (TelegramOutcome, error) {
			return TelegramOutcome{Status: "sent", ThreadID: 888}, nil
		}); err != nil {
			t.Fatal(err)
		}
		job, err = s.ClaimTelegramDelivery(ctx)
		if err != nil || job == nil {
			t.Fatal(err)
		}
		if err = s.DeliverTelegram(ctx, *job, func(context.Context, TelegramPart) (TelegramOutcome, error) {
			return TelegramOutcome{Status: "failed", Code: "THREAD_NOT_FOUND"}, nil
		}); err != nil {
			t.Fatal(err)
		}
		var failed bool
		if e.Pool.QueryRow(ctx, `SELECT status='failed' AND thread_id=888 FROM support_telegram_topics`).Scan(&failed) != nil || !failed {
			t.Fatal("definite missing thread not retained for recovery")
		}
	})
	t.Run("general-ack-no-fake-topic", func(t *testing.T) {
		s, a, e, _, operator := supportTelegramFixture(t)
		ctx := context.Background()
		opCtx, _, err := a.ResolveTelegramContext(ctx, 732)
		if err != nil {
			t.Fatal(err)
		}
		source := supportCommandSource(31, 0)
		receipt, err := s.BeginTelegramCommand(opCtx, source, uuid.Nil, TelegramCommandInput{Action: "pending"})
		if err != nil || receipt.TopicID != uuid.Nil || receipt.ActorAccountID != operator {
			t.Fatal("General command lacks source-only intent", err)
		}
		result := TelegramCommandResult{Status: "completed"}
		if err = s.CompleteTelegramCommand(opCtx, receipt.ID, result); err != nil {
			t.Fatal("General ACK not durable", err)
		}
		if err = s.CompleteTelegramCommand(opCtx, receipt.ID, result); err != nil {
			t.Fatal("General ACK replay", err)
		}
		var topics, messages, jobs int
		if e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM support_telegram_topics),(SELECT count(*) FROM support_messages),(SELECT count(*) FROM support_telegram_deliveries WHERE topic_id IS NULL)`).Scan(&topics, &messages, &jobs) != nil || topics != 0 || messages != 0 || jobs != 1 {
			t.Fatal("General created fake topic/domain message", topics, messages, jobs)
		}
		job, err := s.ClaimTelegramDelivery(ctx)
		if err != nil || job == nil {
			t.Fatal("General claim", err)
		}
		if err = s.DeliverTelegram(ctx, *job, func(_ context.Context, p TelegramPart) (TelegramOutcome, error) {
			if p.Kind != "general" || p.ChatID != source.GroupID || p.ThreadID != 0 {
				t.Fatal("General destination not constrained")
			}
			return TelegramOutcome{Status: "unknown"}, nil
		}); err != nil {
			t.Fatal("General unknown", err)
		}
		if next, err := s.ClaimTelegramDelivery(ctx); err != nil || next != nil {
			t.Fatal("General ACK automatically repeated", err)
		}
		source.MessageID++
		r2, err := s.BeginTelegramCommand(opCtx, source, uuid.Nil, TelegramCommandInput{Action: "info"})
		if err != nil {
			t.Fatal(err)
		}
		if s.CompleteTelegramCommand(opCtx, r2.ID, result) != nil {
			t.Fatal("General second ACK")
		}
		claim, err := s.ClaimTelegramDelivery(ctx)
		if err != nil || claim == nil {
			t.Fatal(err)
		}
		if a.ChangeOperatorRole(ctx, operator, false) != nil {
			t.Fatal("role fixture")
		}
		if err = s.DeliverTelegram(ctx, *claim, func(context.Context, TelegramPart) (TelegramOutcome, error) {
			t.Fatal("revoked operator sent General response")
			return TelegramOutcome{}, nil
		}); err != nil {
			t.Fatal("General revocation not terminal", err)
		}
	})
	t.Run("stable-intent-and-revocation", func(t *testing.T) {
		s, a, e, customer, operator := supportTelegramFixture(t)
		ctx := context.Background()
		if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "owned topic", "", nil); err != nil {
			t.Fatal(err)
		}
		job, err := s.ClaimTelegramDelivery(ctx)
		if err != nil || job == nil {
			t.Fatal(err)
		}
		if s.DeliverTelegram(ctx, *job, func(context.Context, TelegramPart) (TelegramOutcome, error) {
			return TelegramOutcome{Status: "sent", ThreadID: 888}, nil
		}) != nil {
			t.Fatal("create")
		}
		var topic uuid.UUID
		if e.Pool.QueryRow(ctx, `SELECT id FROM support_telegram_topics WHERE account_id=$1`, customer).Scan(&topic) != nil {
			t.Fatal("topic")
		}
		opCtx, _, err := a.ResolveTelegramContext(ctx, 732)
		if err != nil {
			t.Fatal(err)
		}
		source := supportCommandSource(17, 888)
		input := TelegramCommandInput{Action: "comp", Days: 2, Reason: "owned compensation"}
		first, err := s.BeginTelegramCommand(opCtx, source, topic, input)
		if err != nil || first.OwnerKey == uuid.Nil || first.ActorAccountID != operator || first.TargetAccountID == nil || *first.TargetAccountID != customer {
			t.Fatal("stable owner intent missing", err)
		}
		source.UpdateID = 1
		again, err := s.BeginTelegramCommand(opCtx, source, topic, input)
		if err != nil || again.OwnerKey != first.OwnerKey || again.ID != first.ID {
			t.Fatal("owner key regenerated", err)
		}
		input.Days = 3
		if _, err = s.BeginTelegramCommand(opCtx, source, topic, input); err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" {
			t.Fatal("command input conflict accepted", err)
		}
		for _, days := range []int{0, 366} {
			source.MessageID++
			if _, err = s.BeginTelegramCommand(opCtx, source, topic, TelegramCommandInput{Action: "comp", Days: days, Reason: "owned invalid days"}); err == nil {
				t.Fatal("invalid comp days accepted")
			}
		}
		if err = a.ChangeOperatorRole(ctx, operator, false); err != nil {
			t.Fatal(err)
		}
		if err = s.CompleteTelegramCommand(opCtx, first.ID, TelegramCommandResult{Status: "queued", OperationID: uuid.New()}); err == nil {
			t.Fatal("revoked source completed command")
		}
	})
	t.Run("native-unknown-actor", func(t *testing.T) {
		s, _, e, customer, _ := supportTelegramFixture(t)
		ctx := context.Background()
		if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "owned native topic", "", nil); err != nil {
			t.Fatal(err)
		}
		job, err := s.ClaimTelegramDelivery(ctx)
		if err != nil || job == nil {
			t.Fatal(err)
		}
		if s.DeliverTelegram(ctx, *job, func(context.Context, TelegramPart) (TelegramOutcome, error) {
			return TelegramOutcome{Status: "sent", ThreadID: 888}, nil
		}) != nil {
			t.Fatal("create")
		}
		source := supportCommandSource(22, 888)
		source.ActorID = 0
		source.Action = "topic_closed"
		if err = s.ObserveTelegramTopic(ctx, source, true); err != nil {
			t.Fatal("native unknown actor ignored", err)
		}
		if err = s.ObserveTelegramTopic(ctx, source, true); err != nil {
			t.Fatal("native replay", err)
		}
		var status string
		if e.Pool.QueryRow(ctx, `SELECT status FROM support_conversations WHERE account_id=$1`, customer).Scan(&status) != nil || status != "closed" {
			t.Fatal("native state not reflected")
		}
		var raw []byte
		if e.Pool.QueryRow(ctx, `SELECT support_telegram FROM audit_system_events WHERE action='support.telegram'`).Scan(&raw) != nil {
			t.Fatal("native observation absent")
		}
		var event struct{ ActorAccountID, ActorTG, TargetAccountID *string }
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || string(fields["actor_account_id"]) != "null" || string(fields["actor_tg_id"]) != "null" {
			t.Fatal("invented native actor", event)
		}
		source.MessageID++
		source.Action = "topic_reopened"
		source.ActorID = 915
		if err = s.ObserveTelegramTopic(ctx, source, false); err != nil {
			t.Fatal("unregistered native actor ignored", err)
		}
		var roles int
		if e.Pool.QueryRow(ctx, `SELECT count(*) FROM operator_accounts`).Scan(&roles) != nil || roles != 1 {
			t.Fatal("native actor granted operator role")
		}
	})
}

func TestSupportTelegramConfirmation(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown-create", true: "known-replacement"}[known], func(t *testing.T) {
			s, a, e, customer, _ := supportTelegramFixture(t)
			ctx := context.Background()
			if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "owned bind recovery", "", nil); err != nil {
				t.Fatal(err)
			}
			create, err := s.ClaimTelegramDelivery(ctx)
			if err != nil || create == nil {
				t.Fatal("create", err)
			}
			if err = s.DeliverTelegram(ctx, *create, func(context.Context, TelegramPart) (TelegramOutcome, error) {
				if known {
					return TelegramOutcome{Status: "sent", ThreadID: 888}, nil
				}
				return TelegramOutcome{Status: "unknown"}, nil
			}); err != nil {
				t.Fatal(err)
			}
			thread := int64(888)
			failedDelivery := uuid.Nil
			queuedMessage := uuid.Nil
			if known {
				thread = 999
				pending, _, createErr := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "queued behind missing topic", "owned.bin", []byte{7, 8})
				if createErr != nil {
					t.Fatal(createErr)
				}
				queuedMessage = pending.Id
				message, e := s.ClaimTelegramDelivery(ctx)
				if e != nil || message == nil {
					t.Fatal(e)
				}
				failedDelivery = message.ID
				if e = s.DeliverTelegram(ctx, *message, func(context.Context, TelegramPart) (TelegramOutcome, error) {
					return TelegramOutcome{Status: "failed", Code: "THREAD_NOT_FOUND"}, nil
				}); e != nil {
					t.Fatal(e)
				}
			}
			var topic uuid.UUID
			if e.Pool.QueryRow(ctx, `SELECT id FROM support_telegram_topics WHERE account_id=$1`, customer).Scan(&topic) != nil {
				t.Fatal("topic")
			}
			op, actor, err := a.ResolveTelegramContext(ctx, 732)
			if err != nil {
				t.Fatal(err)
			}
			source := supportCommandSource(71, 0)
			intent, err := s.BeginTelegramCommand(op, source, topic, TelegramCommandInput{Action: "bind", ThreadID: thread, Reason: "owned topic attestation"})
			if err != nil || !intent.AwaitingConfirmation || intent.Input.Confirmed {
				t.Fatal("bind needs a durable prompt", err)
			}
			if s.BindTelegramTopic(op, intent.ID, thread, true) == nil {
				t.Fatal("unconfirmed receipt accepted")
			}
			prompt, err := s.ClaimTelegramDelivery(ctx)
			if err != nil || prompt == nil {
				t.Fatal("prompt", err)
			}
			if err = s.DeliverTelegram(ctx, *prompt, func(_ context.Context, p TelegramPart) (TelegramOutcome, error) {
				if p.Kind != "confirmation" || p.ThreadID != 0 || p.ConfirmationID != intent.ID {
					t.Fatal("prompt proof", p.Kind)
				}
				return TelegramOutcome{Status: "sent", MessageID: 91}, nil
			}); err != nil {
				t.Fatal(err)
			}
			callback := supportCommandSource(90, 0)
			callback.Action = "confirm"
			callback.CallbackID = "owned-confirm"
			input := TelegramCommandInput{Action: "confirm", ConfirmationID: intent.ID, Confirmed: true}
			if _, err = s.BeginTelegramCommand(op, callback, uuid.Nil, input); err == nil {
				t.Fatal("wrong prompt authorized")
			}
			callback.MessageID = 91
			confirmed, err := s.BeginTelegramCommand(op, callback, uuid.Nil, input)
			if err != nil || confirmed.ID != intent.ID || confirmed.OwnerKey != intent.OwnerKey || !confirmed.Input.Confirmed || confirmed.AwaitingConfirmation {
				t.Fatal("confirmation changed intent", err)
			}
			again, err := s.BeginTelegramCommand(op, callback, uuid.Nil, input)
			if err != nil || again.OwnerKey != intent.OwnerKey {
				t.Fatal("callback replay", err)
			}
			if err = s.BindTelegramTopic(op, intent.ID, thread, true); err != nil {
				t.Fatal("confirmed bind", err)
			}
			if err = s.BindTelegramTopic(op, intent.ID, thread, true); err != nil {
				t.Fatal("bind replay", err)
			}
			var actualThread int64
			var ready bool
			if e.Pool.QueryRow(ctx, `SELECT thread_id,status='ready' FROM support_telegram_topics WHERE account_id=$1 AND status<>'retired'`, customer).Scan(&actualThread, &ready) != nil || actualThread != thread || !ready {
				t.Fatal("binding not committed")
			}
			var originals int
			if e.Pool.QueryRow(ctx, `SELECT count(*) FROM support_messages WHERE text='owned bind recovery'`).Scan(&originals) != nil || originals != 1 {
				t.Fatal("bind duplicated message")
			}

			if known {
				var retained bool
				var topics int
				if e.Pool.QueryRow(ctx, `SELECT status='retired' AND thread_id=888 FROM support_telegram_topics WHERE id=$1`, topic).Scan(&retained) != nil || !retained {
					t.Fatal("replacement rewrote original topic")
				}
				if e.Pool.QueryRow(ctx, `SELECT count(*) FROM support_telegram_topics`).Scan(&topics) != nil || topics != 2 {
					t.Fatal("replacement mapping or replay", topics)
				}
				page, pageErr := s.Support(ctx, actor.ID, customer, true)
				var stranded *TelegramDeliveryStatus
				for _, m := range page.Messages {
					if m.Id == queuedMessage {
						stranded = m.TelegramDelivery
					}
				}
				if pageErr != nil || stranded == nil || stranded.Status != "failed" || stranded.Code != "TOPIC_RETIRED" || !stranded.RetryCapability {
					t.Fatal("queued message stranded after replacement", pageErr, stranded)
				}
				if recovered, created, retryErr := s.RetryTelegramDelivery(op, actor.ID, customer, stranded.Id, uuid.New(), true, "recover queued message"); retryErr != nil || !created || recovered.Status != "queued" {
					t.Fatal("replacement lost queued message recovery", retryErr)
				}
				key := uuid.New()
				retry, replayed, retryErr := s.RetryTelegramDelivery(op, actor.ID, customer, failedDelivery, key, true, "retry after attested replacement")
				if retryErr != nil || !replayed || retry.Id == uuid.Nil {
					t.Fatal("old delivery cannot follow proven replacement", retryErr)
				}
				second, replayed, retryErr := s.RetryTelegramDelivery(op, actor.ID, customer, failedDelivery, key, true, "retry after attested replacement")
				if retryErr != nil || replayed || second.Id != retry.Id {
					t.Fatal("replacement retry replay", retryErr)
				}
			}
			if replay, err := s.BeginTelegramCommand(op, source, topic, TelegramCommandInput{Action: "bind", ThreadID: thread, Reason: "owned topic attestation"}); err != nil || replay.Result == nil {
				t.Fatal("completed source replay after replacement", err)
			}
			if _, err := s.BeginTelegramCommand(op, callback, uuid.Nil, input); err != nil {
				t.Fatal("completed callback replay after replacement", err)
			}
			if known {
				if err = a.ChangeOperatorRole(ctx, actor.ID, false); err != nil {
					t.Fatal(err)
				}
				calls := 0
				for i := 0; i < 3; i++ {
					job, claimErr := s.ClaimTelegramDelivery(ctx)
					if claimErr != nil || job == nil {
						t.Fatal("queued replacement ACK/retry", claimErr)
					}
					if err = s.DeliverTelegram(ctx, *job, func(context.Context, TelegramPart) (TelegramOutcome, error) {
						calls++
						return TelegramOutcome{Status: "sent", MessageID: 99}, nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				if calls != 0 {
					t.Fatal("revoked Telegram retry operator reached wire", calls)
				}
			}
		})
	}
}
