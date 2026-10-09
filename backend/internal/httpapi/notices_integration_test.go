package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/testkit"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sendNotice(t *testing.T, s *compositionFixture, client, operator supportSession, body string) notifications.NoticePreview {
	t.Helper()
	p, err := s.Notices.Preview(context.Background(), operator.id, notifications.NoticePreviewInput{Mode: "send", Audience: "personal", AccountID: client.id, Body: body, Reason: "Owned delivery fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Notices.Confirm(context.Background(), operator.id, p.ID); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNoticeAuthorityAndRecipientGuards(t *testing.T) {
	for _, mode := range []string{"role", "binding", "credential", "restricted", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			_, s, e, _, _, client, operator := noticeFixture(t, 1)
			ctx := context.Background()
			if mode == "disabled" {
				a, _, err := s.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 733, DisplayName: "Owned notice quarantine", Locale: "en"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
				if err != nil {
					t.Fatal(err)
				}
				client.id = a.Account.ID
			}
			if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=733 WHERE id=$1`, client.id); err != nil {
				t.Fatal(err)
			}
			sendNotice(t, s, client, operator, "<b>Owned guard</b>")
			job, err := s.Notifications.ClaimClient(ctx)
			if err != nil || job == nil {
				t.Fatal("claim", err)
			}
			query := map[string]string{"role": `DELETE FROM operator_accounts WHERE account_id=$1`, "binding": `UPDATE accounts SET telegram_id=734 WHERE id=$1`, "credential": `UPDATE accounts SET credential_version=credential_version+1 WHERE id=$1`, "restricted": `UPDATE accounts SET restricted=true WHERE id=$1`, "disabled": `UPDATE accounts SET telegram_login_disabled=true WHERE id=$1`}[mode]
			target := client.id
			if mode == "role" {
				target = operator.id
			}
			if _, err = e.Pool.Exec(ctx, query, target); err != nil {
				t.Fatal(err)
			}
			calls := 0
			if err = s.Notifications.DeliverClient(ctx, *job, func() (notifications.ClientOutcome, error) {
				calls++
				return notifications.ClientOutcome{State: "sent", MessageID: 42}, nil
			}); err != nil {
				t.Fatal(err)
			}
			var state string
			if e.Pool.QueryRow(ctx, `SELECT telegram_state FROM notice_actions WHERE id=$1`, job.NoticeActionID).Scan(&state) != nil || calls != 0 || state != "skipped" {
				t.Fatal("stale role/recipient reached transport", calls, state)
			}
		})
	}
}

func TestNoticeSortedPairLocks(t *testing.T) {
	_, s, e, _, _, client, operator := noticeFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,now())`, client.id); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		start, done := make(chan struct{}), make(chan error, 2)
		for _, ids := range [][2]uuid.UUID{{operator.id, client.id}, {client.id, operator.id}} {
			go func(actor, target uuid.UUID) {
				<-start
				tx, err := s.pool.Begin(ctx)
				if err == nil {
					defer tx.Rollback(context.Background())
					_, allowed, lockErr := s.Accounts.LockNoticePairTx(ctx, tx, actor, target)
					err = lockErr
					if err == nil && !allowed {
						err = errors.New("owned operator pair denied")
					}
					if err == nil {
						err = tx.Commit(ctx)
					}
				}
				done <- err
			}(ids[0], ids[1])
		}
		close(start)
		for range 2 {
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("reverse actor/target pair failed", err)
				}
			case <-ctx.Done():
				t.Fatal("reverse actor/target pair deadlocked")
			}
		}
	}
}

func TestNoticeChannelOutcomes(t *testing.T) {
	for _, mode := range []string{"sent", "lost", "forbidden", "rate-limit"} {
		t.Run(mode, func(t *testing.T) {
			_, s, e, cfg, _, client, operator := noticeFixture(t, 1)
			ctx := context.Background()
			if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=733 WHERE id=$1`, client.id); err != nil {
				t.Fatal(err)
			}
			p := sendNotice(t, s, client, operator, "<b>Owned channel</b>")
			job, err := s.Notifications.ClaimClient(ctx)
			if err != nil || job == nil {
				t.Fatal("claim", err)
			}
			calls := 0
			if err = s.Notifications.DeliverClient(ctx, *job, func() (notifications.ClientOutcome, error) {
				calls++
				switch mode {
				case "lost":
					return notifications.ClientOutcome{}, errors.New("owned ACK loss")
				case "forbidden":
					return notifications.ClientOutcome{State: "failed", Code: "forbidden"}, nil
				case "rate-limit":
					return notifications.ClientOutcome{State: "retry", RetryAfter: time.Hour}, nil
				}
				job.NoticeResult.MessageAt = time.Now()
				return notifications.ClientOutcome{State: "sent", MessageID: 42}, nil
			}); err != nil {
				t.Fatal("notice result should be durably classified", err)
			}
			result, err := s.Notices.Confirm(ctx, operator.id, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("wrong wire attempts", calls)
			}
			if mode == "sent" && result.Telegram.Succeeded != 1 || mode == "lost" && result.Telegram.Unknown != 1 || mode == "forbidden" && result.Telegram.Failed != 1 || mode == "rate-limit" && result.Telegram.Pending != 1 {
				t.Fatal("unproven or rejected effect misclassified", result.Telegram)
			}
			if mode == "lost" {
				edit, err := s.Notices.Preview(ctx, operator.id, notifications.NoticePreviewInput{Mode: "edit", ExpectedRevision: 1, Body: "Replacement must not send", Reason: "Owned lost ACK"})
				if err != nil {
					t.Fatal(err)
				}
				out, err := s.Notices.Confirm(ctx, operator.id, edit.ID)
				if err != nil || out.Telegram.Unknown != 1 {
					t.Fatal("edit guessed missing message proof", out.Telegram, err)
				}
				var n int
				if e.Pool.QueryRow(ctx, `SELECT count(*) FROM client_telegram_deliveries`).Scan(&n) != nil || n != 1 {
					t.Fatal("unknown first send replaced", n)
				}
				restarted := app.NewModules(s.pool, e.Redis, nil, &cfg)
				original, err := restarted.Notices.Confirm(ctx, operator.id, p.ID)
				if err != nil || original.Telegram.Unknown != 1 {
					t.Fatal("restart lost original uncertain outcome", err)
				}
				if recovered, err := restarted.Notifications.ClaimClient(ctx); err != nil || recovered != nil {
					t.Fatal("restart repeated uncertain terminal send", err)
				}
			}
			if mode == "rate-limit" {
				for range 4 {
					if _, err = e.Pool.Exec(ctx, `UPDATE client_telegram_deliveries SET available_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
						t.Fatal(err)
					}
					job, err = s.Notifications.ClaimClient(ctx)
					if err != nil || job == nil {
						t.Fatal("retry claim", err)
					}
					if err = s.Notifications.DeliverClient(ctx, *job, func() (notifications.ClientOutcome, error) {
						calls++
						return notifications.ClientOutcome{State: "retry", RetryAfter: time.Hour}, nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				result, err = s.Notices.Confirm(ctx, operator.id, p.ID)
				if err != nil || calls != 5 || result.Telegram.Failed != 1 {
					t.Fatal("new notice wire attempt cap failed", calls, result.Telegram, err)
				}
				if recovered, err := s.Notifications.ClaimClient(ctx); err != nil || recovered != nil {
					t.Fatal("sixth notice attempt allowed", err)
				}
			}
		})
	}
}

func TestNoticeRecoveryBeforeOutcomeCommit(t *testing.T) {
	for _, retries := range []int{0, 4} {
		t.Run(string(rune('1'+retries)), func(t *testing.T) {
			_, s, e, cfg, _, client, operator := noticeFixture(t, 1)
			ctx := context.Background()
			if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=733 WHERE id=$1`, client.id); err != nil {
				t.Fatal(err)
			}
			p := sendNotice(t, s, client, operator, "Provider copy without committed outcome")
			for range retries {
				job, err := s.Notifications.ClaimClient(ctx)
				if err != nil || job == nil {
					t.Fatal("confirmed429 claim", err)
				}
				if err = s.Notifications.DeliverClient(ctx, *job, func() (notifications.ClientOutcome, error) {
					return notifications.ClientOutcome{State: "retry", RetryAfter: time.Hour}, nil
				}); err != nil {
					t.Fatal(err)
				}
				if _, err = e.Pool.Exec(ctx, `UPDATE client_telegram_deliveries SET available_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			job, err := s.Notifications.ClaimClient(ctx)
			if err != nil || job == nil {
				t.Fatal("effect claim", err)
			}
			effects := 0
			wireCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if err = s.Notifications.DeliverClient(wireCtx, *job, func() (notifications.ClientOutcome, error) {
				effects++
				job.NoticeResult.MessageAt = time.Now()
				cancel() // Provider accepted the message, then the finish transaction cannot commit.
				return notifications.ClientOutcome{State: "sent", MessageID: 42}, nil
			}); err == nil {
				t.Fatal("canceled finish unexpectedly committed")
			}
			var state string
			if e.Pool.QueryRow(ctx, `SELECT state FROM client_telegram_deliveries WHERE id=$1`, job.ID).Scan(&state) != nil || state != "pending" {
				t.Fatal("fixture did not retain pending recovery window", state)
			}
			if _, err = e.Pool.Exec(ctx, `UPDATE client_telegram_deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
				t.Fatal(err)
			}
			restarted := app.NewModules(s.pool, e.Redis, nil, &cfg)
			recovered, err := restarted.Notifications.ClaimClient(ctx)
			if err != nil || recovered == nil {
				t.Fatal("uncertain job could not settle after lease expiry", err)
			}
			if err = restarted.Notifications.DeliverClient(ctx, *recovered, func() (notifications.ClientOutcome, error) {
				effects++
				recovered.NoticeResult.MessageAt = time.Now()
				return notifications.ClientOutcome{State: "sent", MessageID: 43}, nil
			}); err != nil {
				t.Fatal(err)
			}
			result, err := restarted.Notices.Confirm(ctx, operator.id, p.ID)
			if err != nil || effects != 1 || result.Telegram.Unknown != 1 {
				t.Fatal("uncertain provider effect repeated or hidden", effects, result.Telegram, err)
			}
			if next, err := restarted.Notifications.ClaimClient(ctx); err != nil || next != nil {
				t.Fatal("uncertain recovery did not settle", err)
			}
		})
	}
}

func TestNoticeCloseProofAndAge(t *testing.T) {
	for _, mode := range []string{"success", "foreign", "message", "credential", "age", "failure"} {
		t.Run(mode, func(t *testing.T) {
			_, s, e, _, _, client, operator := noticeFixture(t, 1)
			ctx := context.Background()
			if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=733 WHERE id=$1`, client.id); err != nil {
				t.Fatal(err)
			}
			p := sendNotice(t, s, client, operator, "Owned close proof")
			job, err := s.Notifications.ClaimClient(ctx)
			if err != nil || job == nil {
				t.Fatal(err)
			}
			if err = s.Notifications.DeliverClient(ctx, *job, func() (notifications.ClientOutcome, error) {
				job.NoticeResult.MessageAt = time.Now()
				if mode == "age" {
					job.NoticeResult.MessageAt = job.NoticeResult.MessageAt.Add(-49 * time.Hour)
				}
				return notifications.ClientOutcome{State: "sent", MessageID: 42}, nil
			}); err != nil {
				t.Fatal(err)
			}
			var hash []byte
			if e.Pool.QueryRow(ctx, `SELECT result_hash FROM client_telegram_deliveries WHERE id=$1`, job.ID).Scan(&hash) != nil {
				t.Fatal("missing sent hash")
			}
			if mode == "age" {
				preview, err := s.Notices.Preview(ctx, operator.id, notifications.NoticePreviewInput{Mode: "delete", ExpectedRevision: 1, Reason: "Owned age preview"})
				if err != nil || preview.TelegramCount != 0 {
					t.Fatal("preview promised unavailable deletion before close", preview.TelegramCount, err)
				}
			}
			tg, message := int64(733), int64(42)
			if mode == "foreign" {
				tg = 734
			}
			if mode == "message" {
				message = 43
			}
			if mode == "credential" {
				if _, err = e.Pool.Exec(ctx, `UPDATE accounts SET credential_version=credential_version+1 WHERE id=$1`, client.id); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			valid, err := s.Notifications.CloseNotice(ctx, job.ID, tg, message, func() error {
				calls++
				if mode == "failure" {
					return errors.New("BAD_REQUEST")
				}
				return nil
			})
			blocked := mode == "foreign" || mode == "message" || mode == "credential"
			if blocked {
				if valid || err != nil || calls != 0 {
					t.Fatal("foreign/stale close reached wire", valid, calls, err)
				}
			} else {
				if !valid || mode == "success" && err != nil || (mode == "age" || mode == "failure") && err == nil {
					t.Fatal("physical close result dishonest", valid, calls, err)
				}
			}
			if mode == "age" && calls != 0 {
				t.Fatal("48-hour delete limit bypassed")
			}
			inbox, err := s.Notices.Read(ctx, client.id, 1)
			if err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if blocked {
				want = 1
			}
			if inbox.Total != want {
				t.Fatal("close/dismiss account proof failed", inbox.Total, want)
			}
			var unchanged bool
			if e.Pool.QueryRow(ctx, `SELECT result_hash=$2 AND state='sent' FROM client_telegram_deliveries WHERE id=$1`, job.ID, hash).Scan(&unchanged) != nil || !unchanged {
				t.Fatal("close rewrote successful send/hash")
			}
			if mode == "success" {
				if valid, err = s.Notifications.CloseNotice(ctx, job.ID, 733, 42, func() error { calls++; return nil }); err != nil || !valid || calls != 1 {
					t.Fatal("successful close replayed network", err)
				}
			}
			preview, err := s.Notices.Preview(ctx, operator.id, notifications.NoticePreviewInput{Mode: "delete", ExpectedRevision: 1, Reason: "Owned deletion availability"})
			wantCopies := int32(1)
			if mode == "age" || mode == "success" || mode == "credential" {
				wantCopies = 0
			}
			if err != nil || preview.TelegramCount != wantCopies {
				t.Fatal("deletion availability differs from recorded proof", preview.TelegramCount, wantCopies, err)
			}
			if mode == "age" {
				out, err := s.Notices.Confirm(ctx, operator.id, preview.ID)
				if err != nil || out.Telegram.Skipped != 1 || out.Notice.NoticeID != p.NoticeID {
					t.Fatal("old-message delete misclassified", out.Telegram, err)
				}
			}
		})
	}
}

func TestNoticeMailConsentAndLocks(t *testing.T) {
	_, s, e, cfg, smtp, client, operator := noticeFixture(t, 1)
	ctx := context.Background()
	p, err := s.Notices.Preview(ctx, operator.id, notifications.NoticePreviewInput{Mode: "send", Audience: "personal", AccountID: client.id, Body: "No opt-in", Reason: "Owned consent"})
	if err != nil || p.EmailCount != 0 {
		t.Fatal("mail consent defaults to true", err)
	}
	if _, err = s.Notices.SetEmailPreference(ctx, client.id, true); err != nil {
		t.Fatal(err)
	}
	p = sendNotice(t, s, client, operator, "<b>Notice mail content</b>")
	var delivery uuid.UUID
	if e.Pool.QueryRow(ctx, `SELECT id FROM mail_deliveries WHERE kind='operator_notice'`).Scan(&delivery) != nil {
		t.Fatal("notice mail not queued")
	}
	entered, release := smtp.HoldNextData()
	defer release()
	done := make(chan error, 1)
	go func() { done <- s.MailDelivery.SendMail(ctx, delivery) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("notice SMTP did not start")
	}
	probe, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = probe.Exec(ctx, `SELECT id FROM notice_actions FOR UPDATE NOWAIT`); err != nil {
		probe.Rollback(ctx)
		t.Fatal("SMTP held notification row locks", err)
	}
	if _, err = probe.Exec(ctx, `SELECT id FROM accounts WHERE id=ANY($1::uuid[]) FOR UPDATE NOWAIT`, []uuid.UUID{client.id, operator.id}); err != nil {
		probe.Rollback(ctx)
		t.Fatal("SMTP held account row locks", err)
	}
	probe.Rollback(ctx)
	release()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP guard/MaxConns1 deadlock")
	}
	letters := smtp.Letters()
	if len(letters) != 1 || !strings.Contains(letters[0], "Notice mail content") || strings.Contains(letters[0], "<b>") || !strings.Contains(letters[0], cfg.HTTP.CabinetOrigin+"/cabinet") {
		t.Fatal("notice mail not frozen plaintext with cabinet link")
	}
	result, err := s.Notices.Confirm(ctx, operator.id, p.ID)
	if err != nil || result.Email.Succeeded != 1 {
		t.Fatal("SMTP ACK not recorded", result.Email, err)
	}
	edit, err := s.Notices.Preview(ctx, operator.id, notifications.NoticePreviewInput{Mode: "edit", ExpectedRevision: 1, Body: "Edited mail must not resend", Reason: "Owned immutable letter"})
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.Notices.Confirm(ctx, operator.id, edit.ID)
	if err != nil || result.Email.Unchanged != 1 {
		t.Fatal("delivered letter recall claimed", result.Email, err)
	}
	if err = s.MailDelivery.SendMail(ctx, delivery); err != nil || len(smtp.Letters()) != 1 {
		t.Fatal("sent letter replayed", err)
	}
}

func TestNoticeExpiryOwnershipAndPages(t *testing.T) {
	h, s, e, cfg, _, client, operator := noticeFixture(t, 1)
	ctx := context.Background()
	other := supportLogin(t, h, e, cfg, "notice-other-operator@example.test")
	if _, err := e.Pool.Exec(ctx, `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,now())`, other.id); err != nil {
		t.Fatal(err)
	}
	preview := func() notifications.NoticePreview {
		t.Helper()
		p, err := s.Notices.Preview(ctx, operator.id, notifications.NoticePreviewInput{Mode: "send", Audience: "personal", AccountID: client.id, Body: "Owned pending preview", Reason: "Expiry fixture"})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := preview()
	fresh := preview()
	for _, tc := range []struct {
		actor, id uuid.UUID
		want      string
	}{{operator.id, old.ID, "REQUEST_STATE_CONFLICT"}, {other.id, fresh.ID, "NOT_FOUND"}} {
		if _, err := s.Notices.Confirm(ctx, tc.actor, tc.id); err == nil || err.Error() != tc.want {
			t.Fatal("superseded/foreign preview confirmed", err)
		}
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE notice_previews SET created_at=clock_timestamp()-interval '30 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, fresh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Notices.Confirm(ctx, operator.id, fresh.ID); err == nil || err.Error() != "REQUEST_STATE_CONFLICT" {
		t.Fatal("expired preview confirmed", err)
	}
	for range 21 {
		sendNotice(t, s, client, operator, "Owned page notice")
	}
	first, err := s.Notices.Read(ctx, client.id, 1)
	if err != nil || first.Total != 21 || len(first.Notices) != 20 {
		t.Fatal("first page not bounded", err)
	}
	second, err := s.Notices.Read(ctx, client.id, 2)
	if err != nil || second.Total != 21 || len(second.Notices) != 1 || second.Notices[0].ID == first.Notices[0].ID {
		t.Fatal("second page not stable", err)
	}
	if err = s.Notices.Dismiss(ctx, other.id, first.Notices[0].ID); err == nil || err.Error() != "NOT_FOUND" {
		t.Fatal("foreign account dismissed notice", err)
	}
	if _, err = e.Pool.Exec(ctx, `UPDATE accounts SET credential_version=credential_version+1 WHERE id=$1`, client.id); err != nil {
		t.Fatal(err)
	}
	first, err = s.Notices.Read(ctx, client.id, 1)
	if err != nil || first.Total != 21 {
		t.Fatal("normal password change erased existing cabinet notices", err)
	}
	for _, query := range []string{"page=0", "page=01", "page=2147483648", "page=1&page=2", "page=1&actor=other", "page=%zz", "other=1"} {
		if r := supportRequest(h, &other, "GET", "/api/v1/notices?"+query, "", nil, "", uuid.Nil); r.Code != 400 {
			t.Fatal("bad page query accepted", query, r.Code)
		}
	}
}

func TestNoticeMailSuppression(t *testing.T) {
	for _, mode := range []string{"opt-out", "credential", "email", "role", "dismiss", "edit", "ack-loss"} {
		t.Run(mode, func(t *testing.T) {
			_, s, e, _, smtp, client, operator := noticeFixture(t, 1)
			ctx := context.Background()
			if _, err := s.Notices.SetEmailPreference(ctx, client.id, true); err != nil {
				t.Fatal(err)
			}
			p := sendNotice(t, s, client, operator, "Owned suppressed mail")
			var id uuid.UUID
			if e.Pool.QueryRow(ctx, `SELECT id FROM mail_deliveries WHERE kind='operator_notice'`).Scan(&id) != nil {
				t.Fatal("missing mail")
			}
			switch mode {
			case "opt-out":
				if _, err := s.Notices.SetEmailPreference(ctx, client.id, false); err != nil {
					t.Fatal(err)
				}
			case "credential":
				if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET credential_version=credential_version+1 WHERE id=$1`, client.id); err != nil {
					t.Fatal(err)
				}
			case "email":
				if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET email_key='changed-notice@example.test' WHERE id=$1`, client.id); err != nil {
					t.Fatal(err)
				}
			case "role":
				if _, err := e.Pool.Exec(ctx, `DELETE FROM operator_accounts WHERE account_id=$1`, operator.id); err != nil {
					t.Fatal(err)
				}
			case "dismiss":
				if err := s.Notices.Dismiss(ctx, client.id, p.NoticeID); err != nil {
					t.Fatal(err)
				}
			case "edit":
				edit, err := s.Notices.Preview(ctx, operator.id, notifications.NoticePreviewInput{Mode: "edit", ExpectedRevision: 1, Body: "Owned changed mail", Reason: "Cancel old mail"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.Notices.Confirm(ctx, operator.id, edit.ID); err != nil {
					t.Fatal(err)
				}
			case "ack-loss":
				entered, release := smtp.HoldNextData()
				defer release()
				sendCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- s.MailDelivery.SendMail(sendCtx, id) }()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("SMTP not entered")
				}
				cancel()
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("lost mail ACK classified as success")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("cancelled SMTP stuck")
				}
			}
			if mode != "ack-loss" {
				if err := s.MailDelivery.SendMail(ctx, id); err != nil {
					t.Fatal(err)
				}
				if len(smtp.Letters()) != 0 {
					t.Fatal("stale mail reached SMTP")
				}
			}
			var state string
			var acknowledged bool
			if e.Pool.QueryRow(ctx, `SELECT a.email_state,d.delivered_at IS NOT NULL FROM mail_deliveries d JOIN notice_actions a ON a.id=d.notice_action_id WHERE d.id=$1`, id).Scan(&state, &acknowledged) != nil {
				t.Fatal("missing mail outcome")
			}
			want := "skipped"
			if mode == "ack-loss" {
				want = "unknown"
			}
			if state != want || acknowledged {
				t.Fatal("suppression/lost ACK recorded as delivered", state, acknowledged)
			}
		})
	}
}

func noticeFixture(t *testing.T, maxConns int32) (http.Handler, *compositionFixture, *testkit.Env, app.Config, *testkit.SMTP, supportSession, supportSession) {
	t.Helper()
	s, e, cfg, smtp := mailFixture(t, maxConns)
	h := New(s.Modules, s.pool, cfg.HTTP)
	client := supportLogin(t, h, e, cfg, "notice-workflow-client@example.test")
	operator := supportLogin(t, h, e, cfg, "notice-workflow-operator@example.test")
	if _, err := e.Pool.Exec(context.Background(), `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, operator.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	return h, s, e, cfg, smtp, client, operator
}

// A replay that sends twice, changes the frozen audience, or rolls back an edit must fail.
func TestNoticePreviewReplayAndRevisions(t *testing.T) {
	h, _, e, cfg, _, client, operator := noticeFixture(t, 1)
	call := func(session supportSession, path string, body any) map[string]any {
		t.Helper()
		b, _ := json.Marshal(body)
		r := supportRequest(h, &session, "POST", path, "application/json", b, cfg.HTTP.CabinetOrigin, uuid.Nil)
		if r.Code != 200 && r.Code != 201 {
			t.Fatalf("notice operation want200/201 got%d", r.Code)
		}
		var v map[string]any
		if json.Unmarshal(r.Body.Bytes(), &v) != nil {
			t.Fatal("response")
		}
		return v
	}
	call(client, "/api/v1/notices/preferences", map[string]bool{"email_enabled": true})
	if _, err := e.Pool.Exec(context.Background(), `UPDATE accounts SET telegram_id=733 WHERE id=$1`, client.id); err != nil {
		t.Fatal(err)
	}
	preview := call(operator, "/api/v1/operator/notices/previews", map[string]any{"mode": "send", "audience": "all", "body": "<b>First notice</b>", "reason": "Owned all send"})
	if preview["recipient_count"] != float64(2) || preview["email_count"] != float64(1) || preview["telegram_count"] != float64(1) {
		t.Fatal("frozen preview counts")
	}
	late := supportLogin(t, h, e, cfg, "notice-late@example.test")
	confirmPath := "/api/v1/operator/notices/previews/" + preview["id"].(string) + "/confirm"
	first := call(operator, confirmPath, map[string]bool{"confirmed": true})
	if first["version"] != "operator-notices-v1" || first["revision"] != float64(1) {
		t.Fatal("initial confirmed revision")
	}
	for range 2 {
		call(operator, confirmPath, map[string]bool{"confirmed": true})
	}
	for _, tc := range []struct {
		sql  string
		want int
	}{
		{`SELECT count(*) FROM notices`, 1},
		{`SELECT count(*) FROM notice_recipients`, 2},
		{`SELECT count(*) FROM notice_actions`, 2},
		{`SELECT count(*) FROM client_telegram_deliveries WHERE notice_action_id IS NOT NULL`, 1},
		{`SELECT count(*) FROM mail_deliveries WHERE kind='operator_notice'`, 1},
		{`SELECT count(*) FROM audit_events WHERE action='notice_sent'`, 1},
	} {
		var n int
		if e.Pool.QueryRow(context.Background(), tc.sql).Scan(&n) != nil || n != tc.want {
			t.Fatal("duplicate operation or frozen audience changed", n, tc.want)
		}
	}
	read := func(session supportSession) map[string]any {
		t.Helper()
		r := supportRequest(h, &session, "GET", "/api/v1/notices?page=1", "", nil, "", uuid.Nil)
		if r.Code != 200 {
			t.Fatal("inbox", r.Code)
		}
		var v map[string]any
		json.Unmarshal(r.Body.Bytes(), &v)
		return v
	}
	if read(late)["total"] != float64(0) || read(client)["total"] != float64(1) {
		t.Fatal("late registration entered frozen audience")
	}
	noticeID := preview["notice_id"].(string)
	if r := supportRequest(h, &client, "POST", "/api/v1/notices/"+noticeID+"/dismiss", "", nil, cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 204 {
		t.Fatal("own dismiss", r.Code)
	}
	edit := call(operator, "/api/v1/operator/notices/previews", map[string]any{"mode": "edit", "body": "Changed notice", "expected_revision": 1, "reason": "Owned edit"})
	editResult := call(operator, "/api/v1/operator/notices/previews/"+edit["id"].(string)+"/confirm", map[string]bool{"confirmed": true})
	if editResult["revision"] != float64(2) || read(client)["total"] != float64(0) {
		t.Fatal("edit reopened dismissed inbox")
	}
	if _, err := e.Pool.Exec(context.Background(), `UPDATE notice_previews SET created_at=clock_timestamp()-interval '30 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, preview["id"]); err != nil {
		t.Fatal(err)
	}
	replay := call(operator, confirmPath, map[string]bool{"confirmed": true})
	if replay["revision"] != float64(1) {
		t.Fatal("replay did not return original operation")
	}
	var current int
	if e.Pool.QueryRow(context.Background(), `SELECT revision FROM notices`).Scan(&current) != nil || current != 2 {
		t.Fatal("replay rolled back current revision")
	}
}

func TestNoticeMiniAppAuthority(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	body, _ := json.Marshal(map[string]string{"init_data": testkit.SignedMiniAppData(key, 123, e.Clock(), `{"id":745,"first_name":"Owned notices","language_code":"en"}`, ""), "accepted_terms_version": "1", "accepted_privacy_version": "1"})
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
		{"GET", "/api/v1/notices?page=1", "", auth.CsrfToken, 200},
		{"POST", "/api/v1/notices/preferences", `{"email_enabled":false}`, auth.CsrfToken, 200},
		{"POST", "/api/v1/notices/preferences", `{"email_enabled":true}`, auth.CsrfToken, 409},
		{"POST", "/api/v1/notices/preferences", `{"email_enabled":false}`, "", 403},
		{"POST", "/api/v1/notices/" + uuid.NewString() + "/dismiss", "", auth.CsrfToken, 404},
		{"GET", "/api/v1/operator/notices/last", "", auth.CsrfToken, 403},
		{"POST", "/api/v1/operator/notices/previews", `{"mode":"send","audience":"all","body":"Owned","reason":"Owned"}`, auth.CsrfToken, 403},
	} {
		r = miniAppRequest(h, tc.method, tc.path, tc.body, cfg.HTTP.CabinetOrigin, auth.SessionToken, tc.csrf, "")
		if r.Code != tc.want {
			t.Fatal("notice bearer boundary", tc.path, r.Code, tc.want)
		}
	}
}

// Removing operator/CSRF checks, or accepting client-provided actor fields, must fail this boundary test.
func TestNoticeHTTPAuthority(t *testing.T) {
	h, e, cfg := httpFixture(t)
	customer := supportLogin(t, h, e, cfg, "notice-customer@example.test")
	operator := supportLogin(t, h, e, cfg, "notice-operator@example.test")
	if _, err := e.Pool.Exec(context.Background(), `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, operator.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"mode":"send","audience":"personal","account_id":"` + customer.id.String() + `","body":"<b>Hello</b>","reason":"Owned notice test"}`)
	path := "/api/v1/operator/notices/previews"
	r := supportRequest(h, &operator, "POST", path, "application/json", body, cfg.HTTP.CabinetOrigin, uuid.Nil)
	if r.Code != 201 {
		t.Fatalf("operator preview: want201 got%d", r.Code)
	}
	var preview struct {
		ID             uuid.UUID `json:"id"`
		HTML           string    `json:"html"`
		RecipientCount int       `json:"recipient_count"`
	}
	if json.Unmarshal(r.Body.Bytes(), &preview) != nil || preview.ID == uuid.Nil || preview.HTML != "<b>Hello</b>" || preview.RecipientCount != 1 {
		t.Fatal("preview did not preserve content and selected recipient")
	}
	noCSRF := operator
	noCSRF.csrf = ""
	for _, tc := range []struct {
		name    string
		session *supportSession
		origin  string
		body    []byte
		want    int
	}{
		{"unprivileged", &customer, cfg.HTTP.CabinetOrigin, body, 403},
		{"unauthenticated", nil, cfg.HTTP.CabinetOrigin, body, 401},
		{"csrf", &noCSRF, cfg.HTTP.CabinetOrigin, body, 403},
		{"origin", &operator, "https://foreign.example.test", body, 403},
		{"actor override", &operator, cfg.HTTP.CabinetOrigin, []byte(`{"mode":"send","audience":"all","body":"Hello","reason":"fixture","actor":"` + operator.id.String() + `"}`), 400},
		{"foreign account", &operator, cfg.HTTP.CabinetOrigin, []byte(`{"mode":"send","audience":"personal","account_id":"` + uuid.NewString() + `","body":"Hello","reason":"fixture"}`), 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := supportRequest(h, tc.session, "POST", path, "application/json", tc.body, tc.origin, uuid.Nil)
			if r.Code != tc.want {
				t.Fatalf("want%d got%d", tc.want, r.Code)
			}
		})
	}
	confirm := path + "/" + preview.ID.String() + "/confirm"
	for _, body := range []string{`{"confirmed":false}`, `{"confirmed":true,"body":"replacement"}`, `{"confirmed":true,"actor":"` + operator.id.String() + `"}`} {
		if r := supportRequest(h, &operator, "POST", confirm, "application/json", []byte(body), cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 400 {
			t.Fatal("confirm override accepted", r.Code)
		}
	}
	if r := supportRequest(h, &customer, "GET", "/api/v1/notices?page=0", "", nil, "", uuid.Nil); r.Code != 400 {
		t.Fatal("unbounded page", r.Code)
	}
	var count int
	if e.Pool.QueryRow(context.Background(), `SELECT count(*) FROM client_telegram_deliveries`).Scan(&count) != nil || count != 0 {
		t.Fatal("preview triggered wire delivery")
	}
	if _, err := e.Pool.Exec(context.Background(), `DELETE FROM operator_accounts WHERE account_id=$1`, operator.id); err != nil {
		t.Fatal(err)
	}
	if r := supportRequest(h, &operator, "POST", confirm, "application/json", []byte(`{"confirmed":true}`), cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 403 {
		t.Fatal("revoked operator confirmed", r.Code)
	}
}

// Dangerous markup accepted or safe text silently truncated must fail at the actual preview boundary.
func TestNoticeFormatting(t *testing.T) {
	h, e, cfg := httpFixture(t)
	operator := supportLogin(t, h, e, cfg, "notice-format@example.test")
	if _, err := e.Pool.Exec(context.Background(), `INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)`, operator.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body, html, text string
		want                   int
	}{
		{"plain", "Hello & friends", "Hello &amp; friends", "Hello & friends", 201},
		{"bold", "<strong>Hello</strong>", "<b>Hello</b>", "Hello", 201},
		{"code", "<pre><code class=\"language-go\">fmt.Print(&quot;hi&quot;)</code></pre>", "<pre><code class=\"language-go\">fmt.Print(&#34;hi&#34;)</code></pre>", "fmt.Print(\"hi\")", 201},
		{"link", "<a href=\"https://example.test/help\">Help</a>", "<a href=\"https://example.test/help\">Help</a>", "Help (https://example.test/help)", 201},
		{"spoiler", "<span class=\"tg-spoiler\">Secret</span>", "<tg-spoiler>Secret</tg-spoiler>", "Secret", 201},
		{"emoji", "<tg-emoji emoji-id=\"12345\">🙂</tg-emoji>", "🙂", "🙂", 201},
		{"limit", strings.Repeat("a", 4096), strings.Repeat("a", 4096), strings.Repeat("a", 4096), 201},
		{"too long", strings.Repeat("a", 4097), "", "", 400},
		{"empty visible", "<b> </b>", "", "", 400},
		{"script", "<script>alert(1)</script>", "", "", 400},
		{"attr", "<b onclick=\"alert(1)\">Hi</b>", "", "", 400},
		{"unsafe link", "<a href=\"javascript:alert(1)\">Hi</a>", "", "", 400},
		{"credential link", "<a href=\"https://user:pass@example.test\">Hi</a>", "", "", 400},
		{"unsupported entity", "Hi &copy;", "", "", 400},
		{"numeric control", "Hi &#0;", "", "", 400},
		{"mismatched nesting", "<b><i>Hi</b></i>", "", "", 400},
		{"nested link", "<a href=\"https://example.test\"><a href=\"https://other.test\">Hi</a></a>", "", "", 400},
		{"control", "Hi\x00there", "", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"mode": "send", "audience": "all", "body": tc.body, "reason": "format fixture"})
			r := supportRequest(h, &operator, "POST", "/api/v1/operator/notices/previews", "application/json", body, cfg.HTTP.CabinetOrigin, uuid.Nil)
			if r.Code != tc.want {
				t.Fatalf("want%d got%d", tc.want, r.Code)
			}
			if tc.want == 201 {
				var out struct{ HTML, Text string }
				if json.Unmarshal(r.Body.Bytes(), &out) != nil || out.HTML != tc.html || out.Text != tc.text {
					t.Fatal("preview formatting differs from hand-derived safe output")
				}
			}
		})
	}
}

func TestNoticeUnstartedCopies(t *testing.T) {
	for _, mode := range []string{"edit", "delete"} {
		t.Run(mode, func(t *testing.T) {
			_, s, e, _, _, client, operator := noticeFixture(t, 1)
			ctx := context.Background()
			if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET telegram_id=733 WHERE id=$1`, client.id); err != nil {
				t.Fatal(err)
			}
			original := sendNotice(t, s, client, operator, "Old unstarted body")
			in := notifications.NoticePreviewInput{Mode: mode, ExpectedRevision: 1, Reason: "Owned unstarted copy"}
			if mode == "edit" {
				in.Body = "<b>Newest body</b>"
			}
			preview, err := s.Notices.Preview(ctx, operator.id, in)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Notices.Confirm(ctx, operator.id, preview.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Notices.Preview(ctx, operator.id, notifications.NoticePreviewInput{Mode: "edit", ExpectedRevision: 1, Body: "Stale revision", Reason: "Owned revision conflict"}); err == nil || err.Error() != "REQUEST_STATE_CONFLICT" {
				t.Fatal("stale last revision accepted", err)
			}
			job, err := s.Notifications.ClaimClient(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "delete" && job != nil {
				t.Fatal("withdrawn unstarted copy was claimed")
			}
			if mode == "edit" {
				if job == nil || job.PriorDeliveryID != uuid.Nil || job.NoticeHTML != "<b>Newest body</b>" || job.NoticeMode != "edit" {
					t.Fatal("replacement lost current body or guessed a message")
				}
				if err = s.Notifications.DeliverClient(ctx, *job, func() (notifications.ClientOutcome, error) {
					job.NoticeResult.MessageAt = time.Now()
					return notifications.ClientOutcome{State: "sent", MessageID: 43}, nil
				}); err != nil {
					t.Fatal(err)
				}
				if extra, err := s.Notifications.ClaimClient(ctx); err != nil || extra != nil {
					t.Fatal("unstarted edit left a duplicate copy", err)
				}
			}
			first, err := s.Notices.Confirm(ctx, operator.id, original.ID)
			if err != nil || first.Telegram.Skipped != 1 {
				t.Fatal("old unstarted action not cancelled", err)
			}
		})
	}
}
