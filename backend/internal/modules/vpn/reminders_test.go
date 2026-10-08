package vpn

import (
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/google/uuid"
	"testing"
)

// Expiry-only dedup and treating device metadata as a reset break these periods.
func TestReminderPeriodProof(t *testing.T) {
	panel, profile := "fixture", "regular"
	trial := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	id := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	a := accounts.Snapshot{ID: uuid.New(), VpnID: id, PanelKey: "acct_fixture", SubID: "fixture", AssignedPanelID: &panel, AccessProfile: &profile, HadSubscription: true}
	target := ProvisionTarget{OperationID: trial, PanelID: panel, PanelKey: a.PanelKey, VPNID: id, SubID: a.SubID, InboundIDs: []int64{1}, TrafficLimitBytes: 10, Profile: profile}
	raw, _ := json.Marshal(target)
	b := statisticsBaseline{AccessBaseline: AccessBaseline{TrialID: &trial, TrialStatus: "applied", TrialTarget: raw}}
	got := reminderPeriod(a, b, panel, nil, nil)
	if !got.Known || got.TrafficPeriod != trial.String() || got.ExpiryMS != 0 || got.LimitBytes != 10 {
		t.Fatal("initial finite quota period with no expiry")
	}
	reset, metadata := uuid.New(), uuid.New()
	r := AccessTarget{OperationID: reset, PanelID: panel, PanelKey: a.PanelKey, VPNID: id, SubID: a.SubID, InboundIDs: []int64{1}, TrafficLimitBytes: 10, Profile: profile, Reset: true}
	resetRaw, _ := json.Marshal(r)
	r.OperationID, r.Reset, r.DeviceCount = metadata, false, 2
	b.AccessID = &metadata
	b.AccessTarget, _ = json.Marshal(r)
	got = reminderPeriod(a, b, panel, &reset, resetRaw)
	if !got.Known || got.TrafficPeriod != reset.String() || got.ExpiryMS != 0 {
		t.Fatal("latest metadata must retain monthly reset cycle")
	}
	r.ExpiryTimeMS = 1791504000000
	b.AccessTarget, _ = json.Marshal(r)
	got = reminderPeriod(a, b, panel, &reset, resetRaw)
	if !got.Known || got.TrafficPeriod != reset.String() || got.ExpiryMS != 1791504000000 {
		t.Fatal("expiry change alone must not invent traffic reset")
	}
	for _, name := range []string{"unresolved", "banned", "restricted", "wrong-identity", "wrong-reset-proof"} {
		t.Run(name, func(t *testing.T) {
			c, baseline, rr := a, b, resetRaw
			switch name {
			case "unresolved":
				baseline.Unresolved = true
			case "banned":
				c.VpnBanned = true
			case "restricted":
				c.Restricted = true
			case "wrong-identity":
				c.PanelKey = "other"
			case "wrong-reset-proof":
				rr = []byte(`{"operation_id":"invalid"}`)
			}
			p := reminderPeriod(c, baseline, panel, &reset, rr)
			if name == "wrong-reset-proof" {
				if p.TrafficPeriod != "" {
					t.Fatal("malformed prior reset fabricated cycle")
				}
			} else if p.Known {
				t.Fatal("unconfirmed period accepted")
			}
		})
	}
}
