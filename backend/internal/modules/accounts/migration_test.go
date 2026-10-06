package accounts

import (
	"bytes"
	"context"
	"example.com/cabinet/backend/db"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"os"
	"testing"
	"time"
)

func sessionCount(t *testing.T, e *testkit.Env) int {
	t.Helper()
	var n int
	if err := e.Pool.QueryRow(context.Background(), "SELECT count(*) FROM sessions").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// An additive migration that destroys old sessions, encrypted letters or job identity fails here.
func TestAccountSecurityMigration(t *testing.T) {
	s, e, cfg := fixture(t)
	ctx := context.Background()
	database := stdlib.OpenDBFromPool(e.Pool)
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("../../../db/migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 5); err != nil {
		t.Fatal("empty isolated pre-security schema")
	}
	q := store.New(e.Pool)
	id, proof := uuid.New(), uuid.New()
	now := e.Clock()
	raw, token := opaque(), opaque()
	hash, err := s.hashPassword(ctx, "my long safe password ✨")
	if err != nil {
		t.Fatal(err)
	}
	// The fixture is intentionally at migration 5; current generated inserts use later columns.
	if _, err = e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, "migration@example.test", "en", hash, now, uuid.New(), "0123456789abcdef", "acct_"+id.String(), "1", "1"); err != nil {
		t.Fatal(err)
	}
	if err = q.AddSession(ctx, store.AddSessionParams{IDHash: digest(raw), AccountID: id, CsrfToken: opaque(), CreatedAt: stamp(now), LastSeen: stamp(now), AbsoluteExpiresAt: stamp(now.Add(30 * 24 * time.Hour))}); err != nil {
		t.Fatal(err)
	}
	if err = q.AddChallenge(ctx, store.AddChallengeParams{ID: proof, EmailKey: "pending@example.test", Locale: "en", TermsVersion: "1", PrivacyVersion: "1", TokenHash: digest(token), CodeHash: s.codeDigest(proof, "12345678"), CreatedAt: stamp(now), TokenExpiresAt: stamp(now.Add(24 * time.Hour)), CodeExpiresAt: stamp(now.Add(10 * time.Minute))}); err != nil {
		t.Fatal(err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = s.enqueueMail(ctx, tx, "pending@example.test", &proof, mailPayload{Type: "verify", Locale: "en", Token: token, Code: "12345678"}); err != nil || tx.Commit(ctx) != nil {
		t.Fatal("old mail/job seed")
	}
	var delivery uuid.UUID
	var before []byte
	var jobID int64
	e.Pool.QueryRow(ctx, `SELECT id,ciphertext FROM mail_deliveries`).Scan(&delivery, &before)
	e.Pool.QueryRow(ctx, `SELECT id FROM river_job WHERE kind='mail_delivery'`).Scan(&jobID)
	for range 2 {
		if err = db.Migrate(ctx, e.Pool); err != nil {
			t.Fatal(err)
		}
	}
	var after []byte
	var kind string
	var version int64
	var afterJob int64
	e.Pool.QueryRow(ctx, `SELECT ciphertext,kind FROM mail_deliveries WHERE id=$1`, delivery).Scan(&after, &kind)
	e.Pool.QueryRow(ctx, `SELECT credential_version FROM accounts WHERE id=$1`, id).Scan(&version)
	e.Pool.QueryRow(ctx, `SELECT id FROM river_job WHERE kind='mail_delivery'`).Scan(&afterJob)
	if !bytes.Equal(before, after) || kind != "registration" || version != 0 || afterJob != jobID || sessionCount(t, e) != 1 {
		t.Fatal("migration changed registration data/jobs")
	}
	sent := 0
	mail := notifications.NewMail(e.Pool, nil, func() notifications.MailConfig {
		return notifications.MailConfig{CabinetOrigin: "https://cabinet.example.test", MailKey: cfg.MailKey, Now: e.Clock}
	}, s.WithMailGuard, s.MailProofValidTx, func(context.Context, string, string, string) error { sent++; return nil })
	if err = mail.SendMail(ctx, delivery); err != nil || sent != 1 {
		t.Fatal("old job mail compatibility")
	}
}
