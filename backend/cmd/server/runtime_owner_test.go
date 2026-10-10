package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"example.com/cabinet/backend/internal/testkit"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type ownerRoundTrip func(*http.Request) (*http.Response, error)

func (f ownerRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type lockedBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}
func (b *lockedBuffer) String() string {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.String()
}

func TestRuntimeOwnerProcessHelper(t *testing.T) {
	role := os.Getenv("OWNER_PROCESS_ROLE")
	if role == "" {
		return
	}
	fixture, err := url.Parse(os.Getenv("OWNER_BOT_FIXTURE"))
	if err != nil || fixture.Host == "" {
		os.Exit(2)
	}
	transport := http.DefaultTransport
	http.DefaultTransport = ownerRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.telegram.org" {
			return nil, errors.New("unexpected external request")
		}
		clone := r.Clone(r.Context())
		clone.URL.Scheme, clone.URL.Host = fixture.Scheme, fixture.Host
		clone.Host = fixture.Host
		clone.Header.Set("X-Owner-Instance", os.Getenv("OWNER_PROCESS_INSTANCE"))
		return transport.RoundTrip(clone)
	})
	os.Args = []string{"server", role}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "SERVICE_UNAVAILABLE")
		os.Exit(1)
	}
}

func TestRuntimeOwnerTwoProcessesServeReconcileAndRestart(t *testing.T) {
	env := testkit.Open(t)
	var botStarts atomic.Int64
	var oldExited, lossTriggered, overlap, lateOldCall atomic.Bool
	var replacementStarts atomic.Int64
	oldPollStarted := make(chan struct{}, 1)
	oldPollDone := make(chan struct{}, 1)
	bot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := filepath.Base(r.URL.Path)
		instance := r.Header.Get("X-Owner-Instance")
		if instance == "lost-old" && lossTriggered.Load() {
			lateOldCall.Store(true)
		}
		var result string
		switch method {
		case "getWebhookInfo":
			botStarts.Add(1)
			if instance == "lost-new" {
				replacementStarts.Add(1)
				if !oldExited.Load() {
					overlap.Store(true)
				}
			}
			result = `{}`
		case "getMe":
			result = `{"id":123456789,"is_bot":true,"username":"fixturebot","has_main_web_app":true}`
		case "setChatMenuButton":
			result = `true`
		case "getUpdates":
			if instance == "lost-old" {
				select {
				case oldPollStarted <- struct{}{}:
				default:
				}
				time.Sleep(500 * time.Millisecond)
				select {
				case oldPollDone <- struct{}{}:
				default:
				}
			}
			time.Sleep(50 * time.Millisecond)
			result = `[]`
		default:
			t.Errorf("unexpected Bot API method %s", method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":`+result+`}`)
	}))
	defer bot.Close()
	secret := func(name, value string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	parsed, err := url.Parse(env.Pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + env.Pool.Config().ConnConfig.Database
	databaseURL := parsed.String()
	databaseFile := secret("database", databaseURL)
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))
	mailFile, codeFile := secret("mail", key), secret("code", key)
	botFile := secret("bot", "123456789:abcdefghijklmnopqrstuvwxyz012345678")
	address := func() string {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		return listener.Addr().String()
	}
	baseEnv := []string{
		"OWNER_BOT_FIXTURE=" + bot.URL,
		"DATABASE_URL_FILE=" + databaseFile,
		"REDIS_URL_FILE=" + os.Getenv("TEST_REDIS_URL_FILE"),
		"MAIL_KEY_FILE=" + mailFile,
		"CODE_KEY_FILE=" + codeFile,
		"BOT_TOKEN_FILE=" + botFile,
		"BOT_OPERATOR_IDS=101",
		"TELEGRAM_ENABLED=true",
		"CABINET_ORIGIN=https://cabinet.example.test",
		"TERMS_VERSION=1",
		"PRIVACY_VERSION=1",
		"TRIAL_ENABLED=false",
	}
	type process struct {
		cmd *exec.Cmd
		out lockedBuffer
	}
	start := func(role, addr string, instance ...string) *process {
		t.Helper()
		p := &process{cmd: exec.Command(os.Args[0], "-test.run=^TestRuntimeOwnerProcessHelper$")}
		tag := role
		if len(instance) != 0 {
			tag = instance[0]
		}
		p.cmd.Env = append(append([]string{}, baseEnv...), "OWNER_PROCESS_ROLE="+role, "OWNER_PROCESS_INSTANCE="+tag, "LISTEN_ADDRESS="+addr)
		p.cmd.Stdout, p.cmd.Stderr = &p.out, &p.out
		if err := p.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if p.cmd.ProcessState == nil {
				_ = p.cmd.Process.Kill()
				_ = p.cmd.Wait()
			}
		})
		return p
	}
	waitExit := func(p *process) error {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- p.cmd.Wait() }()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			_ = p.cmd.Process.Kill()
			<-done
			t.Fatal("process did not exit; output:", p.out.String())
			return nil
		}
	}
	const ownerPIDQuery = `SELECT COALESCE((SELECT l.pid FROM pg_locks l WHERE l.locktype='advisory' AND l.granted AND l.mode='ExclusiveLock' AND l.database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND l.classid::bigint=((hashtextextended('operations.runtime-owner-v1',0)>>32)&4294967295) AND l.objid::bigint=(hashtextextended('operations.runtime-owner-v1',0)&4294967295) LIMIT 1),0)`
	waitOwned := func() int32 {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			var pid int32
			if err := env.Pool.QueryRow(context.Background(), ownerPIDQuery).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if pid != 0 {
				return pid
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("process did not acquire runtime ownership")
		return 0
	}
	firstAddr := address()
	first := start("serve", firstAddr)
	waitOwned()
	deadline := time.Now().Add(8 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + firstAddr + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && botStarts.Load() >= 1 {
				ready = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("serve never became ready: %s", first.out.String())
	}
	if botStarts.Load() != 1 {
		t.Fatalf("serve Bot API startup count = %d", botStarts.Load())
	}
	second := start("reconcile", address())
	if err := waitExit(second); err == nil || !strings.Contains(second.out.String(), "SERVICE_UNAVAILABLE") {
		t.Fatalf("competing reconcile did not fail closed: %v %q", err, second.out.String())
	}
	if botStarts.Load() != 1 {
		t.Fatal("competing reconcile reached Bot API")
	}
	if err := first.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitExit(first); err != nil {
		t.Fatalf("serve shutdown: %v %s", err, first.out.String())
	}
	recovery := start("reconcile", address())
	recoveryPID := waitOwned()
	time.Sleep(4 * time.Second) // Reconcile has no HTTP readiness; let its owner warm-up finish.
	var activePID int32
	if err := env.Pool.QueryRow(context.Background(), ownerPIDQuery).Scan(&activePID); err != nil || activePID != recoveryPID {
		t.Fatalf("reconcile lost owner before contention: pid=%d want=%d err=%v output=%s", activePID, recoveryPID, err, recovery.out.String())
	}
	third := start("serve", address())
	if err := waitExit(third); err == nil || !strings.Contains(third.out.String(), "SERVICE_UNAVAILABLE") {
		t.Fatalf("competing serve did not fail closed: %v %q", err, third.out.String())
	}
	if botStarts.Load() != 1 {
		t.Fatal("competing serve reached Bot API")
	}
	if err := recovery.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitExit(recovery); err != nil {
		t.Fatalf("reconcile shutdown: %v %s", err, recovery.out.String())
	}
	last := start("serve", address(), "lost-old")
	pid := waitOwned()
	select {
	case <-oldPollStarted:
	case <-time.After(8 * time.Second):
		t.Fatalf("old process did not begin its Bot API poll: starts=%d output=%s", botStarts.Load(), last.out.String())
	}
	lastDone := make(chan error, 1)
	go func() {
		err := last.cmd.Wait()
		oldExited.Store(true)
		lastDone <- err
	}()
	var terminated bool
	if err := env.Pool.QueryRow(context.Background(), `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("cannot terminate owner session: %v", err)
	}
	lossTriggered.Store(true)
	replacement := start("serve", address(), "lost-new")
	select {
	case err := <-lastDone:
		if err == nil || !strings.Contains(last.out.String(), "runtime ownership lost") {
			t.Fatalf("owner survived session loss: %v %s", err, last.out.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old process did not stop after session loss")
	}
	select {
	case <-oldPollDone:
	case <-time.After(time.Second):
		t.Fatal("old in-flight Bot API poll did not close")
	}
	if overlap.Load() || lateOldCall.Load() {
		t.Fatalf("Bot API overlap after owner loss: replacement_early=%t old_late=%t", overlap.Load(), lateOldCall.Load())
	}
	deadline = time.Now().Add(8 * time.Second)
	for replacementStarts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if replacementStarts.Load() != 1 || overlap.Load() || lateOldCall.Load() {
		t.Fatalf("replacement Bot API started with overlap: starts=%d replacement_early=%t old_late=%t", replacementStarts.Load(), overlap.Load(), lateOldCall.Load())
	}
	if err := replacement.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitExit(replacement); err != nil {
		t.Fatalf("replacement shutdown: %v %s", err, replacement.out.String())
	}
	if overlap.Load() || lateOldCall.Load() {
		t.Fatal("late Bot API call after replacement")
	}
}
