package platform

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
