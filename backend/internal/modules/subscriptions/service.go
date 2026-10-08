package subscriptions

import (
	"context"
	"errors"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Operators                                     []int64
	PanelID, SubscriptionBaseURL                  string
	TrialEnabled                                  bool
	TrialPeriodDays, TrialTrafficGB, TrialDevices int64
	RequireStarsCancellation                      func(context.Context, pgx.Tx, uuid.UUID, string) error
}
type Error struct {
	Status        int
	Code, Message string
	Details       map[string]any
}

func (e *Error) Error() string              { return e.Code }
func failure(status int, code string) error { return &Error{Status: status, Code: code, Message: code} }
func unavailable() error                    { return failure(503, "SERVICE_UNAVAILABLE") }

type Service struct {
	pool          *pgxpool.Pool
	accounts      *accounts.Service
	catalogue     *catalogue.Service
	vpn           *vpn.Service
	notifications *notifications.Service
	config        func() Config
	now           func() time.Time
}

func New(pool *pgxpool.Pool, authority *accounts.Service, catalogue *catalogue.Service, vpn *vpn.Service, notifications *notifications.Service, config func() Config, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, accounts: authority, catalogue: catalogue, vpn: vpn, notifications: notifications, config: config, now: now}
}
func accountError(err error) error {
	if errors.Is(err, accounts.ErrNotFound) {
		return pgx.ErrNoRows
	}
	var e *accounts.Error
	if errors.As(err, &e) {
		return &Error{Status: e.Status, Code: e.Code, Message: e.Message}
	}
	return err
}
func vpnError(err error) error {
	var e *vpn.Error
	if errors.As(err, &e) {
		return failure(e.Status, e.Code)
	}
	return err
}
func (s *Service) accountByID(ctx context.Context, id uuid.UUID) (accounts.Snapshot, error) {
	a, e := s.accounts.Lookup(ctx, id)
	return a, accountError(e)
}
func (s *Service) lockAccount(ctx context.Context, tx pgx.Tx, id uuid.UUID) (accounts.Snapshot, error) {
	a, e := s.accounts.Lock(ctx, tx, id)
	return a, accountError(e)
}
func (s *Service) lockOperatorPair(ctx context.Context, tx pgx.Tx, actor, target uuid.UUID) (accounts.Snapshot, error) {
	a, e := s.accounts.LockOperatorPair(ctx, tx, actor, target)
	return a, accountError(e)
}
func (s *Service) accountVPNBan(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.accounts.VPNBanned(ctx, id)
}
func (s *Service) PanelClient() *vpn.PanelClient { return s.vpn.PanelClient() }
func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// TrialServer exposes the existing server presentation without credentials or I/O.
func (s *Service) TrialServer() (panelID string, enabled bool) {
	c := s.config()
	return c.PanelID, c.TrialEnabled
}
