package platform

import (
	"context"
	"time"

	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

func (s *Service) ApplyAccess(parent context.Context, id uuid.UUID) error {
	return subscriptionError(s.vpn.ApplyAccess(parent, id))
}

type AccessArgs = vpn.AccessArgs
type AccessWorker struct {
	river.WorkerDefaults[AccessArgs]
	Service *Service
}

func (w *AccessWorker) Work(ctx context.Context, j *river.Job[AccessArgs]) error {
	return (&vpn.AccessWorker{Service: w.Service.vpn}).Work(ctx, j)
}
func (w *AccessWorker) Timeout(j *river.Job[AccessArgs]) time.Duration {
	return (&vpn.AccessWorker{}).Timeout(j)
}
func (w *AccessWorker) NextRetry(j *river.Job[AccessArgs]) time.Time {
	return (&vpn.AccessWorker{}).NextRetry(j)
}
