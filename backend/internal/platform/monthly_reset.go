package platform

import (
	"context"
	"time"

	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

func (s *Service) ApplyMonthlyReset(ctx context.Context, account uuid.UUID, period string) error {
	return subscriptionError(s.vpn.ApplyMonthlyReset(ctx, account, period))
}
func (s *Service) EnqueueMonthlyResets(ctx context.Context, at time.Time) (int, error) {
	n, e := s.vpn.EnqueueMonthlyResets(ctx, at)
	return n, subscriptionError(e)
}
func (s *Service) RunMonthlyResetScheduler(ctx context.Context) error {
	return subscriptionError(s.vpn.RunMonthlyResetScheduler(ctx))
}

type MonthlyResetArgs = vpn.MonthlyResetArgs
type MonthlyResetWorker struct {
	river.WorkerDefaults[MonthlyResetArgs]
	Service *Service
}

func (w *MonthlyResetWorker) Work(ctx context.Context, job *river.Job[MonthlyResetArgs]) error {
	return (&vpn.MonthlyResetWorker{Service: w.Service.vpn}).Work(ctx, job)
}
func (w *MonthlyResetWorker) Timeout(j *river.Job[MonthlyResetArgs]) time.Duration {
	return (&vpn.MonthlyResetWorker{}).Timeout(j)
}
