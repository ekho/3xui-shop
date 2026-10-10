package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func cutoverDatabaseFile(t *testing.T, e *testkit.Env) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("private fixture directory unavailable")
	}
	u, err := url.Parse(e.Pool.Config().ConnString())
	if err != nil {
		t.Fatal("owned database URL unavailable")
	}
	u.Path = "/" + e.Pool.Config().ConnConfig.Database
	q := u.Query()
	q.Del("dbname")
	u.RawQuery = q.Encode()
	file := filepath.Join(dir, "database")
	if os.WriteFile(file, []byte(u.String()), 0600) != nil {
		t.Fatal("private database file unavailable")
	}
	return file
}

func TestCutoverCheckReadOnlyAndRevision(t *testing.T) {
	e := testkit.Open(t)
	t.Setenv("DATABASE_URL_FILE", cutoverDatabaseFile(t, e))
	t.Setenv("DATABASE_URL", "")
	original := sourceRevision
	t.Cleanup(func() { sourceRevision = original })
	sourceRevision = strings.Repeat("a", 40)
	var out bytes.Buffer
	if err := runCutoverCheck(&out); err != nil {
		t.Fatal("compatible read-only preflight failed", err)
	}
	var artifact cutoverArtifact
	if json.Unmarshal(out.Bytes(), &artifact) != nil || artifact.SourceRevision != sourceRevision || artifact.LegacyReceiptsVersion != 1 || artifact.Schema.Version != 44 || len(artifact.Schema.Migrations) != 64 || len(artifact.Schema.Structure) != 64 {
		t.Fatal("incomplete artifact identity")
	}
	var jobs int
	if e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job`).Scan(&jobs) != nil || jobs != 0 {
		t.Fatal("read-only preflight started jobs")
	}
	for _, revision := range []string{"unknown", strings.Repeat("0", 39), strings.Repeat("z", 40)} {
		sourceRevision = revision
		out.Reset()
		if runCutoverCheck(&out) == nil || out.Len() != 0 {
			t.Fatal("unattested revision accepted")
		}
	}
	sourceRevision = original
	if _, err := e.Pool.Exec(context.Background(), `UPDATE goose_db_version SET is_applied=false WHERE version_id=44`); err != nil {
		t.Fatal(err)
	}
	sourceRevision = strings.Repeat("a", 40)
	out.Reset()
	if runCutoverCheck(&out) == nil || out.Len() != 0 {
		t.Fatal("unsupported schema accepted")
	}
}

// This profile builds real Git artifacts; regular focused tests need no history checkout.
func TestCutoverArtifactRollback(t *testing.T) {
	if os.Getenv("RUN_CUTOVER_ARTIFACT_TESTS") != "1" {
		t.Skip("enable the explicit artifact rollback profile")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) []byte {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = root
		out, err := command.Output()
		if err != nil {
			t.Fatal("artifact source unavailable")
		}
		return out
	}
	checkpointFile, err := os.ReadFile(filepath.Join(root, "deploy/cutover/checkpoint.txt"))
	if err != nil {
		t.Fatal("compatible checkpoint missing")
	}
	checkpoint := strings.TrimSpace(string(checkpointFile))
	current := strings.TrimSpace(string(git("rev-parse", "HEAD")))
	decoded, decodeErr := hex.DecodeString(checkpoint)
	if decodeErr != nil || len(decoded) != 20 || checkpoint == current || strings.TrimSpace(string(git("rev-parse", checkpoint+"^{commit}"))) != checkpoint {
		t.Fatal("checkpoint must name a distinct verified commit")
	}
	baseline := "e53746c4a13d4209438012a33390ec84eb38539c"
	build := func(revision string) (string, [32]byte) {
		t.Helper()
		dir := t.TempDir()
		archive := git("archive", revision, "backend")
		extract := exec.Command("tar", "-x", "-C", dir)
		extract.Stdin = bytes.NewReader(archive)
		if extract.Run() != nil {
			t.Fatal("owned source archive extraction failed")
		}
		binary := filepath.Join(dir, "server")
		command := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-ldflags=-X main.sourceRevision="+revision, "-o", binary, "./cmd/server")
		command.Dir = filepath.Join(dir, "backend")
		command.Env = append(os.Environ(), "CGO_ENABLED=0")
		if command.Run() != nil {
			t.Fatal("owned artifact build failed")
		}
		data, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal("owned artifact unavailable")
		}
		return binary, sha256.Sum256(data)
	}
	newBinary, newHash := build(current)
	oldBinary, oldHash := build(checkpoint)
	unsupportedBinary, _ := build(baseline)
	if newHash == oldHash {
		t.Fatal("rollback did not change executable artifact")
	}
	e := testkit.Open(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secret := func(name, value string) string {
		t.Helper()
		file := filepath.Join(dir, name)
		if os.WriteFile(file, []byte(value), 0600) != nil {
			t.Fatal("private artifact fixture unavailable")
		}
		return file
	}
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))
	environment := []string{"DATABASE_URL_FILE=" + cutoverDatabaseFile(t, e), "REDIS_URL_FILE=" + os.Getenv("TEST_REDIS_URL_FILE"), "MAIL_KEY_FILE=" + secret("mail", key), "CODE_KEY_FILE=" + secret("code", key), "YOOMONEY_NOTIFICATION_SECRET_FILE=" + secret("yoomoney", "owned-legacy-secret"), "CABINET_ORIGIN=https://cabinet.example.test", "TERMS_VERSION=1", "PRIVACY_VERSION=1", "TELEGRAM_ENABLED=false", "TRIAL_ENABLED=false"}
	preflight := func(binary, revision string) (cutoverArtifact, bool) {
		t.Helper()
		command := exec.Command(binary, "cutover-check")
		command.Env = environment
		out, err := command.Output()
		var artifact cutoverArtifact
		ok := err == nil && json.Unmarshal(out, &artifact) == nil && artifact.SourceRevision == revision && artifact.LegacyReceiptsVersion == 1 && artifact.Schema.Version == 44
		return artifact, ok
	}
	newManifest, newOK := preflight(newBinary, current)
	oldManifest, oldOK := preflight(oldBinary, checkpoint)
	if !newOK || !oldOK || newManifest.Schema != oldManifest.Schema {
		t.Fatal("known checkpoint compatibility preflight failed")
	}
	ctx := context.Background()
	account, order, access, label := uuid.New(), uuid.New(), uuid.New(), uuid.NewString()
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id,legacy_user_id) VALUES($1,'artifact-fixture@example.test','en','fixture',now(),$2,'artifactfixture1',$3,'1','1',701,5)`, []any{account, uuid.New(), "acct_" + account.String()}},
		{`INSERT INTO legacy_payment_transactions(source_id,account_id,source_legacy_user_id,source_tg_id,source_payment_id,payment_id_hash,subscription,status,created_at,updated_at,imported_at) VALUES(9,$1,5,701,$2,sha256(convert_to($2,'UTF8')),'source quote retained','pending',now(),now(),now())`, []any{account, label}},
		{`INSERT INTO access_operations(id,account_id,kind,status,reason,desired,target,created_at,updated_at) VALUES($1,$2,'compensate','applied','Owned retained fixture','{}','{}',now(),now())`, []any{access, account}},
		{`INSERT INTO purchase_orders(id,account_id,idempotency_key,body_hash,quote,amount_minor,payment_type,payment_status,fulfillment_status,active,review_required,review_reason,created_at,expires_at,paid_at) VALUES($1,$2,$3,'fixture','{"currency":"RUB"}',10000,'AC','paid','needs_review',false,true,'Owned retained fixture',now(),now()+interval '1 day',now())`, []any{order, account, uuid.New()}},
		{`INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,created_at) VALUES('native-retained',$1,now(),10000,9700,'643','p2p-incoming',false,false,now())`, []any{order}},
		{`UPDATE purchase_orders SET funding_operation_id='native-retained' WHERE id=$1`, []any{order}},
	} {
		if _, err := e.Pool.Exec(ctx, query.sql, query.args...); err != nil {
			t.Fatal("retained fixture setup failed", err)
		}
	}
	snapshot := func() string {
		t.Helper()
		var value string
		if e.Pool.QueryRow(ctx, `SELECT jsonb_build_object('accounts',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM accounts t),'orders',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM purchase_orders t),'receipts',(SELECT jsonb_agg(to_jsonb(t) ORDER BY operation_id) FROM purchase_receipts t),'access',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM access_operations t),'source',(SELECT jsonb_agg(to_jsonb(t) ORDER BY source_id) FROM legacy_payment_transactions t))::text`).Scan(&value) != nil {
			t.Fatal("retained state snapshot unavailable")
		}
		return value
	}
	retained := snapshot()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	start := func(binary string) *exec.Cmd {
		t.Helper()
		command := exec.Command(binary, "serve")
		command.Env = append(append([]string{}, environment...), "LISTEN_ADDRESS="+address)
		if command.Start() != nil {
			t.Fatal("owned serve start failed")
		}
		t.Cleanup(func() {
			if command.ProcessState == nil {
				command.Process.Kill()
				command.Wait()
			}
		})
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			response, err := client.Get("http://" + address + "/readyz")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return command
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("owned serve readiness failed")
		return nil
	}
	stop := func(command *exec.Cmd) {
		t.Helper()
		if command.Process.Signal(syscall.SIGTERM) != nil {
			t.Fatal("owned serve stop failed")
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal("owned serve did not stop cleanly")
			}
		case <-time.After(25 * time.Second):
			command.Process.Kill()
			<-done
			t.Fatal("owned serve stop timed out; takeover refused")
		}
	}
	timestamps := map[string]string{}
	callback := func(operation string) {
		t.Helper()
		if timestamps[operation] == "" {
			timestamps[operation] = time.Now().UTC().Format(time.RFC3339)
		}
		fields := url.Values{"notification_type": {"p2p-incoming"}, "operation_id": {operation}, "amount": {"97.00"}, "currency": {"643"}, "datetime": {timestamps[operation]}, "sender": {"owned-sender"}, "codepro": {"false"}, "label": {label}}
		parts := []string{fields.Get("notification_type"), fields.Get("operation_id"), fields.Get("amount"), fields.Get("currency"), fields.Get("datetime"), fields.Get("sender"), fields.Get("codepro"), "owned-legacy-secret", fields.Get("label")}
		hash := sha1.Sum([]byte(strings.Join(parts, "&")))
		fields.Set("sha1_hash", hex.EncodeToString(hash[:]))
		response, err := client.PostForm("http://"+address+"/yoomoney", fields)
		if err != nil {
			t.Fatal("owned late callback failed")
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatal("owned late callback rejected", response.StatusCode)
		}
	}
	currentProcess := start(newBinary)
	callback("before-rollback")
	var originalProof string
	if e.Pool.QueryRow(ctx, `SELECT to_jsonb(t)::text FROM legacy_payment_receipts t WHERE source_id='before-rollback'`).Scan(&originalProof) != nil {
		t.Fatal("first financial proof missing")
	}
	if _, ok := preflight(unsupportedBinary, baseline); ok {
		t.Fatal("unsafe baseline accepted for rollback")
	}
	if currentProcess.Process.Signal(syscall.Signal(0)) != nil || snapshot() != retained {
		t.Fatal("rejected rollback changed current PID or retained state")
	}
	callback("before-rollback")
	stop(currentProcess)
	checkpointProcess := start(oldBinary)
	if checkpointProcess.Process.Pid == currentProcess.Process.Pid || checkpointProcess.Path != oldBinary {
		t.Fatal("runtime executable or PID did not change")
	}
	callback("before-rollback")
	callback("after-rollback")
	callback("after-rollback")
	stop(checkpointProcess)
	var afterProof string
	var receipts, jobs int
	if e.Pool.QueryRow(ctx, `SELECT to_jsonb(t)::text FROM legacy_payment_receipts t WHERE source_id='before-rollback'`).Scan(&afterProof) != nil || afterProof != originalProof || snapshot() != retained || e.Pool.QueryRow(ctx, `SELECT count(*) FROM legacy_payment_receipts WHERE amount_minor=9700 AND currency='RUB' AND source_transaction_id=9 AND account_id=$1 AND state='review'`, account).Scan(&receipts) != nil || receipts != 2 || e.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&jobs) != nil || jobs != 0 {
		t.Fatal("rollback lost financial proof, changed native facts or enqueued duplicate access")
	}
	t.Logf("current=%s executable=%x pid=%d; checkpoint=%s executable=%x pid=%d; unsafe baseline rejected, retained state stable, two late receipts", current, newHash, currentProcess.Process.Pid, checkpoint, oldHash, checkpointProcess.Process.Pid)
}
