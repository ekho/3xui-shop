package wire

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSupportContractShapes(t *testing.T) {
	contract, err := GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		schema, body string
	}{
		{"SupportResult", `{"conversation":null,"messages":[],"has_more":false,"oldest_sequence":null}`},
		{"SupportMessage", `{"id":"00000000-0000-4000-8000-000000000001","sequence":1,"sender":"customer","text":"hello","created_at":"2026-10-02T10:00:00Z","attachment":null,"delivery":"stored"}`},
	} {
		var value any
		if err := json.Unmarshal([]byte(tc.body), &value); err != nil {
			t.Fatal(err)
		}
		if err := contract.Components.Schemas[tc.schema].Value.VisitJSON(value); err != nil {
			t.Errorf("%s: %v", tc.schema, err)
		}
	}
}

func TestOperatorContractShapes(t *testing.T) {
	contract, err := GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ schema, body string }{
		{"OperatorClient", `{"account_id":"00000000-0000-4000-8000-000000000001","kind":"telegram","display_name":"Fixture","email":null,"telegram_id":"9223372036854775807","locale":"en","created_at":null,"restricted":false,"vpn_banned":false,"had_subscription":false}`},
		{"OperatorHistoryResult", `{"kind":"trials","trial_requests":[],"audit_events":[],"has_more":false}`},
		{"OperatorDecisionResult", `{"request":{"request_id":"00000000-0000-4000-8000-000000000002","status":"rejected","created_at":"2026-10-02T10:00:00Z","decided_at":"2026-10-02T10:01:00Z","operation_id":null,"previous_request_id":null},"operation_id":null}`},
		{"TelegramPayload", `{"request_id":"00000000-0000-4000-8000-000000000002","operation_id":null,"target_message_id":null,"email":null,"display_name":"Fixture","telegram_id":"9223372036854775807","comment":"","created_at":"2026-10-02T10:00:00Z","status":"pending"}`},
	} {
		ref := contract.Components.Schemas[tc.schema]
		if ref == nil || ref.Value == nil {
			t.Fatalf("missing %s", tc.schema)
		}
		var value any
		if err := json.Unmarshal([]byte(tc.body), &value); err != nil {
			t.Fatal(err)
		}
		if err := ref.Value.VisitJSON(value); err != nil {
			t.Errorf("%s: %v", tc.schema, err)
		}
	}
}
