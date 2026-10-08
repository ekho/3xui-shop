package httpapi

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/testkit"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type statisticsHTTPReport struct {
	Version             string                 `json:"version"`
	CampaignID          *uuid.UUID             `json:"campaign_id"`
	DatabaseObservedAt  time.Time              `json:"database_observed_at"`
	Users               int64                  `json:"users"`
	UnknownUserProfiles int64                  `json:"unknown_user_profiles"`
	Trials              wire.TrialStatistics   `json:"trials"`
	Payments            wire.PaymentStatistics `json:"payments"`
	Conversions         struct {
		TrialPercent  *string `json:"trial_percent"`
		PaidPercent   *string `json:"paid_percent"`
		RepeatPercent *string `json:"repeat_percent"`
	} `json:"conversions"`
	Activity struct {
		ActiveUsers        *int64 `json:"active_users"`
		KnownActiveUsers   int64  `json:"known_active_users"`
		KnownInactiveUsers int64  `json:"known_inactive_users"`
		UnknownUsers       int64  `json:"unknown_users"`
	} `json:"activity"`
	Groups []struct {
		Name                     string `json:"name"`
		UserReferences           int64  `json:"user_references"`
		PlanReferences           int64  `json:"plan_references"`
		InboundReferences        *int64 `json:"inbound_references"`
		EnabledInboundReferences *int64 `json:"enabled_inbound_references"`
	} `json:"groups"`
	Servers []struct {
		PanelID      string `json:"panel_id"`
		Availability string `json:"availability"`
		Clients      *int64 `json:"clients"`
	} `json:"servers"`
}

func statisticsHTTPPanel(t *testing.T, panel http.Handler) (http.Handler, *testkit.Env, app.Config) {
	t.Helper()
	server := httptest.NewTLSServer(panel)
	t.Cleanup(server.Close)
	_, e, cfg := httpFixture(t)
	cfg.Accounts.Now = e.Clock
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	cfg.VPN.Panel = vpn.Config{PanelURL: server.URL, PanelToken: "fixture", PanelRootCAs: roots}
	queue, err := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return New(app.NewModules(e.Pool, e.Redis, queue, &cfg), e.Pool, cfg.HTTP), e, cfg
}

func TestOperatorStatisticsPanel(t *testing.T) {
	var client map[string]any
	var reads, writes atomic.Int64
	var outage atomic.Bool
	h, e, cfg := statisticsHTTPPanel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
			w.WriteHeader(405)
			return
		}
		reads.Add(1)
		if outage.Load() {
			w.WriteHeader(503)
			return
		}
		var obj any
		switch r.URL.Path {
		case "/panel/api/inbounds/list":
			obj = []map[string]any{{"id": 1, "enable": true, "tag": "region-regular-tcp"}, {"id": 2, "enable": false, "tag": "regular"}, {"id": 3, "enable": true, "tag": "nonregular"}}
		case "/panel/api/clients/list":
			obj = []map[string]any{client, {"id": 8, "email": "foreign-panel-client", "uuid": "", "subId": "", "enable": true, "expiryTime": 0, "limitIp": 0, "totalGB": 0, "inboundIds": nil}}
		default:
			writes.Add(1)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": obj})
	}))
	ctx := context.Background()
	actor := campaignOperator(t, h, e, cfg)
	issued := supportLogin(t, h, e, cfg, "reports-issued@example.test")
	unconfirmed := supportLogin(t, h, e, cfg, "reports-unconfirmed@example.test")
	campaign := campaignCreate(t, h, actor, cfg, "One granted in three accounts")
	for _, id := range []uuid.UUID{actor.id, issued.id, unconfirmed.id} {
		if _, err := e.Pool.Exec(ctx, `INSERT INTO campaign_acquisitions(account_id,campaign_id,channel,created_at,source_code) VALUES($1,$2,'web',$3,$4)`, id, campaign.ID, e.Clock(), *campaign.Code); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET assigned_panel_id=$2,restricted=true WHERE id=$1`, issued.id, cfg.Subscriptions.PanelID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET assigned_panel_id=$2,access_profile=NULL WHERE id=$1`, unconfirmed.id, cfg.Subscriptions.PanelID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET vpn_banned=true WHERE id=$1`, actor.id); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"regular", "unlimited"} {
		if _, err := e.Pool.Exec(ctx, `INSERT INTO catalogue_plans(id,current_revision,current_devices,current_profile,current_hidden,archived) VALUES($1,1,2,$2,true,true)`, uuid.New(), profile); err != nil {
			t.Fatal(err)
		}
	}
	var target vpn.ProvisionTarget
	requestID, operationID := uuid.New(), uuid.New()
	if err := e.Pool.QueryRow(ctx, `SELECT panel_key,vpn_id,sub_id FROM accounts WHERE id=$1`, issued.id).Scan(&target.PanelKey, &target.VPNID, &target.SubID); err != nil {
		t.Fatal(err)
	}
	target.OperationID, target.PanelID, target.Profile = operationID, cfg.Subscriptions.PanelID, "regular"
	target.InboundIDs, target.DeviceCount, target.ExpiryTimeMS, target.TrafficLimitBytes = []int64{1}, 1, e.Clock().Add(72*time.Hour).UnixMilli(), 15*1024*1024*1024
	client = map[string]any{"id": 7, "email": target.PanelKey, "uuid": target.VPNID, "subId": target.SubID, "enable": true, "expiryTime": target.ExpiryTimeMS, "limitIp": 2, "totalGB": target.TrafficLimitBytes, "inboundIds": []int64{1, 1}, "providerSecret": "must-not-return", "traffic": map[string]any{"email": target.PanelKey, "uuid": target.VPNID, "subId": target.SubID, "up": 17, "down": 25}}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO trial_requests(id,account_id,status,comment,created_at,decided_at,operator_tg_id,operation_id) VALUES($1,$2,'approved','',$3,$3,101,$4)`, requestID, issued.id, e.Clock(), operationID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO trial_operations(id,account_id,request_id,status,trial_enabled,period_days,traffic_gb,devices,panel_id,created_at,target) VALUES($1,$2,$3,'applied',true,3,15,1,$4,$5,$6)`, operationID, issued.id, requestID, cfg.Subscriptions.PanelID, e.Clock(), mustJSON(t, target)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO trial_grants(account_id,request_id,operation_id,status,created_at,granted_at) VALUES($1,$2,$3,'granted',$4,$4)`, issued.id, requestID, operationID, e.Clock()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var v string
		if err := e.Pool.QueryRow(ctx, `SELECT md5(jsonb_build_array((SELECT jsonb_agg(x ORDER BY id) FROM accounts x),(SELECT jsonb_agg(x ORDER BY id) FROM trial_operations x),(SELECT jsonb_agg(x ORDER BY id) FROM access_operations x),(SELECT jsonb_agg(x ORDER BY id) FROM audit_events x),(SELECT jsonb_agg(x ORDER BY id) FROM river_job x))::text)`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := snapshot()
	for _, scope := range []*uuid.UUID{nil, &campaign.ID} {
		report := readStatisticsHTTP(t, h, actor, cfg.HTTP.CabinetOrigin, scope)
		if report.Users != 3 || report.UnknownUserProfiles != 1 || report.Trials.TrialUsers != 1 || report.Conversions.TrialPercent == nil || *report.Conversions.TrialPercent != "33.33" || report.Activity.ActiveUsers != nil || report.Activity.KnownActiveUsers != 1 || report.Activity.KnownInactiveUsers != 1 || report.Activity.UnknownUsers != 1 || report.Servers[0].Clients == nil || *report.Servers[0].Clients != 2 {
			t.Fatal("truthful selected activity/provider count/one-of-three")
		}
		for _, group := range report.Groups {
			switch group.Name {
			case "regular":
				if group.UserReferences != 2 || group.PlanReferences != 1 || group.InboundReferences == nil || *group.InboundReferences != 2 || *group.EnabledInboundReferences != 1 {
					t.Fatal("regular stored/global tag references")
				}
			case "banned":
				if group.UserReferences != 1 {
					t.Fatal("independent ban overlay")
				}
			case "unlimited":
				if group.PlanReferences != 1 || group.UserReferences != 0 || group.InboundReferences == nil || *group.InboundReferences != 0 {
					t.Fatal("hidden/archived plans or access inheritance doubled references")
				}
			}
		}
		packed := string(mustJSON(t, report))
		for _, secret := range []string{target.PanelKey, target.VPNID.String(), target.SubID, "foreign-panel-client", "must-not-return"} {
			if strings.Contains(packed, secret) {
				t.Fatal("report leaked identity/provider payload")
			}
		}
	}
	if reads.Load() != 4 || writes.Load() != 0 || snapshot() != before {
		t.Fatal("report is not two bulk read-only requests")
	}
	outage.Store(true)
	degraded := readStatisticsHTTP(t, h, actor, cfg.HTTP.CabinetOrigin, &campaign.ID)
	if degraded.Activity.ActiveUsers != nil || degraded.Activity.UnknownUsers != 2 || degraded.Activity.KnownInactiveUsers != 1 || degraded.Servers[0].Availability != "unavailable" || degraded.Servers[0].Clients != nil || degraded.Groups[1].InboundReferences != nil || snapshot() != before {
		t.Fatal("provider outage became zero or modified observations")
	}
}

func TestOperatorStatisticsRoleRevoked(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var stop sync.Once
	defer stop.Do(func() { close(release) })
	h, e, cfg := statisticsHTTPPanel(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panel/api/clients/list" {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": []any{}})
	}))
	actor := campaignOperator(t, h, e, cfg)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- supportRequest(h, &actor, "POST", "/api/v1/operator/reports/statistics", "application/json", []byte(`{"campaign_id":null}`), cfg.HTTP.CabinetOrigin, uuid.Nil)
	}()
	select {
	case <-entered:
	case r := <-done:
		t.Fatalf("report did not reach held panel read: %d", r.Code)
	case <-time.After(3 * time.Second):
		t.Fatal("panel barrier deadline")
	}
	if _, err := e.Pool.Exec(context.Background(), `DELETE FROM operator_accounts WHERE account_id=$1`, actor.id); err != nil {
		t.Fatal(err)
	}
	stop.Do(func() { close(release) })
	select {
	case r := <-done:
		if r.Code != 403 || strings.Contains(r.Body.String(), "payments") || strings.Contains(r.Body.String(), "known_active_users") {
			t.Fatal("revoked operator received report", r.Code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("held report did not exit")
	}
}

func readStatisticsHTTP(t *testing.T, h http.Handler, actor supportSession, origin string, campaign *uuid.UUID) statisticsHTTPReport {
	t.Helper()
	body, err := json.Marshal(map[string]any{"campaign_id": campaign})
	if err != nil {
		t.Fatal(err)
	}
	r := supportRequest(h, &actor, "POST", "/api/v1/operator/reports/statistics", "application/json", body, origin, uuid.Nil)
	var report statisticsHTTPReport
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &report) != nil {
		t.Fatalf("operator statistics want200 got%d", r.Code)
	}
	if report.Version != "statistics-v1" || report.DatabaseObservedAt.IsZero() || len(report.Groups) != 4 || len(report.Servers) != 1 {
		t.Fatal("statistics completeness/version/observation")
	}
	var value any
	if json.Unmarshal(r.Body.Bytes(), &value) != nil {
		t.Fatal("report JSON")
	}
	contract, err := wire.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	schema := contract.Components.Schemas["StatisticsReport"]
	if schema == nil || schema.Value.VisitJSON(value) != nil {
		t.Fatal("report public schema")
	}
	return report
}

// A global fallback for an empty cohort, role/CSRF bypass or a report write
// breaks real HTTP/DB behavior; no report service is replaced by a test double.
func TestOperatorStatisticsScopeAndAuthority(t *testing.T) {
	h, e, cfg, _ := miniAppHTTPFixture(t)
	ctx := context.Background()
	actor := campaignOperator(t, h, e, cfg)
	client := supportLogin(t, h, e, cfg, "reports-client@example.test")
	campaign := campaignCreate(t, h, actor, cfg, "Reports empty cohort")
	global := readStatisticsHTTP(t, h, actor, cfg.HTTP.CabinetOrigin, nil)
	if global.Users != 2 || global.CampaignID != nil || global.Trials.TrialUsers != 0 || global.Payments.PaidOrders != 0 || global.Activity.ActiveUsers == nil || *global.Activity.ActiveUsers != 0 || global.Activity.KnownInactiveUsers != 2 || global.Activity.UnknownUsers != 0 || global.Servers[0].Clients != nil {
		t.Fatal("global identity scope and known absence of access")
	}
	if global.Conversions.TrialPercent == nil || *global.Conversions.TrialPercent != "0.00" || global.Conversions.PaidPercent == nil || *global.Conversions.PaidPercent != "0.00" || global.Conversions.RepeatPercent != nil {
		t.Fatal("zero numerators and zero denominator conversions")
	}
	empty := readStatisticsHTTP(t, h, actor, cfg.HTTP.CabinetOrigin, &campaign.ID)
	if empty.Users != 0 || empty.CampaignID == nil || *empty.CampaignID != campaign.ID || empty.Payments.PaidOrders != 0 || empty.Payments.Money == nil || empty.Payments.Refunds == nil || empty.Payments.Legacy.Money == nil || empty.Trials.TrialUsers != 0 {
		t.Fatal("empty cohort became global or lost typed zero arrays")
	}
	if empty.Conversions.TrialPercent != nil || empty.Conversions.PaidPercent != nil || empty.Conversions.RepeatPercent != nil {
		t.Fatal("empty scope percentages must be unknown")
	}
	if _, err := e.Pool.Exec(ctx, `INSERT INTO campaign_acquisitions(account_id,campaign_id,channel,created_at,source_code) VALUES($1,$2,'web',$3,$4)`, client.id, campaign.ID, e.Clock(), *campaign.Code); err != nil {
		t.Fatal(err)
	}
	for i, state := range []string{"paused", "deleted"} {
		body, _ := json.Marshal(map[string]any{"state": state, "expected_revision": i + 1, "reason": "owned statistics history"})
		r := supportRequest(h, &actor, "POST", "/api/v1/operator/campaigns/"+campaign.ID.String()+"/state", "application/json", body, cfg.HTTP.CabinetOrigin, uuid.New())
		if r.Code != 200 {
			t.Fatal("owned campaign transition", r.Code)
		}
		report := readStatisticsHTTP(t, h, actor, cfg.HTTP.CabinetOrigin, &campaign.ID)
		if report.Users != 1 || report.Activity.KnownInactiveUsers != 1 {
			t.Fatal("historical campaign scope lost or included operator")
		}
	}
	wrongCSRF := actor
	wrongCSRF.csrf = "wrong-proof"
	for _, tc := range []struct {
		name               string
		actor              *supportSession
		body, path, origin string
		want               int
	}{
		{"anonymous", nil, `{"campaign_id":null}`, "", cfg.HTTP.CabinetOrigin, 401},
		{"client", &client, `{"campaign_id":null}`, "", cfg.HTTP.CabinetOrigin, 403},
		{"csrf", &wrongCSRF, `{"campaign_id":null}`, "", cfg.HTTP.CabinetOrigin, 403},
		{"origin", &actor, `{"campaign_id":null}`, "", "https://foreign.example.test", 403},
		{"missing", &actor, `{"campaign_id":"` + uuid.NewString() + `"}`, "", cfg.HTTP.CabinetOrigin, 404},
		{"nil-uuid", &actor, `{"campaign_id":"00000000-0000-0000-0000-000000000000"}`, "", cfg.HTTP.CabinetOrigin, 400},
		{"invalid-uuid", &actor, `{"campaign_id":"bad"}`, "", cfg.HTTP.CabinetOrigin, 400},
		{"forged-actor", &actor, `{"campaign_id":null,"actor":"` + client.id.String() + `"}`, "", cfg.HTTP.CabinetOrigin, 400},
		{"query", &actor, `{"campaign_id":null}`, "?scope=all", cfg.HTTP.CabinetOrigin, 400},
		{"missing-scope", &actor, `{}`, "", cfg.HTTP.CabinetOrigin, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := supportRequest(h, tc.actor, "POST", "/api/v1/operator/reports/statistics"+tc.path, "application/json", []byte(tc.body), tc.origin, uuid.Nil)
			if r.Code != tc.want {
				t.Fatalf("statistics boundary want%d got%d", tc.want, r.Code)
			}
			var body map[string]json.RawMessage
			if json.Unmarshal(r.Body.Bytes(), &body) != nil || body["payments"] != nil || body["activity"] != nil {
				t.Fatal("denied report exposed protected data")
			}
		})
	}
}
