package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"example.com/cabinet/backend/internal/modules/audit_reports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReminderRecipient struct {
	AccountID                     uuid.UUID
	TelegramID, CredentialVersion int64
	Email, Locale                 string
	Eligible, EmailAvailable      bool
}
type ReminderAccess struct {
	Known                           bool
	ExpiryMS, UsedBytes, LimitBytes int64
	TrafficPeriod                   string
	ObservedAt                      time.Time
}
type ReminderStars struct {
	Known, SuppressExpiry, Lapsed bool
	Period                        string
	PaidUntil                     *time.Time
}
type ReminderPorts struct {
	AudienceTx  func(context.Context, pgx.Tx) ([]uuid.UUID, error)
	RecipientTx func(context.Context, pgx.Tx, uuid.UUID, bool) (ReminderRecipient, error)
	AccessTx    func(context.Context, pgx.Tx, []uuid.UUID) (map[uuid.UUID]ReminderAccess, error)
	PeriodTx    func(context.Context, pgx.Tx, uuid.UUID) (ReminderAccess, error)
	StarsTx     func(context.Context, pgx.Tx, uuid.UUID) (ReminderStars, error)
	MailGuard   func(context.Context, string, func(*pgxpool.Conn) error) error
}

type Reminder struct {
	ID                uuid.UUID  `json:"id"`
	Kind              string     `json:"kind"`
	Threshold         int        `json:"threshold"`
	ObservedAt        time.Time  `json:"observed_at"`
	ExpiresAt         *time.Time `json:"expires_at"`
	TrafficUsedBytes  *string    `json:"traffic_used_bytes"`
	TrafficLimitBytes *string    `json:"traffic_limit_bytes"`
	PaidUntil         *time.Time `json:"paid_until"`
	Route             string     `json:"route"`
}
type ReminderResult struct {
	Version        string     `json:"version"`
	EmailEnabled   bool       `json:"email_enabled"`
	EmailAvailable bool       `json:"email_available"`
	Reminders      []Reminder `json:"reminders"`
}

func expiryThreshold(expiryMS int64, now time.Time) int {
	if expiryMS <= 0 {
		return 0
	}
	remaining := time.UnixMilli(expiryMS).Sub(now)
	if remaining < 0 {
		return 0
	}
	if remaining < 48*time.Hour {
		return 1
	}
	if remaining < 96*time.Hour {
		return 3
	}
	return 0
}
func trafficThreshold(used, quota int64) int {
	if quota <= 0 || used < 0 {
		return 0
	}
	if used >= quota {
		return 100
	}
	if used >= quota-quota/5 {
		return 80
	}
	return 0
}

type ReminderService struct {
	pool  *pgxpool.Pool
	ports ReminderPorts
	mail  *MailService
	tg    *Service
	now   func() time.Time
}

func NewReminders(pool *pgxpool.Pool, ports ReminderPorts, mail *MailService, tg *Service, now func() time.Time) *ReminderService {
	s := &ReminderService{pool: pool, ports: ports, mail: mail, tg: tg, now: now}
	mail.reminders, tg.reminders = s, s
	return s
}

type reminderRow struct {
	ID, AccountID                   uuid.UUID
	Kind, Period                    string
	Threshold                       int
	ObservedAt                      time.Time
	ExpiryMS, UsedBytes, LimitBytes int64
	PaidUntil, DismissedAt          *time.Time
	EmailQueued, TelegramQueued     bool
	EmailVersion                    *int64
}

const reminderColumns = `id,account_id,kind,period,threshold,observed_at,expiry_ms,used_bytes,limit_bytes,paid_until,dismissed_at,email_queued,telegram_queued,email_credential_version`

func scanReminder(row pgx.Row) (r reminderRow, err error) {
	err = row.Scan(&r.ID, &r.AccountID, &r.Kind, &r.Period, &r.Threshold, &r.ObservedAt, &r.ExpiryMS, &r.UsedBytes, &r.LimitBytes, &r.PaidUntil, &r.DismissedAt, &r.EmailQueued, &r.TelegramQueued, &r.EmailVersion)
	return
}

func (r reminderRow) public() Reminder {
	v := Reminder{ID: r.ID, Kind: r.Kind, Threshold: r.Threshold, ObservedAt: r.ObservedAt, PaidUntil: r.PaidUntil, Route: "cabinet"}
	if r.Kind == "expiry" {
		t := time.UnixMilli(r.ExpiryMS).UTC()
		v.ExpiresAt = &t
	} else if r.Kind == "traffic" {
		used, limit := strconv.FormatInt(r.UsedBytes, 10), strconv.FormatInt(r.LimitBytes, 10)
		v.TrafficUsedBytes, v.TrafficLimitBytes = &used, &limit
	}
	return v
}

func reminderCurrent(r reminderRow, access ReminderAccess, stars ReminderStars, now time.Time) bool {
	if r.DismissedAt != nil || !access.Known {
		return false
	}
	switch r.Kind {
	case "expiry":
		return stars.Known && !stars.SuppressExpiry && access.ExpiryMS == r.ExpiryMS && r.Period == strconv.FormatInt(access.ExpiryMS, 10) && expiryThreshold(access.ExpiryMS, now) == r.Threshold
	case "traffic":
		return access.TrafficPeriod != "" && access.TrafficPeriod == r.Period && access.LimitBytes == r.LimitBytes && trafficThreshold(r.UsedBytes, r.LimitBytes) == r.Threshold
	case "stars_lapsed":
		return stars.Known && stars.Lapsed && stars.Period == r.Period && stars.PaidUntil != nil && r.PaidUntil != nil && stars.PaidUntil.Equal(*r.PaidUntil)
	}
	return false
}

func reminderEmailEnabled(ctx context.Context, tx pgx.Tx, account uuid.UUID) (bool, error) {
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT email_enabled FROM reminder_preferences WHERE account_id=$1),false)`, account).Scan(&enabled)
	return enabled, err
}

func (s *ReminderService) currentTx(ctx context.Context, tx pgx.Tx, account uuid.UUID) (ReminderRecipient, ReminderAccess, ReminderStars, error) {
	recipient, err := s.ports.RecipientTx(ctx, tx, account, false)
	if err != nil {
		return recipient, ReminderAccess{}, ReminderStars{}, unavailable()
	}
	access, err := s.ports.PeriodTx(ctx, tx, account)
	if err != nil {
		return recipient, access, ReminderStars{}, unavailable()
	}
	stars, err := s.ports.StarsTx(ctx, tx, account)
	if err != nil {
		return recipient, access, stars, unavailable()
	}
	return recipient, access, stars, nil
}

const reminderSuperseded = `SELECT EXISTS(SELECT 1 FROM reminders WHERE account_id=$1 AND kind=$2 AND period=$3 AND
 ((kind='expiry' AND threshold<$4) OR (kind='traffic' AND threshold>$4)))`

func (s *ReminderService) Read(ctx context.Context, account uuid.UUID) (ReminderResult, error) {
	out := ReminderResult{Version: "reminders-v1", Reminders: []Reminder{}}
	if account == uuid.Nil {
		return out, failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	recipient, access, stars, err := s.currentTx(ctx, tx, account)
	if err != nil {
		return out, err
	}
	if !recipient.Eligible {
		return out, failure(403, "INVALID_CREDENTIALS")
	}
	out.EmailAvailable = recipient.EmailAvailable
	out.EmailEnabled, err = reminderEmailEnabled(ctx, tx, account)
	if err != nil {
		return out, unavailable()
	}
	// ponytail: scan retained own events; add bounded current-period queries with history retention if this grows beyond a small account history.
	rows, err := tx.Query(ctx, `SELECT `+reminderColumns+` FROM reminders r WHERE account_id=$1 AND dismissed_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM reminders newer WHERE newer.account_id=r.account_id AND newer.kind=r.kind AND newer.period=r.period
 AND ((r.kind='expiry' AND newer.threshold<r.threshold) OR (r.kind='traffic' AND newer.threshold>r.threshold)))
 ORDER BY observed_at DESC,id DESC`, account)
	if err != nil {
		return out, unavailable()
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanReminder(rows)
		if err != nil {
			return out, unavailable()
		}
		if reminderCurrent(r, access, stars, s.now()) {
			out.Reminders = append(out.Reminders, r.public())
			if len(out.Reminders) == 20 {
				break
			}
		}
	}
	if rows.Err() != nil {
		return out, unavailable()
	}
	return out, nil
}

// Preflight releases its pool connection before acquiring the existing email guard.
func (s *ReminderService) withRecipient(ctx context.Context, account uuid.UUID, work func(pgx.Tx, ReminderRecipient) error) error {
	if account == uuid.Nil {
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	before, err := s.ports.RecipientTx(ctx, tx, account, false)
	tx.Rollback(ctx)
	if err != nil {
		return unavailable()
	}
	run := func(tx pgx.Tx) error {
		defer tx.Rollback(ctx)
		a, err := s.ports.RecipientTx(ctx, tx, account, true)
		if err != nil {
			return unavailable()
		}
		if !a.Eligible {
			return failure(403, "INVALID_CREDENTIALS")
		}
		if a.Email != before.Email || a.CredentialVersion != before.CredentialVersion {
			return failure(409, "REQUEST_STATE_CONFLICT")
		}
		if err = work(tx, a); err != nil {
			return err
		}
		if tx.Commit(ctx) != nil {
			return unavailable()
		}
		return nil
	}
	if before.Email != "" {
		return s.ports.MailGuard(ctx, before.Email, func(conn *pgxpool.Conn) error {
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

func (s *ReminderService) SetEmailPreference(ctx context.Context, account uuid.UUID, enabled bool) (ReminderResult, error) {
	err := s.withRecipient(ctx, account, func(tx pgx.Tx, a ReminderRecipient) error {
		if enabled && !a.EmailAvailable {
			return failure(409, "EMAIL_LOGIN_REQUIRED")
		}
		old, err := reminderEmailEnabled(ctx, tx, account)
		if err != nil {
			return unavailable()
		}
		if old == enabled {
			return nil
		}
		if _, err = tx.Exec(ctx, `INSERT INTO reminder_preferences(account_id,email_enabled,updated_at) VALUES($1,$2,$3)
 ON CONFLICT(account_id) DO UPDATE SET email_enabled=EXCLUDED.email_enabled,updated_at=EXCLUDED.updated_at`, account, enabled, s.now()); err != nil {
			return unavailable()
		}
		if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: "notification_preferences_updated", AccountID: account}) != nil {
			return unavailable()
		}
		return nil
	})
	if err != nil {
		return ReminderResult{}, err
	}
	return s.Read(ctx, account)
}

func (s *ReminderService) Dismiss(ctx context.Context, account, id uuid.UUID) error {
	if id == uuid.Nil {
		return failure(400, "INVALID_INPUT")
	}
	return s.withRecipient(ctx, account, func(tx pgx.Tx, a ReminderRecipient) error {
		r, err := scanReminder(tx.QueryRow(ctx, `SELECT `+reminderColumns+` FROM reminders WHERE id=$1 AND account_id=$2 FOR UPDATE`, id, account))
		if errors.Is(err, pgx.ErrNoRows) {
			return failure(404, "NOT_FOUND")
		}
		if err != nil {
			return unavailable()
		}
		if r.DismissedAt != nil {
			return nil
		}
		if _, err = tx.Exec(ctx, `UPDATE reminders SET dismissed_at=$2 WHERE id=$1`, id, s.now()); err != nil {
			return unavailable()
		}
		if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: "notification_dismissed", AccountID: account}) != nil {
			return unavailable()
		}
		return nil
	})
}

func reminderSeeds(a ReminderAccess, stars ReminderStars, now time.Time) []reminderRow {
	var rows []reminderRow
	if !a.Known {
		return rows
	}
	if threshold := expiryThreshold(a.ExpiryMS, now); stars.Known && !stars.SuppressExpiry && threshold != 0 {
		rows = append(rows, reminderRow{Kind: "expiry", Period: strconv.FormatInt(a.ExpiryMS, 10), Threshold: threshold, ExpiryMS: a.ExpiryMS, ObservedAt: a.ObservedAt})
	}
	if threshold := trafficThreshold(a.UsedBytes, a.LimitBytes); a.TrafficPeriod != "" && threshold != 0 {
		rows = append(rows, reminderRow{Kind: "traffic", Period: a.TrafficPeriod, Threshold: threshold, UsedBytes: a.UsedBytes, LimitBytes: a.LimitBytes, ObservedAt: a.ObservedAt})
	}
	if stars.Known && stars.Lapsed && stars.PaidUntil != nil {
		rows = append(rows, reminderRow{Kind: "stars_lapsed", Period: stars.Period, PaidUntil: stars.PaidUntil, ObservedAt: a.ObservedAt})
	}
	return rows
}

func (s *ReminderService) generateAccount(ctx context.Context, account uuid.UUID, observed ReminderAccess) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	a, err := s.ports.RecipientTx(ctx, tx, account, true)
	if err != nil {
		return unavailable()
	}
	if !a.Eligible {
		return nil
	}
	current, err := s.ports.PeriodTx(ctx, tx, account)
	if err != nil {
		return unavailable()
	}
	if !current.Known || current.ExpiryMS != observed.ExpiryMS || current.LimitBytes != observed.LimitBytes || current.TrafficPeriod != observed.TrafficPeriod {
		return nil
	}
	stars, err := s.ports.StarsTx(ctx, tx, account)
	if err != nil {
		return unavailable()
	}
	emailEnabled, err := reminderEmailEnabled(ctx, tx, account)
	if err != nil {
		return unavailable()
	}
	for _, seed := range reminderSeeds(observed, stars, s.now()) {
		seed.ID, seed.AccountID = uuid.New(), account
		r, err := scanReminder(tx.QueryRow(ctx, `INSERT INTO reminders(id,account_id,kind,period,threshold,observed_at,expiry_ms,used_bytes,limit_bytes,paid_until)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(account_id,kind,period,threshold) DO NOTHING RETURNING `+reminderColumns,
			seed.ID, account, seed.Kind, seed.Period, seed.Threshold, seed.ObservedAt, seed.ExpiryMS, seed.UsedBytes, seed.LimitBytes, seed.PaidUntil))
		if errors.Is(err, pgx.ErrNoRows) {
			r, err = scanReminder(tx.QueryRow(ctx, `SELECT `+reminderColumns+` FROM reminders WHERE account_id=$1 AND kind=$2 AND period=$3 AND threshold=$4 FOR UPDATE`, account, seed.Kind, seed.Period, seed.Threshold))
		} else if err == nil {
			if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: r.ID, CreatedAt: r.ObservedAt, Action: "notification_created", AccountID: account}) != nil {
				return unavailable()
			}
		}
		if err != nil {
			return unavailable()
		}
		var superseded bool
		if err = tx.QueryRow(ctx, reminderSuperseded, account, r.Kind, r.Period, r.Threshold).Scan(&superseded); err != nil {
			return unavailable()
		}
		if !reminderCurrent(r, current, stars, s.now()) || superseded {
			continue
		}
		if !r.TelegramQueued && a.TelegramID > 0 && a.TelegramID <= 1<<52-1 {
			if err = s.tg.EnqueueClientTx(ctx, tx, ClientNotice{AccountID: account, TelegramID: a.TelegramID, CredentialVersion: a.CredentialVersion, Locale: a.Locale, EventKey: "reminder:" + r.ID.String(), Route: r.public().Route, ReminderID: r.ID}, s.now()); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE reminders SET telegram_queued=true WHERE id=$1`, r.ID); err != nil {
				return unavailable()
			}
		}
		if !r.EmailQueued && emailEnabled && a.EmailAvailable {
			if err = s.mail.EnqueueMailTx(ctx, tx, a.Email, nil, nil, "reminder", MailPayload{Type: "reminder", Locale: a.Locale, ReminderID: &r.ID}, s.now()); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE reminders SET email_queued=true,email_credential_version=$2 WHERE id=$1`, r.ID, a.CredentialVersion); err != nil {
				return unavailable()
			}
		}
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}

func (s *ReminderService) Generate(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 12*time.Minute)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return unavailable()
	}
	ids, err := s.ports.AudienceTx(ctx, tx)
	if err != nil {
		tx.Rollback(ctx)
		return unavailable()
	}
	facts, err := s.ports.AccessTx(ctx, tx, ids)
	tx.Rollback(ctx)
	if err != nil {
		return unavailable()
	}
	// ponytail: one configured panel and O(n) audience; add durable batches before a pass approaches the 15-minute interval.
	var failed bool
	for _, id := range ids {
		if ctx.Err() != nil {
			return unavailable()
		}
		if fact := facts[id]; fact.Known {
			if s.generateAccount(ctx, id, fact) != nil {
				failed = true
			}
		}
	}
	if failed {
		return unavailable()
	}
	return nil
}

func (s *ReminderService) RunScheduler(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		if s.Generate(ctx) != nil && ctx.Err() == nil {
			slog.Warn("subscription reminder pass unavailable")
		}
		timer := time.NewTimer(15 * time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func reminderMessage(r reminderRow, locale string) string {
	message := ""
	if locale == "ru" {
		switch r.Kind {
		case "expiry":
			message = "Срок подписки приближается: " + time.UnixMilli(r.ExpiryMS).UTC().Format(time.RFC3339)
		case "traffic":
			message = fmt.Sprintf("Израсходовано не менее %d%% трафика: %d из %d байт.", r.Threshold, r.UsedBytes, r.LimitBytes)
		case "stars_lapsed":
			message = "Оплаченный период Stars завершён. Проверьте состояние подписки в кабинете."
		}
		return message + "\nДанные на " + r.ObservedAt.UTC().Format(time.RFC3339)
	}
	switch r.Kind {
	case "expiry":
		message = "Your subscription expires soon: " + time.UnixMilli(r.ExpiryMS).UTC().Format(time.RFC3339)
	case "traffic":
		message = fmt.Sprintf("At least %d%% of traffic used: %d of %d bytes.", r.Threshold, r.UsedBytes, r.LimitBytes)
	case "stars_lapsed":
		message = "The paid Stars period has ended. Check your subscription in the cabinet."
	}
	return message + "\nObserved at " + r.ObservedAt.UTC().Format(time.RFC3339)
}

func (s *ReminderService) deliveryTx(ctx context.Context, tx pgx.Tx, account, id uuid.UUID, email string) (reminderRow, bool, error) {
	r, err := scanReminder(tx.QueryRow(ctx, `SELECT `+reminderColumns+` FROM reminders WHERE id=$1 AND account_id=$2 FOR UPDATE`, id, account))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, unavailable()
	}
	a, access, stars, err := s.currentTx(ctx, tx, account)
	if err != nil {
		return r, false, err
	}
	if !a.Eligible || !reminderCurrent(r, access, stars, s.now()) {
		return r, false, nil
	}
	var superseded bool
	if tx.QueryRow(ctx, reminderSuperseded, account, r.Kind, r.Period, r.Threshold).Scan(&superseded) != nil {
		return r, false, unavailable()
	}
	if superseded {
		return r, false, nil
	}
	if email != "" {
		enabled, err := reminderEmailEnabled(ctx, tx, account)
		if err != nil {
			return r, false, unavailable()
		}
		if !enabled || !a.EmailAvailable || a.Email != email || r.EmailVersion == nil || *r.EmailVersion != a.CredentialVersion {
			return r, false, nil
		}
	}
	return r, true, nil
}
