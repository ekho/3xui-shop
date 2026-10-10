package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func TestNativeGroupReconciliation(t *testing.T) {
	f := openMode(t, true)
	ctx := context.Background()
	bot := &nativeBot{}
	start, stop := nativeNoticeBinary(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response, err := bot.RoundTrip(r)
		if err != nil {
			http.Error(w, "owned Bot API unavailable", 503)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	}), map[string]string{"TRIAL_ENABLED": "true"})
	pid := start()
	type actor struct {
		id   uuid.UUID
		chat int64
	}
	actors := []actor{}
	for i, locale := range []string{"en", "ru", "en", "en"} {
		_, _, id := f.signupAccount(t, nativeEmail("group-operator"))
		if f.svc.Accounts.ChangeOperatorRole(ctx, id, true) != nil {
			t.Fatal("owned operator role unavailable")
		}
		if i != 2 && f.svc.Accounts.ChangeInfrastructureRole(ctx, id, true) != nil {
			t.Fatal("owned infrastructure role unavailable")
		}
		chat := int64(701 + i)
		if _, err := f.env.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=$2,locale=$3 WHERE id=$1`, id, chat, locale); err != nil {
			t.Fatal(err)
		}
		actors = append(actors, actor{id, chat})
	}
	client, _, trial := f.signup(t, nativeEmail("group-client"))
	card := cardFor(t, bot, 101, trial.RequestId)
	bot.callback(101, card.ID, "a", trial.RequestId, "")
	wait(t, func() bool { return trialStatus(f, trial.RequestId) == "approved" })
	trialID := operationFor(t, f, trial.RequestId)
	wait(t, func() bool { return applied(f, trialID) })
	var account uuid.UUID
	if f.env.Pool.QueryRow(ctx, `SELECT account_id FROM trial_operations WHERE id=$1`, trialID).Scan(&account) != nil {
		t.Fatal("owned grant account missing")
	}
	identity, err := f.svc.Accounts.Lookup(ctx, account)
	if err != nil || identity.AssignedPanelID == nil {
		t.Fatal("owned concrete assignment missing", err)
	}
	key := func() string {
		t.Helper()
		status, raw, response := f.send(t, client, "GET", "/api/v1/subscription/key", nil, "", "", false)
		var out wire.SubscriptionKey
		if status != 200 || json.Unmarshal(raw, &out) != nil || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("compiled subscription key unavailable", status)
		}
		return out.SubscriptionUrl
	}
	beforeKey := key()
	snapshot := func() [32]byte {
		t.Helper()
		var raw string
		if f.env.Pool.QueryRow(ctx, `SELECT jsonb_build_array(a.vpn_id,a.sub_id,a.panel_key,a.assigned_panel_id,
		 (SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM trial_operations o WHERE o.account_id=a.id),
		 (SELECT jsonb_agg(to_jsonb(g) ORDER BY account_id) FROM trial_grants g WHERE g.account_id=a.id),
		 (SELECT count(*) FROM purchase_receipts),(SELECT count(*) FROM vpn_server_reservations))::text
		 FROM accounts a WHERE a.id=$1`, account).Scan(&raw) != nil {
			t.Fatal("owned frozen grant snapshot unavailable")
		}
		return sha256.Sum256([]byte(raw))
	}
	before := snapshot()
	stop()
	// The provider changes, while the persisted grant/identity remains untouched.
	f.panel.mu.Lock()
	f.panel.inbounds = []map[string]any{{"id": 1, "enable": false, "tag": "euru-old"}, {"id": 4, "enable": true, "tag": "regular-new"}, {"id": 91, "enable": true, "tag": "foreign"}}
	f.panel.memberships = map[string][]int64{identity.PanelKey: {1, 91}}
	f.panel.clients[identity.PanelKey]["limitIp"] = int64(1)
	f.panel.clients[identity.PanelKey]["totalGB"] = int64(1234)
	f.panel.usedTraffic = 42
	providerBefore, _ := json.Marshal(f.panel.clients[identity.PanelKey])
	adds := f.panel.adds
	f.panel.offline = true
	f.panel.mu.Unlock()
	tx, err := f.env.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := f.svc.Accounts.ReminderRecipientTx(ctx, tx, actors[3].id, false)
	if err != nil || f.svc.Notifications.EnqueueGroupAlertTx(ctx, tx, recipient, account, "identity_mismatch", f.env.Clock()) != nil || tx.Commit(ctx) != nil {
		t.Fatal("owned queued infrastructure alert unavailable", err)
	}
	if f.svc.Accounts.ChangeInfrastructureRole(ctx, actors[3].id, false) != nil {
		t.Fatal("owned infrastructure revocation failed")
	}
	if next := start(); next == pid || next <= 0 {
		t.Fatal("fresh compiled process did not restart")
	}
	alertCount := func() int {
		bot.mu.Lock()
		defer bot.mu.Unlock()
		n := 0
		for _, message := range bot.messages {
			if strings.HasPrefix(message.Text, "VPN reconciliation\n") || strings.HasPrefix(message.Text, "Сверка VPN\n") {
				n++
			}
		}
		return n
	}
	wait(t, func() bool {
		var skipped int
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM client_telegram_deliveries WHERE account_id=$1 AND vpn_alert_code='identity_mismatch' AND state='skipped'`, actors[3].id).Scan(&skipped) == nil && skipped == 1 && alertCount() == 2
	})
	bot.mu.Lock()
	messages := append([]nativeMessage{}, bot.messages...)
	bot.mu.Unlock()
	for _, message := range messages {
		if message.Chat == actors[0].chat && strings.HasPrefix(message.Text, "VPN reconciliation\n") {
			if !strings.Contains(message.Text, "panel state") || strings.Contains(message.Text, identity.SubID) || strings.Contains(message.Text, identity.PanelKey) {
				t.Fatal("English alert lost its safe failure class")
			}
		} else if message.Chat == actors[1].chat && strings.HasPrefix(message.Text, "Сверка VPN\n") {
			if !strings.Contains(message.Text, "панели") {
				t.Fatal("Russian alert missing")
			}
		} else if message.Chat == actors[2].chat || message.Chat == actors[3].chat {
			t.Fatal("operator without current infrastructure grant received an alert")
		}
	}
	if snapshot() != before {
		t.Fatal("offline reconciliation changed a frozen grant or identity")
	}
	if status, _, _ := f.send(t, client, "GET", "/healthz", nil, "", "", false); status != 200 {
		t.Fatal("panel outage stopped the Go process")
	}
	stop()
	f.panel.mu.Lock()
	f.panel.offline = false
	f.panel.mu.Unlock()
	start()
	var groupID uuid.UUID
	wait(t, func() bool {
		return f.env.Pool.QueryRow(ctx, `SELECT id FROM access_operations WHERE account_id=$1 AND kind='group_reconcile' AND status='applied'`, account).Scan(&groupID) == nil
	})
	var target string
	if f.env.Pool.QueryRow(ctx, `SELECT target::text FROM access_operations WHERE id=$1`, groupID).Scan(&target) != nil {
		t.Fatal("compiled worker target unavailable")
	}
	if key() != beforeKey || snapshot() != before {
		t.Fatal("compiled reconciliation changed server, key, original target or money")
	}
	f.panel.mu.Lock()
	providerAfter, _ := json.Marshal(f.panel.clients[identity.PanelKey])
	preserved := bytes.Equal(providerBefore, providerAfter) && f.panel.usedTraffic == 42 && f.panel.adds == adds && f.panel.forbidden == 0 && reflect.DeepEqual(f.panel.memberships[identity.PanelKey], []int64{91, 4}) && reflect.DeepEqual(f.panel.groupWrites, []string{"attach", "detach"})
	f.panel.mu.Unlock()
	if !preserved {
		t.Fatal("compiled worker changed access/traffic or touched unknown memberships")
	}
	stop()
	start()
	var count int
	var afterTarget string
	if f.env.Pool.QueryRow(ctx, `SELECT count(*),min(target::text) FROM access_operations WHERE account_id=$1 AND kind='group_reconcile'`, account).Scan(&count, &afterTarget) != nil || count != 1 || target != afterTarget || key() != beforeKey || snapshot() != before || alertCount() != 2 {
		t.Fatal("restart duplicated reconciliation or alerts, or rewrote its target")
	}
	stop()
	if _, err := f.env.Pool.Exec(ctx, `UPDATE accounts SET vpn_banned=true WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	f.panel.mu.Lock()
	f.panel.inbounds[1]["enable"] = false
	f.panel.mu.Unlock()
	start()
	wait(t, func() bool {
		var n int
		return f.env.Pool.QueryRow(ctx, `SELECT count(*) FROM access_operations WHERE account_id=$1 AND kind='group_reconcile' AND status='applied'`, account).Scan(&n) == nil && n == 2
	})
	if status, _, _ := f.send(t, client, "GET", "/api/v1/subscription/key", nil, "", "", false); status != 403 || snapshot() != before {
		t.Fatal("compiled VPN-ban gate lost its frozen identity/grant")
	}
	status, raw, _ := f.send(t, client, "GET", "/api/v1/subscription", nil, "", "", false)
	var banned wire.Subscription
	if status != 200 || json.Unmarshal(raw, &banned) != nil || banned.Status != "banned" || banned.DataStale {
		t.Fatal("compiled ban-only readback lost its confirmed status", status, banned.Status)
	}
	f.panel.mu.Lock()
	provider, _ := f.panel.clients[identity.PanelKey]["enable"].(bool)
	preserved = !provider && f.panel.usedTraffic == 42 && f.panel.adds == adds && reflect.DeepEqual(f.panel.memberships[identity.PanelKey], []int64{91, 4}) && reflect.DeepEqual(f.panel.groupWrites, []string{"attach", "detach", "disable"})
	f.panel.mu.Unlock()
	if !preserved {
		t.Fatal("compiled ban changed membership or repeated another panel effect")
	}
	t.Log("PASS: fresh compiled HTTP/River/Telegram, owned TLS panel outage/recovery, ru/en alerts and revoked grant, new/disabled inbound, unknown membership, limitIP=1, traffic/grant/key/target preservation and restart without duplication; Bot API and panel are synthetic")
}
