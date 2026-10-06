package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/platform"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func mailFixture(t *testing.T, maxConnections int32) (*platform.Service, *testkit.Env, platform.Config, *testkit.SMTP) {
	t.Helper()
	e := testkit.Open(t)
	pool := e.Pool
	if maxConnections > 0 {
		cfg := e.Pool.Config().Copy()
		cfg.MaxConns = maxConnections
		var err error
		pool, err = pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
	}
	queue, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	smtp := testkit.MailServer(t)
	cfg := platform.Config{CabinetOrigin: "https://cabinet.example.test", TermsVersion: "1", PrivacyVersion: "1", RateNamespace: uuid.NewString(), MailKey: bytes.Repeat([]byte{1}, 32), CodeKey: bytes.Repeat([]byte{2}, 32), SMTPAddress: smtp.Address, SMTPRootCAs: smtp.Roots, SMTPFrom: "sender@example.test"}
	return NewService(pool, e.Redis, queue, cfg), e, cfg, smtp
}
func pendingMail(t *testing.T, s *platform.Service, e *testkit.Env, cfg platform.Config, email string) (uuid.UUID, uuid.UUID, string, string) {
	t.Helper()
	r, err := s.Register(context.Background(), wire.RegisterInput{Email: openapi_types.Email(email), Locale: "en", AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	id, token, code := testkit.MailSecrets(t, e.Pool, cfg.MailKey, r.ChallengeId)
	return r.ChallengeId, id, token, code
}
func mailSnapshot(t *testing.T, e *testkit.Env, id uuid.UUID) string {
	t.Helper()
	var snapshot string
	if err := e.Pool.QueryRow(context.Background(), `SELECT row_to_json(m)::text FROM mail_deliveries m WHERE id=$1`, id).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// A legacy encrypted row and persisted River args must be consumable without reissue.
func TestMailDeliveryPersistedCompatibility(t *testing.T) {
	s, e, cfg, smtp := mailFixture(t, 0)
	ctx := context.Background()
	_, id, token, code := pendingMail(t, s, e, cfg, "legacy-mail@example.test")
	block, _ := aes.NewCipher(cfg.MailKey)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize()) // Fixed nonce is only for this isolated legacy fixture.
	plain := []byte(fmt.Sprintf(`{"Type":"verify","Locale":"en","Token":"%s","Code":"%s"}`, token, code))
	encrypted := gcm.Seal(nonce, nonce, plain, []byte(id.String()))
	if _, err := e.Pool.Exec(ctx, `UPDATE mail_deliveries SET ciphertext=$2 WHERE id=$1`, id, encrypted); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := e.Pool.QueryRow(ctx, `SELECT id FROM river_job WHERE kind='mail_delivery' AND args->>'delivery_id'=$1`, id.String()).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	oldArgs := []byte(fmt.Sprintf(`{"delivery_id":"%s"}`, id))
	if _, err := e.Pool.Exec(ctx, `UPDATE river_job SET args=$2::jsonb,max_attempts=5 WHERE id=$1`, jobID, oldArgs); err != nil {
		t.Fatal(err)
	}
	var args notifications.MailArgs
	if json.Unmarshal(oldArgs, &args) != nil || args.DeliveryID != id {
		t.Fatal("legacy args lost delivery identity")
	}
	worker := notifications.MailWorker{Service: s.MailDelivery()}
	if err := worker.Work(ctx, &river.Job[notifications.MailArgs]{Args: args}); err != nil {
		t.Fatal("legacy worker mail", err)
	}
	letters := smtp.Letters()
	if len(letters) != 1 || !strings.Contains(letters[0], "/verify-email#token="+token) || !strings.Contains(letters[0], code) {
		t.Fatal("legacy plaintext/link/code incompatible")
	}
	snapshot := mailSnapshot(t, e, id)
	for _, send := range []func(context.Context, uuid.UUID) error{s.MailDelivery().SendMail, s.SendMail} {
		if err := send(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if mailSnapshot(t, e, id) != snapshot || len(smtp.Letters()) != 1 {
		t.Fatal("completed replay changed row or resent mail")
	}
	_, bad, _, _ := pendingMail(t, s, e, cfg, "bad-cipher@example.test")
	if _, err := e.Pool.Exec(ctx, `UPDATE mail_deliveries SET ciphertext=$2 WHERE id=$1`, bad, []byte{1}); err != nil {
		t.Fatal(err)
	}
	before := mailSnapshot(t, e, bad)
	if err := s.MailDelivery().SendMail(ctx, bad); err == nil || err.Error() != "SERVICE_UNAVAILABLE" {
		t.Fatal("bad ciphertext accepted")
	}
	if mailSnapshot(t, e, bad) != before || len(smtp.Letters()) != 1 {
		t.Fatal("failed decrypt lost secret or sent mail")
	}
	if err := s.MailDelivery().SendMail(ctx, uuid.New()); err != nil {
		t.Fatal("absent delivery not noop")
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var mails, jobs int
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM mail_deliveries),(SELECT count(*) FROM river_job)`).Scan(&mails, &jobs); err != nil {
		t.Fatal(err)
	}
	err = s.MailDelivery().EnqueueMailTx(ctx, tx, "notice@example.test", nil, nil, "security_notice", notifications.MailPayload{Type: "security_notice", Locale: "en"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var inMails, inJobs int
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM mail_deliveries),(SELECT count(*) FROM river_job)`).Scan(&inMails, &inJobs); err != nil {
		t.Fatal(err)
	}
	if inMails != mails+1 || inJobs != jobs+1 {
		t.Fatal("mail/job not in caller transaction")
	}
	tx.Rollback(ctx)
	if err = e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mail_deliveries),(SELECT count(*) FROM river_job)`).Scan(&inMails, &inJobs); err != nil {
		t.Fatal(err)
	}
	if inMails != mails || inJobs != jobs {
		t.Fatal("mail/job survived caller rollback")
	}
}

// Cancellation must release the recipient session guard without clearing unsent mail.
func TestMailDeliveryGuard(t *testing.T) {
	s, e, cfg, smtp := mailFixture(t, 0)
	_, id, _, _ := pendingMail(t, s, e, cfg, "cancel-mail@example.test")
	before := mailSnapshot(t, e, id)
	entered, release := smtp.HoldNextData()
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.MailDelivery().SendMail(ctx, id) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP not entered")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled SMTP succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled SMTP/guard stuck")
	}
	if mailSnapshot(t, e, id) != before {
		t.Fatal("cancelled SMTP cleared unsent secret")
	}
	probe, err := e.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Rollback(context.Background())
	var unlocked bool
	if err = probe.QueryRow(context.Background(), `SELECT pg_try_advisory_xact_lock(hashtextextended($1::text,0))`, "cancel-mail@example.test").Scan(&unlocked); err != nil || !unlocked {
		t.Fatal("recipient guard leaked into pool")
	}
}

// All worker transactions must use its dedicated connection when the pool has one slot.
func TestMailDeliverySingleConnection(t *testing.T) {
	s, e, cfg, smtp := mailFixture(t, 1)
	_, id, _, _ := pendingMail(t, s, e, cfg, "single-mail@example.test")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.MailDelivery().SendMail(ctx, id); err != nil {
		t.Fatal("single-connection worker deadlock", err)
	}
	if len(smtp.Letters()) != 1 {
		t.Fatal("single-connection delivery missing")
	}
}
