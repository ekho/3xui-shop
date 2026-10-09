package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A dropped filter, actor field or tuple tie-break would expose the wrong history.
func TestAuditHistoryNativeMetadata(t *testing.T) {
	h, e, cfg := httpFixture(t)
	ctx := context.Background()
	client := supportLogin(t, h, e, cfg, "audit-client@example.test")
	operator := supportLogin(t, h, e, cfg, "audit-operator@example.test")
	if _, err := e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, operator.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	when := e.Clock().UTC().Truncate(time.Microsecond)
	for i := 1; i <= 52; i++ {
		id := uuid.UUID{}
		id[15] = byte(i)
		var actor *uuid.UUID
		var reason *string
		if i != 1 {
			actor = &operator.id
			value := "Recorded operator reason"
			reason = &value
		}
		if _, err := e.Pool.Exec(ctx, `INSERT INTO audit_events(id,account_id,created_at,action,operator_account_id,reason) VALUES($1,$2,$3,'owned.audit',$4,$5)`, id, client.id, when, actor, reason); err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Version   string     `json:"version"`
		Kind      string     `json:"kind"`
		AccountID *uuid.UUID `json:"account_id"`
		Native    []struct {
			AccountID uuid.UUID               `json:"account_id"`
			Event     wire.OperatorAuditEvent `json:"event"`
		} `json:"native_events"`
		Legacy  []any `json:"legacy_events"`
		System  []any `json:"system_events"`
		HasMore bool  `json:"has_more"`
	}
	read := func(input map[string]any) page {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		r := supportRequest(h, &operator, "POST", "/api/v1/operator/audit/history", "application/json", body, cfg.HTTP.CabinetOrigin, uuid.Nil)
		if r.Code != 200 {
			t.Fatalf("authorized journal must return 200, got %d", r.Code)
		}
		var out page
		if json.Unmarshal(r.Body.Bytes(), &out) != nil {
			t.Fatal("invalid audit page")
		}
		if out.Version != "audit-history-v1" || out.Kind != "native" || out.AccountID == nil || *out.AccountID != client.id || out.Native == nil || out.Legacy == nil || out.System == nil || len(out.Legacy) != 0 || len(out.System) != 0 {
			t.Fatal("source/filter contract changed")
		}
		return out
	}
	first := read(map[string]any{"kind": "native", "account_id": client.id})
	if len(first.Native) != 50 || !first.HasMore || first.Native[0].Event.Id[15] != 52 || first.Native[49].Event.Id[15] != 3 {
		t.Fatal("same-time descending first page")
	}
	for _, row := range first.Native {
		if row.AccountID != client.id || row.Event.OperatorAccountId == nil || *row.Event.OperatorAccountId != operator.id || row.Event.Reason == nil || *row.Event.Reason != "Recorded operator reason" || row.Event.Action != "owned.audit" || !row.Event.CreatedAt.Equal(when) {
			t.Fatal("recorded identity/actor/time/reason changed")
		}
	}
	last := first.Native[49].Event
	second := read(map[string]any{"kind": "native", "account_id": client.id, "before_created_at": last.CreatedAt, "before_id": last.Id})
	if len(second.Native) != 2 || second.HasMore || second.Native[0].Event.Id[15] != 2 || second.Native[1].Event.Id[15] != 1 {
		t.Fatal("tuple cursor skipped or repeated event")
	}
	unknown := second.Native[1].Event
	if unknown.OperatorAccountId != nil || unknown.OperatorTgId != nil || unknown.SystemActor != nil || unknown.Reason != nil {
		t.Fatal("unknown actor/reason fabricated")
	}
}

func TestAuditHistoryRightsAndSingleConnection(t *testing.T) {
	h, s, e, cfg, _, client, operator := noticeFixture(t, 1)
	ctx := context.Background()
	path := "/api/v1/operator/audit/history"
	body := []byte(`{"kind":"native"}`)
	for _, check := range []struct {
		session *supportSession
		origin  string
		status  int
	}{
		{nil, cfg.HTTP.CabinetOrigin, 401}, {&client, cfg.HTTP.CabinetOrigin, 403}, {&operator, "https://attacker.example.test", 403}, {&operator, "", 403},
	} {
		if got := supportRequest(h, check.session, "POST", path, "application/json", body, check.origin, uuid.Nil); got.Code != check.status {
			t.Fatal("unauthorized audit data", got.Code, check.status)
		}
	}
	noCSRF := operator
	noCSRF.csrf = ""
	if r := supportRequest(h, &noCSRF, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 403 {
		t.Fatal("CSRF bypass", r.Code)
	}
	req := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer forbidden-mini-app")
	req.Header.Set("Origin", cfg.HTTP.CabinetOrigin)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(operator.cookie)
	req.Header.Set("X-CSRF-Token", operator.csrf)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != 403 {
		t.Fatal("bearer reached audit", r.Code)
	}
	if r = supportRequest(h, &operator, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 200 {
		t.Fatal("one-connection actor/read Tx failed", r.Code)
	}

	// Revoke after transport auth but before the actual caller-Tx role check.
	entered, release := make(chan struct{}), make(chan struct{})
	s.Modules.AuditReports = auditreports.New(e.Pool, auditreports.StatisticsPorts{}, auditreports.HistoryPorts{LockOperatorTx: func(ctx context.Context, tx pgx.Tx, id uuid.UUID) (bool, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		return s.Accounts.LockNoticeOperatorTx(ctx, tx, id)
	}}, auditreports.Config{})
	h = New(s.Modules, s.pool, cfg.HTTP)
	done := make(chan int, 1)
	go func() {
		done <- supportRequest(h, &operator, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, uuid.Nil).Code
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("caller-Tx guard not reached")
	}
	connection, err := pgx.ConnectConfig(ctx, e.Pool.Config().ConnConfig.Copy())
	if err != nil {
		close(release)
		t.Fatal("owned independent role connection unavailable")
	}
	_, err = connection.Exec(ctx, `DELETE FROM operator_accounts WHERE account_id=$1`, operator.id)
	connection.Close(ctx)
	close(release)
	if err != nil {
		t.Fatal("owned revocation failed")
	}
	select {
	case status := <-done:
		if status != 403 {
			t.Fatal("stale transport role read journal", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("single-connection guard blocked")
	}
}

func TestAuditHistoryInvalidCursors(t *testing.T) {
	h, _, _, cfg, _, _, operator := noticeFixture(t, 1)
	id := "aa111111-2222-4333-8444-555555555555"
	for _, body := range []string{
		`{"kind":"native","before_created_at":"2026-10-01T00:00:00.000000001Z","before_id":"` + id + `"}`,
		`{"kind":"native","before_created_at":"0001-01-01T00:00:00Z","before_id":"` + id + `"}`,
		`{"kind":"native","before_id":"` + id + `"}`,
		`{"kind":"native","before_created_at":"2026-10-01T00:00:00Z"}`,
		`{"kind":"native","before_source_id":"42"}`,
		`{"kind":"system","account_id":"` + id + `"}`,
		`{"kind":"native","legacy_target_tg_id":"42"}`,
		`{"kind":"legacy","account_id":"` + id + `","legacy_target_tg_id":"42"}`,
		`{"kind":"legacy","legacy_target_tg_id":"9223372036854775808"}`,
		`{"kind":"native","account_id":"00000000-0000-0000-0000-000000000000"}`,
		`{"kind":"native","account_id":"` + strings.ToUpper(id) + `"}`,
		`{"kind":"native","actor":"forged"}`,
	} {
		if r := supportRequest(h, &operator, "POST", "/api/v1/operator/audit/history", "application/json", []byte(body), cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 400 {
			t.Fatal("invalid or lossy cursor/filter accepted", r.Code)
		}
	}
}

func TestAuditHistoryOriginalTimestamp(t *testing.T) {
	h, _, _, cfg, _, _, operator := noticeFixture(t, 1)
	for _, kind := range []string{"native", "legacy", "system"} {
		for _, stamp := range []string{"2026-10-01T00:00:00.1234560001Z", "2026-10-01T00:00:00.000000000000000001+03:00", "2026-10-01T00:00:00.123456000000000000Z"} {
			t.Run(kind+stamp, func(t *testing.T) {
				in := map[string]string{"kind": kind, "before_created_at": stamp}
				if kind == "legacy" {
					in["before_source_id"] = "1"
				} else {
					in["before_id"] = "aa111111-2222-4333-8444-555555555555"
				}
				body, _ := json.Marshal(in)
				want := 400
				if stamp == "2026-10-01T00:00:00.123456000000000000Z" {
					want = 200
				}
				if r := supportRequest(h, &operator, "POST", "/api/v1/operator/audit/history", "application/json", body, cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != want {
					t.Fatal("original cursor lost precision before validation", r.Code, want)
				}
			})
		}
	}
}

func TestAuditHistoryLegacyIdentity(t *testing.T) {
	h, _, e, cfg, _, client, operator := noticeFixture(t, 1)
	ctx := context.Background()
	other := supportLogin(t, h, e, cfg, "audit-rebound@example.test")
	const target int64 = 9007199254740993
	const lastID int64 = 9223372036854775807
	when := e.Clock().UTC().Truncate(time.Microsecond)
	if _, err := e.Pool.Exec(ctx, `INSERT INTO legacy_approval_snapshots(account_id,source_legacy_user_id,source_tg_id,status) VALUES($1,789,$2,'approved')`, client.id, target); err != nil {
		t.Fatal(err)
	}
	for i := int64(0); i < 52; i++ {
		id := lastID - i
		if _, err := e.Pool.Exec(ctx, `INSERT INTO legacy_audit_imports(source_id,source_hash) VALUES($1,$2);`, id, make([]byte, 32)); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Pool.Exec(ctx, `INSERT INTO legacy_audit_events(source_id,created_at,action,target_tg_id,actor_type,actor_id,actor_name,source,payload_json) VALUES($1,$2,'support.message',$3,'operator',-9007199254740993,'<b>Original name</b>','support_bot',$4)`, id, when, target, []byte(`{"dm_body":"private-audit-must-not-leak"}`)); err != nil {
			t.Fatal(err)
		}
	}
	read := func(input map[string]any) wire.AuditHistory {
		t.Helper()
		body, _ := json.Marshal(input)
		r := supportRequest(h, &operator, "POST", "/api/v1/operator/audit/history", "application/json", body, cfg.HTTP.CabinetOrigin, uuid.Nil)
		if r.Code != 200 {
			t.Fatal("legacy journal must return200", r.Code)
		}
		if strings.Contains(r.Body.String(), "private-audit-must-not-leak") || strings.Contains(r.Body.String(), "payload_json") {
			t.Fatal("private legacy payload escaped owner")
		}
		var out wire.AuditHistory
		if json.Unmarshal(r.Body.Bytes(), &out) != nil || out.Kind != "legacy" || out.Version != "audit-history-v1" || len(out.NativeEvents) != 0 || len(out.SystemEvents) != 0 {
			t.Fatal("legacy source contract")
		}
		return out
	}
	first := read(map[string]any{"kind": "legacy", "account_id": client.id})
	if len(first.LegacyEvents) != 50 || !first.HasMore {
		t.Fatal("legacy50+1")
	}
	row := first.LegacyEvents[0]
	if row.SourceId != "9223372036854775807" || row.TargetTgId == nil || *row.TargetTgId != "9007199254740993" || row.ActorId == nil || *row.ActorId != "-9007199254740993" || row.ActorName == nil || *row.ActorName != "<b>Original name</b>" || row.AccountId == nil || *row.AccountId != client.id {
		t.Fatal("original legacy metadata/immutable target changed")
	}
	if first.LegacyEvents[49].SourceId != strconv.FormatInt(lastID-49, 10) {
		t.Fatal("same-time source cursor ordering")
	}
	second := read(map[string]any{"kind": "legacy", "account_id": client.id, "before_created_at": when, "before_source_id": first.LegacyEvents[49].SourceId})
	if len(second.LegacyEvents) != 2 || second.HasMore || second.LegacyEvents[1].SourceId != strconv.FormatInt(lastID-51, 10) {
		t.Fatal("legacy tuple skipped/repeated")
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=$1 WHERE id=$2`, target, other.id); err != nil {
		t.Fatal(err)
	}
	if rebound := read(map[string]any{"kind": "legacy", "account_id": other.id}); len(rebound.LegacyEvents) != 0 {
		t.Fatal("current TG binding stole legacy history")
	}
	if preserved := read(map[string]any{"kind": "legacy", "account_id": client.id}); len(preserved.LegacyEvents) != 50 || *preserved.LegacyEvents[0].AccountId != client.id {
		t.Fatal("unlink/rebind changed source identity")
	}
	if global := read(map[string]any{"kind": "legacy", "legacy_target_tg_id": "9007199254740993"}); len(global.LegacyEvents) != 50 || *global.LegacyEvents[0].AccountId != client.id {
		t.Fatal("target filter mapped through current binding")
	}
	if empty := read(map[string]any{"kind": "legacy", "legacy_target_tg_id": "9007199254740994"}); len(empty.LegacyEvents) != 0 {
		t.Fatal("legacy target filter dropped")
	}
}

func TestAuditHistorySystemMetadata(t *testing.T) {
	h, _, e, cfg, _, _, operator := noticeFixture(t, 1)
	id := uuid.New()
	if _, err := e.Pool.Exec(context.Background(), `INSERT INTO audit_system_events(id,created_at,action,native_count,legacy_count,system_count) VALUES($1,now(),'audit.legacy_imported',0,7,0)`, id); err != nil {
		t.Fatal(err)
	}
	r := supportRequest(h, &operator, "POST", "/api/v1/operator/audit/history", "application/json", []byte(`{"kind":"system"}`), cfg.HTTP.CabinetOrigin, uuid.Nil)
	if r.Code != 200 {
		t.Fatal("system journal must return200", r.Code)
	}
	var out wire.AuditHistory
	if json.Unmarshal(r.Body.Bytes(), &out) != nil || len(out.SystemEvents) != 1 || len(out.NativeEvents) != 0 || len(out.LegacyEvents) != 0 || out.SystemEvents[0].Id != id || out.SystemEvents[0].LegacyCount != 7 || out.SystemEvents[0].Cutoff != nil || out.SystemEvents[0].PeriodDay != nil {
		t.Fatal("system source metadata changed")
	}
}
