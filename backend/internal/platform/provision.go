package platform

import (
	"context"
	"time"

	"example.com/cabinet/backend/internal/modules/vpn"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

func (s *Service) ReconcileTrialOperation(ctx context.Context, id, key uuid.UUID, in wire.ReconcileInput) (wire.ReconcileResult, error) {
	v, err := s.subscriptions.ReconcileTrialOperation(ctx, id, key, fromSubscriptionReconcileInput(in))
	return toSubscriptionReconcileResult(v), subscriptionError(err)
}
func (s *Service) Provision(parent context.Context, id uuid.UUID) error {
	return subscriptionError(s.vpn.Provision(parent, id))
}

type ProvisionArgs = vpn.ProvisionArgs

const ProvisionRescueAfter = vpn.ProvisionRescueAfter

type ProvisionWorker struct {
	river.WorkerDefaults[ProvisionArgs]
	Service *Service
}

func (w *ProvisionWorker) Work(ctx context.Context, j *river.Job[ProvisionArgs]) error {
	return (&vpn.ProvisionWorker{Service: w.Service.vpn}).Work(ctx, j)
}
func (w *ProvisionWorker) Timeout(j *river.Job[ProvisionArgs]) time.Duration {
	return (&vpn.ProvisionWorker{}).Timeout(j)
}
func (w *ProvisionWorker) NextRetry(j *river.Job[ProvisionArgs]) time.Time {
	return (&vpn.ProvisionWorker{}).NextRetry(j)
}
