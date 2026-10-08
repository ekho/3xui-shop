package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
)

// StarsGateway reuses this runtime's transport and verified bot identity.
func (r *Runtime) StarsGateway() payments.StarsGateway {
	id, _ := strconv.ParseInt(strings.SplitN(r.token, ":", 2)[0], 10, 64)
	ready := func() bool { r.mu.RLock(); defer r.mu.RUnlock(); return r.started }
	return payments.StarsGateway{BotID: id, Ready: ready, Invoice: func(ctx context.Context, in payments.StarsInvoice) (string, error) {
		if !ready() {
			return "", &botapi.APIError{Code: "UNAVAILABLE"}
		}
		return r.api.CreateStarsInvoice(ctx, in.Title, in.Description, in.Payload, in.Amount, in.SubscriptionPeriod)
	}, Refund: func(ctx context.Context, payer int64, charge string) error {
		if !ready() {
			return &botapi.APIError{Code: "UNAVAILABLE"}
		}
		return r.api.RefundStars(ctx, payer, charge)
	}}
}
func (r *Runtime) preCheckout(parent context.Context, raw json.RawMessage) error {
	var q struct {
		ID       string      `json:"id"`
		From     botapi.User `json:"from"`
		Currency string      `json:"currency"`
		Amount   int64       `json:"total_amount"`
		Payload  string      `json:"invoice_payload"`
	}
	if json.Unmarshal(raw, &q) != nil || q.ID == "" || len(q.ID) > 128 || q.From.ID <= 0 || q.From.ID > 1<<52-1 || q.From.IsBot {
		return &ActionError{Code: "UNSUPPORTED_PAYMENT"}
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	ok := false
	if r.clients != nil && !q.From.IsBot && q.From.ID > 0 && q.From.ID <= 1<<52-1 {
		var err error
		ok, err = r.clients.payments.CheckStarsPreCheckout(ctx, payments.StarsPreCheckoutInput{BotID: r.clients.botID, PayerID: q.From.ID, Amount: q.Amount, Currency: q.Currency, Payload: q.Payload, QueryID: q.ID})
		if err != nil {
			return err
		}
	}
	message := ""
	if !ok {
		message = clientText(clientLang(q.From), "Счёт недоступен. Обновите заказ в кабинете или обратитесь в поддержку.", "This invoice is unavailable. Refresh your order in the cabinet or contact support.")
	}
	err := r.api.AnswerStarsPreCheckout(ctx, q.ID, ok, message)
	// Telegram closes expired/already answered queries with 400. Retrying them
	// cannot change money and would pin all subsequent paid/refund updates.
	if safeCode(err) == "BAD_REQUEST" {
		return nil
	}
	return err
}

func (r *Runtime) starsPayment(ctx context.Context, m *botapi.Message) error {
	if r.clients == nil || m == nil || m.ID <= 0 || m.Date <= 0 || m.Chat.Type != "private" || m.Chat.ID <= 0 || m.Chat.ID > 1<<52-1 || present(m.SuccessfulPayment) == present(m.RefundedPayment) {
		return &ActionError{Code: "UNSUPPORTED_PAYMENT"}
	}
	raw := m.SuccessfulPayment
	refunded := present(m.RefundedPayment)
	if refunded {
		raw = m.RefundedPayment
	} else if !clientActor(m.From, m) {
		return &ActionError{Code: "UNSUPPORTED_PAYMENT"}
	}
	var in struct {
		Currency       string `json:"currency"`
		Amount         int64  `json:"total_amount"`
		Payload        string `json:"invoice_payload"`
		Charge         string `json:"telegram_payment_charge_id"`
		ProviderCharge string `json:"provider_payment_charge_id"`
		Recurring      bool   `json:"is_recurring"`
		FirstRecurring bool   `json:"is_first_recurring"`
		Expires        int64  `json:"subscription_expiration_date"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return &ActionError{Code: "UNSUPPORTED_PAYMENT"}
	}
	payment := payments.StarsPaymentInput{StarsPreCheckoutInput: payments.StarsPreCheckoutInput{BotID: r.clients.botID, PayerID: m.Chat.ID, Currency: in.Currency, Amount: in.Amount, Payload: in.Payload}, ChargeID: in.Charge, ProviderChargeID: in.ProviderCharge, At: time.Unix(m.Date, 0).UTC(), Recurring: in.Recurring, FirstRecurring: in.FirstRecurring, SubscriptionExpiresAt: in.Expires}
	var err error
	if refunded {
		err = r.clients.payments.RecordStarsRefund(ctx, payment)
	} else {
		err = r.clients.payments.RecordStarsPayment(ctx, payment)
	}
	var domain *payments.Error
	if errors.As(err, &domain) && domain.Code == "STARS_UNSUPPORTED_PAYMENT" {
		return &ActionError{Code: "UNSUPPORTED_PAYMENT"}
	}
	return err
}
