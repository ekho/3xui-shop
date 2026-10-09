package payments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type Config struct {
	Admission                                func(context.Context, pgx.Tx) error
	CabinetOrigin, PanelID, YooMoneyWalletID string
	YooMoneyEnabled                          bool
	StarsEnabled                             bool
	YooMoneyNotificationSecret               []byte
	ManualEnabled                            bool
	ManualCardDetails                        string
	YooKassaEnabled, YooKassaTestMode        bool
	YooKassaShopID, YooKassaToken, ShopEmail string
	CryptomusEnabled                         bool
	CryptomusMerchantID, CryptomusAPIKey     string
	HeleketEnabled                           bool
	HeleketMerchantID, HeleketAPIKey         string
}

func (s *Service) allowNew(ctx context.Context, tx pgx.Tx) error {
	if check := s.config().Admission; check != nil {
		if err := check(ctx, tx); err != nil {
			if err.Error() == "MAINTENANCE" {
				return failure(503, "MAINTENANCE")
			}
			return unavailable()
		}
	}
	return nil
}

type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string              { return e.Code }
func failure(status int, code string) error { return &Error{Status: status, Code: code} }
func unavailable() error                    { return failure(503, "SERVICE_UNAVAILABLE") }

type Service struct {
	pool          *pgxpool.Pool
	notifications *notifications.Service
	authority     *accounts.Service
	catalogue     *catalogue.Service
	subscriptions *subscriptions.Service
	vpn           *vpn.Service
	queue         func() *river.Client[pgx.Tx]
	config        func() Config
	now           func() time.Time
	http          *http.Client
	stars         *StarsGateway
}

func New(pool *pgxpool.Pool, authority *accounts.Service, catalogueOwner *catalogue.Service, subscriptionOwner *subscriptions.Service, accessOwner *vpn.Service, queue func() *river.Client[pgx.Tx], config func() Config, now func() time.Time, notices *notifications.Service) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, notifications: notices, authority: authority, catalogue: catalogueOwner, subscriptions: subscriptionOwner, vpn: accessOwner, queue: queue, config: config, now: now, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
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

func (s *Service) starsAssignedSourceTx(ctx context.Context, tx pgx.Tx, a accounts.Snapshot) (*vpn.PlanAssignment, error) {
	if a.AssignedPanelID == nil || *a.AssignedPanelID == "" {
		return nil, nil
	}
	if _, err := s.vpn.ServerTx(ctx, tx, *a.AssignedPanelID); errors.Is(err, vpn.ErrPanel) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	source, err := s.vpn.CurrentPlanSourceTx(ctx, tx, a.ID)
	if err != nil || source == nil {
		return source, err
	}
	state, err := s.vpn.AccessStateForAccountTx(ctx, tx, source.OperationID, a.ID)
	if err != nil {
		return nil, err
	}
	var target vpn.AccessTarget
	if json.Unmarshal(state.Target, &target) != nil || target.OperationID != source.OperationID || target.PanelID != *a.AssignedPanelID || target.PanelKey != a.PanelKey || target.VPNID != a.VpnID || target.SubID != a.SubID {
		return nil, nil
	}
	return source, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
