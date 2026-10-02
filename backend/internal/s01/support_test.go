package s01

import (
	"bytes"
	"context"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"sync"
	"testing"
	"time"
)

func supportActors(t *testing.T) (*Service, *testkit.Env, uuid.UUID, uuid.UUID) {
	t.Helper()
	s, e := fixture(t)
	customer := verified(t, s, e, "support-customer@example.test")
	operator := verified(t, s, e, "support-operator@example.test")
	if _, err := e.Pool.Exec(context.Background(), `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, operator, e.Clock()); err != nil {
		t.Fatal(err)
	}
	return s, e, customer, operator
}

func TestSupportConversationFlow(t *testing.T) {
	s, e, customer, operator := supportActors(t)
	ctx := context.Background()
	initial, err := s.Support(ctx, customer, customer, false)
	if err != nil || initial.Conversation != nil || len(initial.Messages) != 0 {
		t.Fatal("initial support", err)
	}
	file := []byte("private fixture")
	key := uuid.New()
	first, created, err := s.CreateSupportMessage(ctx, customer, customer, false, key, "hello", "note.txt", file)
	if err != nil || !created || first.Sequence < 1 || first.Attachment == nil || first.Delivery != wire.Stored {
		t.Fatal("first message", err)
	}
	replay, created, err := s.CreateSupportMessage(ctx, customer, customer, false, key, "hello", "note.txt", file)
	if err != nil || created || replay.Id != first.Id {
		t.Fatal("lost response replay", err)
	}
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, key, "changed", "note.txt", file); status(err) != 409 {
		t.Fatal("changed payload replay")
	}
	view, err := s.Support(ctx, operator, customer, true)
	if err != nil || len(view.Messages) != 1 || view.Messages[0].Delivery != wire.Stored {
		t.Fatal("operator view", err)
	}
	if err := s.AcknowledgeSupport(ctx, operator, customer, true, first.Sequence); err != nil {
		t.Fatal("recipient ack", err)
	}
	view, err = s.Support(ctx, customer, customer, false)
	if err != nil || view.Messages[0].Delivery != wire.Delivered {
		t.Fatal("delivery only after ack", err)
	}
	if err := s.AcknowledgeSupport(ctx, customer, customer, false, first.Sequence); status(err) != 409 {
		t.Fatal("sender self ack")
	}
	if err := s.SetSupportState(ctx, customer, customer, false, "closed"); err != nil {
		t.Fatal(err)
	}
	answer, _, err := s.CreateSupportMessage(ctx, operator, customer, true, uuid.New(), "reply", "", nil)
	if err != nil || answer.Sender != "operator" {
		t.Fatal("operator reply", err)
	}
	view, err = s.Support(ctx, customer, customer, false)
	if err != nil || view.Conversation.Status != "open" || len(view.Messages) != 2 {
		t.Fatal("auto reopen", err)
	}
	name, body, err := s.SupportAttachment(ctx, customer, first.Id)
	if err != nil || name != "note.txt" || !bytes.Equal(body, file) {
		t.Fatal("own attachment", err)
	}
	foreign := verified(t, s, e, "support-foreign@example.test")
	if _, _, err := s.SupportAttachment(ctx, foreign, first.Id); status(err) != 404 {
		t.Fatal("foreign attachment")
	}
}

func TestSupportBanRoleAndQuota(t *testing.T) {
	s, _, customer, operator := supportActors(t)
	ctx := context.Background()
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "hello", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET vpn_banned=true WHERE id=$1`, customer); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSupportBan(ctx, operator, customer, true, "abuse"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "blocked", "", nil); status(err) != 403 {
		t.Fatal("support ban not independent")
	}
	if _, err := s.Support(ctx, customer, customer, false); err != nil {
		t.Fatal("banned history unavailable")
	}
	if _, _, err := s.CreateSupportMessage(ctx, operator, customer, true, uuid.New(), "explanation", "", nil); err != nil {
		t.Fatal("operator explanation", err)
	}
	if err := s.SetSupportBan(ctx, operator, customer, false, "resolved"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "back", "", nil); err != nil {
		t.Fatal("support unban changed VPN", err)
	}
	var vpnBanned bool
	if err := s.pool.QueryRow(ctx, `SELECT vpn_banned FROM accounts WHERE id=$1`, customer).Scan(&vpnBanned); err != nil || !vpnBanned {
		t.Fatal("support ban changed VPN", err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM operator_accounts WHERE account_id=$1`, operator); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Support(ctx, operator, customer, true); status(err) != 403 {
		t.Fatal("revoked role read")
	}
	if _, _, err := s.CreateSupportMessage(ctx, operator, customer, true, uuid.New(), "stale role", "", nil); status(err) != 403 {
		t.Fatal("revoked role write")
	}
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "", "bad/name", []byte{1}); status(err) != 400 {
		t.Fatal("unsafe filename")
	}
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "", "x", make([]byte, 10*1024*1024+1)); status(err) != 413 {
		t.Fatal("oversize file")
	}
}

func TestSupportConcurrentDuplicateAndPaging(t *testing.T) {
	s, _, customer, operator := supportActors(t)
	ctx := context.Background()
	key := uuid.New()
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, _, err := s.CreateSupportMessage(ctx, customer, customer, false, key, "once", "", nil)
			if err == nil {
				ids <- v.Id
			}
		}()
	}
	wg.Wait()
	close(ids)
	var first uuid.UUID
	for id := range ids {
		if first != uuid.Nil && first != id {
			t.Fatal("duplicate message")
		}
		first = id
	}
	if first == uuid.Nil {
		t.Fatal("no message")
	}
	for i := 0; i < 50; i++ {
		if i == 28 {
			s.now = func() time.Time { return time.Now().Add(16 * time.Minute) }
		}
		if _, _, err := s.CreateSupportMessage(ctx, operator, customer, true, uuid.New(), "page", "", nil); err != nil {
			t.Fatal("paging setup", err)
		}
	}
	latest, err := s.Support(ctx, customer, customer, false)
	if err != nil || len(latest.Messages) != 50 || !latest.HasMore || latest.OldestSequence == nil {
		t.Fatal("latest page", err)
	}
	older, err := s.SupportHistory(ctx, customer, customer, false, *latest.OldestSequence)
	if err != nil || len(older.Messages) != 1 || older.Messages[0].Id != first {
		t.Fatal("previous page", err)
	}
}

func TestSupportQuotaAndRate(t *testing.T) {
	s, _, customer, _ := supportActors(t)
	ctx := context.Background()
	file := bytes.Repeat([]byte{7}, 10*1024*1024)
	for i := 0; i < 5; i++ {
		if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "", "chunk.bin", file); err != nil {
			t.Fatal("quota setup", err)
		}
	}
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "", "extra.bin", []byte{1}); status(err) != 413 {
		t.Fatal("conversation quota", err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM support_messages`).Scan(&count); err != nil || count != 5 {
		t.Fatal("quota rollback", err)
	}
	for i := 0; i < 25; i++ {
		if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "text", "", nil); err != nil {
			t.Fatal("rate setup", err)
		}
	}
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "too many", "", nil); status(err) != 429 {
		t.Fatal("rate limit", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM support_messages`).Scan(&count); err != nil || count != 30 {
		t.Fatal("rate rollback", err)
	}
}

func TestSupportRevocationAndBanWinBlockedWrite(t *testing.T) {
	s, _, customer, operator := supportActors(t)
	ctx := context.Background()
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "first", "", nil); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, operator); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(start)
		_, _, writeErr := s.CreateSupportMessage(ctx, operator, customer, true, uuid.New(), "revoked", "", nil)
		done <- writeErr
	}()
	<-start
	waitSupportAccountLock(t, s)
	if _, err = tx.Exec(ctx, `DELETE FROM operator_accounts WHERE account_id=$1`, operator); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; status(err) != 403 {
		t.Fatal("revoked write", err)
	}

	tx, err = s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, customer); err != nil {
		t.Fatal(err)
	}
	start = make(chan struct{})
	done = make(chan error, 1)
	go func() {
		close(start)
		_, _, writeErr := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "banned", "", nil)
		done <- writeErr
	}()
	<-start
	waitSupportAccountLock(t, s)
	if _, err = tx.Exec(ctx, `UPDATE support_conversations SET support_banned=true WHERE account_id=$1`, customer); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; status(err) != 403 {
		t.Fatal("banned write", err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM support_messages`).Scan(&count); err != nil || count != 1 {
		t.Fatal("racing write stored", err)
	}
}

func TestSupportRedisOutageFailsClosed(t *testing.T) {
	s, e, customer, _ := supportActors(t)
	if err := e.Redis.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateSupportMessage(context.Background(), customer, customer, false, uuid.New(), "not stored", "", nil); status(err) != 503 {
		t.Fatal("rate limiter outage did not fail closed", err)
	}
	var count int
	if err := e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM support_messages`).Scan(&count); err != nil || count != 0 {
		t.Fatal("message persisted on limiter outage", err)
	}
}

func TestSupportTextNULRejectedBeforeStorage(t *testing.T) {
	s, e, customer, operator := supportActors(t)
	for _, tc := range []struct {
		actor    uuid.UUID
		operator bool
		text     string
	}{
		{customer, false, "before\x00after"},
		{operator, true, "\x00"},
	} {
		if _, _, err := s.CreateSupportMessage(context.Background(), tc.actor, customer, tc.operator, uuid.New(), tc.text, "", nil); status(err) != 400 {
			t.Fatal("NUL text must be invalid input", err)
		}
	}
	var count int
	if err := e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM support_messages`).Scan(&count); err != nil || count != 0 {
		t.Fatal("NUL text reached storage", err)
	}
}

func TestSupportNewMessageRefreshesConversationTimestamp(t *testing.T) {
	s, e, customer, _ := supportActors(t)
	ctx := context.Background()
	if _, _, err := s.CreateSupportMessage(ctx, customer, customer, false, uuid.New(), "first", "", nil); err != nil {
		t.Fatal(err)
	}
	first, err := s.Support(ctx, customer, customer, false)
	if err != nil || first.Conversation == nil {
		t.Fatal("first conversation", err)
	}
	e.Advance(time.Minute)
	key := uuid.New()
	if _, _, err = s.CreateSupportMessage(ctx, customer, customer, false, key, "second", "", nil); err != nil {
		t.Fatal(err)
	}
	second, err := s.Support(ctx, customer, customer, false)
	if err != nil || second.Conversation == nil || !second.Conversation.UpdatedAt.After(first.Conversation.UpdatedAt) {
		t.Fatal("new message did not refresh updated_at", err)
	}
	e.Advance(time.Minute)
	if _, created, err := s.CreateSupportMessage(ctx, customer, customer, false, key, "second", "", nil); err != nil || created {
		t.Fatal("replay", err)
	}
	replayed, err := s.Support(ctx, customer, customer, false)
	if err != nil || !replayed.Conversation.UpdatedAt.Equal(second.Conversation.UpdatedAt) {
		t.Fatal("replay changed updated_at", err)
	}
}

func waitSupportAccountLock(t *testing.T, s *Service) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		err := s.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%accounts%')`).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("write did not wait for account lock")
}
