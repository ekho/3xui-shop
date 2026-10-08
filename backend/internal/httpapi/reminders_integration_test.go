package httpapi

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type reminderPanel struct {
	mu                     sync.Mutex
	target                 vpn.ProvisionTarget
	used                   int64
	reads, writes          int
	missing, drift, outage bool
}

func reminderFixture(t *testing.T, maxConns int32) (http.Handler, *compositionFixture, *testkit.Env, app.Config, *testkit.SMTP, *reminderPanel, supportSession) {
	t.Helper()
	old, e, cfg, smtp := mailFixture(t, maxConns)
	cfg.Accounts.Now = e.Clock
	cfg.Subscriptions.PanelID = "owned-reminder-panel"
	panel := &reminderPanel{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panel.mu.Lock()
		defer panel.mu.Unlock()
		if r.Method != "GET" {
			panel.writes++
			w.WriteHeader(405)
			return
		}
		panel.reads++
		if panel.outage {
			w.WriteHeader(503)
			return
		}
		var obj any
		switch r.URL.Path {
		case "/panel/api/inbounds/list":
			obj = []map[string]any{{"id": 1, "tag": "regular", "enable": true}}
		case "/panel/api/clients/list":
			target := panel.target
			row := map[string]any{"id": 7, "email": target.PanelKey, "uuid": target.VPNID, "subId": target.SubID, "enable": true, "expiryTime": target.ExpiryTimeMS, "limitIp": target.DeviceCount + 1, "totalGB": target.TrafficLimitBytes, "inboundIds": []int64{1}}
			if !panel.missing {
				row["traffic"] = map[string]any{"email": target.PanelKey, "up": 0, "down": panel.used}
			}
			if panel.drift {
				row["expiryTime"] = target.ExpiryTimeMS + 1
			}
			obj = []any{row, map[string]any{"id": 8, "email": "foreign-unattached", "uuid": "", "subId": "", "enable": true, "expiryTime": 0, "limitIp": 0, "totalGB": 0, "inboundIds": nil}}
		default:
			panel.writes++
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": obj})
	}))
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	cfg.VPN.Panel = vpn.Config{PanelURL: server.URL, PanelToken: "fixture", PanelRootCAs: roots}
	queue, err := river.NewClient(riverpgxv5.New(old.pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s := composeForTest(old.pool, e.Redis, queue, cfg)
	h := New(s.Modules, old.pool, cfg.HTTP)
	client := supportLogin(t, h, e, cfg, "reminder-owned@example.test")
	ctx := context.Background()
	target := vpn.ProvisionTarget{OperationID: uuid.New(), PanelID: cfg.Subscriptions.PanelID, Profile: "regular", InboundIDs: []int64{1}, DeviceCount: 1, ExpiryTimeMS: e.Clock().Add(72 * time.Hour).UnixMilli(), TrafficLimitBytes: 100}
	if err = e.Pool.QueryRow(ctx, `SELECT panel_key,vpn_id,sub_id FROM accounts WHERE id=$1`, client.id).Scan(&target.PanelKey, &target.VPNID, &target.SubID); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.New()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE accounts SET assigned_panel_id=$2,access_profile='regular',had_subscription=true,telegram_id=703 WHERE id=$1`, []any{client.id, cfg.Subscriptions.PanelID}},
		{`INSERT INTO trial_requests(id,account_id,status,comment,created_at,decided_at,operator_tg_id,operation_id) VALUES($1,$2,'approved','fixture',$3,$3,101,$4)`, []any{requestID, client.id, e.Clock(), target.OperationID}},
		{`INSERT INTO trial_operations(id,account_id,request_id,status,trial_enabled,period_days,traffic_gb,devices,panel_id,created_at,target) VALUES($1,$2,$3,'applied',true,3,15,1,$4,$5,$6)`, []any{target.OperationID, client.id, requestID, cfg.Subscriptions.PanelID, e.Clock(), mustJSON(t, target)}},
		{`INSERT INTO trial_grants(account_id,request_id,operation_id,status,created_at,granted_at) VALUES($1,$2,$3,'granted',$4,$4)`, []any{client.id, requestID, target.OperationID, e.Clock()}},
	} {
		if _, err = tx.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	panel.target = target
	panel.used = 80
	panel.mu.Unlock()
	return h, s, e, cfg, smtp, panel, client
}

func reminderBusinessSnapshot(t *testing.T, e *testkit.Env) string {
	t.Helper()
	var value string
	if err := e.Pool.QueryRow(context.Background(), `SELECT md5(jsonb_build_array((SELECT jsonb_agg(x ORDER BY id) FROM accounts x),(SELECT jsonb_agg(x ORDER BY id) FROM trial_operations x),(SELECT jsonb_agg(x ORDER BY id) FROM access_operations x),(SELECT jsonb_agg(x ORDER BY id) FROM purchase_orders x),(SELECT jsonb_agg(x ORDER BY account_id) FROM trial_grants x))::text)`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func readReminderHTTP(t *testing.T, h http.Handler, client supportSession) notifications.ReminderResult {
	t.Helper()
	r := supportRequest(h, &client, "GET", "/api/v1/reminders", "", nil, "", uuid.Nil)
	var out notifications.ReminderResult
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil {
		t.Fatal("own reminder result", r.Code)
	}
	contract, err := wire.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if json.Unmarshal(r.Body.Bytes(), &value) != nil || contract.Components.Schemas["ReminderResult"].Value.VisitJSON(value) != nil {
		t.Fatal("reminder public contract")
	}
	return out
}

func reminderAppliedTarget(t *testing.T, e *testkit.Env, panel *reminderPanel, account uuid.UUID, reset bool, expiry int64, devices int64, used int64) uuid.UUID {
	t.Helper()
	e.Advance(time.Second)
	panel.mu.Lock()
	defer panel.mu.Unlock()
	p := panel.target
	id := uuid.New()
	target := vpn.AccessTarget{OperationID: id, PanelID: p.PanelID, PanelKey: p.PanelKey, VPNID: p.VPNID, SubID: p.SubID, Profile: p.Profile, InboundIDs: p.InboundIDs, DeviceCount: devices, ExpiryTimeMS: expiry, TrafficLimitBytes: p.TrafficLimitBytes, Reset: reset, Enable: true}
	if _, err := e.Pool.Exec(context.Background(), `INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at) VALUES($1,$2,'reset_traffic','applied','fixture','{}',$3,$4,$4)`, id, account, mustJSON(t, target), e.Clock()); err != nil {
		t.Fatal(err)
	}
	panel.target.OperationID = id
	panel.target.ExpiryTimeMS = expiry
	panel.target.DeviceCount = devices
	panel.used = used
	return id
}

// Characterization of the assembled owners: replacing the period with the latest
// metadata operation or forgetting durable dedup breaks restart/reset behavior.
func TestReminderPeriodsAndDelivery(t *testing.T) {
	h, s, e, cfg, smtp, panel, client := reminderFixture(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	before := reminderBusinessSnapshot(t, e)
	for range 2 {
		if err := s.Reminders.Generate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if reminderBusinessSnapshot(t, e) != before {
		t.Fatal("reminder generation changed business facts")
	}
	out := readReminderHTTP(t, h, client)
	if out.EmailEnabled || len(out.Reminders) != 2 {
		t.Fatal("cabinet default-false reminders")
	}
	var count, mails int
	if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM reminders),(SELECT count(*) FROM mail_deliveries WHERE kind='reminder')`).Scan(&count, &mails); err != nil || count != 2 || mails != 0 {
		t.Fatal("duplicate or unsolicited email", count, mails, err)
	}
	for _, r := range out.Reminders {
		if r.Route != "cabinet" || !r.ObservedAt.Equal(e.Clock()) {
			t.Fatal("undated/unsafe route")
		}
		if r.Kind == "traffic" && (r.TrafficUsedBytes == nil || *r.TrafficUsedBytes != "80" || *r.TrafficLimitBytes != "100") {
			t.Fatal("traffic precision")
		}
	}
	other := supportLogin(t, h, e, cfg, "reminder-foreign@example.test")
	path := "/api/v1/reminders/" + out.Reminders[0].ID.String() + "/dismiss"
	if r := supportRequest(h, &other, "POST", path, "", nil, cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 404 {
		t.Fatal("foreign dismissal", r.Code)
	}
	before = reminderBusinessSnapshot(t, e)
	if _, err := s.Reminders.SetEmailPreference(ctx, client.id, true); err != nil {
		t.Fatal(err)
	}
	// A fresh assembly uses the same database/outboxes, with no in-memory dedup.
	queue, err := river.NewClient(riverpgxv5.New(s.pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	restarted := app.NewModules(s.pool, e.Redis, queue, &cfg)
	if err = restarted.Reminders.Generate(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := e.Pool.Query(ctx, `SELECT id FROM mail_deliveries WHERE kind='reminder' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) != nil {
			t.Fatal("mail id")
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 2 {
		t.Fatal("opt-in existing event")
	}
	for _, id := range ids {
		if err = restarted.MailDelivery.SendMail(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if len(smtp.Letters()) != 2 {
		t.Fatal("actual TLS mail missing")
	}
	for _, letter := range smtp.Letters() {
		if !strings.Contains(letter, "https://cabinet.example.test/cabinet?lang=ru") || !strings.Contains(letter, "2026-10-01T00:00:00Z") {
			t.Fatal("dated route mail")
		}
	}
	for range 2 {
		job, err := restarted.Notifications.ClaimClient(ctx)
		if err != nil || job == nil || job.ReminderID == uuid.Nil || job.Route != "cabinet" || !strings.Contains(job.ReminderText, "2026-10-01T00:00:00Z") {
			t.Fatal("dated Telegram claim", err)
		}
		if err = restarted.Notifications.DeliverClient(ctx, *job, func() (notifications.ClientOutcome, error) {
			return notifications.ClientOutcome{State: "sent", MessageID: 42}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if reminderBusinessSnapshot(t, e) != before {
		t.Fatal("reminder delivery changed business facts")
	}
	e.Advance(25 * time.Hour)
	panel.mu.Lock()
	panel.used = 100
	panel.mu.Unlock()
	if err = restarted.Reminders.Generate(ctx); err != nil {
		t.Fatal(err)
	}
	out = readReminderHTTP(t, h, client)
	if len(out.Reminders) != 2 {
		t.Fatal("highest-only result", len(out.Reminders))
	}
	for _, r := range out.Reminders {
		if r.Kind == "expiry" && r.Threshold != 1 || r.Kind == "traffic" && r.Threshold != 100 {
			t.Fatal("urgency fell through")
		}
		for range 2 {
			if err = restarted.Reminders.Dismiss(ctx, client.id, r.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = restarted.Reminders.Generate(ctx); err != nil || len(readReminderHTTP(t, h, client).Reminders) != 0 {
		t.Fatal("dismissed urgency resurrected", err)
	}
	reset := reminderAppliedTarget(t, e, panel, client.id, true, 0, 1, 80)
	if err = restarted.Reminders.Generate(ctx); err != nil {
		t.Fatal(err)
	}
	out = readReminderHTTP(t, h, client)
	if len(out.Reminders) != 1 || out.Reminders[0].Kind != "traffic" || out.Reminders[0].Threshold != 80 {
		t.Fatal("expiry0 reset did not start traffic cycle")
	}
	reminderAppliedTarget(t, e, panel, client.id, false, 0, 2, 80)
	if err = restarted.Reminders.Generate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM reminders WHERE kind='traffic' AND period=$1`, reset.String()).Scan(&count); err != nil || count != 1 {
		t.Fatal("device-only cycle reset", count, err)
	}
	reminderAppliedTarget(t, e, panel, client.id, true, e.Clock().Add(72*time.Hour).UnixMilli(), 2, 0)
	if err = restarted.Reminders.Generate(ctx); err != nil {
		t.Fatal(err)
	}
	out = readReminderHTTP(t, h, client)
	if len(out.Reminders) != 1 || out.Reminders[0].Kind != "expiry" {
		t.Fatal("new expiry or reset cycle not filtered")
	}
	panel.mu.Lock()
	defer panel.mu.Unlock()
	if panel.writes != 0 || panel.reads != 16 {
		t.Fatal("two bulk read-only requests per pass", panel.reads, panel.writes)
	}
}

func TestReminderConsentAndRecipientGuards(t *testing.T) {
	for _, mode := range []string{"optout", "dismiss", "credential", "email", "restricted", "period", "unlink"} {
		t.Run(mode, func(t *testing.T) {
			h, s, e, _, smtp, panel, client := reminderFixture(t, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := s.Reminders.SetEmailPreference(ctx, client.id, true); err != nil {
				t.Fatal(err)
			}
			if err := s.Reminders.Generate(ctx); err != nil {
				t.Fatal(err)
			}
			out := readReminderHTTP(t, h, client)
			switch mode {
			case "optout":
				if _, err := s.Reminders.SetEmailPreference(ctx, client.id, false); err != nil {
					t.Fatal(err)
				}
			case "dismiss":
				for _, r := range out.Reminders {
					if err := s.Reminders.Dismiss(ctx, client.id, r.ID); err != nil {
						t.Fatal(err)
					}
				}
			case "credential":
				if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET credential_version=credential_version+1 WHERE id=$1`, client.id); err != nil {
					t.Fatal(err)
				}
			case "email":
				if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET email_key='new-owned@example.test',credential_version=credential_version+1 WHERE id=$1`, client.id); err != nil {
					t.Fatal(err)
				}
			case "restricted":
				if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, client.id); err != nil {
					t.Fatal(err)
				}
			case "period":
				reminderAppliedTarget(t, e, panel, client.id, true, e.Clock().Add(10*24*time.Hour).UnixMilli(), 1, 0)
			case "unlink":
				if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=NULL,credential_version=credential_version+1 WHERE id=$1`, client.id); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := e.Pool.Query(ctx, `SELECT id FROM mail_deliveries WHERE kind='reminder'`)
			if err != nil {
				t.Fatal(err)
			}
			var ids []uuid.UUID
			for rows.Next() {
				var id uuid.UUID
				if rows.Scan(&id) != nil {
					t.Fatal("mail id")
				}
				ids = append(ids, id)
			}
			rows.Close()
			for _, id := range ids {
				if err = s.MailDelivery.SendMail(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			if len(smtp.Letters()) != 0 {
				t.Fatal("stale/no-consent SMTP send")
			}
			sends := 0
			for range 2 {
				j, err := s.Notifications.ClaimClient(ctx)
				if err != nil || j == nil {
					t.Fatal("notice claim", err)
				}
				if err = s.Notifications.DeliverClient(ctx, *j, func() (notifications.ClientOutcome, error) {
					sends++
					return notifications.ClientOutcome{State: "sent", MessageID: 42}, nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			want := 0
			if mode == "optout" {
				want = 2
			}
			if sends != want {
				t.Fatal("Telegram independent consent/current recipient", sends, want)
			}
			if mode == "optout" {
				if err = s.Reminders.Generate(ctx); err != nil {
					t.Fatal(err)
				}
				var count int
				if e.Pool.QueryRow(ctx, `SELECT count(*) FROM mail_deliveries WHERE kind='reminder'`).Scan(&count) != nil || count != 2 {
					t.Fatal("old recipient requeued")
				}
			}
		})
	}
}

func TestReminderSMTPGuardAndUnlockedRows(t *testing.T) {
	_, s, e, _, smtp, _, client := reminderFixture(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.Reminders.SetEmailPreference(ctx, client.id, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Reminders.Generate(ctx); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if e.Pool.QueryRow(ctx, `SELECT id FROM mail_deliveries WHERE kind='reminder' LIMIT 1`).Scan(&id) != nil {
		t.Fatal("mail id")
	}
	entered, release := smtp.HoldNextData()
	defer release()
	sent := make(chan error, 1)
	go func() { sent <- s.MailDelivery.SendMail(ctx, id) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("SMTP barrier")
	}
	probe, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SELECT id FROM accounts WHERE id=$1 FOR UPDATE NOWAIT`, `SELECT id FROM reminders WHERE account_id=$1 FOR UPDATE NOWAIT`, `SELECT id FROM mail_deliveries WHERE reminder_id IN(SELECT id FROM reminders WHERE account_id=$1) FOR UPDATE NOWAIT`} {
		rows, err := probe.Query(ctx, q, client.id)
		if err != nil {
			t.Fatal("row lock held during SMTP", err)
		}
		rows.Close()
	}
	probe.Rollback(ctx)
	done := make(chan error, 1)
	go func() { _, err := s.Reminders.SetEmailPreference(ctx, client.id, false); done <- err }()
	select {
	case err := <-done:
		t.Fatal("optout bypassed active email guard", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	if err = <-sent; err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if len(smtp.Letters()) != 1 {
		t.Fatal("in-flight delivery not linearized")
	}
}

func TestReminderProviderUnknownAndNoWrites(t *testing.T) {
	for _, mode := range []string{"missing", "drift", "outage"} {
		t.Run(mode, func(t *testing.T) {
			_, s, e, _, _, panel, _ := reminderFixture(t, 1)
			before := reminderBusinessSnapshot(t, e)
			panel.mu.Lock()
			panel.missing = mode == "missing"
			panel.drift = mode == "drift"
			panel.outage = mode == "outage"
			panel.mu.Unlock()
			err := s.Reminders.Generate(context.Background())
			if (err != nil) != (mode == "outage") {
				t.Fatal("provider unknown result", err)
			}
			var count int
			if e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM reminders`).Scan(&count) != nil || count != 0 || reminderBusinessSnapshot(t, e) != before {
				t.Fatal("unknown fabricated facts or changed business")
			}
			panel.mu.Lock()
			defer panel.mu.Unlock()
			if panel.writes != 0 {
				t.Fatal("provider write")
			}
		})
	}
}

func TestReminderSchedulerKeepsHTTP(t *testing.T) {
	h, s, _, _, _, panel, _ := reminderFixture(t, 1)
	panel.mu.Lock()
	panel.outage = true
	panel.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Reminders.RunScheduler(ctx); err != nil {
		t.Fatal("dependency failure killed loop", err)
	}
	if r := request(h, "GET", "/healthz", "", ""); r.Code != 200 {
		t.Fatal("dependency failure killed HTTP", r.Code)
	}
	panel.mu.Lock()
	defer panel.mu.Unlock()
	if panel.reads != 1 || panel.writes != 0 {
		t.Fatal("failed pass hot loop", panel.reads, panel.writes)
	}
}

func TestReminderMiniAppAuthority(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	body, _ := json.Marshal(map[string]string{"init_data": testkit.SignedMiniAppData(key, 123, e.Clock(), `{"id":744,"first_name":"Reminder","language_code":"en"}`, ""), "accepted_terms_version": "1", "accepted_privacy_version": "1"})
	r := request(h, "POST", "/api/v1/telegram/mini-app/session", string(body), cfg.HTTP.CabinetOrigin)
	var auth struct {
		SessionToken string `json:"session_token"`
		CsrfToken    string `json:"csrf_token"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &auth) != nil {
		t.Fatal("signed login", r.Code)
	}
	for _, tc := range []struct {
		method, path, body, csrf string
		want                     int
	}{
		{"GET", "/api/v1/reminders", "", auth.CsrfToken, 200},
		{"POST", "/api/v1/reminders/preferences", `{"email_enabled":false}`, auth.CsrfToken, 200},
		{"POST", "/api/v1/reminders/preferences", `{"email_enabled":true}`, auth.CsrfToken, 409},
		{"POST", "/api/v1/reminders/preferences", `{"email_enabled":false}`, "", 403},
		{"POST", "/api/v1/reminders/" + uuid.NewString() + "/dismiss", "", auth.CsrfToken, 404},
	} {
		r = miniAppRequest(h, tc.method, tc.path, tc.body, cfg.HTTP.CabinetOrigin, auth.SessionToken, tc.csrf, "")
		if r.Code != tc.want {
			t.Fatal("Mini App permission", tc.path, r.Code, tc.want)
		}
	}
	for _, path := range []string{"/api/v1/reminders", "/api/v1/reminders/preferences", "/api/v1/reminders/:id/dismiss"} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			want := path == "/api/v1/reminders" && method == "GET" || path != "/api/v1/reminders" && method == "POST"
			if miniAppRouteAllowed(path, method) != want {
				t.Fatal("implicit bearer method grant", path, method)
			}
		}
	}
}

// The public payment owner must prove the paid source, not just elapsed time.
func TestStarsReminderCurrentPolicy(t *testing.T) {
	_, s, e, auth, m, order := starsSubscriptionFixture(t)
	ctx := context.Background()
	read := func() notifications.ReminderStars {
		t.Helper()
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		out, err := m.Payments.ReminderPolicyTx(ctx, tx, auth.Account.AccountId)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := read()
	if !out.Known || !out.SuppressExpiry || out.Lapsed || out.PaidUntil == nil {
		t.Fatal("canonical funded active policy")
	}
	until := *out.PaidUntil
	s.now = func() time.Time { return until.Add(24 * time.Hour) }
	if out = read(); !out.Known || !out.SuppressExpiry || out.Lapsed {
		t.Fatal("exact grace boundary")
	}
	s.now = func() time.Time { return until.Add(24*time.Hour + time.Nanosecond) }
	if out = read(); !out.Known || out.SuppressExpiry || !out.Lapsed {
		t.Fatal("dated lapse after grace")
	}
	for _, q := range []string{`UPDATE purchase_orders SET review_required=true WHERE id=$1`} {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, q, order.OrderId); err != nil {
			t.Fatal(err)
		}
		out, err = m.Payments.ReminderPolicyTx(ctx, tx, auth.Account.AccountId)
		if err != nil || out.Known {
			t.Fatal("unsafe current funding/source policy", err)
		}
		tx.Rollback(ctx)
	}
	for _, q := range []string{`UPDATE accounts SET legacy_user_id=744 WHERE id=$1`, `UPDATE accounts SET access_profile='euru' WHERE id=$1`, `UPDATE accounts SET telegram_id=745 WHERE id=$1`, `UPDATE accounts SET restricted=true WHERE id=$1`} {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, q, auth.Account.AccountId); err != nil {
			t.Fatal(err)
		}
		out, err = m.Payments.ReminderPolicyTx(ctx, tx, auth.Account.AccountId)
		if err != nil || out.Known {
			t.Fatal("unproved identity/current source", err)
		}
		tx.Rollback(ctx)
	}
	for _, q := range []string{`UPDATE stars_subscriptions SET provider_state='unknown' WHERE root_order_id=$1`, `UPDATE stars_subscriptions SET control_state='uncertain' WHERE root_order_id=$1`, `UPDATE stars_subscriptions SET control_state='rejected' WHERE root_order_id=$1`} {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, q, order.OrderId); err != nil {
			t.Fatal(err)
		}
		out, err = m.Payments.ReminderPolicyTx(ctx, tx, auth.Account.AccountId)
		if err != nil || out.Known {
			t.Fatal("unproved native state", err)
		}
		tx.Rollback(ctx)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var raw []byte
	var target vpn.AccessTarget
	if tx.QueryRow(ctx, `SELECT target FROM access_operations WHERE id=$1`, *order.AccessOperationId).Scan(&raw) != nil || json.Unmarshal(raw, &target) != nil {
		t.Fatal("owned current source")
	}
	target.OperationID = uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at) VALUES($1,$2,'starter_trial','applied','fixture','{}',$3,$4,$4)`, target.OperationID, auth.Account.AccountId, mustJSON(t, target), e.Clock().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if out, err = m.Payments.ReminderPolicyTx(ctx, tx, auth.Account.AccountId); err != nil || out.Known {
		t.Fatal("changed current access source", err)
	}
	tx.Rollback(ctx)
	if err := m.Payments.RecordStarsRefund(ctx, starsPayment(order.OrderId, e.Clock())); err != nil {
		t.Fatal("actual negative funding proof", err)
	}
	if read().Known {
		t.Fatal("refunded canonical funding became safe")
	}
}

func TestStarsReminderMultipleContracts(t *testing.T) {
	_, _, e, auth, m, order := starsSubscriptionFixture(t)
	ctx := context.Background()
	extra := starsPayment(order.OrderId, e.Clock())
	extra.Recurring = true
	extra.FirstRecurring = true
	extra.SubscriptionExpiresAt = extra.At.Add(30 * 24 * time.Hour).Unix()
	extra.ChargeID = "owned-reminder-extra-first-charge"
	if err := m.Payments.RecordStarsPayment(ctx, extra); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	out, err := m.Payments.ReminderPolicyTx(ctx, tx, auth.Account.AccountId)
	if err != nil || out.Known {
		t.Fatal("multiple native chains appeared safe", err)
	}
}
