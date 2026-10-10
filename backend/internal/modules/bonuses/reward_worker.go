package bonuses

import (
	"context"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"time"
)

type RewardArgs struct {
	RewardID uuid.UUID `json:"reward_id"`
}

func (RewardArgs) Kind() string { return "referral_reward" }

type RewardWorker struct {
	river.WorkerDefaults[RewardArgs]
	Service *Service
}

func (w *RewardWorker) Work(ctx context.Context, job *river.Job[RewardArgs]) error {
	return w.Service.ProcessReward(ctx, job.Args.RewardID)
}
func (w *RewardWorker) Timeout(*river.Job[RewardArgs]) time.Duration {
	return 2*time.Minute + 5*time.Second
}
func (w *RewardWorker) NextRetry(job *river.Job[RewardArgs]) time.Time {
	return time.Now().Add(time.Duration(min(job.Attempt, 5)) * 10 * time.Second)
}
