package catalogue

import (
	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string              { return e.Code }
func failure(status int, code string) error { return &Error{Status: status, Code: code} }
func unavailable() error                    { return failure(503, "SERVICE_UNAVAILABLE") }

type Service struct {
	pool      *pgxpool.Pool
	authority *accounts.Service
	now       func() time.Time
}

func New(pool *pgxpool.Pool, authority *accounts.Service, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, authority: authority, now: now}
}
