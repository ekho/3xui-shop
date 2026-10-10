package httpapi

import (
	"errors"
	"example.com/cabinet/backend/internal/modules/notifications"
)

func notificationError(err error) error {
	var domain *notifications.Error
	if errors.As(err, &domain) {
		return &apiError{Status: domain.Status, Code: domain.Code, Message: domain.Message}
	}
	return subscriptionError(err)
}
