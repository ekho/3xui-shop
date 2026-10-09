package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/support"
	"github.com/google/uuid"
)

func TestSupportTelegramAuditHTTP(t *testing.T) {
	h, e, cfg := httpFixture(t)
	ctx := context.Background()
	operator := supportLogin(t, h, e, cfg, "relay-audit-operator@example.test")
	if _, err := e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, operator.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	sourceID := int64(9223372036854775807)
	for _, event := range []auditreports.SupportTelegramEvent{
		{ID: uuid.New(), BotID: 973, GroupID: -10074001, ChatID: -10074001, MessageID: 9, UpdateID: 0, Kind: "topic_closed", Outcome: "observed"},
		{ID: uuid.New(), BotID: 973, GroupID: -10074001, ChatID: -10074001, Kind: "legacy_imported", Outcome: "completed", SourceID: &sourceID},
	} {
		if err = auditreports.RecordSupportTelegramTx(ctx, tx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := supportRequest(h, &operator, "POST", "/api/v1/operator/audit/history", "application/json", []byte(`{"kind":"system"}`), cfg.HTTP.CabinetOrigin, uuid.Nil)
	var page struct {
		Rows []struct {
			Action  string                     `json:"action"`
			Support map[string]json.RawMessage `json:"support_telegram"`
		} `json:"system_events"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.Rows) != 2 {
		t.Fatal("support system page", r.Code)
	}
	for _, row := range page.Rows {
		if row.Action != "support.telegram" || row.Support == nil {
			t.Fatal("typed support source missing")
		}
		if string(row.Support["bot_id"]) != `"973"` || string(row.Support["group_id"]) != `"-10074001"` || string(row.Support["actor_account_id"]) != "null" || string(row.Support["actor_tg_id"]) != "null" {
			t.Fatal("source or unknown actor changed")
		}
		if string(row.Support["kind"]) == `"legacy_imported"` {
			if string(row.Support["source_id"]) != `"9223372036854775807"` || row.Support["message_id"] != nil || row.Support["update_id"] != nil {
				t.Fatal("legacy ID rounded or absent provider IDs invented")
			}
		} else if string(row.Support["message_id"]) != `"9"` || string(row.Support["update_id"]) != `"0"` || row.Support["source_id"] != nil {
			t.Fatal("native source facts changed")
		}
	}
}

func TestSupportTelegramRetryHTTP(t *testing.T) {
	h, e, cfg := httpFixture(t)
	ctx := context.Background()
	customer := supportLogin(t, h, e, cfg, "relay-client@example.test")
	operator := supportLogin(t, h, e, cfg, "relay-operator@example.test")
	foreign := supportLogin(t, h, e, cfg, "relay-foreign@example.test")
	modules := app.NewModules(e.Pool, e.Redis, nil, &cfg)
	if err := modules.Accounts.ChangeOperatorRole(ctx, operator.id, true); err != nil {
		t.Fatal(err)
	}
	if err := modules.Support.ConfigureTelegram(973, -10074001); err != nil {
		t.Fatal(err)
	}
	h = New(modules, e.Pool, cfg.HTTP)
	message, _, err := modules.Support.CreateSupportMessage(ctx, operator.id, customer.id, true, uuid.New(), "Relay result", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"topic_create", "message"} {
		job, err := modules.Support.ClaimTelegramDelivery(ctx)
		if err != nil || job == nil {
			t.Fatal("delivery fixture", kind, err)
		}
		err = modules.Support.DeliverTelegram(ctx, *job, func(_ context.Context, p support.TelegramPart) (support.TelegramOutcome, error) {
			if kind == "topic_create" {
				return support.TelegramOutcome{Status: "sent", ThreadID: 900}, nil
			}
			return support.TelegramOutcome{Status: "unknown", Code: "ACK_UNKNOWN"}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var delivery uuid.UUID
	if err = e.Pool.QueryRow(ctx, "SELECT id FROM support_telegram_deliveries WHERE message_id=$1", message.Id).Scan(&delivery); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/operator/clients/" + customer.id.String() + "/support"
	t.Run("history-projection", func(t *testing.T) {
		r := supportRequest(h, &operator, "GET", base, "", nil, "", uuid.Nil)
		var page struct {
			Messages []struct {
				Delivery string                          `json:"delivery"`
				Telegram *support.TelegramDeliveryStatus `json:"telegram_delivery"`
			} `json:"messages"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.Messages) != 1 || page.Messages[0].Telegram == nil {
			t.Fatal("missing protected delivery projection", r.Code)
		}
		d := page.Messages[0].Telegram
		if d.Id != delivery || d.Status != "unknown" || d.Code != "ACK_UNKNOWN" || !d.RetryCapability || d.MediaAvailability != "stored" || page.Messages[0].Delivery != "stored" {
			t.Fatal("relay and read ACK confused")
		}
	})
	t.Run("retry-capability-current-topic-and-ban", func(t *testing.T) {
		var oldTopic uuid.UUID
		if e.Pool.QueryRow(ctx, `UPDATE support_telegram_topics SET status='retired' WHERE account_id=$1 RETURNING id`, customer.id).Scan(&oldTopic) != nil {
			t.Fatal("owned replacement source unavailable")
		}
		newTopic := uuid.New()
		if _, err := e.Pool.Exec(ctx, `INSERT INTO support_telegram_topics(id,bot_id,group_id,kind,account_id,thread_id,status) VALUES($1,973,-10074001,'account',$2,901,'ready')`, newTopic, customer.id); err != nil {
			t.Fatal(err)
		}
		defer func() {
			e.Pool.Exec(ctx, `DELETE FROM support_telegram_topics WHERE id=$1`, newTopic)
			e.Pool.Exec(ctx, `UPDATE support_telegram_topics SET status='ready' WHERE id=$1`, oldTopic)
			e.Pool.Exec(ctx, `UPDATE support_conversations SET support_banned=false WHERE account_id=$1`, customer.id)
		}()
		capability := func(want bool) {
			t.Helper()
			r := supportRequest(h, &operator, "GET", base, "", nil, "", uuid.Nil)
			var page support.SupportResult
			if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.Messages) != 1 || page.Messages[0].TelegramDelivery == nil || page.Messages[0].TelegramDelivery.RetryCapability != want {
				t.Fatal("capability ignored current replacement or ban", want, r.Code)
			}
		}
		capability(true)
		if _, err := e.Pool.Exec(ctx, `UPDATE support_conversations SET support_banned=true WHERE account_id=$1`, customer.id); err != nil {
			t.Fatal(err)
		}
		capability(false)
	})
	path := base + "/telegram-deliveries/" + delivery.String() + "/retry"
	valid := []byte(`{"confirmed":true,"reason":"Owned retry"}`)
	noCSRF := operator
	noCSRF.csrf = ""
	for _, tc := range []struct {
		name   string
		actor  *supportSession
		path   string
		body   []byte
		origin string
		key    uuid.UUID
		status int
	}{
		{"no-auth", nil, path, valid, cfg.HTTP.CabinetOrigin, uuid.New(), 401},
		{"customer", &customer, path, valid, cfg.HTTP.CabinetOrigin, uuid.New(), 403},
		{"nonoperator", &foreign, path, valid, cfg.HTTP.CabinetOrigin, uuid.New(), 403},
		{"csrf", &noCSRF, path, valid, cfg.HTTP.CabinetOrigin, uuid.New(), 403},
		{"origin", &operator, path, valid, "https://foreign.example.test", uuid.New(), 403},
		{"no-key", &operator, path, valid, cfg.HTTP.CabinetOrigin, uuid.Nil, 400},
		{"extra-json", &operator, path, []byte(`{"confirmed":true,"reason":"Owned","extra":1}`), cfg.HTTP.CabinetOrigin, uuid.New(), 400},
		{"unconfirmed", &operator, path, []byte(`{"confirmed":false,"reason":"Owned"}`), cfg.HTTP.CabinetOrigin, uuid.New(), 400},
		{"no-reason", &operator, path, []byte(`{"confirmed":true,"reason":" "}`), cfg.HTTP.CabinetOrigin, uuid.New(), 400},
		{"cross-account", &operator, "/api/v1/operator/clients/" + foreign.id.String() + "/support/telegram-deliveries/" + delivery.String() + "/retry", valid, cfg.HTTP.CabinetOrigin, uuid.New(), 404},
		{"extra-query", &operator, path + "?extra=1", valid, cfg.HTTP.CabinetOrigin, uuid.New(), 400},
		{"bad-delivery", &operator, base + "/telegram-deliveries/not-uuid/retry", valid, cfg.HTTP.CabinetOrigin, uuid.New(), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := supportRequest(h, tc.actor, "POST", tc.path, "application/json", tc.body, tc.origin, tc.key)
			if r.Code != tc.status {
				t.Fatalf("status %d, want %d", r.Code, tc.status)
			}
		})
	}
	key := uuid.New()
	r := supportRequest(h, &operator, "POST", path, "application/json", valid, cfg.HTTP.CabinetOrigin, key)
	if r.Code != 201 {
		t.Fatal("confirmed retry", r.Code)
	}
	var out support.TelegramDeliveryStatus
	if json.Unmarshal(r.Body.Bytes(), &out) != nil || out.Id == delivery || out.Status != "queued" {
		t.Fatal("retry response")
	}
	replay := supportRequest(h, &operator, "POST", path, "application/json", valid, cfg.HTTP.CabinetOrigin, key)
	if replay.Code != 200 || replay.Body.String() != r.Body.String() {
		t.Fatal("retry replay changed")
	}
	for _, request := range []struct {
		body   []byte
		key    uuid.UUID
		status int
	}{{[]byte(`{"confirmed":true,"reason":"Changed"}`), key, 409}, {valid, uuid.New(), 409}} {
		if response := supportRequest(h, &operator, "POST", path, "application/json", request.body, cfg.HTTP.CabinetOrigin, request.key); response.Code != request.status {
			t.Fatal("retry conflict", response.Code)
		}
	}
	if _, err = e.Pool.Exec(ctx, "UPDATE support_telegram_deliveries SET status='sent' WHERE id=$1", out.Id); err != nil {
		t.Fatal(err)
	}
	successPath := fmt.Sprintf("%s/telegram-deliveries/%s/retry", base, out.Id)
	if r = supportRequest(h, &operator, "POST", successPath, "application/json", valid, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 409 {
		t.Fatal("successful intent retried", r.Code)
	}
	if err = modules.Accounts.ChangeOperatorRole(ctx, operator.id, false); err != nil {
		t.Fatal(err)
	}
	if r = supportRequest(h, &operator, "POST", path, "application/json", valid, cfg.HTTP.CabinetOrigin, key); r.Code != 403 {
		t.Fatal("revoked operator replay", r.Code)
	}
	var messages, audits, retries int
	if err = e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM support_messages),(SELECT count(*) FROM audit_events WHERE action='support_message'),(SELECT count(*) FROM support_telegram_deliveries WHERE prior_delivery_id IS NOT NULL)`).Scan(&messages, &audits, &retries); err != nil || messages != 1 || audits != 1 || retries != 1 {
		t.Fatal("retry duplicated domain effects", err, messages, audits, retries)
	}
}
