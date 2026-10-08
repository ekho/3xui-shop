package telegram

import (
	"context"
	"encoding/json"
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
	return payments.StarsGateway{BotID: id, Invoice: func(ctx context.Context, in payments.StarsInvoice) (string, error) {
		if !ready() {
			return "", &botapi.APIError{Code: "UNAVAILABLE"}
		}
		return r.api.CreateStarsInvoice(ctx, in.Title, in.Description, in.Payload, in.Amount)
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
		ok, err = r.clients.payments.CheckStarsPreCheckout(ctx, payments.StarsPreCheckoutInput{BotID: r.clients.botID, PayerID: q.From.ID, Amount: q.Amount, Currency: q.Currency, Payload: q.Payload})
		if err != nil {
			return err
		}
	}
	message := ""
	if !ok {
		message = clientText(clientLang(q.From), "Счёт недоступен. Обновите заказ в кабинете или обратитесь в поддержку.", "This invoice is unavailable. Refresh your order in the cabinet or contact support.")
	}
	return r.api.AnswerStarsPreCheckout(ctx, q.ID, ok, message)
}
