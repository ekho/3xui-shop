package auditreports

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/testkit"
	"fmt"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func auditTestZone(t *testing.T, now time.Time, due bool) *time.Location {
	t.Helper()
	for offset := -12; offset <= 12; offset++ {
		zone, err := time.LoadLocation(fmt.Sprintf("Etc/GMT%+d", offset))
		if err != nil {
			continue
		}
		hour := now.In(zone).Hour()
		if due && hour >= 4 || !due && hour == 1 {
			return zone
		}
	}
	t.Fatal("no bounded IANA test zone")
	return nil
}

func TestAuditMirrorCommitAndNoRetry(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	s := New(e.Pool, StatisticsPorts{}, HistoryPorts{}, Config{})
	account := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) VALUES($1,'audit-mirror@example.test','ru','fixture',now(),$2,'aaaaaaaaaaaaaaaa',$3,'1','1')`, account, uuid.New(), "acct_"+account.String()); err != nil {
		t.Fatal(err)
	}
	oldID := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO audit_events(id,account_id,created_at,action) VALUES($1,$2,now(),'old.audit')`, oldID, account); err != nil {
		t.Fatal(err)
	}
	if err := s.muteMirror(ctx); err != nil {
		t.Fatal(err)
	}
	if text, err := s.claimMirror(ctx); err != nil || text != "" {
		t.Fatal("startup mirrored old history", err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	id := uuid.New()
	reason := "private-reason-and-body"
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err = RecordTx(ctx, tx, Event{ID: id, AccountID: account, CreatedAt: now, Action: "owned.audit", Reason: &reason}); err != nil {
		t.Fatal(err)
	}
	if text, err := s.claimMirror(ctx); err != nil || text != "" {
		t.Fatal("uncommitted event mirrored", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	text, err := s.claimMirror(ctx)
	if err != nil || !strings.Contains(text, "action=owned.audit") || !strings.Contains(text, "id="+id.String()) || !strings.Contains(text, "account_id="+account.String()) || strings.Contains(text, reason) || strings.Contains(text, "email") {
		t.Fatal("committed metadata mirror missing/private", err)
	}
	var attempted bool
	if err = e.Pool.QueryRow(ctx, `SELECT mirror_attempted_at IS NOT NULL FROM audit_events WHERE id=$1`, id).Scan(&attempted); err != nil || !attempted {
		t.Fatal("wire claim not committed first", err)
	}
	for i := 0; i < 3; i++ {
		if text, err = s.claimMirror(ctx); err != nil || text != "" {
			t.Fatal("unknown wire attempt retried", err)
		}
	}
	rolled, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = RecordTx(ctx, rolled, Event{ID: uuid.New(), AccountID: account, CreatedAt: now, Action: "rolled.audit"}); err != nil {
		t.Fatal(err)
	}
	if err = rolled.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if text, err = s.claimMirror(ctx); err != nil || text != "" {
		t.Fatal("rolled back event mirrored", err)
	}
	pending := uuid.New()
	if _, err = e.Pool.Exec(ctx, `INSERT INTO audit_events(id,account_id,created_at,action) VALUES($1,$2,now(),'pending.audit')`, pending, account); err != nil {
		t.Fatal(err)
	}
	if err = s.muteMirror(ctx); err != nil {
		t.Fatal(err)
	}
	if text, err = s.claimMirror(ctx); err != nil || text != "" {
		t.Fatal("restart/retarget resent pending history", err)
	}
}

func TestAuditRetentionBoundaryAndReceipt(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	zone := auditTestZone(t, now, true)
	s := New(e.Pool, StatisticsPorts{}, HistoryPorts{}, Config{RetentionDays: 1, Timezone: zone})
	cutoff := now.Add(-24 * time.Hour)
	account := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version) VALUES($1,'audit-retention@example.test','ru','fixture',now(),$2,'aaaaaaaaaaaaaaaa',$3,'1','1')`, account, uuid.New(), "acct_"+account.String()); err != nil {
		t.Fatal(err)
	}
	conversation, message := uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO support_conversations(id,account_id,status,created_at,updated_at) VALUES($1,$2,'open',$3,$3)`, conversation, account, cutoff.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO support_messages(id,conversation_id,sender_account_id,sender_kind,text,created_at,attachment_name,attachment_bytes) VALUES($1,$2,$3,'customer','Retained support body',$4,'retained.txt',$5)`, message, conversation, account, cutoff.Add(-time.Hour), []byte("retained-attachment")); err != nil {
		t.Fatal(err)
	}
	for i, when := range []time.Time{cutoff.Add(-time.Microsecond), cutoff, cutoff.Add(time.Microsecond)} {
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,account_id,created_at,action,reason) VALUES($1,$2,$3,'owned.audit','private-reason')`, uuid.New(), account, when); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO legacy_audit_imports(source_id,source_hash) VALUES($1,$2)`, i+1, make([]byte, 32)); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO legacy_audit_events(source_id,created_at,action,payload_json) VALUES($1,$2,'support.message',$3)`, i+1, when, []byte(`{"dm":"private-body"}`)); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_system_events(id,created_at,action,native_count,legacy_count,system_count) VALUES($1,$2,'audit.legacy_imported',0,1,0)`, uuid.New(), when); err != nil {
			t.Fatal(err)
		}
	}
	receipt, changed, err := s.pruneTx(ctx, tx)
	if err != nil || !changed {
		t.Fatal("daily retention did not run", err)
	}
	if receipt.Action != "audit.pruned" || !receipt.CreatedAt.Equal(now) || receipt.Cutoff == nil || !receipt.Cutoff.Equal(cutoff) || receipt.RetentionDays == nil || *receipt.RetentionDays != 1 || receipt.PeriodDay == nil || *receipt.PeriodDay != now.In(zone).Format("2006-01-02") || receipt.NativeCount != 1 || receipt.LegacyCount != 1 || receipt.SystemCount != 1 {
		t.Fatal("receipt clock/cutoff/counts changed")
	}
	var native, legacy, ledger, system, accounts int
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events),(SELECT count(*) FROM legacy_audit_events),(SELECT count(*) FROM legacy_audit_imports),(SELECT count(*) FROM audit_system_events),(SELECT count(*) FROM accounts WHERE id=$1 AND panel_key=$2)`, account, "acct_"+account.String()).Scan(&native, &legacy, &ledger, &system, &accounts); err != nil || native != 2 || legacy != 2 || ledger != 3 || system != 3 || accounts != 1 {
		t.Fatal("retention crossed boundary/equality or erased identity", err)
	}
	if _, changed, err = s.pruneTx(ctx, tx); err != nil || changed {
		t.Fatal("same-day prune repeated", err)
	}
	var retained bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM support_messages WHERE id=$1 AND text='Retained support body' AND attachment_bytes=$2)`, message, []byte("retained-attachment")).Scan(&retained); err != nil || !retained {
		t.Fatal("audit prune erased owned support body/attachment", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	s = New(e.Pool, StatisticsPorts{}, HistoryPorts{}, Config{RetentionDays: 365, Timezone: zone})
	if _, changed, err = s.Prune(ctx); err != nil || changed {
		t.Fatal("restart/config change repeated day", err)
	}
}

func TestAuditRetentionReceiptFailureRollsBack(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	var now time.Time
	if err := e.Pool.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	s := New(e.Pool, StatisticsPorts{}, HistoryPorts{}, Config{RetentionDays: 1, Timezone: auditTestZone(t, now, true)})
	payload := `{"body":"retained-if-receipt-fails"}`
	p := LegacyAuditPackage{Version: 1, Events: []LegacyAuditInput{{SourceID: 1, CreatedAt: now.Add(-48 * time.Hour), Action: "support.message", PayloadJSON: &payload}}}
	if _, err := s.ImportLegacy(ctx, p, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `CREATE FUNCTION reject_audit_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='audit.pruned' THEN RAISE EXCEPTION 'owned receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_audit_receipt BEFORE INSERT ON audit_system_events FOR EACH ROW EXECUTE FUNCTION reject_audit_receipt()`); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := s.Prune(ctx); err == nil || changed {
		t.Fatal("failed receipt committed delete")
	}
	var raw []byte
	var receipts int
	if err := e.Pool.QueryRow(ctx, `SELECT payload_json,(SELECT count(*) FROM audit_system_events WHERE action='audit.pruned') FROM legacy_audit_events WHERE source_id=1`).Scan(&raw, &receipts); err != nil || string(raw) != payload || receipts != 0 {
		t.Fatal("receipt failure lost private source", err)
	}
}

func TestAuditRetentionReplay(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	var now time.Time
	if err := e.Pool.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	s := New(e.Pool, StatisticsPorts{}, HistoryPorts{}, Config{RetentionDays: 1, Timezone: auditTestZone(t, now, true)})
	payload := `{"body":"private-replayed-body"}`
	p := LegacyAuditPackage{Version: 1, Events: []LegacyAuditInput{{SourceID: 9223372036854775807, CreatedAt: now.Add(-370 * 24 * time.Hour), Action: "support.message", PayloadJSON: &payload}}}
	if out, err := s.ImportLegacy(ctx, p, false); err != nil || out.Inserted != 1 {
		t.Fatal("seed import", err)
	}
	if out, changed, err := s.Prune(ctx); err != nil || !changed || out.LegacyCount != 1 {
		t.Fatal("legacy private row not pruned", err)
	}
	for _, dry := range []bool{true, false} {
		if out, err := s.ImportLegacy(ctx, p, dry); err != nil || out.Inserted != 0 || out.Replayed != 1 {
			t.Fatal("retention replay reinstated source", err)
		}
	}
	var raw, ledger, system int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM legacy_audit_events),(SELECT count(*) FROM legacy_audit_imports),(SELECT count(*) FROM audit_system_events)`).Scan(&raw, &ledger, &system); err != nil || raw != 0 || ledger != 1 || system != 2 {
		t.Fatal("pruned private history resurrected", err)
	}
	other := `{"body":"changed-private-body"}`
	p.Events[0].PayloadJSON = &other
	if _, err := s.ImportLegacy(ctx, p, false); err == nil {
		t.Fatal("pruned source conflict ignored")
	} else {
		var fault *Error
		if !errors.As(err, &fault) || fault.Status != 409 || fault.Code != "IMPORT_SOURCE_CONFLICT" {
			t.Fatal("unsafe replay conflict")
		}
	}
}

func TestAuditRetentionScheduleAndConcurrency(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	var now time.Time
	if err := e.Pool.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	early := New(e.Pool, StatisticsPorts{}, HistoryPorts{}, Config{RetentionDays: 1, Timezone: auditTestZone(t, now, false)})
	if _, changed, err := early.Prune(ctx); err != nil || changed {
		t.Fatal("pruned before03:30", err)
	}
	s := New(e.Pool, StatisticsPorts{}, HistoryPorts{}, Config{RetentionDays: 1, Timezone: auditTestZone(t, now, true)})
	type result struct {
		changed bool
		err     error
	}
	done := make(chan result, 8)
	for i := 0; i < 8; i++ {
		go func() { _, changed, err := s.Prune(ctx); done <- result{changed, err} }()
	}
	runs := 0
	for i := 0; i < 8; i++ {
		out := <-done
		if out.err != nil {
			t.Fatal("parallel prune", out.err)
		}
		if out.changed {
			runs++
		}
	}
	var receipts int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_system_events WHERE action='audit.pruned'`).Scan(&receipts); err != nil || runs != 1 || receipts != 1 {
		t.Fatal("parallel daily prune duplicated", runs, receipts, err)
	}
}
