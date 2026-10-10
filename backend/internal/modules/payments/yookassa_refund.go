package payments

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Refund notifications identify a refund, not a payment. Both objects need API proof.
func (s *Service) ReceiveYooKassaRefund(ctx context.Context, rawRefund, rawPayment string) error {
	refundID, refundErr := uuid.Parse(rawRefund)
	paymentID, paymentErr := uuid.Parse(rawPayment)
	if refundErr != nil || paymentErr != nil || refundID == uuid.Nil || paymentID == uuid.Nil || len(rawRefund) != 36 || len(rawPayment) != 36 {
		return failure(400, "INVALID_INPUT")
	}
	config := s.config()
	if config.YooKassaShopID == "" || config.YooKassaToken == "" {
		return unavailable()
	}
	c := kassaRow{shop: config.YooKassaShopID, test: config.YooKassaTestMode, id: &paymentID}
	var refund struct {
		ID        string      `json:"id"`
		PaymentID string      `json:"payment_id"`
		Status    string      `json:"status"`
		Created   string      `json:"created_at"`
		Amount    kassaAmount `json:"amount"`
	}
	if _, err := s.kassaAPIRequest(ctx, c, "GET", "/refunds/"+refundID.String(), nil, &refund); err != nil {
		return err
	}
	amount, amountErr := minorUnits(refund.Amount.Value)
	created, timeErr := time.Parse(time.RFC3339Nano, refund.Created)
	if refund.ID != refundID.String() || refund.PaymentID != paymentID.String() || refund.Status != "succeeded" || amountErr != nil || amount <= 0 || refund.Amount.Currency != "RUB" || timeErr != nil || created.After(s.now().Add(5*time.Minute)) {
		return failure(409, "PAYMENT_IDENTITY_CONFLICT")
	}
	payment, _, err := s.kassaRequest(ctx, c, "GET", "/"+paymentID.String(), nil)
	if err != nil {
		return err
	}
	gross, grossErr := minorUnits(payment.Amount.Value)
	captured, capturedErr := time.Parse(time.RFC3339Nano, payment.Captured)
	paidCreated, paidCreatedErr := time.Parse(time.RFC3339Nano, payment.Created)
	if grossErr != nil || gross < amount || capturedErr != nil || paidCreatedErr != nil || captured.Before(paidCreated) || created.Before(captured) || payment.ID != paymentID.String() || payment.Recipient.AccountID != c.shop || payment.Test == nil || *payment.Test != c.test || payment.Paid == nil || !*payment.Paid || payment.Status != "succeeded" || payment.Amount.Currency != "RUB" {
		return failure(409, "PAYMENT_IDENTITY_CONFLICT")
	}
	proof := map[string]any{"id": refund.ID, "payment_id": refund.PaymentID, "status": refund.Status, "amount": refund.Amount, "created_at": refund.Created, "shop_id": c.shop, "test": c.test}
	var order uuid.UUID
	err = s.pool.QueryRow(ctx, "SELECT order_id FROM yookassa_checkouts WHERE payment_id=$1", paymentID).Scan(&order)
	if err == nil {
		observation, _ := json.Marshal(proof)
		return s.kassaReview(ctx, order, "provider_refund_received", observation)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return unavailable()
	}
	paidProof := map[string]any{"id": payment.ID, "status": payment.Status, "paid": *payment.Paid, "shop_id": payment.Recipient.AccountID, "test": *payment.Test, "amount": payment.Amount, "created_at": payment.Created, "captured_at": payment.Captured}
	if err = s.retainLegacyReceipt(ctx, legacyReceipt{provider: "yookassa", kind: "paid", sourceID: payment.ID, reference: payment.ID, amount: &gross, currency: "RUB", at: captured, proof: paidProof}); err != nil {
		return err
	}
	return s.retainLegacyReceipt(ctx, legacyReceipt{provider: "yookassa", kind: "refunded", sourceID: refund.ID, reference: payment.ID, amount: &amount, currency: "RUB", at: created, proof: proof})
}
