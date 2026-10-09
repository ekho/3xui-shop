package notifications

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"time"

	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type noticeDeliveryProof struct {
	ActorID, AccountID, NoticeID, PreviewID, RecipientID uuid.UUID
	CredentialVersion, TelegramID                        int64
	Locale, Mode, HTML, Text                             string
	EmailHash                                            []byte
	EmailEnabled, Visible, Current                       bool
	DismissedAt, EmailStartedAt                          *time.Time
	TelegramState                                        string
}

func noticeDeliveryIdentity(ctx context.Context, tx pgx.Tx, action uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	var actor, account uuid.UUID
	err := tx.QueryRow(ctx, `SELECT p.operator_account_id,r.account_id FROM notice_actions a JOIN notice_previews p ON p.id=a.preview_id JOIN notice_recipients r ON r.id=a.recipient_id WHERE a.id=$1`, action).Scan(&actor, &account)
	return actor, account, err
}
func (s *NoticeService) deliveryProofTx(ctx context.Context, tx pgx.Tx, action uuid.UUID) (noticeDeliveryProof, error) {
	var r noticeDeliveryProof
	err := tx.QueryRow(ctx, `SELECT p.operator_account_id,r.account_id,r.notice_id,p.id,r.id,r.credential_version,r.telegram_id,r.locale,r.email_hash,r.email_enabled,p.mode,p.html,p.plain_text FROM notice_actions a JOIN notice_recipients r ON r.id=a.recipient_id JOIN notice_previews p ON p.id=a.preview_id WHERE a.id=$1`, action).Scan(&r.ActorID, &r.AccountID, &r.NoticeID, &r.PreviewID, &r.RecipientID, &r.CredentialVersion, &r.TelegramID, &r.Locale, &r.EmailHash, &r.EmailEnabled, &r.Mode, &r.HTML, &r.Text)
	if err != nil {
		return r, unavailable()
	}
	var current uuid.UUID
	var deleted bool
	if tx.QueryRow(ctx, `SELECT current_preview_id,deleted FROM notices WHERE id=$1 FOR UPDATE`, r.NoticeID).Scan(&current, &deleted) != nil {
		return r, unavailable()
	}
	if tx.QueryRow(ctx, `SELECT cabinet_visible,dismissed_at FROM notice_recipients WHERE id=$1 FOR UPDATE`, r.RecipientID).Scan(&r.Visible, &r.DismissedAt) != nil {
		return r, unavailable()
	}
	if tx.QueryRow(ctx, `SELECT email_started_at,telegram_state FROM notice_actions WHERE id=$1 FOR UPDATE`, action).Scan(&r.EmailStartedAt, &r.TelegramState) != nil {
		return r, unavailable()
	}
	r.Current = current == r.PreviewID && (r.Mode == "delete" || !deleted)
	return r, nil
}

func (s *NoticeService) deliver(parent context.Context, j ClientJob, send func() (ClientOutcome, error)) error {
	if j.NoticeActionID == uuid.Nil || j.NoticeActorID == uuid.Nil || send == nil {
		return failure(400, "INVALID_INPUT")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	valid, err := s.ports.DeliveryGuard(ctx, j.NoticeActorID, j.AccountID, j.TelegramID, j.CredentialVersion, func(tx pgx.Tx) error {
		r, err := s.deliveryProofTx(ctx, tx, j.NoticeActionID)
		if err != nil {
			return err
		}
		if r.ActorID != j.NoticeActorID || r.AccountID != j.AccountID || r.TelegramID != j.TelegramID || r.CredentialVersion != j.CredentialVersion || r.Locale != j.Locale || r.Mode != j.NoticeMode || r.HTML != j.NoticeHTML {
			return failure(409, "REQUEST_STATE_CONFLICT")
		}
		state, err := lockClient(ctx, tx, j)
		if err != nil {
			return err
		}
		if state != "pending" {
			return nil
		}
		if j.noticeUncertain {
			return s.skipTelegramTx(ctx, tx, j, r.TelegramState)
		}
		if !r.Current || r.Mode != "delete" && (!r.Visible || r.DismissedAt != nil) {
			return s.skipTelegramTx(ctx, tx, j, r.TelegramState)
		}
		var originalMessageAt *time.Time
		if j.PriorDeliveryID != uuid.Nil {
			var message int64
			var messageAt *time.Time
			if tx.QueryRow(ctx, `SELECT d.message_id,a.telegram_message_at FROM client_telegram_deliveries d JOIN notice_actions a ON a.id=d.notice_action_id JOIN notice_previews p ON p.id=a.preview_id WHERE d.id=$1 AND d.account_id=$2 AND d.telegram_id=$3 AND d.credential_version=$4 AND d.state='sent' AND a.recipient_id=$5 AND p.mode<>'delete'`, j.PriorDeliveryID, j.AccountID, j.TelegramID, j.CredentialVersion, r.RecipientID).Scan(&message, &messageAt) != nil || message != j.NoticeMessageID || message <= 0 {
				return failure(409, "REQUEST_STATE_CONFLICT")
			}
			if r.Mode == "delete" && (messageAt == nil || time.Now().Before(*messageAt) || time.Since(*messageAt) >= 48*time.Hour) {
				return s.skipTelegramTx(ctx, tx, j, r.TelegramState)
			}
			originalMessageAt = messageAt
		}
		out, sendErr := send()
		outcome := "unknown"
		var messageAt *time.Time
		if sendErr != nil {
			out = ClientOutcome{State: "skipped"}
		} else {
			switch out.State {
			case "sent":
				outcome = "succeeded"
				if j.PriorDeliveryID != uuid.Nil {
					messageAt = originalMessageAt
				} else if j.NoticeResult != nil && !j.NoticeResult.MessageAt.IsZero() {
					messageAt = &j.NoticeResult.MessageAt
				}
				if messageAt == nil {
					outcome = "unknown"
				}
			case "failed":
				outcome = "failed"
			case "skipped":
				outcome = "skipped"
			case "retry":
				var attempts int
				if tx.QueryRow(ctx, `SELECT attempts FROM client_telegram_deliveries WHERE id=$1`, j.ID).Scan(&attempts) != nil {
					return unavailable()
				}
				outcome = "pending"
				if attempts >= 5 {
					outcome = "failed"
					out = ClientOutcome{State: "skipped"}
				}
			default:
				return failure(400, "INVALID_INPUT")
			}
		}
		if err = finishClient(ctx, tx, j, out); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE notice_actions SET telegram_state=$2,telegram_message_at=$3 WHERE id=$1`, j.NoticeActionID, outcome, messageAt); err != nil {
			return unavailable()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if valid {
		return nil
	}
	// No wire call was allowed; preserve uncertainty from any earlier claim.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer noticeRollback(tx)
	r, err := s.deliveryProofTx(ctx, tx, j.NoticeActionID)
	if err != nil {
		return err
	}
	state, err := lockClient(ctx, tx, j)
	if err != nil {
		return err
	}
	if state != "pending" {
		return nil
	}
	if err = s.skipTelegramTx(ctx, tx, j, r.TelegramState); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}
func (s *NoticeService) skipTelegramTx(ctx context.Context, tx pgx.Tx, j ClientJob, old string) error {
	var attempts int
	if tx.QueryRow(ctx, `SELECT attempts FROM client_telegram_deliveries WHERE id=$1`, j.ID).Scan(&attempts) != nil {
		return unavailable()
	}
	state := "skipped"
	if attempts > 1 && old == "unknown" {
		state = "unknown"
	}
	if err := finishClient(ctx, tx, j, ClientOutcome{State: "skipped"}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE notice_actions SET telegram_state=$2 WHERE id=$1`, j.NoticeActionID, state); err != nil {
		return unavailable()
	}
	return nil
}
func (s *NoticeService) mailProofTx(ctx context.Context, tx pgx.Tx, action uuid.UUID, email string) (noticeDeliveryProof, bool, error) {
	actor, account, err := noticeDeliveryIdentity(ctx, tx, action)
	if err != nil {
		return noticeDeliveryProof{}, false, unavailable()
	}
	current, allowed, err := s.ports.LockPairTx(ctx, tx, actor, account)
	if err != nil {
		return noticeDeliveryProof{}, false, unavailable()
	}
	r, err := s.deliveryProofTx(ctx, tx, action)
	if err != nil {
		return r, false, err
	}
	enabled, err := noticeEmailEnabled(ctx, tx, account)
	if err != nil {
		return r, false, unavailable()
	}
	valid := allowed && r.Current && r.Mode == "send" && r.Visible && r.DismissedAt == nil && enabled && r.EmailEnabled && current.EmailAvailable && current.Email == email && current.CredentialVersion == r.CredentialVersion && subtle.ConstantTimeCompare(r.EmailHash, digest(email)) == 1
	return r, valid, nil
}
func (s *NoticeService) suppressMailTx(ctx context.Context, tx pgx.Tx, delivery, action uuid.UUID, started *time.Time) error {
	state := "skipped"
	if started != nil {
		state = "unknown"
	}
	if _, err := tx.Exec(ctx, `UPDATE notice_actions SET email_state=$2 WHERE id=$1 AND email_state<>'succeeded'`, action, state); err != nil {
		return unavailable()
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_deliveries SET ciphertext=NULL WHERE id=$1 AND delivered_at IS NULL`, delivery); err != nil {
		return unavailable()
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}

func noticeTelegramCopyTx(ctx context.Context, tx pgx.Tx, recipient uuid.UUID) (uuid.UUID, *time.Time, bool, error) {
	var prior uuid.UUID
	var messageAt *time.Time
	err := tx.QueryRow(ctx, `SELECT d.id,a.telegram_message_at FROM client_telegram_deliveries d JOIN notice_actions a ON a.id=d.notice_action_id JOIN notice_previews p ON p.id=a.preview_id WHERE a.recipient_id=$1 AND d.state='sent' AND d.message_id>0 AND p.mode<>'delete' AND NOT EXISTS(SELECT 1 FROM client_telegram_deliveries c JOIN notice_actions ca ON ca.id=c.notice_action_id WHERE ca.recipient_id=a.recipient_id AND c.message_id=d.message_id AND c.notice_close_state='succeeded') ORDER BY d.sequence DESC LIMIT 1`, recipient).Scan(&prior, &messageAt)
	if err == nil {
		return prior, messageAt, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil, false, err
	}
	var attempted bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM client_telegram_deliveries d JOIN notice_actions a ON a.id=d.notice_action_id WHERE a.recipient_id=$1 AND d.attempts>0 AND NOT EXISTS(SELECT 1 FROM client_telegram_deliveries c JOIN notice_actions ca ON ca.id=c.notice_action_id WHERE ca.recipient_id=a.recipient_id AND c.notice_close_state='succeeded'))`, recipient).Scan(&attempted)
	return uuid.Nil, nil, attempted, err
}
func (s *NoticeService) enqueueChannelsTx(ctx context.Context, tx pgx.Tx, p noticePreviewRow, r noticeProof, current ReminderRecipient, valid bool, action uuid.UUID, now time.Time) error {
	closed := r.DismissedAt != nil || !r.CabinetVisible
	if p.Mode == "send" && valid && !closed && r.EmailAvailable && r.EmailEnabled && current.EmailAvailable && subtle.ConstantTimeCompare(r.EmailHash, digest(current.Email)) == 1 {
		enabled, err := noticeEmailEnabled(ctx, tx, r.AccountID)
		if err != nil {
			return unavailable()
		}
		if enabled {
			if s.mail.EnqueueMailTx(ctx, tx, current.Email, nil, nil, "operator_notice", MailPayload{Type: "operator_notice", Locale: r.Locale, NoticeActionID: &action}, now) != nil {
				return unavailable()
			}
			if _, err = tx.Exec(ctx, `UPDATE notice_actions SET email_state='pending' WHERE id=$1`, action); err != nil {
				return unavailable()
			}
		}
	}
	if p.Mode != "send" {
		// An acknowledged or started letter remains an immutable fact. No replacement letter.
		var started bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notice_actions WHERE recipient_id=$1 AND id<>$2 AND (email_state='succeeded' OR email_started_at IS NOT NULL))`, r.ID, action).Scan(&started) != nil {
			return unavailable()
		}
		state := "skipped"
		if started {
			state = "unchanged"
		}
		if _, err := tx.Exec(ctx, `UPDATE notice_actions SET email_state=$2 WHERE id=$1`, action, state); err != nil {
			return unavailable()
		}
		if _, err := tx.Exec(ctx, `UPDATE mail_deliveries d SET ciphertext=NULL FROM notice_actions a WHERE d.notice_action_id=a.id AND a.recipient_id=$1 AND a.id<>$2 AND a.email_started_at IS NULL AND d.delivered_at IS NULL`, r.ID, action); err != nil {
			return unavailable()
		}
		if _, err := tx.Exec(ctx, `UPDATE notice_actions SET email_state='skipped' WHERE recipient_id=$1 AND id<>$2 AND email_state='pending' AND email_started_at IS NULL`, r.ID, action); err != nil {
			return unavailable()
		}
	}
	if !valid || r.TelegramID <= 0 || current.TelegramID != r.TelegramID || closed && p.Mode != "delete" {
		return nil
	}
	prior := uuid.Nil
	if p.Mode != "send" {
		// Lock the owned rows after the actor/current notice. Claimed copies are never replaced.
		var messageAt *time.Time
		var attempted bool
		var err error
		prior, messageAt, attempted, err = noticeTelegramCopyTx(ctx, tx, r.ID)
		if err != nil {
			return unavailable()
		}
		raw, _ := json.Marshal(ClientOutcome{State: "skipped"})
		if _, err = tx.Exec(ctx, `UPDATE client_telegram_deliveries d SET state='skipped',completed_at=clock_timestamp(),result_hash=$2 FROM notice_actions a WHERE d.notice_action_id=a.id AND a.recipient_id=$1 AND d.state='pending' AND d.attempts=0`, r.ID, digest(string(raw))); err != nil {
			return unavailable()
		}
		if _, err = tx.Exec(ctx, `UPDATE notice_actions a SET telegram_state='skipped' WHERE a.recipient_id=$1 AND a.id<>$2 AND a.telegram_state='pending' AND EXISTS(SELECT 1 FROM client_telegram_deliveries d WHERE d.notice_action_id=a.id AND d.state='skipped' AND d.attempts=0)`, r.ID, action); err != nil {
			return unavailable()
		}
		if prior == uuid.Nil && attempted {
			_, err = tx.Exec(ctx, `UPDATE notice_actions SET telegram_state='unknown' WHERE id=$1`, action)
			if err != nil {
				return unavailable()
			}
			return nil
		}
		if p.Mode == "delete" && (prior == uuid.Nil || messageAt == nil || now.Before(*messageAt) || now.Sub(*messageAt) >= 48*time.Hour) {
			return nil
		}
	}
	if err := s.tg.EnqueueClientTx(ctx, tx, ClientNotice{AccountID: r.AccountID, TelegramID: r.TelegramID, CredentialVersion: r.CredentialVersion, Locale: r.Locale, EventKey: "notice:" + action.String(), Route: "cabinet", NoticeActionID: action, PriorDeliveryID: prior}, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE notice_actions SET telegram_state='pending' WHERE id=$1`, action); err != nil {
		return unavailable()
	}
	return nil
}

// Only new notice jobs carry these proofs; the older ClientOutcome wire/hash stays unchanged.
type NoticeWireResult struct{ MessageAt time.Time }

func (s *NoticeService) claim(ctx context.Context, j *ClientJob) error {
	var state string
	if s.pool.QueryRow(ctx, `SELECT p.operator_account_id,p.mode,p.html,COALESCE(d.message_id,0),prior.telegram_message_at,a.telegram_state FROM notice_actions a JOIN notice_previews p ON p.id=a.preview_id LEFT JOIN client_telegram_deliveries d ON d.id=$2 LEFT JOIN notice_actions prior ON prior.id=d.notice_action_id WHERE a.id=$1`, j.NoticeActionID, nullableUUID(j.PriorDeliveryID)).Scan(&j.NoticeActorID, &j.NoticeMode, &j.NoticeHTML, &j.NoticeMessageID, &j.NoticeMessageAt, &state) != nil {
		return unavailable()
	}
	// Only an acknowledged 429 restores pending; a recovered unknown never repeats the wire call.
	j.noticeUncertain = state == "unknown"
	j.NoticeResult = &NoticeWireResult{}
	// A crash/ACK loss after this durable claim must remain unknown, never an unattempted send.
	if _, err := s.pool.Exec(ctx, `UPDATE notice_actions SET telegram_state='unknown',telegram_started_at=COALESCE(telegram_started_at,clock_timestamp()) WHERE id=$1 AND telegram_state IN ('pending','unknown')`, j.NoticeActionID); err != nil {
		return unavailable()
	}
	return nil
}
func nullableUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func (s *Service) CloseNotice(parent context.Context, id uuid.UUID, tg, message int64, remove func() error) (bool, error) {
	if s.notices == nil {
		return false, unavailable()
	}
	if id == uuid.Nil || tg <= 0 || tg > 1<<52-1 || message <= 0 || remove == nil {
		return false, failure(400, "INVALID_INPUT")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	var account, action uuid.UUID
	var version int64
	err := s.pool.QueryRow(ctx, `SELECT d.account_id,d.credential_version,d.notice_action_id FROM client_telegram_deliveries d JOIN notice_actions a ON a.id=d.notice_action_id JOIN notice_previews p ON p.id=a.preview_id WHERE d.id=$1 AND d.telegram_id=$2 AND d.message_id=$3 AND d.state='sent' AND a.telegram_state='succeeded' AND p.mode<>'delete'`, id, tg, message).Scan(&account, &version, &action)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, unavailable()
	}
	closed := false
	var wireErr error
	valid, err := s.clientGuard(ctx, account, tg, version, func(tx pgx.Tx) error {
		r, err := s.notices.deliveryProofTx(ctx, tx, action)
		if err != nil {
			return err
		}
		var state string
		var messageAt *time.Time
		var priorClose *string
		err = tx.QueryRow(ctx, `SELECT d.state,a.telegram_message_at,d.notice_close_state FROM client_telegram_deliveries d JOIN notice_actions a ON a.id=d.notice_action_id WHERE d.id=$1 AND d.account_id=$2 AND d.telegram_id=$3 AND d.credential_version=$4 AND d.message_id=$5 AND d.notice_action_id=$6 FOR UPDATE OF d`, id, account, tg, version, message, action).Scan(&state, &messageAt, &priorClose)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return unavailable()
		}
		if state != "sent" || r.AccountID != account || r.TelegramID != tg || r.CredentialVersion != version {
			return nil
		}
		if priorClose != nil && *priorClose == "succeeded" {
			closed = true
			return nil
		}
		var now time.Time
		if tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now) != nil {
			return unavailable()
		}
		if r.DismissedAt == nil {
			if _, err = tx.Exec(ctx, `UPDATE notice_recipients SET dismissed_at=$2 WHERE id=$1`, r.RecipientID, now); err != nil {
				return unavailable()
			}
			if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: account, CreatedAt: now, Action: "notice_dismissed"}) != nil {
				return unavailable()
			}
		}
		outcome := "failed"
		if messageAt != nil && !now.Before(*messageAt) && now.Sub(*messageAt) < 48*time.Hour {
			wireErr = remove()
			outcome = "succeeded"
			if wireErr != nil {
				outcome = "unknown"
				if wireErr.Error() == "BAD_REQUEST" || wireErr.Error() == "FORBIDDEN" {
					outcome = "failed"
				}
			}
		} else {
			wireErr = failure(409, "NOTICE_DELETE_UNAVAILABLE")
		}
		if _, err = tx.Exec(ctx, `UPDATE client_telegram_deliveries SET notice_closed_at=$2,notice_close_state=$3 WHERE id=$1`, id, now, outcome); err != nil {
			return unavailable()
		}
		closed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return valid && closed, wireErr
}
