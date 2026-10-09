package notifications

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"example.com/cabinet/backend/internal/modules/notifications/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string              { return e.Code }
func failure(status int, code string) error { return &Error{Status: status, Code: code, Message: code} }
func unavailable() error                    { return failure(503, "SERVICE_UNAVAILABLE") }

type TelegramJob struct {
	ChatID         int64           `json:"chat_id"`
	JobID          uuid.UUID       `json:"job_id"`
	Kind           string          `json:"kind"`
	LeaseExpiresAt time.Time       `json:"lease_expires_at"`
	LeaseToken     string          `json:"lease_token"`
	Payload        json.RawMessage `json:"payload"`
}

// Field order matches outcomes already persisted by the former wire union.
type TelegramSent struct {
	ChatId    int64  `json:"chat_id"`
	Kind      string `json:"kind"`
	MessageId int64  `json:"message_id"`
}
type TelegramFailed struct {
	Code string `json:"code"`
	Kind string `json:"kind"`
}
type Service struct {
	pool        *pgxpool.Pool
	reminders   *ReminderService
	notices     *NoticeService
	clientGuard ClientGuard
	operators   func() []int64
	allowed     func(int64) bool
	card        func(context.Context, pgx.Tx, uuid.UUID, int64) (json.RawMessage, error)
}

func New(pool *pgxpool.Pool, operators func() []int64, allowed func(int64) bool, card func(context.Context, pgx.Tx, uuid.UUID, int64) (json.RawMessage, error), guard ClientGuard) *Service {
	return &Service{pool: pool, operators: operators, allowed: allowed, card: card, clientGuard: guard}
}
func (s *Service) EnqueueTelegramTx(ctx context.Context, tx pgx.Tx, request uuid.UUID, operation *uuid.UUID, chat int64, kind string, payload json.RawMessage, createdAt time.Time) error {
	if store.New(tx).AddTelegramDelivery(ctx, store.AddTelegramDeliveryParams{ID: uuid.New(), RequestID: request, OperationID: operation, ChatID: chat, Kind: kind, Payload: payload, CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true}}) != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) LatestTelegramMessageTx(ctx context.Context, tx pgx.Tx, request uuid.UUID, chat int64) (*int64, error) {
	message, err := store.New(tx).LatestTelegramMessage(ctx, store.LatestTelegramMessageParams{RequestID: request, ChatID: chat})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, unavailable()
	}
	if !message.Valid {
		return nil, nil
	}
	return &message.Int64, nil
}
func (s *Service) LatestTelegramStateTx(ctx context.Context, tx pgx.Tx, request uuid.UUID, chat int64) (string, error) {
	state, err := store.New(tx).LatestTelegramState(ctx, store.LatestTelegramStateParams{RequestID: request, ChatID: chat})
	if errors.Is(err, pgx.ErrNoRows) {
		return "pending", nil
	}
	if err != nil {
		return "", unavailable()
	}
	return state, nil
}
func digest(v string) []byte { h := sha256.Sum256([]byte(v)); return h[:] }
func (s *Service) ClaimTelegramJobs(ctx context.Context, limit int) ([]TelegramJob, error) {
	out := []TelegramJob{}
	if limit != 1 {
		return out, failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		return out, unavailable()
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	j, err := store.New(tx).LeaseTelegram(ctx, store.LeaseTelegramParams{LeaseHash: digest(token), OperatorIds: s.operators()})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, unavailable()
	}
	payload, err := s.card(ctx, tx, j.RequestID, j.ChatID)
	if err != nil {
		return out, err
	}
	out = append(out, TelegramJob{ChatID: j.ChatID, JobID: j.ID, Kind: j.Kind, LeaseExpiresAt: j.LeaseExpiresAt.Time, LeaseToken: token, Payload: payload})
	if tx.Commit(ctx) != nil {
		return nil, unavailable()
	}
	return out, nil
}
func (s *Service) CompleteTelegramJob(ctx context.Context, id uuid.UUID, leaseToken string, result json.RawMessage) error {
	if id == uuid.Nil || len(leaseToken) != 43 {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	var discriminator struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(result, &discriminator) != nil {
		return failure(400, "INVALID_INPUT")
	}
	state := "failed"
	var message pgtype.Int8
	var code pgtype.Text
	var chat int64
	switch discriminator.Kind {
	case "sent":
		var v TelegramSent
		if json.Unmarshal(result, &v) != nil || v.Kind != "sent" || v.ChatId <= 0 || v.MessageId <= 0 {
			return failure(400, "INVALID_INPUT")
		}
		state, chat = "sent", v.ChatId
		message = pgtype.Int8{Int64: v.MessageId, Valid: true}
	case "delivery_failed":
		var v TelegramFailed
		if json.Unmarshal(result, &v) != nil || v.Kind != "delivery_failed" {
			return failure(400, "INVALID_INPUT")
		}
		switch v.Code {
		case "edit_failed", "forbidden", "invalid_response", "network", "timeout":
			code = pgtype.Text{String: v.Code, Valid: true}
		default:
			return failure(400, "INVALID_INPUT")
		}
	default:
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	j, err := q.LockTelegram(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	if err != nil {
		return unavailable()
	}
	if !j.LeaseValid || !s.allowed(j.ChatID) || subtle.ConstantTimeCompare(j.LeaseHash, digest(leaseToken)) != 1 || (discriminator.Kind == "sent" && chat != j.ChatID) {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	// Keep the raw union's key order and unknown fields for old result_hash.
	raw, _ := json.Marshal(result)
	hash := digest(string(raw))
	if j.State != "pending" {
		if j.State == state && bytes.Equal(j.ResultHash, hash) {
			return nil
		}
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	if q.FinishTelegram(ctx, store.FinishTelegramParams{ID: id, State: state, MessageID: message, FailureCode: code, ResultHash: hash}) != nil || tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}
