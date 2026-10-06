package payments

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"time"
)

type HeleketArgs struct {
	OrderID uuid.UUID `json:"order_id"`
}

func (HeleketArgs) Kind() string { return "heleket_payment" }

type HeleketWorker struct {
	river.WorkerDefaults[HeleketArgs]
	Service *Service
}

func (w *HeleketWorker) Work(ctx context.Context, job *river.Job[HeleketArgs]) error {
	return w.Service.SyncHeleket(ctx, job.Args.OrderID)
}
func (w *HeleketWorker) Timeout(*river.Job[HeleketArgs]) time.Duration { return 30 * time.Second }
func (w *HeleketWorker) NextRetry(*river.Job[HeleketArgs]) time.Time {
	return time.Now().Add(10 * time.Second)
}

func (s *Service) queueHeleketTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, quote PurchaseQuote) error {
	return s.queueCryptoTx(ctx, tx, id, quote, heleketProvider)
}
func (s *Service) HeleketSourceAllowed(raw string) bool { return heleketProvider.sourceAllowed(raw) }
func (s *Service) SyncHeleket(ctx context.Context, order uuid.UUID) error {
	return s.syncCrypto(ctx, order, heleketProvider)
}
func (s *Service) ReceiveHeleket(ctx context.Context, raw []byte) error {
	return s.receiveCrypto(ctx, raw, heleketProvider)
}
