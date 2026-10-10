package vpn

import (
	"context"
	"errors"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type Settings struct {
	Panel                        Config
	PanelID, AccessResetTimezone string
	SubscriptionBaseURL          string
	PanelDuplicateGuardVerified  bool
}
type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string              { return e.Code }
func failure(status int, code string) error { return &Error{Status: status, Code: code, Message: code} }
func unavailable() error                    { return failure(503, "SERVICE_UNAVAILABLE") }

type Service struct {
	pool         *pgxpool.Pool
	accounts     *accounts.Service
	queue        func() *river.Client[pgx.Tx]
	config       func() Settings
	now          func() time.Time
	outcome      func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, string) error
	purchase     PurchaseHooks
	bonus        BonusHooks
	groupFailure func(context.Context, pgx.Tx, uuid.UUID, string) error
}

type PurchaseHooks struct {
	Check   func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, uuid.UUID) (string, error)
	Outcome func(context.Context, pgx.Tx, uuid.UUID, string, string) error
}

func New(pool *pgxpool.Pool, authority *accounts.Service, queue func() *river.Client[pgx.Tx], config func() Settings, now func() time.Time, outcome func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, string) error, purchase PurchaseHooks) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, accounts: authority, queue: queue, config: config, now: now, outcome: outcome, purchase: purchase}
}

func (s *Service) checkPurchase(ctx context.Context, tx pgx.Tx, order *uuid.UUID, account, operation uuid.UUID) string {
	if order == nil || s.purchase.Check == nil || s.purchase.Outcome == nil {
		return "purchase_funding_invalid"
	}
	reason, err := s.purchase.Check(ctx, tx, *order, account, operation)
	if err != nil && reason == "" {
		return "purchase_funding_invalid"
	}
	return reason
}

func (s *Service) purchaseOutcome(ctx context.Context, tx pgx.Tx, operation uuid.UUID, status, reason string) error {
	if s.purchase.Outcome == nil {
		return unavailable()
	}
	return s.purchase.Outcome(ctx, tx, operation, status, reason)
}
func (s *Service) PanelClient() *PanelClient { return NewPanelClient(s.config().Panel) }
func (s *Service) recordOutcome(ctx context.Context, tx pgx.Tx, request, operation uuid.UUID, status string) error {
	if s.outcome == nil {
		return unavailable()
	}
	return s.outcome(ctx, tx, request, operation, status)
}
func accountError(err error) error {
	if errors.Is(err, accounts.ErrNotFound) {
		return pgx.ErrNoRows
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
func (s *Service) accountVPNBan(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.accounts.VPNBanned(ctx, id)
}
func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
