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
