// Package botapi is private transport code for the Telegram module.
package botapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type User struct {
	ID            int64  `json:"id"`
	IsBot         bool   `json:"is_bot"`
	Username      string `json:"username"`
	LanguageCode  string `json:"language_code"`
	HasMainWebApp bool   `json:"has_main_web_app"`
}
type Chat struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	IsForum bool   `json:"is_forum,omitempty"`
}
type Message struct {
	ID                int64           `json:"message_id"`
	ThreadID          int64           `json:"message_thread_id,omitempty"`
	From              *User           `json:"from"`
	Chat              Chat            `json:"chat"`
	Text              string          `json:"text"`
	Date              int64           `json:"date"`
	ReplyMarkup       *InlineKeyboard `json:"reply_markup"`
	SuccessfulPayment json.RawMessage `json:"successful_payment"`
	RefundedPayment   json.RawMessage `json:"refunded_payment"`
}
type Callback struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}
type Update struct {
	ID           int64           `json:"update_id"`
	Message      *Message        `json:"message"`
	Callback     *Callback       `json:"callback_query"`
	PreCheckout  json.RawMessage `json:"pre_checkout_query"`
	Subscription json.RawMessage `json:"subscription"`
}
type Button struct {
	Text   string      `json:"text"`
	Data   string      `json:"callback_data,omitempty"`
	URL    string      `json:"url,omitempty"`
	WebApp *WebAppInfo `json:"web_app,omitempty"`
}
type WebAppInfo struct {
	URL string `json:"url"`
}
type InlineKeyboard struct {
	Rows [][]Button `json:"inline_keyboard"`
}
type WebhookInfo struct {
	URL string `json:"url"`
}
type APIError struct {
	Code       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string { return e.Code }

type Client struct {
	token string
	http  *http.Client
}

func New(token string, client *http.Client) *Client {
	var h http.Client
	if client != nil {
		h = *client
	}
	if h.Timeout <= 0 || h.Timeout > 40*time.Second {
		h.Timeout = 40 * time.Second
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{token: token, http: &h}
}
func invalid() error { return &APIError{Code: "INVALID_RESPONSE"} }

func (c *Client) call(ctx context.Context, method string, body, result any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	b, err := json.Marshal(body)
	if err != nil {
		return &APIError{Code: "INVALID_INPUT"}
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+c.token+"/"+method, bytes.NewReader(b))
	if err != nil {
		return &APIError{Code: "INVALID_INPUT"}
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(r)
	if err != nil {
		return &APIError{Code: "UNAVAILABLE"}
	} // URL errors contain the bot token.
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return invalid()
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return &APIError{Code: "UNAVAILABLE"}
	}
	if len(data) > 1<<20 {
		return invalid()
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	parseErr := json.Unmarshal(data, &envelope)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || (parseErr == nil && !envelope.OK) {
		status := envelope.ErrorCode
		if status == 0 {
			status = resp.StatusCode
		}
		e := &APIError{Code: "UNAVAILABLE"}
		switch status {
		case 401:
			e.Code = "UNAUTHORIZED"
		case 409:
			e.Code = "CONFLICT"
		case 403:
			e.Code = "FORBIDDEN"
		case 429:
			e.Code = "RATE_LIMITED"
			seconds := envelope.Parameters.RetryAfter
			if seconds < 1 {
				seconds = 1
			}
			if seconds > 86400 {
				seconds = 86400
			}
			e.RetryAfter = time.Duration(seconds) * time.Second
		case 400:
			e.Code = "BAD_REQUEST"
			if strings.EqualFold(strings.TrimSpace(envelope.Description), "Bad Request: message thread not found") {
				e.Code = "THREAD_NOT_FOUND"
			}
			if strings.Contains(strings.ToLower(envelope.Description), "message is not modified") {
				e.Code = "NOT_MODIFIED"
			}
		}
		return e
	}
	if parseErr != nil || len(envelope.Result) == 0 || bytes.Equal(envelope.Result, []byte("null")) || json.Unmarshal(envelope.Result, result) != nil {
		return invalid()
	}
	return nil
}
func (c *Client) GetUpdates(ctx context.Context, offset int64) ([]Update, error) {
	var out []Update
	err := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "limit": 1, "timeout": 30, "allowed_updates": []string{"message", "callback_query", "pre_checkout_query", "subscription"}}, &out, 40*time.Second)
	if err == nil {
		if len(out) > 1 {
			return nil, invalid()
		}
		for _, u := range out {
			if u.ID < 0 || u.ID == math.MaxInt64 {
				return nil, invalid()
			}
		}
	}
	return out, err
}
func (c *Client) GetWebhookInfo(ctx context.Context) (WebhookInfo, error) {
	var out WebhookInfo
	err := c.call(ctx, "getWebhookInfo", struct{}{}, &out, 10*time.Second)
	return out, err
}
func (c *Client) message(ctx context.Context, method string, chatID, messageID int64, text string, keyboard *InlineKeyboard) (Message, error) {
	var out Message
	if chatID <= 0 || !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') {
		return out, &APIError{Code: "INVALID_INPUT"}
	}
	if keyboard != nil {
		for _, row := range keyboard.Rows {
			for _, b := range row {
				choices := 0
				if b.Data != "" {
					choices++
					if len(b.Data) > 64 || !utf8.ValidString(b.Data) || strings.ContainsRune(b.Data, '\x00') {
						return out, &APIError{Code: "INVALID_INPUT"}
					}
				}
				if b.URL != "" {
					choices++
					if !validURL(b.URL) {
						return out, &APIError{Code: "INVALID_INPUT"}
					}
				}
				if b.WebApp != nil {
					choices++
					if !validURL(b.WebApp.URL) {
						return out, &APIError{Code: "INVALID_INPUT"}
					}
				}
				if choices != 1 || !utf8.ValidString(b.Text) || strings.TrimSpace(b.Text) == "" || strings.ContainsRune(b.Text, '\x00') {
					return out, &APIError{Code: "INVALID_INPUT"}
				}
			}
		}
	}
	in := map[string]any{"chat_id": chatID, "reply_markup": keyboard}
	if method != "editMessageReplyMarkup" {
		in["text"] = text
		in["parse_mode"] = "HTML"
	}
	if messageID > 0 {
		in["message_id"] = messageID
	}
	err := c.call(ctx, method, in, &out, 10*time.Second)
	if err == nil && (out.ID <= 0 || out.Chat.ID != chatID || (messageID > 0 && out.ID != messageID)) {
		return Message{}, invalid()
	}
	return out, err
}
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, k *InlineKeyboard) (Message, error) {
	return c.message(ctx, "sendMessage", chatID, 0, text, k)
}

// General is a separate negative-group transport; private chat guards stay intact.
func (c *Client) SendGeneral(ctx context.Context, groupID int64, text string) error {
	if groupID >= 0 || groupID < -(1<<52-1) || !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 4096 {
		return &APIError{Code: "INVALID_INPUT"}
	}
	var out struct {
		ID       int64  `json:"message_id"`
		Chat     Chat   `json:"chat"`
		ThreadID *int64 `json:"message_thread_id"`
	}
	err := c.call(ctx, "sendMessage", map[string]any{"chat_id": groupID, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}}, &out, 10*time.Second)
	if err == nil && (out.ID <= 0 || out.Chat.ID != groupID || out.Chat.Type != "supergroup" || out.ThreadID != nil && *out.ThreadID != 1) {
		return invalid()
	}
	return err
}
func (c *Client) EditMessage(ctx context.Context, chatID, messageID int64, text string, k *InlineKeyboard) (Message, error) {
	if messageID <= 0 {
		return Message{}, &APIError{Code: "INVALID_INPUT"}
	}
	return c.message(ctx, "editMessageText", chatID, messageID, text, k)
}
func (c *Client) AnswerCallback(ctx context.Context, id, text string, alert bool) error {
	if id == "" || len(id) > 128 || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 200 {
		return &APIError{Code: "INVALID_INPUT"}
	}
	var ok bool
	err := c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text, "show_alert": alert}, &ok, 10*time.Second)
	if err == nil && !ok {
		return invalid()
	}
	return err
}
func (c *Client) ClearKeyboard(ctx context.Context, chatID, messageID int64) error {
	if messageID <= 0 {
		return &APIError{Code: "INVALID_INPUT"}
	}
	_, err := c.message(ctx, "editMessageReplyMarkup", chatID, messageID, "", &InlineKeyboard{Rows: [][]Button{}})
	return err
}

func (c *Client) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	if chatID <= 0 || chatID > 1<<52-1 || messageID <= 0 {
		return &APIError{Code: "INVALID_INPUT"}
	}
	var ok bool
	err := c.call(ctx, "deleteMessage", map[string]any{"chat_id": chatID, "message_id": messageID}, &ok, 10*time.Second)
	if err == nil && !ok {
		return invalid()
	}
	return err
}

func validURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && utf8.ValidString(raw) && !strings.ContainsRune(raw, '\x00')
}
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var out User
	e := c.call(ctx, "getMe", struct{}{}, &out, 10*time.Second)
	if e == nil && (out.ID <= 0 || !out.IsBot) {
		return User{}, invalid()
	}
	return out, e
}
func (c *Client) SetChatMenuButton(ctx context.Context, raw string) error {
	if !validURL(raw) {
		return &APIError{Code: "INVALID_INPUT"}
	}
	var ok bool
	e := c.call(ctx, "setChatMenuButton", map[string]any{"menu_button": map[string]any{"type": "web_app", "text": "Кабинет / Cabinet", "web_app": WebAppInfo{URL: raw}}}, &ok, 10*time.Second)
	if e == nil && !ok {
		return invalid()
	}
	return e
}

func (c *Client) CreateStarsInvoice(ctx context.Context, title, description, payload string, amount, period int64) (string, error) {
	var out string
	if amount <= 0 || period != 0 && (period != 2592000 || amount > 10000) || !utf8.ValidString(payload) || len(payload) < 1 || len(payload) > 128 || len(title) < 1 || utf8.RuneCountInString(title) > 32 || len(description) < 1 || utf8.RuneCountInString(description) > 255 {
		return out, &APIError{Code: "INVALID_INPUT"}
	}
	in := map[string]any{"title": title, "description": description, "payload": payload, "provider_token": "", "currency": "XTR", "prices": []map[string]any{{"label": "Subscription", "amount": amount}}}
	if period != 0 {
		in["subscription_period"] = period
	}
	err := c.call(ctx, "createInvoiceLink", in, &out, 10*time.Second)
	return out, err
}
func (c *Client) RefundStars(ctx context.Context, payer int64, charge string) error {
	if payer <= 0 || payer > 1<<52-1 || len(charge) < 1 || len(charge) > 4096 || !utf8.ValidString(charge) || strings.ContainsRune(charge, '\x00') {
		return &APIError{Code: "INVALID_INPUT"}
	}
	var ok bool
	err := c.call(ctx, "refundStarPayment", map[string]any{"user_id": payer, "telegram_payment_charge_id": charge}, &ok, 10*time.Second)
	if err == nil && !ok {
		return invalid()
	}
	return err
}
func (c *Client) AnswerStarsPreCheckout(ctx context.Context, id string, ok bool, message string) error {
	if len(id) < 1 || len(id) > 128 || !utf8.ValidString(id) || strings.ContainsRune(id, '\x00') || !utf8.ValidString(message) || !ok && message == "" {
		return &APIError{Code: "INVALID_INPUT"}
	}
	in := map[string]any{"pre_checkout_query_id": id, "ok": ok}
	if !ok {
		in["error_message"] = message
	}
	var result bool
	err := c.call(ctx, "answerPreCheckoutQuery", in, &result, 5*time.Second)
	if err == nil && !result {
		return invalid()
	}
	return err
}

func (c *Client) EditStarsSubscription(ctx context.Context, payer int64, charge string, canceled bool) error {
	if payer <= 0 || payer > 1<<52-1 || len(charge) < 1 || len(charge) > 4096 || !utf8.ValidString(charge) || strings.ContainsRune(charge, '\x00') {
		return &APIError{Code: "INVALID_INPUT"}
	}
	var ok bool
	err := c.call(ctx, "editUserStarSubscription", map[string]any{"user_id": payer, "telegram_payment_charge_id": charge, "is_canceled": canceled}, &ok, 10*time.Second)
	if err == nil && !ok {
		return invalid()
	}
	return err
}
