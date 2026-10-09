package subscriptions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func TestLegacyContractJSON(t *testing.T) {
	// Field order/omitempty affect persisted hashes, not just JSON consumers.
	for _, tc := range []struct {
		value any
		want  string
	}{
		{DecisionInput{CallbackQueryId: "frozen", Decision: "approve", OperatorTgId: 101}, `{"callback_query_id":"frozen","decision":"approve","operator_tg_id":101}`},
		{struct {
			Target uuid.UUID
			Input  AccessOperationInput
		}{uuid.MustParse("11111111-1111-4111-8111-111111111111"), AccessOperationInput{Kind: "assign_plan", PlanId: idPointer("22222222-2222-4222-8222-222222222222"), PeriodDays: intPointer(30), Reason: "fixture", Revision: intPointer(9007199254740993)}}, `{"Target":"11111111-1111-4111-8111-111111111111","Input":{"kind":"assign_plan","period_days":30,"plan_id":"22222222-2222-4222-8222-222222222222","reason":"fixture","revision":9007199254740993}}`},
		{TrialRequestInput{}, `{}`},
	} {
		got, err := json.Marshal(tc.value)
		if err != nil || string(got) != tc.want {
			t.Fatalf("persisted contract drift: %s (%v)", got, err)
		}
		hash := sha256.Sum256([]byte(tc.want))
		if !bytes.Equal(bodyHash(tc.value), hash[:]) {
			t.Fatal("legacy hash no longer matches literal JSON")
		}
	}
	const desired = `{"devices":2,"expires_at":null,"period_days":null,"plan_id":null,"profile":"regular","reset_traffic":false,"revision":null,"traffic_limit_bytes":9007199254740993,"vpn_banned":false}`
	var out AccessDesired
	if json.Unmarshal([]byte(desired), &out) != nil || out.TrafficLimitBytes != 9007199254740993 {
		t.Fatal("int64 snapshot lost")
	}
	raw, err := json.Marshal(out)
	if err != nil || string(raw) != desired {
		t.Fatal("legacy snapshot no longer round-trips")
	}
}

func intPointer(v int64) *int64     { return &v }
func idPointer(v string) *uuid.UUID { id := uuid.MustParse(v); return &id }

func TestLegacyTrialReplay(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	account := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	request := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	key := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,vpn_id,sub_id,panel_key,kind,locale,email_key,password_hash,verified_at,terms_version,privacy_version,restricted) VALUES($1,$2,'0123456789abcdef','fixture-key','web','ru','fixture@example.test','fixture','2026-10-01T00:00:00Z','t1','p1',true)`, account, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO trial_requests(id,account_id,status,comment,created_at) VALUES($1,$2,'pending','',$3)`, request, account, e.Now); err != nil {
		t.Fatal(err)
	}
	const result = `{"created_at":"2026-10-01T00:00:00Z","decided_at":null,"operation_id":null,"previous_request_id":null,"request_id":"22222222-2222-4222-8222-222222222222","status":"pending"}`
	hash := sha256.Sum256([]byte(`{}`))
	if _, err := e.Pool.Exec(ctx, `INSERT INTO idempotency_records(principal,operation,key,body_hash,result,created_at) VALUES($1,'createTrialRequest',$2,$3,$4,$5)`, "account:"+account.String(), key, hash[:], []byte(result), e.Now); err != nil {
		t.Fatal(err)
	}
	authority := accounts.New(e.Pool, e.Redis, nil, accounts.Config{})
	svc := New(e.Pool, authority, nil, nil, nil, func() Config { return Config{} }, e.Clock)
	// Cached success precedes eligibility; this account is intentionally restricted.
	out, created, err := svc.CreateTrialRequest(ctx, account, key, TrialRequestInput{})
	if err != nil || created || out.RequestId != request || out.Status != "pending" {
		t.Fatalf("old saved result not replayed: created=%v err=%v", created, err)
	}
	raw, err := json.Marshal(out)
	if err != nil || string(raw) != result {
		t.Fatal("old result representation changed")
	}
	var count int
	if e.Pool.QueryRow(ctx, `SELECT count(*) FROM trial_requests WHERE account_id=$1`, account).Scan(&count) != nil || count != 1 {
		t.Fatal("replay created another request")
	}
}

func TestLegacyDecisionReplay(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	account := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	request := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,vpn_id,sub_id,panel_key,kind,locale,telegram_id,display_name,restricted) VALUES($1,$2,'0123456789abcdef','fixture-key','telegram','ru',202,'Fixture',true)`, account, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO trial_requests(id,account_id,status,comment,created_at,decided_at,operator_tg_id,reason) VALUES($1,$2,'rejected','',$3,$3,101,'support_declined')`, request, account, e.Now); err != nil {
		t.Fatal(err)
	}
	const input = `{"RequestID":"22222222-2222-4222-8222-222222222222","Input":{"callback_query_id":"frozen-callback","decision":"reject","operator_tg_id":101}}`
	const result = `{"card":{"comment":"","created_at":"2026-10-01T00:00:00Z","email":null,"operation_id":null,"request_id":"22222222-2222-4222-8222-222222222222","status":"rejected","target_message_id":null},"delivery_state":"pending","operation_id":null,"request":{"created_at":"2026-10-01T00:00:00Z","decided_at":"2026-10-01T00:00:00Z","operation_id":null,"previous_request_id":null,"request_id":"22222222-2222-4222-8222-222222222222","status":"rejected"}}`
	hash := sha256.Sum256([]byte(input))
	if _, err := e.Pool.Exec(ctx, `INSERT INTO decision_callbacks(id,request_id,operator_tg_id,body_hash,result,created_at) VALUES('frozen-callback',$1,101,$2,$3,$4)`, request, hash[:], []byte(result), e.Now); err != nil {
		t.Fatal(err)
	}
	authority := accounts.New(e.Pool, e.Redis, nil, accounts.Config{Operators: []int64{101}})
	svc := New(e.Pool, authority, nil, nil, nil, func() Config { return Config{} }, e.Clock)
	out, err := svc.DecideTrialRequest(ctx, request, DecisionInput{CallbackQueryId: "frozen-callback", Decision: "reject", OperatorTgId: 101})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(out)
	if err != nil || string(raw) != result {
		t.Fatalf("legacy callback representation drift: %s (%v)", raw, err)
	}
	var callbacks, grants int
	if e.Pool.QueryRow(ctx, `SELECT count(*) FROM decision_callbacks`).Scan(&callbacks) != nil || callbacks != 1 || e.Pool.QueryRow(ctx, `SELECT count(*) FROM trial_grants`).Scan(&grants) != nil || grants != 0 {
		t.Fatal("callback replay duplicated its effects")
	}
}

func TestPendingOperatorTrials(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	operator := uuid.New()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id) VALUES($1,'queue-operator@example.test','ru','fixture',$2,$3,'aaaaaaaaaaaaaaaa',$4,'1','1',732)`, operator, e.Clock(), uuid.New(), "acct_"+operator.String()); err != nil {
		t.Fatal(err)
	}
	a := accounts.New(e.Pool, e.Redis, nil, accounts.Config{})
	if a.ChangeOperatorRole(ctx, operator, true) != nil {
		t.Fatal("operator")
	}
	if _, err := e.Pool.Exec(ctx, `WITH inserted AS(INSERT INTO accounts(id,vpn_id,sub_id,panel_key,kind,locale,email_key,password_hash,verified_at,terms_version,privacy_version) SELECT id,gen_random_uuid(),left(md5(id::text),16),'acct_'||id,'web','ru','pending-'||id||'@example.test','fixture',$1,'1','1' FROM(SELECT gen_random_uuid() AS id FROM generate_series(1,51)) s RETURNING id) INSERT INTO trial_requests(id,account_id,status,comment,created_at) SELECT gen_random_uuid(),id,'pending','private trial comment',$1 FROM inserted`, e.Clock()); err != nil {
		t.Fatal(err)
	}
	service := New(e.Pool, a, nil, nil, nil, func() Config { return Config{} }, e.Clock)
	proof, _, err := a.ResolveTelegramContext(ctx, 732)
	if err != nil {
		t.Fatal(err)
	}
	rows, more, err := service.PendingOperatorTrials(proof, operator)
	if err != nil || len(rows) != 50 || !more {
		t.Fatal("bounded pending manual trials", len(rows), more, err)
	}
	for _, row := range rows {
		if row.AccountID == uuid.Nil || row.RequestID == uuid.Nil || row.CreatedAt.IsZero() {
			t.Fatal("queue identity")
		}
	}
	raw, err := json.Marshal(rows)
	if err != nil || bytes.Contains(raw, []byte("private trial comment")) {
		t.Fatal("queue leaks bodies")
	}
	if a.ChangeOperatorRole(ctx, operator, false) != nil {
		t.Fatal("revoke")
	}
	if _, _, err = service.PendingOperatorTrials(proof, operator); err == nil {
		t.Fatal("revoked queue read")
	}
}
