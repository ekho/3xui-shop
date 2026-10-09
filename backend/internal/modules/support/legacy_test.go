package support

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/google/uuid"
	"testing"
)

func TestLegacySupportPrecision(t *testing.T) {
	const prefix = `{"source_id":9007199254740995,"tg_id":9007199254740993,"thread_id":null,"status":"closed","created_at":"`
	for _, tc := range []struct {
		name, time, extra string
		valid             bool
	}{
		{"exact", "2026-10-01T00:00:00.123456Z", "", true},
		{"trailing-zeroes", "2026-10-01T00:00:00.12345600000Z", "", true},
		{"lost-fraction", "2026-10-01T00:00:00.12345600001Z", "", false},
		{"missing", "", "", false},
		{"unknown-field", "2026-10-01T00:00:00Z", `,"invented":1`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var row LegacySupportRow
			err := json.Unmarshal([]byte(prefix+tc.time+`","updated_at":"2026-10-01T00:00:01Z"`+tc.extra+`}`), &row)
			if err == nil {
				err = ValidateLegacySupportInput(LegacySupportInput{Version: 1, BotID: 973, GroupID: -10074001, Tickets: []LegacySupportRow{row}})
			}
			if (err == nil) != tc.valid {
				t.Fatal("raw source precision/fields", err)
			}
			if tc.valid && (row.SourceID != 9007199254740995 || row.TelegramID != 9007199254740993 || row.ThreadID != nil || row.CreatedAt != tc.time) {
				t.Fatal("source facts rounded")
			}
		})
	}
}

func TestLegacySupportIdentity(t *testing.T) {
	s, a, e, customer, _ := supportTelegramFixture(t)
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, `WITH changed AS(UPDATE accounts SET legacy_user_id=951,telegram_id=733 WHERE id=$1 RETURNING id) INSERT INTO legacy_approval_snapshots(account_id,source_legacy_user_id,source_tg_id,status) SELECT id,951,731,'approved' FROM changed`, customer); err != nil {
		t.Fatal(err)
	}
	rebound, _, err := a.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 731, DisplayName: "different current owner", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	conversation := uuid.New()
	if _, err = e.Pool.Exec(ctx, `INSERT INTO support_conversations(id,account_id,status,support_banned,created_at,updated_at) VALUES($1,$2,'open',false,$3,$3)`, conversation, customer, e.Clock()); err != nil {
		t.Fatal(err)
	}
	thread := int64(888)
	p := LegacySupportInput{Version: 1, BotID: 973, GroupID: -10074001, Tickets: []LegacySupportRow{{SourceID: 5, TelegramID: 731, ThreadID: &thread, Status: "banned", CreatedAt: "2026-10-01T00:00:00.1234560000Z", UpdatedAt: "2026-10-01T00:00:01Z"}, {SourceID: 9007199254740995, TelegramID: 9007199254740993, Status: "closed", CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-01T00:00:00Z"}}}
	dry, err := s.ImportLegacySupport(ctx, p, true)
	if err != nil || dry.Inserted != 2 || dry.Orphans != 1 {
		t.Fatal("read-only source projection", dry, err)
	}
	var topics int
	if e.Pool.QueryRow(ctx, `SELECT count(*) FROM support_telegram_topics`).Scan(&topics) != nil || topics != 0 {
		t.Fatal("dry-run wrote topics")
	}
	result, err := s.ImportLegacySupport(ctx, p, false)
	if err != nil || result.Inserted != 2 || result.Orphans != 1 {
		t.Fatal("source import", result, err)
	}
	var account uuid.UUID
	var banned bool
	if e.Pool.QueryRow(ctx, `SELECT account_id,support_banned FROM support_telegram_topics WHERE source_id=5`).Scan(&account, &banned) != nil || account != customer || account == rebound.Account.ID || !banned {
		t.Fatal("current binding stole original topic")
	}
	var untouched bool
	if e.Pool.QueryRow(ctx, `SELECT status='open' AND NOT support_banned FROM support_conversations WHERE id=$1`, conversation).Scan(&untouched) != nil || !untouched {
		t.Fatal("legacy status rewrote common history")
	}
	replay, err := s.ImportLegacySupport(ctx, p, false)
	if err != nil || replay.Inserted != 0 || replay.Replayed != 2 {
		t.Fatal("source replay", replay, err)
	}
	p.Tickets[0].Status = "open"
	if _, err = s.ImportLegacySupport(ctx, p, false); err == nil || err.Error() != "IMPORT_SOURCE_CONFLICT" {
		t.Fatal("source change accepted", err)
	}
	fresh := p.Tickets[1]
	fresh.SourceID = 1
	fresh.TelegramID = 901
	conflict := p
	conflict.Tickets = []LegacySupportRow{fresh, p.Tickets[0]}
	for _, dry := range []bool{true, false} {
		if _, err = s.ImportLegacySupport(ctx, conflict, dry); err == nil || err.Error() != "IMPORT_SOURCE_CONFLICT" {
			t.Fatal("batch source conflict", err)
		}
		if e.Pool.QueryRow(ctx, `SELECT count(*) FROM legacy_support_imports WHERE source_id=1`).Scan(&topics) != nil || topics != 0 {
			t.Fatal("partial import survived conflict")
		}
	}
	var old json.RawMessage
	if e.Pool.QueryRow(ctx, `SELECT source_json FROM legacy_support_imports WHERE source_id=9007199254740995`).Scan(&old) != nil {
		t.Fatal("source snapshot")
	}
	var exact LegacySupportRow
	if json.Unmarshal(old, &exact) != nil || exact.TelegramID != 9007199254740993 || exact.ThreadID != nil {
		t.Fatal("orphan source lost")
	}
	var changed int
	if e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM support_messages)+(SELECT count(*) FROM trial_requests)+(SELECT count(*) FROM trial_operations)+(SELECT count(*) FROM credential_challenges)`).Scan(&changed) != nil || changed != 0 {
		t.Fatal("import created access or messages")
	}
	var audits int
	if e.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_system_events WHERE support_telegram->>'kind'='legacy_imported' AND support_telegram ? 'source_id' AND NOT support_telegram ? 'message_id' AND NOT support_telegram ? 'update_id' AND support_telegram->>'actor_tg_id' IS NULL AND support_telegram->>'actor_account_id' IS NULL`).Scan(&audits) != nil || audits != 2 {
		t.Fatal("import invented provider facts")
	}
	boundCtx, _, err := a.ResolveTelegramContext(ctx, 733)
	if err != nil {
		t.Fatal(err)
	}
	in := TelegramInput{Source: TelegramSource{BotID: 973, GroupID: -10074001, ChatID: 733, ActorID: 733, MessageID: 67, UpdateID: 1, Action: "message", Digest: [32]byte{1}}, Text: "blocked legacy contact"}
	if _, err = s.ReceiveTelegramMessage(boundCtx, in); err == nil || err.Error() != "ACCOUNT_RESTRICTED" {
		t.Fatal("legacy topic ban bypassed", err)
	}
	opCtx, _, err := a.ResolveTelegramContext(ctx, 732)
	if err != nil {
		t.Fatal(err)
	}
	var topic uuid.UUID
	if e.Pool.QueryRow(ctx, `SELECT id FROM support_telegram_topics WHERE source_id=5`).Scan(&topic) != nil {
		t.Fatal("legacy topic")
	}
	if _, err = s.BeginTelegramCommand(opCtx, supportCommandSource(68, 888), topic, TelegramCommandInput{Action: "unban", Reason: "support reconsideration"}); err != nil {
		t.Fatal("legacy unban", err)
	}
	if e.Pool.QueryRow(ctx, `SELECT support_banned FROM support_telegram_topics WHERE id=$1`, topic).Scan(&banned) != nil || banned {
		t.Fatal("legacy unban projection")
	}
	p.Tickets[0].Status = "banned"
	if replay, err = s.ImportLegacySupport(ctx, p, false); err != nil || replay.Replayed != 2 {
		t.Fatal("unban changed immutable import", err)
	}
}

func TestLegacySupportPendingAndOrphan(t *testing.T) {
	s, a, e, customer, _ := supportTelegramFixture(t)
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, `WITH changed AS(UPDATE accounts SET legacy_user_id=951 WHERE id=$1 RETURNING id) INSERT INTO legacy_approval_snapshots(account_id,source_legacy_user_id,source_tg_id,status) SELECT id,951,731,'approved' FROM changed`, customer); err != nil {
		t.Fatal(err)
	}
	thread := int64(889)
	p := LegacySupportInput{Version: 1, BotID: 973, GroupID: -10074001, Tickets: []LegacySupportRow{{SourceID: 11, TelegramID: 731, Status: "open", CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-01T00:00:00Z"}, {SourceID: 12, TelegramID: 923, ThreadID: &thread, Status: "closed", CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-01T00:00:00Z"}}}
	if _, err := s.ImportLegacySupport(ctx, p, false); err != nil {
		t.Fatal(err)
	}
	var jobs int
	if e.Pool.QueryRow(ctx, `SELECT count(*) FROM support_telegram_deliveries`).Scan(&jobs) != nil || jobs != 0 {
		t.Fatal("import started provider work")
	}
	opCtx, _, err := a.ResolveTelegramContext(ctx, 732)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginTelegramCommand(opCtx, supportCommandSource(70, 889), uuid.Nil, TelegramCommandInput{Action: "close"}); err == nil || err.Error() != "REQUEST_STATE_CONFLICT" {
		t.Fatal("orphan allowed mutation", err)
	}
	if _, _, err = s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "new web contact", "", nil); err != nil {
		t.Fatal(err)
	}
	if e.Pool.QueryRow(ctx, `SELECT count(*) FROM support_telegram_deliveries WHERE kind='topic_create'`).Scan(&jobs) != nil || jobs != 1 {
		t.Fatal("new contact cannot create NULL legacy topic", jobs)
	}
}

func TestLegacySupportOriginalBind(t *testing.T) {
	s, a, e, customer, _ := supportTelegramFixture(t)
	ctx := context.Background()
	thread := int64(888)
	p := LegacySupportInput{Version: 1, BotID: 973, GroupID: -10074001, Tickets: []LegacySupportRow{{SourceID: 5, TelegramID: 731, ThreadID: &thread, Status: "closed", CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-01T00:00:00Z"}}}
	if _, err := s.ImportLegacySupport(ctx, p, false); err != nil {
		t.Fatal(err)
	}
	if err := s.BindLegacySupport(ctx, 973, -10074001, 5, customer); err == nil || err.Error() != "IMPORT_IDENTITY_CONFLICT" {
		t.Fatal("current binding is not original proof", err)
	}
	if _, err := e.Pool.Exec(ctx, `WITH changed AS(UPDATE accounts SET legacy_user_id=951,telegram_id=733 WHERE id=$1 RETURNING id) INSERT INTO legacy_approval_snapshots(account_id,source_legacy_user_id,source_tg_id,status) SELECT id,951,731,'approved' FROM changed`, customer); err != nil {
		t.Fatal(err)
	}
	rebound, _, err := a.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 731, DisplayName: "new binding", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BindLegacySupport(ctx, 973, -10074001, 5, rebound.Account.ID); err == nil || err.Error() != "IMPORT_IDENTITY_CONFLICT" {
		t.Fatal("rebound account stole orphan", err)
	}
	var before []byte
	if e.Pool.QueryRow(ctx, `SELECT source_json FROM legacy_support_imports WHERE source_id=5`).Scan(&before) != nil {
		t.Fatal("snapshot missing")
	}
	for i := 0; i < 2; i++ {
		if err = s.BindLegacySupport(ctx, 973, -10074001, 5, customer); err != nil {
			t.Fatal("proven original bind", err)
		}
	}
	var exact, linked bool
	var auditCount, jobs int
	if e.Pool.QueryRow(ctx, `SELECT source_json=$1 FROM legacy_support_imports WHERE source_id=5`, before).Scan(&exact) != nil || !exact {
		t.Fatal("bind rewrote source")
	}
	if e.Pool.QueryRow(ctx, `SELECT kind='account' AND account_id=$1 AND thread_id=888 AND closed FROM support_telegram_topics WHERE source_id=5`, customer).Scan(&linked) != nil || !linked {
		t.Fatal("original projection lost")
	}
	if e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_system_events WHERE support_telegram->>'kind'='legacy_bound'),(SELECT count(*) FROM support_telegram_deliveries)`).Scan(&auditCount, &jobs) != nil || auditCount != 1 || jobs != 0 {
		t.Fatal("bind duplicated audit or started work")
	}
}

func TestLegacySupportWrongLegacyProof(t *testing.T) {
	s, _, e, customer, _ := supportTelegramFixture(t)
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, `WITH changed AS(UPDATE accounts SET legacy_user_id=952 WHERE id=$1 RETURNING id) INSERT INTO legacy_approval_snapshots(account_id,source_legacy_user_id,source_tg_id,status) SELECT id,951,731,'approved' FROM changed`, customer); err != nil {
		t.Fatal(err)
	}
	p := LegacySupportInput{Version: 1, BotID: 973, GroupID: -10074001, Tickets: []LegacySupportRow{{SourceID: 5, TelegramID: 731, Status: "closed", CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-01T00:00:00Z"}}}
	for _, dry := range []bool{true, false} {
		if _, err := s.ImportLegacySupport(ctx, p, dry); err == nil || err.Error() != "IMPORT_IDENTITY_CONFLICT" {
			t.Fatal("mismatched original LegacyUserID accepted", err)
		}
	}
}
