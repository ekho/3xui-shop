package platform

import (
	"context"
	"net/url"
)

func (s *Service) ReceiveYooMoney(ctx context.Context, fields url.Values) error {
	return paymentError(s.payments.ReceiveYooMoney(ctx, fields))
}
