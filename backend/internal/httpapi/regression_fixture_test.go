package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"time"
)

type regressionFixture struct {
	*API
	pool         *pgxpool.Pool
	limiter      *redis.Client
	queue        *river.Client[pgx.Tx]
	cfg          *app.Config
	now          func() time.Time
	vpn          *vpn.Service
	mailDelivery *notifications.MailService
}

func newRegressionFixture(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg app.Config) *regressionFixture {
	s := &regressionFixture{pool: pool, limiter: limiter, queue: queue, cfg: &cfg, now: time.Now}
	cfg.Accounts.Now = func() time.Time { return s.now() }
	modules := app.NewModules(pool, limiter, queue, &cfg)
	contract, err := wire.GetSwagger()
	if err != nil {
		panic(err)
	}
	s.API = newAPI(modules, pool, cfg.HTTP, contract)
	s.vpn, s.mailDelivery = modules.VPN, modules.MailDelivery
	return s
}
func regressionVPNSettings(cfg *app.Config) vpn.Settings {
	out := cfg.VPN
	out.PanelID = cfg.Subscriptions.PanelID
	return out
}
func (s *regressionFixture) provision(ctx context.Context, id uuid.UUID) error {
	return subscriptionError(s.vpn.Provision(ctx, id))
}
func (s *regressionFixture) applyAccess(ctx context.Context, id uuid.UUID) error {
	return subscriptionError(s.vpn.ApplyAccess(ctx, id))
}
func (s *regressionFixture) applyMonthlyReset(ctx context.Context, id uuid.UUID, period string) error {
	return subscriptionError(s.vpn.ApplyMonthlyReset(ctx, id, period))
}
func (s *regressionFixture) enqueueMonthlyResets(ctx context.Context, at time.Time) (int, error) {
	n, e := s.vpn.EnqueueMonthlyResets(ctx, at)
	return n, subscriptionError(e)
}
func (s *regressionFixture) fulfillPurchase(ctx context.Context, id uuid.UUID) error {
	return paymentError(s.payments.FulfillPurchase(ctx, id))
}
func (s *regressionFixture) sendMail(ctx context.Context, id uuid.UUID) error {
	return notificationError(s.mailDelivery.SendMail(ctx, id))
}
func (s *regressionFixture) changeOperatorRole(ctx context.Context, id uuid.UUID, grant bool) error {
	return accountError(s.accounts.ChangeOperatorRole(ctx, id, grant))
}
func (s *regressionFixture) seedUnlimitedCatalogue(ctx context.Context) (wire.OperatorCataloguePlan, bool, error) {
	out, created, err := s.catalogueOwner.SeedUnlimitedCatalogue(ctx)
	return catalogueOperatorPlan(out), created, catalogueError(err)
}
func (s *regressionFixture) importLegacyCatalogue(ctx context.Context, p catalogue.LegacyCataloguePackage, dry bool) (catalogue.CatalogueImportResult, error) {
	out, err := s.catalogueOwner.ImportLegacyCatalogue(ctx, p, dry)
	return out, catalogueError(err)
}
func (s *regressionFixture) importLegacyApprovals(ctx context.Context, p accounts.LegacyApprovalPackage, dry bool) (accounts.LegacyApprovalImportResult, error) {
	out, err := s.accounts.ImportLegacyApprovals(ctx, p, dry)
	return out, accountError(err)
}
func (s *regressionFixture) accountByID(ctx context.Context, id uuid.UUID) (accounts.Snapshot, error) {
	out, err := s.accounts.Lookup(ctx, id)
	return out, accountError(err)
}
func digest(value string) []byte { h := sha256.Sum256([]byte(value)); return h[:] }
func opaque() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
