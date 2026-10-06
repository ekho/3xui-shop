package platform

import (
	"context"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/testkit"
)

// Holding a SQL transaction during the real SMTP DATA handshake fails here.
func TestMailDeliveryNoTransaction(t *testing.T) {
	s, e := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	account := verified(t, s, e, "network-mail@example.test")
	_, delivery, _, _ := resetProof(t, s, e, "network-mail@example.test")
	smtp := testkit.MailServer(t)
	s.cfg.SMTPAddress, s.cfg.SMTPRootCAs, s.cfg.SMTPFrom = smtp.Address, smtp.Roots, "sender@example.test"
	entered, release := smtp.HoldNextData()
	defer release()
	sent := make(chan error, 1)
	go func() { sent <- s.SendMail(ctx, delivery) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("SMTP did not reach DATA")
	}
	var transactions int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND xact_start IS NOT NULL`).Scan(&transactions); err != nil {
		t.Fatal(err)
	}
	if transactions != 0 {
		t.Errorf("SMTP holds %d SQL transaction(s)", transactions)
	}
	probe, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = probe.Exec(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE NOWAIT`, account)
	probe.Rollback(ctx)
	if err != nil {
		t.Error("SMTP holds account row lock")
	}
	release()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatal("SMTP send failed", status(err))
		}
	case <-ctx.Done():
		t.Fatal("SMTP worker did not finish")
	}
}
