package platform

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type fakePanel struct {
	blockRead                                                                        bool
	afterAdd                                                                         func()
	failAfterAdd                                                                     bool
	dropAdd                                                                          bool
	mu                                                                               sync.Mutex
	server                                                                           *httptest.Server
	client                                                                           map[string]any
	ids                                                                              []int64
	adds, attaches, otherWrites                                                      int
	updates, resets, disables, detaches                                              int
	up, down                                                                         int64
	loseReset                                                                        bool
	resetLeavesTraffic                                                               bool
	strictNativeUpdate                                                               bool
	refuseActivation                                                                 bool
	loseAdd, emptyRead, failRead, partial, noRegular, noEuru, sharedRegularUnlimited bool
}

func panelFixture(t *testing.T, s *Service) *fakePanel {
	t.Helper()
	p := &fakePanel{up: 1234}
	p.server = httptest.NewTLSServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	s.cfg.PanelURL = p.server.URL
	s.cfg.PanelToken = "fixture-panel-token"
	s.cfg.SubscriptionBaseURL = "https://subscriptions.example.test/sub/"
	s.cfg.PanelRootCAs = x509.NewCertPool()
	s.cfg.PanelRootCAs.AddCert(p.server.Certificate())
	return p
}
func (p *fakePanel) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	if r.Header.Get("Authorization") != "Bearer fixture-panel-token" {
		w.WriteHeader(401)
		reply(map[string]any{"success": false, "msg": "unauthorized", "obj": nil})
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/panel/api/inbounds/list":
		rows := []map[string]any{{"id": 1, "enable": true, "tag": "node-regular-tcp"}, {"id": 2, "enable": true, "tag": "regular-second"}, {"id": 3, "enable": true, "tag": "euru-only"}, {"id": 9, "enable": true, "tag": "unlimited-only"}, {"id": 99, "enable": true, "tag": "unknown"}}
		if p.noRegular {
			rows = rows[2:]
		}
		if p.noEuru {
			for _, row := range rows {
				if row["id"] == 3 {
					row["tag"] = "manual-only"
				}
			}
		}
		if p.sharedRegularUnlimited {
			rows[0]["tag"] = "node-regular-unlimited-tcp"
		}
		reply(map[string]any{"success": true, "obj": rows})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/panel/api/clients/get/"):
		if p.blockRead && p.client != nil {
			<-r.Context().Done()
			return
		}
		if p.failRead {
			w.WriteHeader(503)
			reply(map[string]any{"success": false, "msg": "unavailable", "obj": nil})
			return
		}
		if p.emptyRead {
			reply(map[string]any{"success": true, "obj": map[string]any{}})
			return
		}
		if p.client == nil {
			reply(map[string]any{"success": false, "msg": "Obtain (record not found)", "obj": nil})
			return
		}
		record := make(map[string]any, len(p.client)+1)
		for key, value := range p.client {
			record[key] = value
		}
		vpn := p.client["id"]
		if original, ok := p.client["uuid"]; ok {
			vpn = original
		}
		record["uuid"], record["id"] = vpn, 1
		reply(map[string]any{"success": true, "obj": map[string]any{"client": record, "inboundIds": p.ids, "usedTraffic": p.up + p.down}})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/panel/api/clients/traffic/"):
		if p.failRead {
			w.WriteHeader(503)
			return
		}
		if p.client == nil {
			w.WriteHeader(404)
			return
		}
		vpn := p.client["id"]
		if original, ok := p.client["uuid"]; ok {
			vpn = original
		}
		reply(map[string]any{"success": true, "obj": map[string]any{"email": p.client["email"], "uuid": vpn, "subId": p.client["subId"], "up": p.up, "down": p.down}})
	case r.Method == "POST" && r.URL.Path == "/panel/api/clients/add":
		p.adds++
		var body struct {
			Client     map[string]any `json:"client"`
			InboundIDs []int64        `json:"inboundIds"`
		}
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		if d.Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		if p.client != nil {
			reply(map[string]any{"success": false, "msg": "duplicate", "obj": nil})
			return
		}
		if p.dropAdd {
			p.failRead = true
			w.WriteHeader(503)
			return
		}
		p.client = body.Client
		p.client["password"] = "fixture protocol value"
		p.ids = body.InboundIDs
		if p.partial {
			p.ids = p.ids[:1]
		}
		if p.afterAdd != nil {
			p.afterAdd()
			p.afterAdd = nil
		}
		if p.failAfterAdd {
			p.failRead = true
		}
		if p.loseAdd {
			p.loseAdd = false
			c, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				c.Close()
			}
			return
		}
		reply(map[string]any{"success": true, "obj": nil})
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/attach"):
		p.attaches++
		var body struct {
			InboundIDs []int64 `json:"inboundIds"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		p.ids = append(p.ids, body.InboundIDs...)
		reply(map[string]any{"success": true, "obj": nil})
	case r.Method == "POST" && strings.Contains(r.URL.Path, "/update/"):
		p.updates++
		var body map[string]any
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		if d.Decode(&body) != nil || p.client == nil {
			w.WriteHeader(400)
			return
		}
		if p.strictNativeUpdate {
			id, validID := body["id"].(string)
			ips, validIPs := body["allowedIPs"].([]any)
			if !validID || id != p.client["id"] || !validIPs || len(ips) != 2 || ips[0] != "10.0.0.2/32" || ips[1] != "10.0.0.3/32" {
				w.WriteHeader(400)
				reply(map[string]any{"success": false, "msg": "invalid client model", "obj": nil})
				return
			}
		}
		if p.refuseActivation && body["enable"] == true {
			body["enable"] = false
		}
		p.client = body
		reply(map[string]any{"success": true, "obj": nil})
	case r.Method == "POST" && strings.Contains(r.URL.Path, "/resetTraffic/"):
		p.resets++
		if !p.resetLeavesTraffic {
			p.up = 0
			p.down = 0
		}
		if p.client != nil {
			p.client["enable"] = true
		}
		if p.loseReset {
			p.loseReset = false
			c, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				c.Close()
			}
			return
		}
		reply(map[string]any{"success": true, "obj": nil})
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/bulkDisable"):
		p.disables++
		if p.client != nil {
			p.client["enable"] = false
		}
		reply(map[string]any{"success": true, "obj": nil})
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/detach"):
		p.detaches++
		var body struct {
			InboundIDs []int64 `json:"inboundIds"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		remove := map[int64]bool{}
		for _, id := range body.InboundIDs {
			remove[id] = true
		}
		ids := []int64{}
		for _, id := range p.ids {
			if !remove[id] {
				ids = append(ids, id)
			}
		}
		p.ids = ids
		reply(map[string]any{"success": true, "obj": nil})
	default:
		if r.Method != "GET" {
			p.otherWrites++
		}
		w.WriteHeader(404)
		reply(map[string]any{"success": false, "msg": "unsupported endpoint", "obj": nil})
	}
}
func approved(t *testing.T, s *Service, e *testkit.Env, email string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	account := verified(t, s, e, email)
	r, _, err := s.CreateTrialRequest(ctx, account, uuid.New(), wire.TrialRequestInput{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.DecideTrialRequest(ctx, r.RequestId, decision(101, "approve"))
	if err != nil || out.OperationId == nil {
		t.Fatal("approve", err)
	}
	return account, *out.OperationId
}
func integer(t *testing.T, v any) int64 {
	t.Helper()
	n, ok := v.(json.Number)
	if !ok {
		t.Fatal("expected integer in panel payload")
	}
	value, err := n.Int64()
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func TestProvisionReconciliation(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	p.loseAdd = true
	account, operation := approved(t, s, e, "provision@example.test")
	s.cfg.TrialPeriodDays = 9
	s.cfg.TrialTrafficGB = 0
	s.cfg.TrialDevices = 7
	ctx := context.Background()
	if err := s.Provision(ctx, operation); err != nil {
		t.Fatalf("lost add reply should reconcile: %v", err)
	}
	var state, grant string
	e.Pool.QueryRow(ctx, `SELECT status FROM trial_operations WHERE id=$1`, operation).Scan(&state)
	e.Pool.QueryRow(ctx, `SELECT status FROM trial_grants WHERE account_id=$1`, account).Scan(&grant)
	if state != "applied" || grant != "granted" || p.adds != 1 || p.otherWrites != 0 {
		t.Fatal("false success or duplicate write")
	}
	if integer(t, p.client["limitIp"]) != 2 || integer(t, p.client["totalGB"]) != 15*1024*1024*1024 {
		t.Fatal("panel units")
	}
	originalExpiry := integer(t, p.client["expiryTime"])
	if originalExpiry != e.Clock().Add(3*24*time.Hour).UnixMilli() {
		t.Fatal("approval snapshot replaced by runtime settings")
	}
	originalUUID := p.client["id"]
	originalSub := p.client["subId"]
	e.Advance(2 * time.Hour)
	s.cfg.TrialPeriodDays = 10
	s.cfg.TrialTrafficGB = 0
	if err := s.Provision(ctx, operation); err != nil {
		t.Fatal(err)
	}
	if p.adds != 1 || p.client["id"] != originalUUID || p.client["subId"] != originalSub || integer(t, p.client["expiryTime"]) != originalExpiry {
		t.Fatal("immutable targets")
	}
	sub, err := s.Subscription(ctx, account)
	if err != nil || sub.Status != "active" || sub.Devices != 1 || sub.TrafficUsedBytes == nil || *sub.TrafficUsedBytes != 1234 {
		t.Fatal("subscription view", err)
	}
	p.failRead = true
	e.Advance(time.Minute)
	stale, err := s.Subscription(ctx, account)
	if err != nil || !stale.DataStale || stale.ObservedAt == nil || !stale.ObservedAt.Equal(*sub.ObservedAt) || *stale.TrafficUsedBytes != 1234 {
		t.Fatal("stale usage fabricated", err)
	}
}
func TestProvisionReconciliationPartial(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	p.partial = true
	_, operation := approved(t, s, e, "partial@example.test")
	if err := s.Provision(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if p.adds != 1 || p.attaches != 1 || len(p.ids) != 2 || p.client["password"] != "fixture protocol value" || p.otherWrites != 0 {
		t.Fatal("partial membership or protocol fields changed")
	}
	for _, id := range p.ids {
		if id != 1 && id != 2 {
			t.Fatal("unknown group modified")
		}
	}
}
func TestProvisionReconciliationNoRegular(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	p.noRegular = true
	_, operation := approved(t, s, e, "no-inbound@example.test")
	s.Provision(context.Background(), operation)
	var state string
	e.Pool.QueryRow(context.Background(), `SELECT status FROM trial_operations WHERE id=$1`, operation).Scan(&state)
	if p.adds != 0 || state != "needs_review" || count(t, e, "trial_grants") != 1 {
		t.Fatal("empty regular set should retain reservation without write")
	}
}
func TestSubscriptionPrivacy(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	account, operation := approved(t, s, e, "key@example.test")
	ctx := context.Background()
	if _, err := s.SubscriptionKey(ctx, account); status(err) != 409 {
		t.Fatal("key exposed before applied")
	}
	if err := s.Provision(ctx, operation); err != nil {
		t.Fatal(err)
	}
	key, err := s.SubscriptionKey(ctx, account)
	if err != nil || !strings.HasPrefix(key.SubscriptionUrl, s.cfg.SubscriptionBaseURL) {
		t.Fatal("key unavailable after confirmed write", err)
	}
	other := verified(t, s, e, "other@example.test")
	if _, err = s.SubscriptionKey(ctx, other); status(err) != 409 {
		t.Fatal("other owner received key")
	}
	p.client["id"] = uuid.NewString()
	if _, err = s.SubscriptionKey(ctx, account); status(err) != 409 {
		t.Fatal("foreign panel client accepted")
	}
	if p.adds != 1 || p.otherWrites != 0 {
		t.Fatal("foreign client rewritten")
	}
}

func TestProvisionReconciliationRecovery(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	p.failAfterAdd = true
	account, id := approved(t, s, e, "ambiguous@example.test")
	ctx := context.Background()
	if err := s.Provision(ctx, id); err != nil {
		t.Fatal(err)
	}
	op, err := testProvisionRow(ctx, e.Pool, id)
	if err != nil || op.Status != "needs_review" || !op.WriteStarted || len(op.Target) == 0 {
		t.Fatal("ambiguous write lost reservation/intent", err)
	}
	original := append([]byte(nil), op.Target...)
	p.failRead = false
	p.failAfterAdd = false
	e.Advance(time.Hour)
	in := wire.ReconcileInput{OperatorTgId: 101, Reason: "Panel connection restored"}
	key := uuid.New()
	out, err := s.ReconcileTrialOperation(ctx, id, key, in)
	if err != nil || out.OperationId != id {
		t.Fatal(err)
	}
	if _, err = s.ReconcileTrialOperation(ctx, id, key, in); err != nil {
		t.Fatal("manual retry not idempotent", err)
	}
	if err = s.Provision(ctx, id); err != nil {
		t.Fatal(err)
	}
	op, err = testProvisionRow(ctx, e.Pool, id)
	if err != nil || op.Status != "applied" || !bytes.Equal(original, op.Target) || p.adds != 1 || count(t, e, "trial_grants") != 1 {
		t.Fatal("recovery replaced operation or extended access", err)
	}
	sub, err := s.Subscription(ctx, account)
	if err != nil || sub.Status != "active" {
		t.Fatal(err)
	}
}
func TestProvisionReconciliationUnknownReads(t *testing.T) {
	for _, kind := range []string{"empty", "unavailable", "restricted", "zero"} {
		t.Run(kind, func(t *testing.T) {
			s, e := fixture(t)
			p := panelFixture(t, s)
			if kind == "zero" {
				s.cfg.TrialTrafficGB = 0
				s.cfg.TrialDevices = 0
			}
			account, id := approved(t, s, e, kind+"@example.test")
			ctx := context.Background()
			if kind == "empty" {
				p.emptyRead = true
			}
			if kind == "unavailable" {
				p.failRead = true
			}
			if kind == "restricted" {
				if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, account); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "zero" {
				if err := s.Provision(ctx, id); err != nil {
					t.Fatal(err)
				}
				if integer(t, p.client["limitIp"]) != 0 || integer(t, p.client["totalGB"]) != 0 {
					t.Fatal("unlimited zero units")
				}
				e.Advance(3 * 24 * time.Hour)
				sub, err := s.Subscription(ctx, account)
				if err != nil || sub.Status != "expired" {
					t.Fatal("expiry boundary", err)
				}
				return
			}
			for range 5 {
				_ = s.Provision(ctx, id)
			}
			op, err := testProvisionRow(ctx, e.Pool, id)
			if err != nil || op.Status != "needs_review" || p.adds != 0 || p.otherWrites != 0 {
				t.Fatal("unknown read treated as absence", err)
			}
		})
	}
	for _, values := range []struct{ days, gb, devices int64 }{{0, 15, 1}, {-1, 15, 1}, {3, -1, 1}, {3, 15, -1}, {math.MaxInt64, 15, 1}, {3, math.MaxInt64, 1}, {3, 15, math.MaxInt64}} {
		s, e := fixture(t)
		p := panelFixture(t, s)
		s.cfg.TrialPeriodDays = values.days
		s.cfg.TrialTrafficGB = values.gb
		s.cfg.TrialDevices = values.devices
		account := verified(t, s, e, "invalid@example.test")
		r, _, err := s.CreateTrialRequest(context.Background(), account, uuid.New(), wire.TrialRequestInput{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.DecideTrialRequest(context.Background(), r.RequestId, decision(101, "approve"))
		if err == nil || p.adds != 0 {
			t.Fatal("invalid target wrote panel")
		}
	}
}
func TestProvisionReconciliationAbsentAfterAmbiguity(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	p.dropAdd = true
	_, id := approved(t, s, e, "absence@example.test")
	ctx := context.Background()
	_ = s.Provision(ctx, id)
	p.dropAdd = false
	p.failRead = false
	_, err := s.ReconcileTrialOperation(ctx, id, uuid.New(), wire.ReconcileInput{OperatorTgId: 101, Reason: "Investigated missing client"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Provision(ctx, id)
	op, err := testProvisionRow(ctx, e.Pool, id)
	if err != nil || p.adds != 1 || op.Status != "needs_review" {
		t.Fatal("ambiguous absence bypassed uniqueness guarantee", err)
	}
	s.cfg.PanelDuplicateGuardVerified = true
	_, err = s.ReconcileTrialOperation(ctx, id, uuid.New(), wire.ReconcileInput{OperatorTgId: 101, Reason: "Fixture uniqueness was verified"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Provision(ctx, id); err != nil {
		t.Fatal(err)
	}
	op, err = testProvisionRow(ctx, e.Pool, id)
	if err != nil || op.Status != "applied" || p.adds != 2 {
		t.Fatal("attested fixture recovery failed", err)
	}
}
func TestProvisionReconciliationDBCrash(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	_, id := approved(t, s, e, "db-crash@example.test")
	ctx := context.Background()
	p.blockRead = true
	p.afterAdd = func() {
		var pid int32
		if err := e.Pool.QueryRow(ctx, `SELECT worker_pid FROM trial_operations WHERE id=$1`, id).Scan(&pid); err != nil {
			t.Error(err)
			return
		}
		var killed bool
		if err := e.Pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&killed); err != nil || !killed {
			t.Error("controlled session termination failed", err)
		}
	}
	began := time.Now()
	_ = s.Provision(ctx, id)
	if time.Since(began) > 3*time.Second {
		t.Fatal("owner loss did not cancel HTTP")
	}
	p.mu.Lock()
	p.blockRead = false
	p.mu.Unlock()
	op, err := testProvisionRow(ctx, e.Pool, id)
	if err != nil || op.Status != "needs_review" || p.adds != 1 {
		t.Fatal("ownership loss falsely applied", err)
	}
	var granted int
	if err = e.Pool.QueryRow(ctx, `SELECT count(*) FROM trial_grants WHERE status='granted'`).Scan(&granted); err != nil || granted != 0 {
		t.Fatal("grant after owner loss", err)
	}
	if _, err = s.ReconcileTrialOperation(ctx, id, uuid.New(), wire.ReconcileInput{OperatorTgId: 101, Reason: "Database session restored"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Provision(ctx, id); err != nil {
		t.Fatal(err)
	}
	op, err = testProvisionRow(ctx, e.Pool, id)
	if err != nil || op.Status != "applied" || p.adds != 1 {
		t.Fatal("session-loss recovery duplicated client", err)
	}
}
func TestProvisionReconciliationApplyFailure(t *testing.T) {
	s, e := fixture(t)
	p := panelFixture(t, s)
	_, id := approved(t, s, e, "apply-fail@example.test")
	ctx := context.Background()
	if _, err := e.Pool.Exec(ctx, `CREATE FUNCTION fixture_fail_grant() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'controlled apply failure'; END $$; CREATE TRIGGER fixture_grant BEFORE UPDATE ON trial_grants FOR EACH ROW EXECUTE FUNCTION fixture_fail_grant();`); err != nil {
		t.Fatal(err)
	}
	_ = s.Provision(ctx, id)
	op, err := testProvisionRow(ctx, e.Pool, id)
	if err != nil || op.Status != "needs_review" || p.adds != 1 {
		t.Fatal("failed apply abandoned operation", err)
	}
}
func TestSubscriptionPrivacyErrorContract(t *testing.T) {
	s, e := fixture(t)
	account, _ := approved(t, s, e, "error-code@example.test")
	_, err := s.SubscriptionKey(context.Background(), account)
	var domain *Error
	if !errors.As(err, &domain) || domain.Code != "OPERATION_NOT_READY" {
		t.Fatal("noncanonical key error", err)
	}
}

func TestSubscriptionPrivacyForeignTargets(t *testing.T) {
	for _, field := range []string{"id", "subId"} {
		t.Run(field, func(t *testing.T) {
			s, e := fixture(t)
			p := panelFixture(t, s)
			account, id := approved(t, s, e, strings.ToLower(field)+"@example.test")
			ctx := context.Background()
			if err := s.Provision(ctx, id); err != nil {
				t.Fatal(err)
			}
			p.mu.Lock()
			if field == "id" {
				p.client[field] = uuid.NewString()
			} else {
				p.client[field] = "foreignfixture000"
			}
			p.mu.Unlock()
			_, err := s.SubscriptionKey(ctx, account)
			if status(err) != 409 || p.adds != 1 || p.otherWrites != 0 {
				t.Fatal("conflicting target exposed or rewritten", err)
			}
			op, err := testProvisionRow(ctx, e.Pool, id)
			if err != nil || op.Status != "applied" {
				t.Fatal("profile read changed operation", err)
			}
		})
	}
}
func TestProvisionReconciliationBackoff(t *testing.T) {
	w := &ProvisionWorker{}
	for i, d := range []time.Duration{10, 30, 120, 300} {
		before := time.Now()
		next := w.NextRetry(&river.Job[ProvisionArgs]{JobRow: &rivertype.JobRow{Attempt: i + 1}})
		if next.Sub(before) < d*time.Second || next.Sub(before) > d*time.Second+time.Second {
			t.Fatal("retry backoff")
		}
	}
}
