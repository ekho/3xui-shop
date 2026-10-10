package telegram

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/operations"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"github.com/google/uuid"
)

type Client struct {
	origin, username string
	botID            int64
	api              *botapi.Client
	accounts         *accounts.Service
	bonuses          *bonuses.Service
	payments         *payments.Service
	notices          *notifications.Service
	maintenance      *operations.Maintenance
}

var botUsername = regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`)

func NewClient(origin string, a *accounts.Service, p *payments.Service, n *notifications.Service, maintenance ...*operations.Maintenance) (*Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || a == nil || p == nil || n == nil {
		return nil, errors.New("invalid Telegram client configuration")
	}
	c := &Client{origin: strings.TrimSuffix(origin, "/"), accounts: a, payments: p, notices: n}
	if len(maintenance) > 0 {
		c.maintenance = maintenance[0]
	}
	return c, nil
}
func (c *Client) start(ctx context.Context, api *botapi.Client, token string) error {
	me, err := api.GetMe(ctx)
	if err != nil {
		return err
	}
	id, _ := strconv.ParseInt(strings.SplitN(token, ":", 2)[0], 10, 64)
	if !me.IsBot || me.ID != id || !botUsername.MatchString(me.Username) {
		return &botapi.APIError{Code: "INVALID_RESPONSE"}
	}
	if !me.HasMainWebApp {
		return &ActionError{Code: "MINI_APP_NOT_CONFIGURED"}
	}
	c.api, c.botID, c.username = api, me.ID, me.Username
	return api.SetChatMenuButton(ctx, c.origin+"/mini-app/cabinet")
}
func clientActor(u *botapi.User, m *botapi.Message) bool {
	return u != nil && !u.IsBot && u.ID > 0 && u.ID <= 1<<52-1 && m != nil && m.ID > 0 && m.Date > 0 && m.Chat.Type == "private" && m.Chat.ID == u.ID
}
func clientLang(u botapi.User) string {
	if u.LanguageCode == "" || strings.HasPrefix(u.LanguageCode, "ru") {
		return "ru"
	}
	return "en"
}
func startPayload(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, v := range s {
		if !(v >= 'A' && v <= 'Z' || v >= 'a' && v <= 'z' || v >= '0' && v <= '9' || v == '_' || v == '-') {
			return false
		}
	}
	return true
}
func clientText(lang, ru, en string) string {
	if lang == "ru" {
		return ru
	}
	return en
}
func (c *Client) route(path, lang string) string {
	prefix := "/mini-app/cabinet"
	if strings.HasPrefix(path, "/orders/") || strings.HasPrefix(path, "/catalogue") {
		prefix = "/mini-app"
	}
	u, err := url.Parse(c.origin + prefix + path)
	if err != nil {
		return c.origin + "/mini-app/cabinet?lang=" + lang
	}
	q := u.Query()
	q.Set("lang", lang)
	u.RawQuery = q.Encode()
	return u.String()
}
func (c *Client) send(ctx context.Context, chat int64, lang, path, source string) error {
	b := botapi.Button{Text: clientText(lang, "Открыть кабинет", "Open cabinet"), WebApp: &botapi.WebAppInfo{URL: c.route(path, lang)}}
	if source != "" {
		b.WebApp = nil
		b.URL = "https://t.me/" + c.username + "?startapp=" + url.QueryEscape(source)
	}
	k := &botapi.InlineKeyboard{Rows: [][]botapi.Button{{b}, {{
		Text: clientText(lang, "Поддержка", "Support"), WebApp: &botapi.WebAppInfo{URL: c.route("/support", lang)},
	}}}}
	if c.bonuses != nil {
		k.Rows = append(k.Rows, []botapi.Button{{Text: clientText(lang, "Приглашения", "Invitations"), WebApp: &botapi.WebAppInfo{URL: c.route("/referrals", lang)}}})
	}
	message := clientText(lang, "Подписка, подключение и поддержка доступны в кабинете.", "Your subscription, connection and support are available in the cabinet.")
	if c.maintenance != nil {
		status, err := c.maintenance.Status(ctx)
		if err != nil {
			message = clientText(lang, "Статус обслуживания временно недоступен. Кабинет и поддержка доступны.", "Maintenance status is temporarily unavailable. Cabinet and support remain available.")
		} else if status.Enabled {
			message = clientText(lang, "Идут технические работы. Новые покупки и пробные подписки временно недоступны. Кабинет и поддержка доступны.", "Maintenance is in progress. New purchases and trials are temporarily unavailable. Cabinet and support remain available.")
		}
	}
	_, err := c.api.SendMessage(ctx, chat, message, k)
	return cosmetic(err)
}
func (c *Client) deliver(parent context.Context, j notifications.ClientJob) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	path := ""
	switch j.Route {
	case "history", "support", "renew":
		path = "/" + j.Route
	case "cabinet":
	default:
		if !strings.HasPrefix(j.Route, "orders:") {
			return &ActionError{Code: "INVALID_INPUT"}
		}
		path = "/orders/" + strings.TrimPrefix(j.Route, "orders:")
	}
	var limited error
	err := c.notices.DeliverClient(ctx, j, func() (notifications.ClientOutcome, error) {
		label := clientText(j.Locale, "Открыть кабинет", "Open cabinet")
		if j.Route == "renew" {
			label = clientText(j.Locale, "Продлить подписку", "Renew subscription")
		}
		closeData := "cn1:" + j.ID.String()
		if j.NoticeActionID != uuid.Nil {
			closeData = "on1:" + j.ID.String()
		}
		k := &botapi.InlineKeyboard{Rows: [][]botapi.Button{{{
			Text: label, WebApp: &botapi.WebAppInfo{URL: c.route(path, j.Locale)},
		}}, {{Text: clientText(j.Locale, "Закрыть", "Close"), Data: closeData}}}}
		if j.VPNAlertCode != "" {
			k.Rows = k.Rows[1:]
		}
		text := j.ReminderText
		if text == "" {
			text = clientText(j.Locale, "В кабинете есть обновление.", "There is an update in your cabinet.")
		}
		if j.NoticeActionID != uuid.Nil {
			text = j.NoticeHTML
		}
		var msg botapi.Message
		var err error
		if j.NoticeActionID != uuid.Nil && j.NoticeMode == "delete" {
			err = c.api.DeleteMessage(ctx, j.TelegramID, j.NoticeMessageID)
			msg.ID = j.NoticeMessageID
		} else if j.NoticeActionID != uuid.Nil && j.PriorDeliveryID != uuid.Nil {
			msg, err = c.api.EditMessage(ctx, j.TelegramID, j.NoticeMessageID, text, k)
		} else {
			msg, err = c.api.SendMessage(ctx, j.TelegramID, text, k)
		}
		if err == nil {
			if j.NoticeActionID != uuid.Nil && j.PriorDeliveryID == uuid.Nil {
				if msg.Date <= 0 || j.NoticeResult == nil {
					return notifications.ClientOutcome{}, &botapi.APIError{Code: "INVALID_RESPONSE"}
				}
				j.NoticeResult.MessageAt = time.Unix(msg.Date, 0)
			}
			return notifications.ClientOutcome{State: "sent", MessageID: msg.ID}, nil
		}
		switch safeCode(err) {
		case "NOT_MODIFIED":
			if j.NoticeActionID != uuid.Nil && j.NoticeMode == "edit" && j.PriorDeliveryID != uuid.Nil {
				return notifications.ClientOutcome{State: "sent", MessageID: j.NoticeMessageID}, nil
			}
		case "FORBIDDEN":
			return notifications.ClientOutcome{State: "failed", Code: "forbidden"}, nil
		case "BAD_REQUEST":
			return notifications.ClientOutcome{State: "failed", Code: "bad_request"}, nil
		case "RATE_LIMITED":
			var fault *botapi.APIError
			if errors.As(err, &fault) {
				limited = err
				return notifications.ClientOutcome{State: "retry", RetryAfter: fault.RetryAfter}, nil
			}
		}
		return notifications.ClientOutcome{}, err
	})
	if err != nil {
		return err
	}
	return limited
}
func (c *Client) handle(ctx context.Context, u botapi.Update) (bool, error) {
	if u.Callback != nil {
		return c.callback(ctx, u.Callback)
	}
	m := u.Message
	if m == nil || !clientActor(m.From, m) {
		return false, nil
	}
	fields := strings.Fields(m.Text)
	if len(fields) == 0 {
		return false, nil
	}
	command := strings.SplitN(fields[0], "@", 2)
	switch command[0] {
	case "/start", "/help", "/support", "/paysupport", "/referrals":
	default:
		return false, nil
	}
	if len(command) == 2 && !strings.EqualFold(command[1], c.username) {
		return false, nil
	}
	lang := clientLang(*m.From)
	if len(fields) > 2 || (command[0] != "/start" && len(fields) != 1) || (len(fields) == 2 && !startPayload(fields[1])) {
		_, err := c.api.SendMessage(ctx, m.Chat.ID, clientText(lang, "Некорректная команда. Используйте /start.", "Invalid command. Use /start."), nil)
		return true, cosmetic(err)
	}
	if command[0] == "/referrals" {
		return true, c.sendReferrals(ctx, m.Chat.ID, lang)
	}
	path, source := "", ""
	if command[0] == "/support" || command[0] == "/paysupport" {
		path = "/support"
	}
	if len(fields) == 2 {
		source = fields[1]
	}
	return true, c.send(ctx, m.Chat.ID, lang, path, source)
}

func (c *Client) callback(parent context.Context, q *botapi.Callback) (bool, error) {
	path, state := "", q.Data
	legacySubscription := strings.HasPrefix(state, "subscription:")
	closeNotice := strings.HasPrefix(state, "cn1:")
	closeOperatorNotice := strings.HasPrefix(state, "on1:")
	if !legacySubscription && !closeNotice && !closeOperatorNotice {
		switch state {
		case "start", "main_menu", "profile", "show_key", "download", "platform", "platform_ios", "platform_android", "platform_macos", "platform_windows", "download_show_qr", "support", "how_to_connect", "vpn_not_working", "subscription", "close_notification", "redirect_to_download", "referral":
		default:
			return false, nil
		}
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	lang := clientLang(q.From)
	refuse := func() (bool, error) {
		return true, cosmetic(c.api.AnswerCallback(ctx, q.ID, clientText(lang, "Кнопка недоступна. Используйте /start.", "This button is unavailable. Use /start."), true))
	}
	m := q.Message
	if !clientActor(&q.From, m) || m.From == nil || !m.From.IsBot || m.From.ID != c.botID || len(q.ID) == 0 || len(q.ID) > 128 || !utf8.ValidString(q.ID) || strings.ContainsRune(q.ID, '\x00') || len(q.Data) > 64 || !utf8.ValidString(q.Data) || m.ReplyMarkup == nil {
		return refuse()
	}
	button := false
	for _, row := range m.ReplyMarkup.Rows {
		for _, b := range row {
			if b.Data == q.Data {
				button = true
			}
		}
	}
	if !button {
		return refuse()
	}
	if state == "referral" {
		if err := c.sendReferrals(ctx, q.From.ID, lang); err != nil {
			return true, err
		}
		return true, cosmetic(c.api.AnswerCallback(ctx, q.ID, "", false))
	}
	if closeNotice {
		raw := strings.TrimPrefix(q.Data, "cn1:")
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil || id.String() != raw {
			return refuse()
		}
		valid, err := c.notices.CloseClient(ctx, id, q.From.ID, m.ID, func() error { return cosmetic(c.api.ClearKeyboard(ctx, q.From.ID, m.ID)) })
		if err != nil {
			return true, err
		}
		if !valid {
			return refuse()
		}
		return true, cosmetic(c.api.AnswerCallback(ctx, q.ID, "", false))
	}
	if closeOperatorNotice {
		raw := strings.TrimPrefix(q.Data, "on1:")
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil || id.String() != raw {
			return refuse()
		}
		valid, err := c.notices.CloseNotice(ctx, id, q.From.ID, m.ID, func() error { return c.api.DeleteMessage(ctx, q.From.ID, m.ID) })
		if !valid {
			if err != nil {
				return true, err
			}
			return refuse()
		}
		if err != nil {
			return true, cosmetic(c.api.AnswerCallback(ctx, q.ID, clientText(lang, "Сообщение закрыто в кабинете. Удаление в Telegram не подтверждено.", "The notice is closed in your cabinet. Removal in Telegram is unconfirmed."), true))
		}
		return true, cosmetic(c.api.AnswerCallback(ctx, q.ID, "", false))
	}
	if legacySubscription {
		var err error
		state, err = payments.LegacyTelegramButton(q.Data, q.From.ID)
		if err != nil {
			return refuse()
		}
	}
	switch state {
	case "show_key", "download", "platform", "download_show_qr", "how_to_connect", "redirect_to_download":
		path = "#connection-title"
	case "platform_ios", "platform_android", "platform_macos", "platform_windows":
		path = "?platform=" + strings.TrimPrefix(state, "platform_") + "#connection-title"
	case "support", "vpn_not_working":
		path = "/support"
	case "subscription", "process", "devices", "duration", "promocode", "back_to_duration", "back_to_payment":
		path = "/catalogue"
	case "extend":
		path = "/renew"
	case "change":
		path = "/change-plan"
	default:
		if strings.HasPrefix(state, "pay_") {
			ref, err := c.payments.LegacyTelegramReference(ctx, q.From.ID, q.Data)
			if err != nil {
				var e *payments.Error
				if errors.As(err, &e) && (e.Status == 400 || e.Status == 401 || e.Status == 403) {
					return refuse()
				}
				return true, err
			}
			path = "/history?kind=legacy"
			if ref != nil {
				path += "&legacy_source_id=" + *ref
			}
		}
	}
	if state == "close_notification" || state == "redirect_to_download" {
		if err := cosmetic(c.api.ClearKeyboard(ctx, q.From.ID, m.ID)); err != nil {
			return true, err
		}
		if state == "close_notification" {
			return true, cosmetic(c.api.AnswerCallback(ctx, q.ID, "", false))
		}
	}
	if err := c.send(ctx, q.From.ID, lang, path, ""); err != nil {
		return true, err
	}
	return true, cosmetic(c.api.AnswerCallback(ctx, q.ID, "", false))
}
