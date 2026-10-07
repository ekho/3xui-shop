package notifications

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
	"unicode/utf8"
)

type ClientGuard func(context.Context, uuid.UUID, int64, int64, func(pgx.Tx) error) (bool, error)
type ClientNotice struct {
	AccountID                     uuid.UUID
	TelegramID, CredentialVersion int64
	Locale, EventKey, Route       string
}
type ClientJob struct {
	ClientNotice
	ID             uuid.UUID
	LeaseToken     string
	LeaseExpiresAt time.Time
}
type ClientOutcome struct {
	State     string
	MessageID int64
	Code      string
}

func clientRoute(route string) bool {
	switch route {
	case "cabinet", "history", "support":
		return true
	}
	if !strings.HasPrefix(route, "orders:") {
		return false
	}
	id, err := uuid.Parse(strings.TrimPrefix(route, "orders:"))
	return err == nil && id != uuid.Nil && route == "orders:"+id.String()
}
func (s *Service) EnqueueClientTx(ctx context.Context, tx pgx.Tx, n ClientNotice, created time.Time) error {
	if tx == nil || n.AccountID == uuid.Nil || n.TelegramID <= 0 || n.TelegramID > 1<<52-1 || n.CredentialVersion < 0 || (n.Locale != "ru" && n.Locale != "en") || len(n.EventKey) == 0 || len(n.EventKey) > 128 || !utf8.ValidString(n.EventKey) || strings.ContainsRune(n.EventKey, '\x00') || !clientRoute(n.Route) || created.IsZero() {
		return failure(400, "INVALID_INPUT")
	}
	// Keep the first recipient proof; credential changes cannot block a business replay.
	_, err := tx.Exec(ctx, `INSERT INTO client_telegram_deliveries(id,account_id,telegram_id,credential_version,locale,event_key,route,created_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(account_id,event_key) DO NOTHING`,
		uuid.New(), n.AccountID, n.TelegramID, n.CredentialVersion, n.Locale, n.EventKey, n.Route, created)
	if err != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) ClaimClient(ctx context.Context) (*ClientJob, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, unavailable()
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	j := ClientJob{LeaseToken: token}
	err := s.pool.QueryRow(ctx, `WITH candidate AS(
 SELECT id FROM client_telegram_deliveries WHERE state='pending' AND available_at<=clock_timestamp()
 AND (lease_expires_at IS NULL OR lease_expires_at<=clock_timestamp()) ORDER BY sequence LIMIT 1 FOR UPDATE SKIP LOCKED)
 UPDATE client_telegram_deliveries SET lease_hash=$1,lease_expires_at=clock_timestamp()+interval '60 seconds',attempts=attempts+1
 WHERE id=(SELECT id FROM candidate) RETURNING id,account_id,telegram_id,credential_version,locale,event_key,route,lease_expires_at`, digest(token)).
		Scan(&j.ID, &j.AccountID, &j.TelegramID, &j.CredentialVersion, &j.Locale, &j.EventKey, &j.Route, &j.LeaseExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, unavailable()
	}
	return &j, nil
}
func lockClient(ctx context.Context, tx pgx.Tx, j ClientJob) (string, error) {
	if j.ID == uuid.Nil || len(j.LeaseToken) != 43 {
		return "", failure(409, "REQUEST_STATE_CONFLICT")
	}
	var n ClientNotice
	var hash []byte
	var state string
	var valid bool
	err := tx.QueryRow(ctx, `SELECT account_id,telegram_id,credential_version,locale,event_key,route,lease_hash,state,
 lease_expires_at>clock_timestamp()+interval '10 seconds' FROM client_telegram_deliveries WHERE id=$1 FOR UPDATE`, j.ID).
		Scan(&n.AccountID, &n.TelegramID, &n.CredentialVersion, &n.Locale, &n.EventKey, &n.Route, &hash, &state, &valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", failure(409, "REQUEST_STATE_CONFLICT")
	}
	if err != nil {
		return "", unavailable()
	}
	if !valid || n != j.ClientNotice || subtle.ConstantTimeCompare(hash, digest(j.LeaseToken)) != 1 {
		return "", failure(409, "REQUEST_STATE_CONFLICT")
	}
	return state, nil
}
func finishClient(ctx context.Context, tx pgx.Tx, j ClientJob, out ClientOutcome) error {
	var message *int64
	var code *string
	switch out.State {
	case "sent":
		if out.MessageID <= 0 || out.Code != "" {
			return failure(400, "INVALID_INPUT")
		}
		message = &out.MessageID
	case "failed":
		if out.MessageID != 0 || (out.Code != "forbidden" && out.Code != "bad_request") {
			return failure(400, "INVALID_INPUT")
		}
		code = &out.Code
	case "skipped":
		if out.MessageID != 0 || out.Code != "" {
			return failure(400, "INVALID_INPUT")
		}
	default:
		return failure(400, "INVALID_INPUT")
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return unavailable()
	}
	r, err := tx.Exec(ctx, `UPDATE client_telegram_deliveries SET state=$2,message_id=$3,failure_code=$4,result_hash=$5,completed_at=clock_timestamp()
 WHERE id=$1 AND state='pending' AND lease_hash=$6 AND lease_expires_at>clock_timestamp()`, j.ID, out.State, message, code, digest(string(raw)), digest(j.LeaseToken))
	if err != nil {
		return unavailable()
	}
	if r.RowsAffected() != 1 {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	return nil
}
func (s *Service) DeliverClient(parent context.Context, j ClientJob, send func() (ClientOutcome, error)) error {
	if s.clientGuard == nil || send == nil {
		return unavailable()
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	valid, err := s.clientGuard(ctx, j.AccountID, j.TelegramID, j.CredentialVersion, func(tx pgx.Tx) error {
		state, err := lockClient(ctx, tx, j)
		if err != nil {
			return err
		}
		if state != "pending" {
			return nil
		}
		out, err := send()
		if err != nil {
			return err
		}
		return finishClient(ctx, tx, j, out)
	})
	if err != nil {
		return err
	}
	if valid {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		tx.Rollback(cleanup)
	}()
	state, err := lockClient(ctx, tx, j)
	if err != nil {
		return err
	}
	if state != "pending" {
		return nil
	}
	if err = finishClient(ctx, tx, j, ClientOutcome{State: "skipped"}); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}

func (s *Service) CloseClient(parent context.Context, id uuid.UUID, tg, message int64, close func() error) (bool, error) {
	if id == uuid.Nil || tg <= 0 || tg > 1<<52-1 || message <= 0 || close == nil {
		return false, failure(400, "INVALID_INPUT")
	}
	if s.clientGuard == nil {
		return false, unavailable()
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	var account uuid.UUID
	var version int64
	err := s.pool.QueryRow(ctx, `SELECT account_id,credential_version FROM client_telegram_deliveries WHERE id=$1 AND telegram_id=$2 AND message_id=$3 AND state='sent'`, id, tg, message).Scan(&account, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, unavailable()
	}
	closed := false
	valid, err := s.clientGuard(ctx, account, tg, version, func(tx pgx.Tx) error {
		var found uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM client_telegram_deliveries WHERE id=$1 AND account_id=$2 AND credential_version=$3 AND telegram_id=$4 AND message_id=$5 AND state='sent' FOR UPDATE`, id, account, version, tg, message).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return unavailable()
		}
		if err = close(); err != nil {
			return err
		}
		closed = true
		return nil
	})
	return valid && closed, err
}
