package app

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/platform"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

// Seed the old schema directly so an owner transfer cannot redefine stored events.
func TestAuditReportsPersistedCompatibility(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	s := NewService(e.Pool, e.Redis, nil, platform.Config{RateNamespace: uuid.NewString()})
	customer, operator, other := paymentAccount(t, e), paymentAccount(t, e), paymentAccount(t, e)
	if err := s.ChangeOperatorRole(ctx, operator, true); err != nil {
		t.Fatal(err)
	}
	when := e.Clock().UTC().Truncate(time.Microsecond)
	request, operation, conversation, message, access := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO trial_requests(id,account_id,status,comment,created_at) VALUES($1,$2,'pending','',$3)`, []any{request, customer, when}},
		{`INSERT INTO trial_operations(id,account_id,request_id,status,trial_enabled,period_days,traffic_gb,devices,panel_id,created_at)
		 VALUES($1,$2,$3,'pending',true,3,15,1,'fixture',$4)`, []any{operation, customer, request, when}},
		{`INSERT INTO support_conversations(id,account_id,status,created_at,updated_at) VALUES($1,$2,'open',$3,$3)`, []any{conversation, customer, when}},
		{`INSERT INTO support_messages(id,conversation_id,sender_account_id,sender_kind,text,created_at) VALUES($1,$2,$3,'customer','old message',$4)`, []any{message, conversation, customer, when}},
		{`INSERT INTO access_operations(id,account_id,operator_account_id,kind,status,reason,desired,target,created_at,updated_at)
		 VALUES($1,$2,$3,'reset_traffic','skipped','fixture','{}','{}',$4,$4)`, []any{access, customer, operator, when}},
	} {
		if _, err := e.Pool.Exec(ctx, seed.query, seed.args...); err != nil {
			t.Fatal("legacy audit reference fixture", err)
		}
	}
	empty, period, system := "", "2026-10", false
	maximum := int64(9223372036854775807)
	old := make([]auditreports.Event, 52)
	for i := range old {
		id := uuid.UUID{}
		id[15] = byte(i + 1)
		row := auditreports.Event{ID: id, AccountID: customer, CreatedAt: when, Action: "old_fixture",
			RequestID: &request, OperationID: &operation, SupportMessageID: &message, AccessOperationID: &access,
			Reason: &empty, SystemActor: &system, MonthlyPeriod: &period}
		if i%2 == 0 {
			row.OperatorTgID = &maximum
		} else {
			row.OperatorAccountID = &operator
		}
		if i == 0 {
			row.RequestID, row.OperationID, row.SupportMessageID, row.AccessOperationID = nil, nil, nil, nil
			row.Reason, row.SystemActor, row.MonthlyPeriod, row.OperatorTgID = nil, nil, nil, nil
		}
		old[i] = row
		if _, err := e.Pool.Exec(ctx, `INSERT INTO audit_events(id,created_at,action,account_id,request_id,operation_id,
		 operator_tg_id,reason,operator_account_id,support_message_id,access_operation_id,system_actor,monthly_period)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, row.ID, row.CreatedAt, row.Action, row.AccountID,
			row.RequestID, row.OperationID, row.OperatorTgID, row.Reason, row.OperatorAccountID,
			row.SupportMessageID, row.AccessOperationID, row.SystemActor, row.MonthlyPeriod); err != nil {
			t.Fatal("legacy audit event fixture", err)
		}
	}
	owner := s.AuditReports()
	if owner == nil {
		t.Fatal("app did not compose audit owner")
	}
	first, more, err := owner.Page(ctx, customer, nil, uuid.New())
	if err != nil || !more || len(first) != 50 {
		t.Fatal("legacy first page", err)
	}
	last := first[49]
	second, more, err := owner.Page(ctx, customer, &last.CreatedAt, last.ID)
	if err != nil || more || len(second) != 2 {
		t.Fatal("legacy second page", err)
	}
	all := append(append([]auditreports.Event{}, first...), second...)
	for i, row := range all {
		row.CreatedAt = row.CreatedAt.UTC()
		if !reflect.DeepEqual(row, old[51-i]) {
			t.Fatal("legacy fields, NULL/empty/false/int64 or tuple ordering changed", i)
		}
	}
	isolated, more, err := owner.Page(ctx, other, nil, uuid.Nil)
	if err != nil || more || isolated == nil || len(isolated) != 0 {
		t.Fatal("audit target isolation or empty list changed", err)
	}
	history, err := s.OperatorClientHistory(ctx, operator, customer, wire.OperatorHistoryInput{Kind: "audit"})
	if err != nil || !history.HasMore || len(history.AuditEvents) != 50 {
		t.Fatal("composed history", err)
	}
	card, err := s.OperatorClient(ctx, operator, customer)
	if err != nil || !card.AuditHasMore || !reflect.DeepEqual(card.AuditEvents, history.AuditEvents) {
		t.Fatal("composed card", err)
	}
	for i, got := range history.AuditEvents {
		got.CreatedAt = got.CreatedAt.UTC()
		row := old[51-i]
		want := wire.OperatorAuditEvent{Id: row.ID, CreatedAt: row.CreatedAt, Action: row.Action,
			RequestId: row.RequestID, OperationId: row.OperationID, OperatorAccountId: row.OperatorAccountID,
			SupportMessageId: row.SupportMessageID, AccessOperationId: row.AccessOperationID,
			Reason: row.Reason, SystemActor: row.SystemActor, MonthlyPeriod: row.MonthlyPeriod}
		if row.OperatorTgID != nil {
			text := strconv.FormatInt(*row.OperatorTgID, 10)
			want.OperatorTgId = &text
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("legacy wire projection changed", i)
		}
	}
	written := old[51]
	written.ID, written.CreatedAt = uuid.New(), when.Add(time.Hour)
	for _, commit := range []bool{false, true} {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = auditreports.RecordTx(ctx, tx, written); err != nil {
			tx.Rollback(ctx)
			t.Fatal("record caller Tx", err)
		}
		var inside, outside int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE id=$1", written.ID).Scan(&inside); err != nil {
			tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE id=$1", written.ID).Scan(&outside); err != nil || inside != 1 || outside != 0 {
			tx.Rollback(ctx)
			t.Fatal("audit escaped caller Tx", err)
		}
		if commit {
			err = tx.Commit(ctx)
		} else {
			err = tx.Rollback(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE id=$1", written.ID).Scan(&outside); err != nil || outside != map[bool]int{false: 0, true: 1}[commit] {
			t.Fatal("caller rollback/commit lost atomicity", err)
		}
	}
	readback, _, err := owner.Page(ctx, customer, nil, uuid.Nil)
	if len(readback) > 0 {
		readback[0].CreatedAt = readback[0].CreatedAt.UTC()
	}
	if err != nil || len(readback) == 0 || !reflect.DeepEqual(readback[0], written) {
		t.Fatal("caller event ID/time/fields changed", err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bad := written
	bad.ID, bad.OperatorTgID = uuid.New(), &maximum
	if err = auditreports.RecordTx(ctx, tx, bad); err == nil {
		tx.Rollback(ctx)
		t.Fatal("old single-actor constraint bypassed or error suppressed")
	}
	tx.Rollback(ctx)
	if err = s.ChangeOperatorRole(ctx, operator, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.OperatorClientHistory(ctx, operator, customer, wire.OperatorHistoryInput{Kind: "audit"}); err == nil || err.Error() != "INVALID_CREDENTIALS" {
		t.Fatal("revoked operator read audit", err)
	}
}
