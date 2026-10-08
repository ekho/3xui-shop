package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const noticeVersion = "operator-notices-v1"

type NoticePorts struct {
	AudienceTx      func(context.Context, pgx.Tx) ([]uuid.UUID, error)
	RecipientTx     func(context.Context, pgx.Tx, uuid.UUID, bool) (ReminderRecipient, bool, error)
	LockOperatorTx  func(context.Context, pgx.Tx, uuid.UUID) (bool, error)
	LockPairTx      func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (ReminderRecipient, bool, error)
	DeliveryGuard   func(context.Context, uuid.UUID, uuid.UUID, int64, int64, func(pgx.Tx) error) (bool, error)
	RequireOperator func(context.Context, uuid.UUID) error
}
type NoticePreviewInput struct {
	Mode, Audience, Body, Reason string
	AccountID                    uuid.UUID
	ExpectedRevision             int32
}
type NoticePreview struct {
	ID             uuid.UUID `json:"id"`
	NoticeID       uuid.UUID `json:"notice_id"`
	Mode           string    `json:"mode"`
	Revision       int32     `json:"revision"`
	HTML           string    `json:"html"`
	Text           string    `json:"text"`
	Reason         string    `json:"reason"`
	ExpiresAt      time.Time `json:"expires_at"`
	RecipientCount int32     `json:"recipient_count"`
	CabinetCount   int32     `json:"cabinet_count"`
	TelegramCount  int32     `json:"telegram_count"`
	EmailCount     int32     `json:"email_count"`
}
type NoticeChannelCounts struct {
	Pending   int32 `json:"pending"`
	Succeeded int32 `json:"succeeded"`
	Failed    int32 `json:"failed"`
	Skipped   int32 `json:"skipped"`
	Unknown   int32 `json:"unknown"`
	Unchanged int32 `json:"unchanged"`
}
type NoticeBatchResult struct {
	Version  string              `json:"version"`
	Notice   *NoticePreview      `json:"notice"`
	Revision int32               `json:"revision"`
	Cabinet  NoticeChannelCounts `json:"cabinet"`
	Telegram NoticeChannelCounts `json:"telegram"`
	Email    NoticeChannelCounts `json:"email"`
}
type Notice struct {
	ID        uuid.UUID `json:"id"`
	Revision  int32     `json:"revision"`
	HTML      string    `json:"html"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}
type NoticeResult struct {
	Version        string   `json:"version"`
	EmailEnabled   bool     `json:"email_enabled"`
	EmailAvailable bool     `json:"email_available"`
	Page           int32    `json:"page"`
	PerPage        int32    `json:"per_page"`
	Total          int64    `json:"total"`
	Notices        []Notice `json:"notices"`
}
type noticeProof struct {
	ID, AccountID                                          uuid.UUID
	CredentialVersion, TelegramID                          int64
	Locale                                                 string
	EmailHash                                              []byte
	Eligible, EmailAvailable, EmailEnabled, CabinetVisible bool
	TelegramAvailable                                      bool
	DismissedAt                                            *time.Time
}
type noticePreviewRow struct {
	NoticePreview
	ActorID                   uuid.UUID
	Audience                  string
	ExpectedRevision          int32
	Snapshot                  []noticeProof
	SupersededAt, ConfirmedAt *time.Time
}

const noticePreviewColumns = `id,notice_id,mode,html,plain_text,reason,expires_at,operator_account_id,audience,expected_revision,recipient_snapshot,superseded_at,confirmed_at`

func scanNoticePreview(row pgx.Row) (noticePreviewRow, error) {
	var p noticePreviewRow
	var raw []byte
	err := row.Scan(&p.ID, &p.NoticeID, &p.Mode, &p.HTML, &p.Text, &p.Reason, &p.ExpiresAt, &p.ActorID, &p.Audience, &p.ExpectedRevision, &raw, &p.SupersededAt, &p.ConfirmedAt)
	if err != nil {
		return p, err
	}
	if json.Unmarshal(raw, &p.Snapshot) != nil {
		return p, unavailable()
	}
	p.Revision = p.ExpectedRevision + 1
	p.RecipientCount = int32(len(p.Snapshot))
	for _, r := range p.Snapshot {
		if r.Eligible && r.CabinetVisible && r.DismissedAt == nil {
			p.CabinetCount++
		}
		if r.TelegramAvailable {
			p.TelegramCount++
		}
		if p.Mode == "send" && r.Eligible && r.EmailAvailable && r.EmailEnabled && r.DismissedAt == nil {
			p.EmailCount++
		}
	}
	return p, nil
}

type NoticeService struct {
	pool  *pgxpool.Pool
	ports NoticePorts
	mail  *MailService
	tg    *Service
	now   func() time.Time
}

func NewNotices(pool *pgxpool.Pool, ports NoticePorts, mail *MailService, tg *Service, now func() time.Time) *NoticeService {
	s := &NoticeService{pool: pool, ports: ports, mail: mail, tg: tg, now: now}
	mail.notices, tg.notices = s, s
	return s
}
func noticeReason(reason string) bool {
	return utf8.ValidString(reason) && utf8.RuneCountInString(reason) >= 1 && utf8.RuneCountInString(reason) <= 512 && strings.TrimSpace(reason) != "" && !noticeControls(reason)
}
func noticeEmailEnabled(ctx context.Context, tx pgx.Tx, account uuid.UUID) (bool, error) {
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT email_enabled FROM notice_preferences WHERE account_id=$1),false)`, account).Scan(&enabled)
	return enabled, err
}
func (s *NoticeService) lockActor(ctx context.Context, tx pgx.Tx, actor uuid.UUID) error {
	if actor == uuid.Nil {
		return failure(400, "INVALID_INPUT")
	}
	allowed, err := s.ports.LockOperatorTx(ctx, tx, actor)
	if err != nil {
		return unavailable()
	}
	if !allowed {
		return failure(403, "INVALID_CREDENTIALS")
	}
	return nil
}
func noticeRollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tx.Rollback(ctx)
}
func (s *NoticeService) Preview(ctx context.Context, actor uuid.UUID, in NoticePreviewInput) (NoticePreview, error) {
	var empty NoticePreview
	if !noticeReason(in.Reason) {
		return empty, failure(400, "INVALID_INPUT")
	}
	if in.Mode == "send" {
		if in.ExpectedRevision != 0 || in.Audience != "personal" && in.Audience != "all" || in.Audience == "personal" && in.AccountID == uuid.Nil || in.Audience == "all" && in.AccountID != uuid.Nil {
			return empty, failure(400, "INVALID_INPUT")
		}
	} else if in.Mode == "edit" || in.Mode == "delete" {
		if in.Audience != "" || in.AccountID != uuid.Nil || in.ExpectedRevision <= 0 || in.ExpectedRevision == math.MaxInt32 || in.Mode == "delete" && in.Body != "" {
			return empty, failure(400, "INVALID_INPUT")
		}
	} else {
		return empty, failure(400, "INVALID_INPUT")
	}
	p := noticePreviewRow{NoticePreview: NoticePreview{ID: uuid.New(), Mode: in.Mode, Reason: in.Reason}, ActorID: actor, Audience: in.Audience, ExpectedRevision: in.ExpectedRevision, Snapshot: []noticeProof{}}
	var err error
	if in.Mode != "delete" {
		p.HTML, p.Text, err = normalizeNoticeBody(in.Body)
		if err != nil {
			return empty, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer noticeRollback(tx)
	if err = s.lockActor(ctx, tx, actor); err != nil {
		return empty, err
	}
	var created time.Time
	if tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&created) != nil {
		return empty, unavailable()
	}
	p.ExpiresAt = created.Add(15 * time.Minute)
	if in.Mode == "send" {
		p.NoticeID = p.ID
		ids := []uuid.UUID{in.AccountID}
		if in.Audience == "all" {
			ids, err = s.ports.AudienceTx(ctx, tx)
			if err != nil {
				return empty, unavailable()
			}
		}
		if len(ids) == 0 || len(ids) > math.MaxInt32 {
			return empty, failure(409, "REQUEST_STATE_CONFLICT")
		}
		// ponytail: one O(n) snapshot transaction; batch only when actual audiences exceed its HTTP deadline.
		for _, id := range ids {
			r, exists, err := s.ports.RecipientTx(ctx, tx, id, false)
			if err != nil {
				return empty, unavailable()
			}
			if !exists {
				return empty, failure(404, "NOT_FOUND")
			}
			enabled, err := noticeEmailEnabled(ctx, tx, id)
			if err != nil {
				return empty, unavailable()
			}
			tg := r.TelegramID
			if tg < 0 || tg > 1<<52-1 {
				tg = 0
			}
			p.Snapshot = append(p.Snapshot, noticeProof{ID: uuid.New(), AccountID: id, CredentialVersion: r.CredentialVersion, TelegramID: tg, Locale: r.Locale, EmailHash: digest(r.Email), Eligible: r.Eligible, EmailAvailable: r.EmailAvailable, EmailEnabled: enabled, CabinetVisible: r.Eligible, TelegramAvailable: r.Eligible && tg > 0})
		}
	} else {
		var current int32
		var deleted bool
		var prior uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id,revision,deleted,current_preview_id FROM notices WHERE operator_account_id=$1 ORDER BY sequence DESC LIMIT 1 FOR UPDATE`, actor).Scan(&p.NoticeID, &current, &deleted, &prior)
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, failure(409, "REQUEST_STATE_CONFLICT")
		}
		if err != nil {
			return empty, unavailable()
		}
		if deleted || current != in.ExpectedRevision {
			return empty, failure(409, "REQUEST_STATE_CONFLICT")
		}
		old, err := scanNoticePreview(tx.QueryRow(ctx, `SELECT `+noticePreviewColumns+` FROM notice_previews WHERE id=$1`, prior))
		if err != nil {
			return empty, unavailable()
		}
		p.Audience = old.Audience
		if in.Mode == "delete" {
			p.HTML, p.Text = old.HTML, old.Text
		}
		p.Snapshot, err = s.recipientsTx(ctx, tx, p.NoticeID)
		if err != nil {
			return empty, err
		}
		for i := range p.Snapshot {
			r := &p.Snapshot[i]
			current, exists, err := s.ports.RecipientTx(ctx, tx, r.AccountID, false)
			if err != nil {
				return empty, unavailable()
			}
			r.Eligible = exists && current.Eligible
			if !r.Eligible || r.TelegramID <= 0 || current.TelegramID != r.TelegramID || current.CredentialVersion != r.CredentialVersion || in.Mode == "edit" && (!r.CabinetVisible || r.DismissedAt != nil) {
				continue
			}
			prior, messageAt, attempted, err := noticeTelegramCopyTx(ctx, tx, r.ID)
			if err != nil {
				return empty, unavailable()
			}
			r.TelegramAvailable = prior != uuid.Nil || !attempted
			if in.Mode == "delete" {
				r.TelegramAvailable = prior != uuid.Nil && messageAt != nil && !created.Before(*messageAt) && created.Sub(*messageAt) < 48*time.Hour
			}
		}
	}
	raw, err := json.Marshal(p.Snapshot)
	if err != nil {
		return empty, unavailable()
	}
	if _, err = tx.Exec(ctx, `UPDATE notice_previews SET superseded_at=$2 WHERE operator_account_id=$1 AND confirmed_at IS NULL AND superseded_at IS NULL`, actor, created); err != nil {
		return empty, unavailable()
	}
	if _, err = tx.Exec(ctx, `INSERT INTO notice_previews(id,operator_account_id,notice_id,mode,audience,html,plain_text,reason,expected_revision,recipient_snapshot,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, p.ID, actor, p.NoticeID, p.Mode, p.Audience, p.HTML, p.Text, p.Reason, p.ExpectedRevision, raw, created, p.ExpiresAt); err != nil {
		return empty, unavailable()
	}
	p, err = scanNoticePreview(tx.QueryRow(ctx, `SELECT `+noticePreviewColumns+` FROM notice_previews WHERE id=$1`, p.ID))
	if err != nil || tx.Commit(ctx) != nil {
		return empty, unavailable()
	}
	return p.NoticePreview, nil
}
func (s *NoticeService) recipientsTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) ([]noticeProof, error) {
	rows, err := tx.Query(ctx, `SELECT id,account_id,credential_version,telegram_id,locale,email_hash,email_enabled,cabinet_visible,dismissed_at FROM notice_recipients WHERE notice_id=$1 ORDER BY account_id`, id)
	if err != nil {
		return nil, unavailable()
	}
	defer rows.Close()
	out := []noticeProof{}
	for rows.Next() {
		var r noticeProof
		if rows.Scan(&r.ID, &r.AccountID, &r.CredentialVersion, &r.TelegramID, &r.Locale, &r.EmailHash, &r.EmailEnabled, &r.CabinetVisible, &r.DismissedAt) != nil {
			return nil, unavailable()
		}
		r.Eligible = r.CabinetVisible
		r.EmailAvailable = r.EmailEnabled
		out = append(out, r)
	}
	if rows.Err() != nil {
		return nil, unavailable()
	}
	return out, nil
}
func (s *NoticeService) Confirm(ctx context.Context, actor, id uuid.UUID) (NoticeBatchResult, error) {
	var empty NoticeBatchResult
	if id == uuid.Nil {
		return empty, failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer noticeRollback(tx)
	if err = s.lockActor(ctx, tx, actor); err != nil {
		return empty, err
	}
	p, err := scanNoticePreview(tx.QueryRow(ctx, `SELECT `+noticePreviewColumns+` FROM notice_previews WHERE id=$1 AND operator_account_id=$2 FOR UPDATE`, id, actor))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(404, "NOT_FOUND")
	}
	if err != nil {
		return empty, unavailable()
	}
	if p.ConfirmedAt != nil {
		return s.batchTx(ctx, tx, p)
	}
	var now time.Time
	if tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now) != nil {
		return empty, unavailable()
	}
	if p.SupersededAt != nil || !now.Before(p.ExpiresAt) {
		return empty, failure(409, "REQUEST_STATE_CONFLICT")
	}
	if p.Mode == "send" {
		if _, err = tx.Exec(ctx, `INSERT INTO notices(id,operator_account_id,current_preview_id,revision,created_at) VALUES($1,$2,$1,1,$3)`, p.NoticeID, actor, now); err != nil {
			return empty, unavailable()
		}
	} else {
		var last uuid.UUID
		var revision int32
		var deleted bool
		err = tx.QueryRow(ctx, `SELECT id,revision,deleted FROM notices WHERE operator_account_id=$1 ORDER BY sequence DESC LIMIT 1 FOR UPDATE`, actor).Scan(&last, &revision, &deleted)
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, failure(409, "REQUEST_STATE_CONFLICT")
		}
		if err != nil {
			return empty, unavailable()
		}
		if last != p.NoticeID || revision != p.ExpectedRevision || deleted {
			return empty, failure(409, "REQUEST_STATE_CONFLICT")
		}
		if _, err = tx.Exec(ctx, `UPDATE notices SET current_preview_id=$2,revision=$3,deleted=$4 WHERE id=$1`, p.NoticeID, p.ID, p.Revision, p.Mode == "delete"); err != nil {
			return empty, unavailable()
		}
	}
	for _, proof := range p.Snapshot {
		r, exists, err := s.ports.RecipientTx(ctx, tx, proof.AccountID, false)
		if err != nil {
			return empty, unavailable()
		}
		valid := exists && r.Eligible && r.CredentialVersion == proof.CredentialVersion
		if p.Mode == "send" {
			proof.CabinetVisible = proof.CabinetVisible && valid
			if _, err = tx.Exec(ctx, `INSERT INTO notice_recipients(id,notice_id,account_id,credential_version,telegram_id,locale,email_hash,email_enabled,cabinet_visible) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, proof.ID, p.NoticeID, proof.AccountID, proof.CredentialVersion, proof.TelegramID, proof.Locale, proof.EmailHash, proof.EmailEnabled, proof.CabinetVisible); err != nil {
				return empty, unavailable()
			}
		} else {
			// Dismiss may have committed after preview; always use its current owned fact.
			if tx.QueryRow(ctx, `SELECT cabinet_visible,dismissed_at FROM notice_recipients WHERE id=$1 FOR UPDATE`, proof.ID).Scan(&proof.CabinetVisible, &proof.DismissedAt) != nil {
				return empty, unavailable()
			}
		}
		cabinet := "skipped"
		if proof.CabinetVisible && proof.DismissedAt == nil {
			cabinet = "succeeded"
		}
		if p.Mode == "edit" && cabinet == "succeeded" && (!exists || !r.Eligible) {
			cabinet = "skipped"
		}
		action := uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO notice_actions(id,preview_id,recipient_id,cabinet_state,telegram_state,email_state) VALUES($1,$2,$3,$4,'skipped','skipped')`, action, p.ID, proof.ID, cabinet); err != nil {
			return empty, unavailable()
		}
		if err = s.enqueueChannelsTx(ctx, tx, p, proof, r, valid, action, now); err != nil {
			return empty, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE notice_previews SET confirmed_at=$2 WHERE id=$1`, p.ID, now); err != nil {
		return empty, unavailable()
	}
	account := actor
	if p.Audience == "personal" {
		account = p.Snapshot[0].AccountID
	}
	action := map[string]string{"send": "notice_sent", "edit": "notice_edited", "delete": "notice_deleted"}[p.Mode]
	if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: p.ID, AccountID: account, CreatedAt: now, Action: action, OperatorAccountID: &actor, Reason: &p.Reason}) != nil {
		return empty, unavailable()
	}
	out, err := s.batchTx(ctx, tx, p)
	if err != nil {
		return empty, err
	}
	if tx.Commit(ctx) != nil {
		return empty, unavailable()
	}
	return out, nil
}
func (s *NoticeService) batchTx(ctx context.Context, tx pgx.Tx, p noticePreviewRow) (NoticeBatchResult, error) {
	out := NoticeBatchResult{Version: noticeVersion, Notice: &p.NoticePreview, Revision: p.Revision}
	rows, err := tx.Query(ctx, `SELECT cabinet_state,telegram_state,email_state FROM notice_actions WHERE preview_id=$1`, p.ID)
	if err != nil {
		return out, unavailable()
	}
	defer rows.Close()
	for rows.Next() {
		var states [3]string
		if rows.Scan(&states[0], &states[1], &states[2]) != nil {
			return out, unavailable()
		}
		for i, count := range []*NoticeChannelCounts{&out.Cabinet, &out.Telegram, &out.Email} {
			switch states[i] {
			case "pending":
				count.Pending++
			case "succeeded":
				count.Succeeded++
			case "failed":
				count.Failed++
			case "skipped":
				count.Skipped++
			case "unknown":
				count.Unknown++
			case "unchanged":
				count.Unchanged++
			default:
				return out, unavailable()
			}
		}
	}
	if rows.Err() != nil {
		return out, unavailable()
	}
	return out, nil
}
func (s *NoticeService) Last(ctx context.Context, actor uuid.UUID) (NoticeBatchResult, error) {
	out := NoticeBatchResult{Version: noticeVersion}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer noticeRollback(tx)
	if err = s.lockActor(ctx, tx, actor); err != nil {
		return out, err
	}
	p, err := scanNoticePreview(tx.QueryRow(ctx, `SELECT `+noticePreviewColumns+` FROM notice_previews WHERE id=(SELECT current_preview_id FROM notices WHERE operator_account_id=$1 ORDER BY sequence DESC LIMIT 1)`, actor))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, unavailable()
	}
	return s.batchTx(ctx, tx, p)
}
func (s *NoticeService) Read(ctx context.Context, account uuid.UUID, page int32) (NoticeResult, error) {
	out := NoticeResult{Version: noticeVersion, Page: page, PerPage: 20, Notices: []Notice{}}
	if account == uuid.Nil || page < 1 {
		return out, failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer noticeRollback(tx)
	r, exists, err := s.ports.RecipientTx(ctx, tx, account, false)
	if err != nil {
		return out, unavailable()
	}
	if !exists || !r.Eligible {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	out.EmailAvailable = r.EmailAvailable
	out.EmailEnabled, err = noticeEmailEnabled(ctx, tx, account)
	if err != nil {
		return out, unavailable()
	}
	if tx.QueryRow(ctx, `SELECT count(*) FROM notice_recipients r JOIN notices n ON n.id=r.notice_id WHERE r.account_id=$1 AND r.cabinet_visible AND r.dismissed_at IS NULL AND NOT n.deleted`, account).Scan(&out.Total) != nil {
		return out, unavailable()
	}
	rows, err := tx.Query(ctx, `SELECT n.id,n.revision,p.html,p.plain_text,n.created_at FROM notice_recipients r JOIN notices n ON n.id=r.notice_id JOIN notice_previews p ON p.id=n.current_preview_id WHERE r.account_id=$1 AND r.cabinet_visible AND r.dismissed_at IS NULL AND NOT n.deleted ORDER BY n.sequence DESC LIMIT 20 OFFSET $2`, account, (int64(page)-1)*20)
	if err != nil {
		return out, unavailable()
	}
	defer rows.Close()
	for rows.Next() {
		var n Notice
		if rows.Scan(&n.ID, &n.Revision, &n.HTML, &n.Text, &n.CreatedAt) != nil {
			return out, unavailable()
		}
		out.Notices = append(out.Notices, n)
	}
	if rows.Err() != nil {
		return out, unavailable()
	}
	return out, nil
}
func (s *NoticeService) withRecipient(ctx context.Context, account uuid.UUID, work func(pgx.Tx, ReminderRecipient) error) error {
	if account == uuid.Nil {
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	before, exists, err := s.ports.RecipientTx(ctx, tx, account, false)
	noticeRollback(tx)
	if err != nil {
		return unavailable()
	}
	if !exists {
		return failure(404, "NOT_FOUND")
	}
	run := func(tx pgx.Tx) error {
		defer noticeRollback(tx)
		r, exists, err := s.ports.RecipientTx(ctx, tx, account, true)
		if err != nil {
			return unavailable()
		}
		if !exists || !r.Eligible {
			return failure(403, "INVALID_CREDENTIALS")
		}
		if r.Email != before.Email || r.CredentialVersion != before.CredentialVersion {
			return failure(409, "REQUEST_STATE_CONFLICT")
		}
		if err = work(tx, r); err != nil {
			return err
		}
		if tx.Commit(ctx) != nil {
			return unavailable()
		}
		return nil
	}
	if before.Email != "" {
		return s.mail.guard(ctx, before.Email, func(conn *pgxpool.Conn) error {
			tx, err := conn.Begin(ctx)
			if err != nil {
				return unavailable()
			}
			return run(tx)
		})
	}
	tx, err = s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	return run(tx)
}
func (s *NoticeService) SetEmailPreference(ctx context.Context, account uuid.UUID, enabled bool) (NoticeResult, error) {
	err := s.withRecipient(ctx, account, func(tx pgx.Tx, r ReminderRecipient) error {
		if enabled && !r.EmailAvailable {
			return failure(409, "EMAIL_LOGIN_REQUIRED")
		}
		old, err := noticeEmailEnabled(ctx, tx, account)
		if err != nil {
			return unavailable()
		}
		if old == enabled {
			return nil
		}
		if _, err = tx.Exec(ctx, `INSERT INTO notice_preferences(account_id,email_enabled,updated_at) VALUES($1,$2,$3) ON CONFLICT(account_id) DO UPDATE SET email_enabled=EXCLUDED.email_enabled,updated_at=EXCLUDED.updated_at`, account, enabled, s.now()); err != nil {
			return unavailable()
		}
		if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: account, CreatedAt: s.now(), Action: "notice_preferences_updated"}) != nil {
			return unavailable()
		}
		return nil
	})
	if err != nil {
		return NoticeResult{}, err
	}
	return s.Read(ctx, account, 1)
}
func (s *NoticeService) Dismiss(ctx context.Context, account, id uuid.UUID) error {
	if id == uuid.Nil {
		return failure(400, "INVALID_INPUT")
	}
	return s.withRecipient(ctx, account, func(tx pgx.Tx, r ReminderRecipient) error {
		var dismissed *time.Time
		err := tx.QueryRow(ctx, `SELECT dismissed_at FROM notice_recipients WHERE notice_id=$1 AND account_id=$2 FOR UPDATE`, id, account).Scan(&dismissed)
		if errors.Is(err, pgx.ErrNoRows) {
			return failure(404, "NOT_FOUND")
		}
		if err != nil {
			return unavailable()
		}
		if dismissed != nil {
			return nil
		}
		if _, err = tx.Exec(ctx, `UPDATE notice_recipients SET dismissed_at=$3 WHERE notice_id=$1 AND account_id=$2`, id, account, s.now()); err != nil {
			return unavailable()
		}
		if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: account, CreatedAt: s.now(), Action: "notice_dismissed"}) != nil {
			return unavailable()
		}
		return nil
	})
}
