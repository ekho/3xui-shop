package platform

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"

	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Service) ClaimTelegramJobs(ctx context.Context, in wire.ClaimInput) (wire.ClaimResult, error) {
	out := wire.ClaimResult{Jobs: []wire.TelegramJob{}}
	if in.Limit != 1 {
		return out, failure(400, "INVALID_INPUT")
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	token := opaque()
	j, e := q.LeaseTelegram(ctx, store.LeaseTelegramParams{LeaseHash: digest(token), OperatorIds: s.cfg.Operators})
	if errors.Is(e, pgx.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return out, unavailable()
	}
	card, e := s.subscriptions.CardTx(ctx, tx, j.RequestID, j.ChatID)
	if e != nil {
		return out, subscriptionError(e)
	}
	payload := toSubscriptionTelegramPayload(card)
	out.Jobs = append(out.Jobs, wire.TelegramJob{JobId: j.ID, ChatId: j.ChatID, Kind: wire.TelegramJobKind(j.Kind), Payload: payload, LeaseToken: token, LeaseExpiresAt: j.LeaseExpiresAt.Time})
	if tx.Commit(ctx) != nil {
		return wire.ClaimResult{}, unavailable()
	}
	return out, nil
}
func (s *Service) CompleteTelegramJob(ctx context.Context, id uuid.UUID, in wire.TelegramResultInput) error {
	if id == uuid.Nil || len(in.LeaseToken) != 43 {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	kind, e := in.Result.Discriminator()
	if e != nil {
		return failure(400, "INVALID_INPUT")
	}
	state := "failed"
	var message pgtype.Int8
	var code pgtype.Text
	var chat int64
	switch kind {
	case "sent":
		v, err := in.Result.AsTelegramSent()
		if err != nil || v.Kind != "sent" || v.ChatId <= 0 || v.MessageId <= 0 {
			return failure(400, "INVALID_INPUT")
		}
		state = "sent"
		chat = v.ChatId
		message = pgtype.Int8{Int64: v.MessageId, Valid: true}
	case "delivery_failed":
		v, err := in.Result.AsTelegramFailed()
		if err != nil || v.Kind != "delivery_failed" || !v.Code.Valid() {
			return failure(400, "INVALID_INPUT")
		}
		code = pgtype.Text{String: string(v.Code), Valid: true}
	default:
		return failure(400, "INVALID_INPUT")
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	j, e := q.LockTelegram(ctx, id)
	if errors.Is(e, pgx.ErrNoRows) {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	if e != nil {
		return unavailable()
	}
	if !j.LeaseValid || !s.operatorAllowed(j.ChatID) || subtle.ConstantTimeCompare(j.LeaseHash, digest(in.LeaseToken)) != 1 || (kind == "sent" && chat != j.ChatID) {
		return failure(409, "REQUEST_STATE_CONFLICT")
	}
	hash := bodyHash(in.Result)
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
