package telegram

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"fmt"
	"github.com/google/uuid"
	"html"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func keyboard(action, id, text string) *botapi.InlineKeyboard {
	return &botapi.InlineKeyboard{Rows: [][]botapi.Button{{{Text: text, Data: "wt1:" + action + ":" + id}}}}
}
func renderCard(p TrialCard) (string, *botapi.InlineKeyboard, error) {
	states := map[string]string{"pending": "Ожидает решения", "approved": "Одобрено. Выдача запускается", "rejected": "Отказано", "provisioning": "Выдача выполняется", "needs_review": "Нужна проверка поддержки", "active": "Доступ выдан", "expired": "Доступ истёк"}
	status, ok := states[p.Status]
	if !ok || p.RequestID == uuid.Nil || p.CreatedAt.IsZero() || !validReason(p.Comment, 0) || (p.OperationID != nil && *p.OperationID == uuid.Nil) || (p.TargetMessageID != nil && *p.TargetMessageID <= 0) {
		return "", nil, &ActionError{Code: "INVALID_INPUT"}
	}
	identity := "Email: " + html.EscapeString(p.Email) + "\n"
	if p.Email == "" {
		id, err := strconv.ParseInt(p.TelegramID, 10, 64)
		if err != nil || id <= 0 || p.DisplayName == "" || utf8.RuneCountInString(p.DisplayName) > 128 {
			return "", nil, &ActionError{Code: "INVALID_INPUT"}
		}
		identity = "Имя: " + html.EscapeString(p.DisplayName) + "\nTelegram ID: " + html.EscapeString(p.TelegramID) + "\n"
	}
	text := fmt.Sprintf("<b>Web-триал</b>\nЗаявка: <code>%s</code>\n%sСоздана: %s\nКомментарий: %s\n\n%s", p.RequestID, identity, p.CreatedAt.UTC().Format(time.RFC3339), html.EscapeString(p.Comment), status)
	var k *botapi.InlineKeyboard
	switch p.Status {
	case "pending":
		k = keyboard("a", p.RequestID.String(), "Одобрить")
		k.Rows[0] = append(k.Rows[0], botapi.Button{Text: "Отказать", Data: "wt1:r:" + p.RequestID.String()})
	case "rejected":
		k = keyboard("s", p.RequestID.String(), "Пересмотреть")
	case "needs_review":
		if p.OperationID != nil {
			k = keyboard("c", p.OperationID.String(), "Проверить выдачу")
		}
	}
	return text, k, nil
}
func validReason(text string, min int) bool {
	return utf8.ValidString(text) && !strings.ContainsRune(text, '\x00') && utf8.RuneCountInString(text) <= 1000 && utf8.RuneCountInString(strings.TrimSpace(text)) >= min
}

type confirmation struct {
	SupportAction
	action, callbackID string
	messageID          int64
}
type dispatcher struct {
	api       *botapi.Client
	actions   TrialActions
	operators map[int64]bool
	// ponytail: unfinished dialogues are volatile; persist them only if operators
	// need to continue an uncommitted confirmation after a process restart.
	pending map[int64]*confirmation
}

func newDispatcher(api *botapi.Client, actions TrialActions, operators []int64) *dispatcher {
	d := &dispatcher{api: api, actions: actions, operators: map[int64]bool{}, pending: map[int64]*confirmation{}}
	for _, id := range operators {
		d.operators[id] = true
	}
	return d
}
func (d *dispatcher) allowed(u *botapi.User, m *botapi.Message) bool {
	return u != nil && !u.IsBot && u.ID > 0 && d.operators[u.ID] && m != nil && m.ID > 0 && m.Date > 0 && m.Chat.Type == "private" && m.Chat.ID == u.ID
}

// A lost cosmetic response does not undo a committed decision. Authentication
// and polling-conflict errors stop the channel; rate limits preserve the pause.
// Other cosmetic replies are best effort.
func cosmetic(err error) error {
	var api *botapi.APIError
	if errors.As(err, &api) && (api.Code == "UNAUTHORIZED" || api.Code == "CONFLICT" || api.Code == "RATE_LIMITED") {
		return err
	}
	return nil
}
func (d *dispatcher) promptFailure(actor int64, err error) error {
	switch safeCode(err) {
	case "FORBIDDEN", "BAD_REQUEST":
		delete(d.pending, actor)
		return nil // This dialogue cannot be delivered; other operators may proceed.
	default:
		return err
	}
}
func (d *dispatcher) answer(ctx context.Context, c *botapi.Callback, text string) error {
	return cosmetic(d.api.AnswerCallback(ctx, c.ID, text, true))
}
func (d *dispatcher) failure(ctx context.Context, c *botapi.Callback, err error) error {
	text := "Не удалось подтвердить результат. Повторите действие позже."
	var action *ActionError
	if errors.As(err, &action) {
		if action.Code == "INVALID_CREDENTIALS" {
			text = "Действие недоступно этому оператору."
		}
		if action.CurrentRequestStatus == "approved" {
			text = "Решение уже принято: одобрена."
		}
		if action.CurrentRequestStatus == "rejected" {
			text = "Решение уже принято: отказано."
		}
	}
	if replyErr := d.answer(ctx, c, text); replyErr != nil {
		return replyErr
	}
	if action == nil || action.Code == "SERVICE_UNAVAILABLE" {
		return err
	}
	return nil // Refused domain commands are safe to acknowledge.
}
func (d *dispatcher) handle(ctx context.Context, u botapi.Update) error {
	if u.Callback != nil {
		return d.callback(ctx, u.Callback)
	}
	m := u.Message
	if m == nil || !d.allowed(m.From, m) {
		return nil
	}
	p := d.pending[m.From.ID]
	if p == nil {
		return nil
	}
	if strings.HasPrefix(strings.TrimSpace(m.Text), "/") {
		delete(d.pending, m.From.ID)
		return nil
	}
	if p.Reason != "" {
		return nil
	}
	if !validReason(m.Text, 1) {
		_, err := d.api.SendMessage(ctx, m.Chat.ID, "Причина должна содержать от 1 до 1000 символов.", nil)
		return cosmetic(err)
	}
	k := keyboard("y", p.TargetID.String(), "Подтвердить")
	k.Rows[0] = append(k.Rows[0], botapi.Button{Text: "Отмена", Data: "wt1:x:" + p.TargetID.String()})
	msg, err := d.api.SendMessage(ctx, m.Chat.ID, "Подтвердите действие поддержки.\nПричина: "+html.EscapeString(m.Text), k)
	if err != nil {
		return d.promptFailure(m.From.ID, err)
	}
	p.Reason = m.Text
	p.messageID = msg.ID
	return nil
}
func (d *dispatcher) callback(ctx context.Context, c *botapi.Callback) error {
	if c.ID == "" || len(c.ID) > 128 || !utf8.ValidString(c.ID) || strings.ContainsRune(c.ID, '\x00') {
		return nil
	}
	if !d.allowed(&c.From, c.Message) {
		return d.answer(ctx, c, "Действие недоступно.")
	}
	parts := strings.Split(c.Data, ":")
	if len(parts) != 3 || parts[0] != "wt1" || len(parts[1]) != 1 || !strings.Contains("arscyx", parts[1]) || len(c.Data) > 64 {
		return d.answer(ctx, c, "Некорректная кнопка.")
	}
	id, err := uuid.Parse(parts[2])
	if err != nil || id == uuid.Nil {
		return d.answer(ctx, c, "Некорректная кнопка.")
	}
	action := parts[1]
	if action == "a" || action == "r" {
		decision := "approve"
		if action == "r" {
			decision = "reject"
		}
		_, err = d.actions.Decide(ctx, TrialDecision{RequestID: id, ActorID: c.From.ID, Action: decision, CallbackID: c.ID})
		if err != nil {
			return d.failure(ctx, c, err)
		}
		return d.answer(ctx, c, "Решение принято.")
	}
	if action == "s" || action == "c" {
		p := d.pending[c.From.ID]
		if p == nil || p.callbackID != c.ID {
			p = &confirmation{SupportAction: SupportAction{TargetID: id, ActorID: c.From.ID, Key: uuid.New()}, action: action, callbackID: c.ID}
			d.pending[c.From.ID] = p
		}
		msg, err := d.api.SendMessage(ctx, c.From.ID, "Укажите причину обращения поддержки (1–1000 символов).", keyboard("x", id.String(), "Отмена"))
		if err != nil {
			return d.promptFailure(c.From.ID, err)
		}
		p.messageID = msg.ID
		return d.answer(ctx, c, "")
	}
	p := d.pending[c.From.ID]
	if p == nil || p.TargetID != id || p.messageID != c.Message.ID {
		return d.answer(ctx, c, "Подтверждение устарело.")
	}
	if action == "x" {
		delete(d.pending, c.From.ID)
		return d.answer(ctx, c, "Действие отменено.")
	}
	if p.Reason == "" {
		return d.answer(ctx, c, "Сначала укажите причину.")
	}
	if p.action == "s" {
		_, err = d.actions.Reconsider(ctx, p.SupportAction)
	} else {
		_, err = d.actions.Reconcile(ctx, p.SupportAction)
	}
	if err != nil {
		return d.failure(ctx, c, err)
	}
	delete(d.pending, c.From.ID)
	if err = d.answer(ctx, c, "Действие поддержки принято."); err != nil {
		return err
	}
	return cosmetic(d.api.ClearKeyboard(ctx, c.From.ID, c.Message.ID))
}
