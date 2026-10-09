package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func TestNativeTrialAudit(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TESTS") != "1" {
		t.Skip("requires RUN_BROWSER_TESTS=1")
	}
	f := openMode(t, true)
	ctx := context.Background()
	execute := func(query string, args ...any) {
		t.Helper()
		if _, err := f.env.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal("owned audit seed failed", err)
		}
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if f.env.Pool.QueryRow(ctx, query, args...).Scan(&n) != nil {
			t.Fatal("owned audit count unavailable")
		}
		return n
	}
	var now time.Time
	if f.env.Pool.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&now) != nil {
		t.Fatal("owned database clock unavailable")
	}
	// Start before the configured daily window; later choose an already-open
	// IANA window from the same DB clock. No sleep until tomorrow or clock patch.
	offset := now.UTC().Hour()
	if offset > 12 {
		offset -= 24
	}
	earlyZone := fmt.Sprintf("Etc/GMT%+d", offset)
	if offset == 0 {
		earlyZone = "UTC"
	}
	if _, err := time.LoadLocation(earlyZone); err != nil {
		t.Fatal("owned schedule timezone unavailable")
	}
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if os.WriteFile(path, data, 0600) != nil {
			t.Fatal("owned private audit input unavailable")
		}
		return path
	}
	const supportToken = "987654321:abcdefghijklmnopqrstuvwxyz012345678"
	settings := map[string]string{"TELEGRAM_ENABLED": "false", "AUDIT_MIRROR_ENABLED": "true", "SUPPORT_BOT_TOKEN_FILE": write("support-token", []byte(supportToken)), "SUPPORT_GROUP_ID": "-1001234567890", "AUDIT_RETENTION_DAYS": "365", "AUDIT_RETENTION_TIMEZONE": earlyZone}
	var mu sync.Mutex
	calls := map[string]int{}
	var texts []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/bot"+supportToken+"/sendMessage" {
			t.Error("main bot or unexpected provider route")
			http.Error(w, "owned mirror only", 400)
			return
		}
		var body map[string]json.RawMessage
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil {
			t.Error("invalid owned mirror body")
			return
		}
		var chat int64
		var text string
		json.Unmarshal(body["chat_id"], &chat)
		json.Unmarshal(body["text"], &text)
		if chat != -1001234567890 || len(body) != 3 || string(body["link_preview_options"]) != `{"is_disabled":true}` {
			t.Error("mirror is not plain General metadata")
		}
		action := ""
		id := ""
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "action=") {
				action = strings.TrimPrefix(line, "action=")
			}
			if strings.HasPrefix(line, "id=") {
				id = strings.TrimPrefix(line, "id=")
			}
		}
		if strings.HasPrefix(action, "native.audit.") && count("SELECT count(*) FROM audit_events WHERE id=$1 AND mirror_attempted_at IS NOT NULL", id) != 1 {
			t.Error("wire preceded the committed claim")
		}
		mu.Lock()
		calls[action]++
		texts = append(texts, text)
		mu.Unlock()
		switch action {
		case "native.audit.lost":
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				connection.Close()
			}
			return
		case "native.audit.rate":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			io.WriteString(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":1}}`)
			return
		case "native.audit.crash":
			<-r.Context().Done()
			return // The real child stops after the provider accepted a copy.
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1, "chat": map[string]any{"id": chat, "type": "supergroup"}}})
	})
	attempts := func(action string) int { mu.Lock(); defer mu.Unlock(); return calls[action] }
	start, stop := nativeNoticeBinary(t, f, handler, settings)
	firstPID := start()
	operator, csrf, actor := f.signupAccount(t, nativeEmail("audit-operator"))
	if f.svc.Accounts.ChangeOperatorRole(ctx, actor, true) != nil {
		t.Fatal("owned audit operator unavailable")
	}
	client, clientCSRF, account := f.signupAccount(t, nativeEmail("audit-client"))
	target := int64(9007199254740993)
	execute("INSERT INTO legacy_approval_snapshots(account_id,source_legacy_user_id,source_tg_id,status) VALUES($1,987,$2,'approved')", account, target)
	execute("UPDATE accounts SET telegram_id=$2 WHERE id=$1", actor, target)
	old := now.UTC().Add(-400 * 24 * time.Hour).Truncate(time.Microsecond)
	message, conversation, access, order := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	execute("INSERT INTO support_conversations(id,account_id,status,created_at,updated_at) VALUES($1,$2,'open',$3,$3)", conversation, account, old)
	execute("INSERT INTO support_messages(id,conversation_id,sender_account_id,sender_kind,text,created_at,attachment_name,attachment_bytes) VALUES($1,$2,$3,'customer','Retained native support body',$4,'retained.txt',$5)", message, conversation, account, old, []byte("retained-native-attachment"))
	execute("INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at) VALUES($1,$2,'reset_traffic','applied','retained fixture','{}','{}',$3,$3)", access, account, old)
	quote := `{"amount_minor":"9007199254740993","currency":"RUB","devices":2,"period_days":30,"plan_id":"00000000-0000-4000-8000-000000000123","profile":"regular","revision":1,"traffic_gb":15}`
	execute("INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,payment_status,fulfillment_status,active,created_at,expires_at,paid_at,action) VALUES($1,$2,$3,$4,$5,9007199254740993,'AC','paid','needs_review',false,$6,$7,$6,'renew')", order, account, uuid.New(), []byte{1}, quote, old, old.Add(time.Hour))
	execute("INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,created_at) VALUES('native-audit-receipt',$1,$2,9007199254740993,9007199254740900,'643','p2p-incoming',false,false,$2)", order, old)
	execute("INSERT INTO purchase_refunds(id,order_id,receipt_operation_id,payment_method,reference,returned_amount,returned_currency,reason,operator_account_id,created_at) VALUES($1,$2,'native-audit-receipt','yoomoney','native-audit-return','90071992547409.93','RUB','Retained full return',$3,$4)", uuid.New(), order, actor, old)
	snapshot := func() [32]byte {
		t.Helper()
		var state string
		query := `SELECT jsonb_build_array(
   (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM accounts x),
   (SELECT jsonb_agg(to_jsonb(x) ORDER BY account_id) FROM trial_grants x),
   (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM trial_operations x),
   (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM access_operations x),
   (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM purchase_orders x),
   (SELECT jsonb_agg(to_jsonb(x) ORDER BY operation_id) FROM purchase_receipts x),
   (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM purchase_refunds x),
   (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM support_conversations x),
   (SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM support_messages x),
   (SELECT jsonb_agg(to_jsonb(x) ORDER BY account_id) FROM legacy_approval_snapshots x))::text`
		if f.env.Pool.QueryRow(ctx, query).Scan(&state) != nil {
			t.Fatal("owned nonempty business snapshot unavailable")
		}
		return sha256.Sum256([]byte(state))
	}
	before := snapshot()
	database, err := url.Parse(f.env.Pool.Config().ConnString())
	if err != nil {
		t.Fatal("owned database URL unavailable")
	}
	database.Path = "/" + f.env.Pool.Config().ConnConfig.Database
	dbFile := write("import-database", []byte(database.String()))
	binary := filepath.Join(dir, "import-server")
	build := exec.Command("go", "build", "-race", "-o", binary, "./cmd/server")
	build.Dir = filepath.Join(f.root, "backend")
	if build.Run() != nil {
		t.Fatal("fresh owned import binary failed")
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal("fresh owned import binary unavailable")
	}
	t.Logf("fresh import cmd/server SHA256 %x", sha256.Sum256(data))
	name, source, payload, actorType := "<b>Native original name</b>", "support_bot", `{"dm_body": "native-private-payload"}`, "operator"
	actorID := int64(-9007199254740993)
	packet := auditreports.LegacyAuditPackage{Version: 1, Events: []auditreports.LegacyAuditInput{
		{SourceID: 9223372036854775807, CreatedAt: old, Action: "legacy.native.old", TargetTgID: &target, ActorType: &actorType, ActorID: &actorID, ActorName: &name, Source: &source, PayloadJSON: &payload},
		{SourceID: 9223372036854775806, CreatedAt: now.UTC().Truncate(time.Microsecond), Action: "legacy.native.current", TargetTgID: &target, ActorType: &actorType, ActorID: &actorID, ActorName: &name, Source: &source, PayloadJSON: &payload},
	}}
	importCLI := func(flag string, p any, wantErr string) auditreports.LegacyAuditImportResult {
		t.Helper()
		raw, _ := json.Marshal(p)
		command := exec.Command(binary, "import-legacy-audit", flag)
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "DATABASE_URL_FILE=" + dbFile}
		command.Stdin = bytes.NewReader(raw)
		output, err := command.CombinedOutput()
		if bytes.Contains(output, []byte(payload)) || bytes.Contains(output, []byte(name)) || bytes.Contains(output, []byte("native-private-payload")) {
			t.Fatal("compiled import exposed private input")
		}
		if wantErr != "" {
			var out struct {
				Error string `json:"error"`
			}
			if err == nil || json.Unmarshal(bytes.Split(output, []byte("\n"))[0], &out) != nil || out.Error != wantErr {
				t.Fatal("compiled import did not refuse unsafe source")
			}
			return auditreports.LegacyAuditImportResult{}
		}
		var out auditreports.LegacyAuditImportResult
		if err != nil || json.Unmarshal(output, &out) != nil {
			t.Fatal("compiled import failed safely")
		}
		return out
	}
	dry := importCLI("--dry-run", packet, "")
	if dry.Inserted != 2 || dry.Replayed != 0 || count("SELECT count(*) FROM legacy_audit_imports") != 0 || count("SELECT count(*) FROM legacy_audit_events") != 0 {
		t.Fatal("compiled dry-run wrote or lost sources")
	}
	for _, flag := range []string{"--dry-run", "--apply"} {
		importCLI(flag, json.RawMessage(`{"version":1,"events":[{"source_id":1,"created_at":"2026-10-01T00:00:00.1234560001Z","action":"support.message"}]}`), "IMPORT_INVALID_PACKAGE")
	}
	if count("SELECT count(*) FROM legacy_audit_imports") != 0 || count("SELECT count(*) FROM legacy_audit_events") != 0 {
		t.Fatal("compiled lossy timestamp reached persistence")
	}
	applied := importCLI("--apply", packet, "")
	if applied.Inserted != 2 || applied.Replayed != 0 {
		t.Fatal("compiled import did not preserve both sources")
	}
	replay := importCLI("--apply", packet, "")
	if replay.Inserted != 0 || replay.Replayed != 2 || count("SELECT count(*) FROM legacy_audit_events") != 2 {
		t.Fatal("compiled replay created new history")
	}
	bad := packet
	bad.Events = append([]auditreports.LegacyAuditInput{}, packet.Events...)
	bad.Events[0].Action = "changed.source"
	bad.Events = append(bad.Events, auditreports.LegacyAuditInput{SourceID: 1, CreatedAt: now.UTC().Truncate(time.Microsecond), Action: "partial.source"})
	importCLI("--apply", bad, "IMPORT_SOURCE_CONFLICT")
	if count("SELECT count(*) FROM legacy_audit_imports WHERE source_id=1") != 0 || count("SELECT count(*) FROM legacy_audit_events") != 2 {
		t.Fatal("late compiled source conflict was not atomic")
	}
	history := func(kind string, owner *uuid.UUID) wire.AuditHistory {
		t.Helper()
		input := wire.AuditHistoryInput{Kind: wire.AuditHistoryInputKind(kind), AccountId: owner}
		status, raw, _ := f.send(t, operator, "POST", "/api/v1/operator/audit/history", input, csrf, "", false)
		var out wire.AuditHistory
		if status != 200 || json.Unmarshal(raw, &out) != nil || out.Version != "audit-history-v1" || string(out.Kind) != kind || bytes.Contains(raw, []byte("native-private-payload")) {
			t.Fatal("compiled journal changed scope or exposed private payload", status)
		}
		return out
	}
	legacy := history("legacy", &account)
	if len(legacy.LegacyEvents) != 2 || legacy.LegacyEvents[0].SourceId != "9223372036854775806" || legacy.LegacyEvents[0].ActorId == nil || *legacy.LegacyEvents[0].ActorId != "-9007199254740993" || legacy.LegacyEvents[0].AccountId == nil || *legacy.LegacyEvents[0].AccountId != account || len(history("legacy", &actor).LegacyEvents) != 0 {
		t.Fatal("compiled legacy source identity moved to the current Telegram binding")
	}
	status, _, _ := f.send(t, client, "POST", "/api/v1/operator/audit/history", map[string]string{"kind": "native"}, clientCSRF, "", false)
	if status != 403 {
		t.Fatal("client read operator journal", status)
	}
	reason := "<b>Native private reason</b>"
	record := func(action string, commit bool) (uuid.UUID, func()) {
		t.Helper()
		tx, err := f.env.Pool.Begin(ctx)
		if err != nil {
			t.Fatal("owned audit transaction unavailable")
		}
		id := uuid.New()
		system := false
		if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: id, AccountID: account, CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Action: action, Reason: &reason, OperatorAccountID: &actor, SystemActor: &system, SupportMessageID: &message, AccessOperationID: &access}) != nil {
			tx.Rollback(ctx)
			t.Fatal("owned transaction audit unavailable")
		}
		if commit {
			if tx.Commit(ctx) != nil {
				t.Fatal("owned audit commit failed")
			}
			return id, func() {}
		}
		return id, func() {
			if tx.Rollback(ctx) != nil {
				t.Fatal("owned rollback failed")
			}
		}
	}
	_, rollback := record("native.audit.rollback", false)
	committed, _ := record("native.audit.committed", true)
	wait(t, func() bool { return attempts("native.audit.committed") == 1 })
	if attempts("native.audit.rollback") != 0 {
		t.Fatal("uncommitted event reached the wire")
	}
	rollback()
	record("native.audit.lost", true)
	record("native.audit.rate", true)
	wait(t, func() bool { return attempts("native.audit.lost") == 1 && attempts("native.audit.rate") == 1 })
	record("native.audit.crash", true)
	wait(t, func() bool { return attempts("native.audit.crash") == 1 })
	stop()
	pending := uuid.New()
	oldNative := uuid.New()
	oldSystem := uuid.New()
	execute("INSERT INTO audit_events(id,account_id,created_at,action) VALUES($1,$2,now(),'native.audit.pending')", pending, actor)
	execute("INSERT INTO audit_events(id,account_id,created_at,action,reason) VALUES($1,$2,$3,'native.audit.old','old-private-reason')", oldNative, account, old)
	execute("INSERT INTO audit_system_events(id,created_at,action,native_count,legacy_count,system_count) VALUES($1,$2,'audit.legacy_imported',0,1,0)", oldSystem, old)
	readyZone := "UTC"
	if now.UTC().Hour() < 3 || now.UTC().Hour() == 3 && now.UTC().Minute() < 30 {
		readyZone = "Asia/Tokyo"
	}
	settings["AUDIT_RETENTION_TIMEZONE"] = readyZone
	if start() == firstPID {
		t.Fatal("compiled audit process did not restart")
	}
	wait(t, func() bool { return count("SELECT count(*) FROM audit_system_events WHERE action='audit.pruned'") == 1 })
	system := history("system", nil)
	var receipt *wire.SystemAuditHistoryEvent
	for i := range system.SystemEvents {
		if system.SystemEvents[i].Action == "audit.pruned" {
			receipt = &system.SystemEvents[i]
		}
	}
	if receipt == nil || receipt.NativeCount != 1 || receipt.LegacyCount != 1 || receipt.SystemCount != 1 || receipt.RetentionDays == nil || *receipt.RetentionDays != 365 || receipt.Cutoff == nil || receipt.PeriodDay == nil {
		t.Fatal("compiled daily prune lost actual counts or cutoff")
	}
	if count("SELECT count(*) FROM audit_events WHERE id=$1", oldNative) != 0 || count("SELECT count(*) FROM legacy_audit_events WHERE source_id=9223372036854775807") != 0 || count("SELECT count(*) FROM legacy_audit_imports") != 2 || snapshot() != before {
		t.Fatal("compiled prune deleted foreign facts or replay ledger")
	}
	replay = importCLI("--apply", packet, "")
	if replay.Inserted != 0 || replay.Replayed != 2 || count("SELECT count(*) FROM legacy_audit_events") != 1 {
		t.Fatal("compiled replay resurrected expired private history")
	}
	// Use the actual built web consumer and cookie against the compiled HTTP API.
	u, _ := url.Parse(f.public.URL)
	cookie := ""
	for _, c := range operator.Jar.Cookies(u) {
		if c.Name == "__Host-session" {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("owned browser session unavailable")
	}
	browserInput, _ := json.Marshal(map[string]any{"origin": f.public.URL, "account": account, "pending": pending, "committed": committed, "reason": reason, "legacyName": name, "cookie": map[string]any{"name": "__Host-session", "value": cookie, "url": f.public.URL, "httpOnly": true, "secure": true, "sameSite": "Lax"}})
	file := write("browser-input.json", browserInput)
	script := `import fs from 'node:fs';import assert from 'node:assert/strict';import {chromium} from 'playwright';
const input=JSON.parse(fs.readFileSync(process.env.OWN_AUDIT_BROWSER_FILE,'utf8')),browser=await chromium.launch();
try{const context=await browser.newContext({ignoreHTTPSErrors:true,baseURL:input.origin});await context.addCookies([input.cookie]);const page=await context.newPage();
let release,started=false,settled;const hold=new Promise(r=>release=r),done=new Promise(r=>settled=r);let reads=0;
await page.route('**/api/v1/operator/audit/history',async route=>{if(++reads!==1)return route.continue();try{const response=await route.fetch();started=true;await hold;await route.fulfill({response});}catch{}finally{settled();}});
await page.goto('/admin/audit?lang=en');await page.waitForFunction(()=>document.querySelector('h1')?.textContent==='Action journal');const deadline=Date.now()+15000;while(!started&&Date.now()<deadline)await new Promise(r=>setTimeout(r,10));assert(started,'held real reader did not finish');
const region=page.getByRole('region',{name:'Action journal',exact:true});await region.getByLabel('Account ID',{exact:true}).fill(input.account);await region.getByRole('button',{name:'Apply filters',exact:true}).click();await region.getByText(input.committed,{exact:true}).waitFor();release();await done;
assert.equal(await region.getByText(input.pending,{exact:true}).count(),0);await region.locator('li').filter({has:page.getByText(input.committed,{exact:true})}).getByText(input.reason,{exact:true}).waitFor();assert.equal(await region.locator('b').count(),0);
await region.getByLabel('Journal source',{exact:true}).selectOption('legacy');await region.getByLabel('Account ID',{exact:true}).fill(input.account);await region.getByRole('button',{name:'Apply filters',exact:true}).click();
await region.getByText('9223372036854775806',{exact:true}).waitFor();await region.getByText('-9007199254740993',{exact:true}).waitFor();await region.getByText(input.legacyName,{exact:true}).waitFor();assert.equal(await region.locator('b').count(),0);assert(!await region.innerText().then(t=>t.includes('native-private-payload')));
await region.getByLabel('Journal source',{exact:true}).selectOption('system');await region.getByRole('heading',{name:'audit.pruned',exact:true}).waitFor();await context.close();console.log('PASS: compiled HTTP journal browser exact legacy IDs, keyed filter/late reply, plain metadata and daily receipt');
}finally{await browser.close();}`
	browserCtx, cancelBrowser := context.WithTimeout(ctx, 45*time.Second)
	defer cancelBrowser()
	browser := exec.CommandContext(browserCtx, "node", "--input-type=module", "-e", script)
	browser.Dir = filepath.Join(f.root, "web")
	browser.Env = append(os.Environ(), "OWN_AUDIT_BROWSER_FILE="+file)
	if output, err := browser.CombinedOutput(); err != nil {
		t.Fatalf("owned compiled audit browser failed: %s", output)
	}
	stop()
	start()
	if count("SELECT count(*) FROM audit_system_events WHERE action='audit.pruned'") != 1 || count("SELECT count(*) FROM audit_system_events WHERE id=$1", receipt.Id) != 1 || attempts("native.audit.pending") != 0 || attempts("native.audit.lost") != 1 || attempts("native.audit.rate") != 1 || attempts("native.audit.crash") != 1 || snapshot() != before {
		t.Fatal("restart repeated uncertain mirror/prune or changed business facts")
	}
	stop()
	settings["AUDIT_MIRROR_ENABLED"] = "false"
	settings["SUPPORT_BOT_TOKEN_FILE"] = ""
	settings["SUPPORT_GROUP_ID"] = ""
	start()
	record("native.audit.disabled", true)
	_ = history("native", &account)
	time.Sleep(1200 * time.Millisecond)
	if attempts("native.audit.disabled") != 0 {
		t.Fatal("disabled mirror called the provider")
	}
	stop()
	mu.Lock()
	allText := strings.Join(texts, "\n")
	mu.Unlock()
	for _, private := range []string{reason, name, payload, "native-private-payload", "Retained native support body", "retained-native-attachment", "example.test", "password", "support-token"} {
		if strings.Contains(allText, private) {
			t.Fatal("private data escaped through General mirror")
		}
	}
	if snapshot() != before {
		t.Fatal("compiled audit lifecycle changed retained business facts")
	}
	t.Log("PASS: compiled HTTP/CLI/TLS SMTP; main Telegram disabled; exact immutable legacy identity/import/dry-run/atomic conflict/replay after prune; plain post-commit General/no private body/no lost-ACK or 429 retry across three restarts; one daily receipt, nonempty money/access/support/identity preserved and bounded shutdown")
}
