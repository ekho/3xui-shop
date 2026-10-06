package payments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/payments/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"strings"
	"time"
	"unicode/utf8"
)

func stamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
func validText(value string, min, max int) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && utf8.RuneCountInString(value) <= max && utf8.RuneCountInString(strings.TrimSpace(value)) >= min
}
func bodyHash(value any) []byte {
	b, _ := json.Marshal(value)
	hash := sha256.Sum256(b)
	return hash[:]
}
func replay[T any](ctx context.Context, q *store.Queries, principal, operation string, key uuid.UUID, hash []byte) (T, bool, error) {
	var out T
	record, err := q.IdempotencyByKey(ctx, store.IdempotencyByKeyParams{Principal: principal, Operation: operation, Key: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, unavailable()
	}
	if !bytes.Equal(record.BodyHash, hash) {
		return out, false, failure(409, "IDEMPOTENCY_CONFLICT")
	}
	if json.Unmarshal(record.Result, &out) != nil {
		return out, false, unavailable()
	}
	return out, true, nil
}
func (s *Service) saveIdempotency(ctx context.Context, q *store.Queries, principal, operation string, key uuid.UUID, hash []byte, out any) error {
	result, err := json.Marshal(out)
	if err != nil {
		return unavailable()
	}
	if q.AddIdempotency(ctx, store.AddIdempotencyParams{Principal: principal, Operation: operation, Key: key, BodyHash: hash, Result: result, CreatedAt: stamp(s.now())}) != nil {
		return unavailable()
	}
	return nil
}
