package httpapi

import (
	"bytes"
	"context"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"testing"
	"time"
)

// A batch must lock every account before taking recipients shared with another account.
func TestRegressionLegacyApprovalMailLockOrder(t *testing.T) {
	s, e := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	ids := []uuid.UUID{verified(t, s, e, "batch-a@example.test"), verified(t, s, e, "batch-b@example.test")}
	emails := []string{"batch-a@example.test", "batch-b@example.test"}
	if bytes.Compare(ids[0][:], ids[1][:]) > 0 {
		ids[0], ids[1] = ids[1], ids[0]
		emails[0], emails[1] = emails[1], emails[0]
	}
	var lastSession string
	for i, id := range ids {
		_, raw, err := s.login(ctx, wire.LoginInput{Email: signup(emails[i]).Email, Password: "my long safe password ✨"}, "127.0.0.1")
		if err != nil {
			t.Fatal("batch session", status(err))
		}
		emailPair(t, s, e, raw, "batch-shared@example.test")
		if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=$2,legacy_user_id=$3 WHERE id=$1`, id, int64(2401+i), int64(3401+i)); err != nil {
			t.Fatal(err)
		}
		lastSession = raw
	}
	blocker, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,0))`, "batch-shared@example.test"); err != nil {
		t.Fatal(err)
	}
	waitBlocked := func(want int) {
		t.Helper()
		for {
			var blocked int
			if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND cardinality(pg_blocking_pids(pid))>0`).Scan(&blocked); err != nil {
				t.Fatal("batch lock barrier", err)
			}
			if blocked >= want {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("batch lock barrier timed out")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	pkg := accounts.LegacyApprovalPackage{Version: 1, Users: []accounts.LegacyApprovalUser{
		{SourceLegacyUserID: 3401, SourceTgID: 2401, Status: "rejected"},
		{SourceLegacyUserID: 3402, SourceTgID: 2402, Status: "approved"},
	}, ApprovalEvents: []accounts.LegacyApprovalSourceEvent{}}
	imported, cancelled := make(chan error, 1), make(chan error, 1)
	go func() { _, err := s.importLegacyApprovals(ctx, pkg, false); imported <- err }()
	waitBlocked(1) // Import waits on the controlled shared recipient.
	go func() { cancelled <- s.cancelEmailChange(ctx, lastSession) }()
	waitBlocked(2) // Cancel holds/waits for B; releasing C exposes the old C → B cycle.
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, done := range []<-chan error{imported, cancelled} {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("legacy batch/cancel deadlocked or failed: status %d", status(err))
			}
		case <-ctx.Done():
			t.Fatal("legacy batch/cancel did not finish")
		}
	}
	if t.Failed() {
		return
	}
	var snapshots, activeProofs, pendingPayloads int
	if err := e.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM legacy_approval_snapshots WHERE account_id IN ($1,$2)),
		(SELECT count(*) FROM credential_challenges WHERE account_id IN ($1,$2) AND NOT revoked),
		(SELECT count(*) FROM mail_deliveries m JOIN credential_challenges c ON c.id=m.credential_challenge_id WHERE c.account_id IN ($1,$2) AND m.ciphertext IS NOT NULL)`, ids[0], ids[1]).Scan(&snapshots, &activeProofs, &pendingPayloads); err != nil {
		t.Fatal(err)
	}
	if snapshots != 2 || activeProofs != 0 || pendingPayloads != 0 {
		t.Fatal("batch snapshot or credential revocation was not committed atomically")
	}
}
