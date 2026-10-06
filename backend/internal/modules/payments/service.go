package payments

import (
	"context"
	"errors"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type Config struct {
	CabinetOrigin, PanelID, YooMoneyWalletID string
	YooMoneyEnabled                          bool
	YooMoneyNotificationSecret               []byte
	ManualEnabled                            bool
	ManualCardDetails                        string
}

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
	catalogue *catalogue.Service
	vpn       *vpn.Service
	queue     func() *river.Client[pgx.Tx]
	config    func() Config
	now       func() time.Time
}

func New(pool *pgxpool.Pool, authority *accounts.Service, catalogueOwner *catalogue.Service, accessOwner *vpn.Service, queue func() *river.Client[pgx.Tx], config func() Config, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, authority: authority, catalogue: catalogueOwner, vpn: accessOwner, queue: queue, config: config, now: now}
}

func accountResult(a accounts.Snapshot, err error) (accounts.Snapshot, error) {
	if errors.Is(err, accounts.ErrNotFound) {
		return a, pgx.ErrNoRows
	}
	return a, err
}
func (s *Service) accountByID(ctx context.Context, id uuid.UUID) (accounts.Snapshot, error) {
	return accountResult(s.authority.Lookup(ctx, id))
}
func (s *Service) accountByIDTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (accounts.Snapshot, error) {
	return accountResult(s.authority.LookupTx(ctx, tx, id))
}
func (s *Service) lockAccount(ctx context.Context, tx pgx.Tx, id uuid.UUID) (accounts.Snapshot, error) {
	return accountResult(s.authority.Lock(ctx, tx, id))
}
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
