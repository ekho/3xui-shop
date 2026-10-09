package auditreports

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func TestSupportTelegramSystemAudit(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	input := SupportTelegramEvent{ID: uuid.New(), BotID: 973, GroupID: -10074001, ChatID: -10074001, MessageID: 12, UpdateID: 1, Kind: "topic_closed", Outcome: "observed"}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = RecordSupportTelegramTx(ctx, tx, input); err != nil {
		t.Fatal("unknown actor observation rejected", err)
	}
	var raw []byte
	var invented int
	if err = tx.QueryRow(ctx, `SELECT support_telegram,(SELECT count(*) FROM audit_events) FROM audit_system_events WHERE id=$1 AND action='support.telegram'`, input.ID).Scan(&raw, &invented); err != nil {
		t.Fatal("native observation disappeared", err)
	}
	var stored SupportTelegramEvent
	if json.Unmarshal(raw, &stored) != nil || stored.ActorTgID != nil || stored.ActorAccountID != nil || invented != 0 || stored.MessageID != 12 || stored.Kind != "topic_closed" {
		t.Fatal("unknown actor replaced with fabricated identity")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if e.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_system_events WHERE id=$1`, input.ID).Scan(&count) != nil || count != 0 {
		t.Fatal("audit escaped caller transaction")
	}
	input.Kind = strings.Repeat("a", 256)
	tx, err = e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if RecordSupportTelegramTx(ctx, tx, input) == nil {
		t.Fatal("unbounded audit action accepted")
	}
}
