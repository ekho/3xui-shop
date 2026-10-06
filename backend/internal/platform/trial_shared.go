package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/jackc/pgx/v5/pgtype"

	"example.com/cabinet/backend/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func validText(value string, min, max int) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && utf8.RuneCountInString(value) <= max && utf8.RuneCountInString(strings.TrimSpace(value)) >= min
}
func bodyHash(value any) []byte { b, _ := json.Marshal(value); return digest(string(b)) }
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

func sourceEligible(a store.Account) bool {
	return accounts.SourceEligible(accounts.Snapshot{Kind: a.Kind, VerifiedAt: timePointer(a.VerifiedAt), EmailKey: textPointer(a.EmailKey), PasswordSet: a.PasswordHash.Valid, TelegramID: intPointer(a.TelegramID), DisplayName: textPointer(a.DisplayName)})
}
func timePointer(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}
func textPointer(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
func intPointer(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
