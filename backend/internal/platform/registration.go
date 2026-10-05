package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"example.com/cabinet/backend/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

func opaque() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func digest(v string) []byte               { h := sha256.Sum256([]byte(v)); return h[:] }
func stamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func (s *Service) revokeCredentialProofs(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) error {
	q := store.New(tx)
	if q.RevokeCredentialProofs(ctx, &accountID) != nil || q.ClearRevokedCredentialMail(ctx, &accountID) != nil {
		return unavailable()
	}
	return nil
}
