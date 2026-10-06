package httpapi

import (
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"testing"
	"time"
)

// These literals were persisted by the pre-extraction workers.
func TestRegressionPanelTargetCompatibility(t *testing.T) {
	provisionJSON := `{"operation_id":"11111111-1111-4111-8111-111111111111","panel_id":"test","panel_key":"acct_fixture","vpn_id":"22222222-2222-4222-8222-222222222222","sub_id":"fixture","inbound_ids":[1,3],"expiry_time_ms":1800000000000,"device_count":2,"traffic_limit_bytes":9007199254740993}`
	accessJSON := `{"operation_id":"11111111-1111-4111-8111-111111111111","panel_id":"test","panel_key":"acct_fixture","vpn_id":"22222222-2222-4222-8222-222222222222","sub_id":"fixture","expiry_time_ms":1800000000000,"device_count":2,"traffic_limit_bytes":2048,"profile":"regular","inbound_ids":[1,3],"missing":false,"reset":true,"banned":false,"previous_banned":false,"enable":false,"no_client_intent":false,"restore_enabled":false,"previous_expiry_ms":1700000000000,"previous_limit_ip":3,"previous_traffic_limit_bytes":1024,"previous_inbound_ids":[1]}`
	var provision vpn.ProvisionTarget
	var access vpn.AccessTarget
	for _, tc := range []struct {
		literal string
		target  any
	}{{provisionJSON, &provision}, {accessJSON, &access}} {
		if err := json.Unmarshal([]byte(tc.literal), tc.target); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(tc.target)
		if err != nil || string(encoded) != tc.literal {
			t.Fatal("persisted target representation changed", err)
		}
	}
	view := &vpn.PanelClientView{PanelKey: "acct_fixture", VPNID: uuid.MustParse("22222222-2222-4222-8222-222222222222"), SubID: "fixture", ExpiryTimeMS: 1800000000000, LimitIP: 3, TrafficLimitBytes: 9007199254740993, Enabled: true, InboundIDs: []int64{1}}
	if !vpn.Matches(view, provision, time.UnixMilli(1700000000000)) {
		t.Fatal("persisted provisioning identity/limits no longer match")
	}
	if ids := vpn.MissingInbounds(view, provision); len(ids) != 1 || ids[0] != 3 {
		t.Fatal("missing membership changed")
	}
	view.VPNID = uuid.New()
	if vpn.Matches(view, provision, time.UnixMilli(1700000000000)) {
		t.Fatal("different client identity accepted")
	}
}
