package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type State struct {
	Enabled, Degraded bool
	Code              string
}
type Runtime struct {
	enabled                            bool
	token                              string
	api                                *botapi.Client
	dispatcher                         *dispatcher
	outbox                             Outbox
	clients                            *Client
	mu                                 sync.RWMutex
	pollCode, deliveryCode, clientCode string
}

func New(cfg Config, client *http.Client, actions TrialActions, outbox Outbox, clients *Client) (*Runtime, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	r := &Runtime{enabled: cfg.Enabled, outbox: outbox, clients: clients, token: cfg.Token}
	if cfg.Enabled {
		if actions == nil || outbox == nil {
			return nil, errors.New("missing Telegram domain contracts")
		}
		r.api = botapi.New(cfg.Token, client)
		if clients != nil {
			clients.api = r.api
		}
		r.dispatcher = newDispatcher(r.api, actions, cfg.Operators)
	}
	return r, nil
}
func (r *Runtime) State() State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.enabled {
		return State{Code: "DISABLED"}
	}
	code := r.pollCode
	if code == "" {
		code = r.deliveryCode
	}
	if code == "" {
		code = r.clientCode
	}
	return State{Enabled: true, Degraded: code != "", Code: code}
}
func safeCode(err error) string {
	var api *botapi.APIError
	if errors.As(err, &api) {
		switch api.Code {
		case "UNAUTHORIZED", "CONFLICT", "RATE_LIMITED", "FORBIDDEN", "BAD_REQUEST", "NOT_MODIFIED", "INVALID_RESPONSE", "INVALID_INPUT", "UNAVAILABLE":
			return api.Code
		}
	}
	var action *ActionError
	if errors.As(err, &action) {
		switch action.Code {
		case "REQUEST_STATE_CONFLICT", "UNSUPPORTED_PAYMENT", "WEBHOOK_CONFIGURED", "MINI_APP_NOT_CONFIGURED", "INVALID_INPUT":
			return action.Code
		}
	}
	return "SERVICE_UNAVAILABLE"
}
func (r *Runtime) setCode(poll bool, code string) {
	field := &r.deliveryCode
	if poll {
		field = &r.pollCode
	}
	r.setLoopCode(field, code)
}
func (r *Runtime) setLoopCode(field *string, code string) {
	r.mu.Lock()
	changed := *field != code
	*field = code
	r.mu.Unlock()
	if changed && code != "" {
		slog.Warn("Telegram channel degraded", "code", code)
	}
}
func fatal(err error) bool {
	switch safeCode(err) {
	case "UNAUTHORIZED", "CONFLICT", "WEBHOOK_CONFIGURED", "UNSUPPORTED_PAYMENT", "MINI_APP_NOT_CONFIGURED":
		return true
	}
	return false
}
func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func retryDelay(err error, backoff time.Duration) time.Duration {
	var api *botapi.APIError
	if errors.As(err, &api) && api.RetryAfter > 0 {
		return api.RetryAfter
	}
	return backoff
}
func nextDelay(d time.Duration) time.Duration {
	d *= 2
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

func (r *Runtime) Run(parent context.Context) error {
	if !r.enabled {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	delay := time.Second
	for {
		info, err := r.api.GetWebhookInfo(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil && info.URL != "" {
			err = &ActionError{Code: "WEBHOOK_CONFIGURED"}
		}
		if err == nil && r.clients != nil {
			err = r.clients.start(ctx, r.api, r.token)
		}
		if err == nil {
			r.setCode(true, "")
			break
		}
		r.setCode(true, safeCode(err))
		if fatal(err) {
			return err
		}
		if !pause(ctx, retryDelay(err, delay)) {
			return nil
		}
		delay = nextDelay(delay)
	}
	workers := 2
	if r.clients != nil {
		workers++
	}
	results := make(chan error, workers)
	go func() { results <- r.poll(ctx) }()
	go func() { results <- r.deliveries(ctx) }()
	if r.clients != nil {
		go func() { results <- r.clientDeliveries(ctx) }()
	}
	err := <-results
	cancel()
	for i := 1; i < workers; i++ {
		<-results
	}
	if parent.Err() != nil {
		return nil
	}
	return err
}
func (r *Runtime) poll(ctx context.Context) error {
	offset := int64(0)
	delay := time.Second
	for ctx.Err() == nil {
		updates, err := r.api.GetUpdates(ctx, offset)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			for _, u := range updates {
				err = r.handle(ctx, u)
				if err != nil {
					break
				}
				offset = u.ID + 1 // The next request acknowledges only completed/refused handling.
			}
		}
		if err == nil {
			r.setCode(true, "")
			delay = time.Second
			continue
		}
		r.setCode(true, safeCode(err))
		if fatal(err) {
			return err
		}
		if !pause(ctx, retryDelay(err, delay)) {
			return nil
		}
		delay = nextDelay(delay)
	}
	return nil
}

func present(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
func (r *Runtime) clientDeliveries(ctx context.Context) error {
	delay := time.Second
	for ctx.Err() == nil {
		claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		job, err := r.clients.notices.ClaimClient(claimCtx)
		cancel()
		if err == nil && job == nil {
			if !pause(ctx, 5*time.Second) {
				return nil
			}
			continue
		}
		if err == nil {
			err = r.clients.deliver(ctx, *job)
		}
		if err == nil {
			r.setLoopCode(&r.clientCode, "")
			delay = time.Second
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		r.setLoopCode(&r.clientCode, safeCode(err))
		if fatal(err) {
			return err
		}
		if !pause(ctx, retryDelay(err, delay)) {
			return nil
		}
		delay = nextDelay(delay)
	}
	return nil
}
func (r *Runtime) deliveries(ctx context.Context) error {
	delay := time.Second
	for ctx.Err() == nil {
		claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		d, err := r.outbox.Claim(claimCtx)
		cancel()
		if err == nil && d == nil {
			if !pause(ctx, 5*time.Second) {
				return nil
			}
			continue
		}
		if err == nil {
			err = r.deliver(ctx, *d)
		}
		if err == nil {
			r.setCode(false, "")
			delay = time.Second
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		r.setCode(false, safeCode(err))
		if fatal(err) {
			return err
		}
		if !pause(ctx, retryDelay(err, delay)) {
			return nil
		}
		delay = nextDelay(delay)
	}
	return nil
}
func (r *Runtime) deliver(ctx context.Context, d Delivery) error {
	if !r.dispatcher.operators[d.ChatID] || !time.Now().Before(d.LeaseExpiresAt) {
		return &ActionError{Code: "REQUEST_STATE_CONFLICT"}
	}
	text, k, err := renderCard(d.Card)
	out := DeliveryOutcome{Kind: "delivery_failed", Code: "invalid_response"}
	if err == nil {
		var msg botapi.Message
		if target := d.Card.TargetMessageID; target != nil {
			msg, err = r.api.EditMessage(ctx, d.ChatID, *target, text, k)
			if safeCode(err) == "NOT_MODIFIED" {
				msg = botapi.Message{ID: *target, Chat: botapi.Chat{ID: d.ChatID}}
				err = nil
			} else if safeCode(err) == "BAD_REQUEST" {
				msg, err = r.api.SendMessage(ctx, d.ChatID, text, k)
			}
		} else {
			msg, err = r.api.SendMessage(ctx, d.ChatID, text, k)
		}
		if err == nil {
			out = DeliveryOutcome{Kind: "sent", ChatID: d.ChatID, MessageID: msg.ID}
		} else {
			switch safeCode(err) {
			case "FORBIDDEN":
				out.Code = "forbidden"
			case "BAD_REQUEST":
				if d.Card.TargetMessageID != nil {
					out.Code = "edit_failed"
				}
			default:
				return err // Network/invalid response: the send may have happened.
			}
		}
	}
	completeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return r.outbox.Complete(completeCtx, d, out)
}

func (r *Runtime) handle(ctx context.Context, u botapi.Update) error {
	if present(u.PreCheckout) || (u.Message != nil && (present(u.Message.SuccessfulPayment) || present(u.Message.RefundedPayment))) {
		return &ActionError{Code: "UNSUPPORTED_PAYMENT"}
	}
	if r.clients != nil {
		handled, err := r.clients.handle(ctx, u)
		if handled {
			if u.Message != nil && u.Message.From != nil {
				delete(r.dispatcher.pending, u.Message.From.ID)
			}
			return err
		}
	}
	return r.dispatcher.handle(ctx, u)
}
