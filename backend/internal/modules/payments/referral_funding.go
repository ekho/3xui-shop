package payments

import (
	"context"
	"errors"
	"time"

	"example.com/cabinet/backend/internal/modules/payments/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type PaidPurchase struct {
	OrderID, AccountID uuid.UUID
	FundingID          string
	PaidAt             time.Time
}

// ConfirmedPurchaseTx keeps funding verification with the money owner.
func (s *Service) ConfirmedPurchaseTx(ctx context.Context, tx pgx.Tx, order uuid.UUID) (PaidPurchase, bool, error) {
	var q store.DBTX = s.pool
	if tx != nil {
		q = tx
		// Refunds and fulfillment use this same order lock.
		var locked uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM purchase_orders WHERE id=$1 FOR UPDATE`, order).Scan(&locked); errors.Is(err, pgx.ErrNoRows) {
			return PaidPurchase{}, false, nil
		} else if err != nil {
			return PaidPurchase{}, false, unavailable()
		}
	}
	var paid PaidPurchase
	err := q.QueryRow(ctx, `SELECT p.id,p.account_id,r.operation_id,p.paid_at
		FROM purchase_orders p JOIN purchase_receipts r ON r.operation_id=p.funding_operation_id AND r.order_id=p.id
		WHERE p.id=$1 AND (`+purchaseFundingCheck+`)`, order).Scan(&paid.OrderID, &paid.AccountID, &paid.FundingID, &paid.PaidAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PaidPurchase{}, false, nil
	}
	if err != nil {
		return PaidPurchase{}, false, unavailable()
	}
	return paid, true, nil
}
