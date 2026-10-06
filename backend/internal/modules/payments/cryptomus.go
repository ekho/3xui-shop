package payments

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"time"
)

type CryptomusArgs struct {
	OrderID uuid.UUID `json:"order_id"`
}

func (CryptomusArgs) Kind() string { return "cryptomus_payment" }

type CryptomusWorker struct {
	river.WorkerDefaults[CryptomusArgs]
	Service *Service
}

func (w *CryptomusWorker) Work(ctx context.Context, job *river.Job[CryptomusArgs]) error {
	return w.Service.SyncCryptomus(ctx, job.Args.OrderID)
}
func (w *CryptomusWorker) Timeout(*river.Job[CryptomusArgs]) time.Duration { return 30 * time.Second }
func (w *CryptomusWorker) NextRetry(*river.Job[CryptomusArgs]) time.Time {
	return time.Now().Add(10 * time.Second)
}

func (s *Service) queueCryptomusTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, quote PurchaseQuote) error {
	return s.queueCryptoTx(ctx, tx, id, quote, cryptomusProvider)
}
func (s *Service) CryptomusSourceAllowed(raw string) bool {
	return cryptomusProvider.sourceAllowed(raw)
}
func (s *Service) SyncCryptomus(ctx context.Context, order uuid.UUID) error {
	return s.syncCrypto(ctx, order, cryptomusProvider)
}
func (s *Service) ReceiveCryptomus(ctx context.Context, raw []byte) error {
	return s.receiveCrypto(ctx, raw, cryptomusProvider)
}
