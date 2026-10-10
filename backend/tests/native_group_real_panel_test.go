package tests

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func TestNativeGroupReconciliationRealPanel(t *testing.T) {
	if os.Getenv("SERVER_MANAGEMENT_NATIVE_STATE") == "" {
		t.Skip("group API acceptance requires the owned isolated TLS 3X-UI fixture")
	}
	f, start, stop := serverManagementFixture(t)
	ctx := context.Background()
	start()
	client, _, account := f.signupAccount(t, nativeEmail("group-real-panel"))
	identity, err := f.svc.Accounts.Lookup(ctx, account)
	if err != nil {
		t.Fatal("owned account unavailable")
	}
	stop()
	jar, _ := cookiejar.New(nil)
	setup := &http.Client{Jar: jar, Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.cfg.VPN.Panel.PanelRootCAs, MinVersion: tls.VersionTLS12}}}
	t.Cleanup(setup.CloseIdleConnections)
	csrf := ""
	panelCall := func(path string, body any) (json.RawMessage, error) {
		method := "GET"
		var data []byte
		if body != nil {
			method = "POST"
			var err error
			data, err = json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("owned provider request body invalid")
			}
		}
		request, err := http.NewRequestWithContext(ctx, method, f.cfg.VPN.Panel.PanelURL+"/"+path, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("owned provider request invalid")
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", csrf)
		response, err := setup.Do(request)
		if err != nil {
			return nil, fmt.Errorf("owned TLS provider unavailable")
		}
		defer response.Body.Close()
		var reply struct {
			Success bool
			Obj     json.RawMessage
		}
		if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&reply) != nil || !reply.Success {
			return nil, fmt.Errorf("owned provider fixture request failed: method=%s status=%d", method, response.StatusCode)
		}
		return reply.Obj, nil
	}
	call := func(path string, body any) json.RawMessage {
		t.Helper()
		obj, err := panelCall(path, body)
		if err != nil {
			t.Fatal(err)
		}
		return obj
	}
	if json.Unmarshal(call("csrf-token", nil), &csrf) != nil || csrf == "" {
		t.Fatal("owned provider CSRF unavailable")
	}
	call("login", map[string]string{"username": f.cfg.VPN.Panel.PanelUsername, "password": f.cfg.VPN.Panel.PanelPassword})
	rows := func() []map[string]any {
		t.Helper()
		var out []map[string]any
		if json.Unmarshal(call("panel/api/inbounds/list", nil), &out) != nil {
			t.Fatal("owned inbound fixture invalid")
		}
		return out
	}
	var original map[string]any
	for _, row := range rows() {
		if row["tag"] == "local-regular-vless" {
			original = row
		}
	}
	if original == nil || original["enable"] != true {
		t.Fatal("owned regular fixture absent")
	}
	originalID := int64(original["id"].(float64))
	var createdInbounds []int64
	var createdClients []string
	restoreOriginal := false
	t.Cleanup(func() {
		stop()
		if restoreOriginal {
			original["enable"] = true
			if _, err := panelCall(fmt.Sprintf("panel/api/inbounds/update/%d", originalID), original); err != nil {
				t.Errorf("owned inbound restore failed: %v", err)
			}
		}
		for _, key := range createdClients {
			if _, err := panelCall("panel/api/clients/del/"+key, map[string]any{}); err != nil {
				t.Errorf("owned client cleanup failed: %v", err)
			}
		}
		for _, id := range createdInbounds {
			if _, err := panelCall(fmt.Sprintf("panel/api/inbounds/del/%d", id), map[string]any{}); err != nil {
				t.Errorf("owned inbound cleanup failed: %v", err)
			}
		}
	})
	addInbound := func(tag string, port int) int64 {
		t.Helper()
		obj := call("panel/api/inbounds/add", map[string]any{"enable": true, "remark": "Owned S40 acceptance", "listen": "0.0.0.0", "port": port, "protocol": "vless", "tag": tag, "settings": map[string]any{"clients": []any{}, "decryption": "none", "fallbacks": []any{}}, "streamSettings": map[string]any{"network": "tcp", "security": "none"}, "sniffing": map[string]any{"enabled": false}})
		var inbound struct{ ID int64 }
		if json.Unmarshal(obj, &inbound) != nil || inbound.ID <= 0 {
			t.Fatal("owned inbound creation unconfirmed")
		}
		createdInbounds = append(createdInbounds, inbound.ID)
		return inbound.ID
	}
	unknown := addInbound("local-s40-foreign-"+uuid.NewString(), 24445)
	p := vpn.NewPanelClient(f.cfg.VPN.Panel)
	t.Cleanup(p.Close)
	expiry := f.env.Clock().Add(30 * 24 * time.Hour).UnixMilli()
	owned := vpn.ProvisionTarget{OperationID: uuid.New(), PanelID: f.cfg.Subscriptions.PanelID, PanelKey: identity.PanelKey, VPNID: identity.VpnID, SubID: identity.SubID, InboundIDs: []int64{originalID, unknown}, Profile: "regular", DeviceCount: 2, TrafficLimitBytes: 1234567, ExpiryTimeMS: expiry}
	foreign := vpn.ProvisionTarget{OperationID: uuid.New(), PanelKey: "acct_s40_foreign_" + uuid.NewString(), VPNID: uuid.New(), SubID: uuid.NewString(), InboundIDs: []int64{originalID, unknown}, Profile: "regular", DeviceCount: 3, TrafficLimitBytes: 7654321, ExpiryTimeMS: expiry}
	createdClients = append(createdClients, owned.PanelKey)
	call("panel/api/clients/add", map[string]any{"client": map[string]any{"email": owned.PanelKey, "id": owned.VPNID, "subId": owned.SubID, "expiryTime": expiry, "limitIp": 1, "totalGB": owned.TrafficLimitBytes, "enable": true, "flow": "xtls-rprx-vision"}, "inboundIds": owned.InboundIDs})
	createdClients = append(createdClients, foreign.PanelKey)
	if p.AddClient(ctx, foreign) != nil {
		t.Fatal("owned synthetic clients unavailable")
	}
	before, err := p.GetClient(ctx, owned.PanelKey)
	if err != nil || before == nil {
		t.Fatal("owned real provider client unavailable")
	}
	// Seed only the owned provider counter so an unintended reset is observable.
	call("panel/api/clients/updateTraffic/"+owned.PanelKey, map[string]int64{"upload": 42, "download": 24})
	newID := addInbound("local-s40-regular-"+uuid.NewString(), 24446)
	for _, row := range rows() {
		if int64(row["id"].(float64)) == originalID {
			original = row
		}
	}
	original["enable"] = false
	restoreOriginal = true
	call(fmt.Sprintf("panel/api/inbounds/update/%d", originalID), original)
	before, err = p.GetClient(ctx, owned.PanelKey)
	foreignBefore, foreignErr := p.GetClient(ctx, foreign.PanelKey)
	up, down, trafficErr := p.Traffic(ctx, owned.PanelKey, owned.VPNID, owned.SubID)
	if err != nil || foreignErr != nil || before == nil || foreignBefore == nil || trafficErr != nil || up+down == 0 || before.LimitIP != 1 {
		if before != nil {
			t.Logf("baseline read failures owned=%v foreign=%v traffic=%v; limitIP=%d up=%d down=%d", err != nil, foreignErr != nil, trafficErr != nil, before.LimitIP, up, down)
		}
		t.Fatal("real provider baseline was not confirmed")
	}
	baselineIDs := slices.Clone(before.InboundIDs)
	slices.Sort(baselineIDs)
	if !slices.Equal(baselineIDs, []int64{originalID, unknown}) {
		t.Fatal("fixture mutation removed memberships before group attach/detach")
	}
	if _, err := f.env.Pool.Exec(ctx, `UPDATE accounts SET assigned_panel_id=$2,access_profile='regular',had_subscription=true WHERE id=$1`, account, owned.PanelID); err != nil {
		t.Fatal("owned assignment unavailable")
	}
	start()
	var operation uuid.UUID
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT id FROM access_operations WHERE account_id=$1 AND kind='group_reconcile' AND status='applied'`, account).Scan(&operation) == nil
	})
	priorKey := ""
	assertPreserved := func() {
		t.Helper()
		after, err := p.GetClient(ctx, owned.PanelKey)
		other, otherErr := p.GetClient(ctx, foreign.PanelKey)
		u, d, trafficErr := p.Traffic(ctx, owned.PanelKey, owned.VPNID, owned.SubID)
		if err != nil || otherErr != nil || after == nil || after.VPNID != before.VPNID || after.SubID != before.SubID || after.PanelKey != before.PanelKey || after.ExpiryTimeMS != before.ExpiryTimeMS || after.TrafficLimitBytes != before.TrafficLimitBytes || after.LimitIP != before.LimitIP || after.Enabled != before.Enabled || trafficErr != nil || u != up || d != down {
			t.Fatal("real group executor changed identity, access, disable or traffic")
		}
		got, want := slices.Clone(after.InboundIDs), []int64{unknown, newID}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) || !reflect.DeepEqual(other, foreignBefore) {
			t.Fatal("real group executor lost unknown membership or changed the foreign client")
		}
		status, raw, _ := f.send(t, client, "GET", "/api/v1/subscription", nil, "", "", false)
		var subscription wire.Subscription
		if status != 200 || json.Unmarshal(raw, &subscription) != nil || subscription.Status != "active" || subscription.DataStale {
			t.Fatal("real confirmed group state unavailable through HTTP", status, subscription.Status)
		}
		status, raw, response := f.send(t, client, "GET", "/api/v1/subscription/key", nil, "", "", false)
		var key wire.SubscriptionKey
		if status != 200 || json.Unmarshal(raw, &key) != nil || response.Header.Get("Cache-Control") != "no-store" || key.SubscriptionUrl != f.cfg.Subscriptions.SubscriptionBaseURL+owned.SubID || priorKey != "" && priorKey != key.SubscriptionUrl {
			t.Fatal("real group confirmation changed the subscription URL or cache guard")
		}
		priorKey = key.SubscriptionUrl
	}
	assertPreserved()
	var target string
	if f.env.Pool.QueryRow(ctx, `SELECT target::text FROM access_operations WHERE id=$1`, operation).Scan(&target) != nil {
		t.Fatal("real group target absent")
	}
	stop()
	start()
	var count int
	var repeatedTarget string
	if f.env.Pool.QueryRow(ctx, `SELECT count(*),min(target::text) FROM access_operations WHERE account_id=$1`, account).Scan(&count, &repeatedTarget) != nil || count != 1 || repeatedTarget != target {
		t.Fatal("restart repeated real group operation or changed its frozen target")
	}
	assertPreserved()
	t.Log("PASS: freshly compiled HTTP/River with real isolated TLS 3X-UI3.7.0 attach/detach, disabled known and preserved unknown memberships, stable UUID/subID/expiry/quota/limitIP=1/nonzero seeded traffic, untouched foreign client and no-op restart; Bot API and SMTP are fixtures, no VPN data-plane traffic generated")
}
