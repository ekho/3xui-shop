package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func notificationError(err error) error {
	var domain *notifications.Error
	if errors.As(err, &domain) {
		return &apiError{Status: domain.Status, Code: domain.Code, Message: domain.Message}
	}
	return subscriptionError(err)
}

func (a *API) claimTelegramJobs(ctx context.Context, in wire.ClaimInput) (wire.ClaimResult, error) {
	jobs, err := a.notifications.ClaimTelegramJobs(ctx, int(in.Limit))
	out := wire.ClaimResult{Jobs: []wire.TelegramJob{}}
	for _, j := range jobs {
		var payload subscriptions.TelegramPayload
		if json.Unmarshal(j.Payload, &payload) != nil {
			return wire.ClaimResult{}, unavailable()
		}
		out.Jobs = append(out.Jobs, wire.TelegramJob{JobId: j.JobID, ChatId: j.ChatID, Kind: wire.TelegramJobKind(j.Kind), Payload: toSubscriptionTelegramPayload(payload), LeaseToken: j.LeaseToken, LeaseExpiresAt: j.LeaseExpiresAt})
	}
	return out, notificationError(err)
}

func (a *API) completeTelegramJob(ctx context.Context, id uuid.UUID, in wire.TelegramResultInput) error {
	// Invalid union data remains nil so the owner keeps id/token-first validation.
	raw, _ := json.Marshal(in.Result)
	return notificationError(a.notifications.CompleteTelegramJob(ctx, id, in.LeaseToken, raw))
}
