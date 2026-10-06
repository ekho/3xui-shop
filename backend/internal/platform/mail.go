package platform

import (
	"context"
	"example.com/cabinet/backend/internal/modules/notifications"
	"github.com/google/uuid"
)

func (s *Service) SendMail(ctx context.Context, id uuid.UUID) error {
	return notificationError(s.mailDelivery.SendMail(ctx, id))
}
func SendSMTP(ctx context.Context, cfg Config, to, subject, body string) error {
	return notificationError(notifications.SendSMTP(ctx, cfg.MailSettings(), to, subject, body))
}
