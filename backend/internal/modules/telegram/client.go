package telegram

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
)

type Client struct {
	origin, username string
	botID            int64
	api              *botapi.Client
	accounts         *accounts.Service
	payments         *payments.Service
	notices          *notifications.Service
}

var botUsername = regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`)

func NewClient(origin string, a *accounts.Service, p *payments.Service, n *notifications.Service) (*Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || a == nil || p == nil || n == nil {
		return nil, errors.New("invalid Telegram client configuration")
	}
	return &Client{origin: strings.TrimSuffix(origin, "/"), accounts: a, payments: p, notices: n}, nil
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
	if strings.HasPrefix(path, "/orders/") {
		return c.origin + "/mini-app" + path + "?lang=" + lang
	}
	return c.origin + "/mini-app/cabinet" + path + "?lang=" + lang
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
	_, err := c.api.SendMessage(ctx, chat, clientText(lang, "Подписка, подключение и поддержка доступны в кабинете.", "Your subscription, connection and support are available in the cabinet."), k)
	return cosmetic(err)
}
func (c *Client) deliver(parent context.Context, j notifications.ClientJob) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	path := ""
	switch j.Route {
	case "history", "support":
		path = "/" + j.Route
	case "cabinet":
	default:
		if !strings.HasPrefix(j.Route, "orders:") {
			return &ActionError{Code: "INVALID_INPUT"}
		}
		path = "/orders/" + strings.TrimPrefix(j.Route, "orders:")
	}
	return c.notices.DeliverClient(ctx, j, func() (notifications.ClientOutcome, error) {
		k := &botapi.InlineKeyboard{Rows: [][]botapi.Button{{{
			Text: clientText(j.Locale, "Открыть кабинет", "Open cabinet"), WebApp: &botapi.WebAppInfo{URL: c.route(path, j.Locale)},
		}}, {{Text: clientText(j.Locale, "Закрыть", "Close"), Data: "cn1:" + j.ID.String()}}}}
		msg, err := c.api.SendMessage(ctx, j.TelegramID, clientText(j.Locale, "В кабинете есть обновление.", "There is an update in your cabinet."), k)
		if err == nil {
			return notifications.ClientOutcome{State: "sent", MessageID: msg.ID}, nil
		}
		switch safeCode(err) {
		case "FORBIDDEN":
			return notifications.ClientOutcome{State: "failed", Code: "forbidden"}, nil
		case "BAD_REQUEST":
			return notifications.ClientOutcome{State: "failed", Code: "bad_request"}, nil
		}
		return notifications.ClientOutcome{}, err
	})
}
func (c *Client) handle(ctx context.Context, u botapi.Update) (bool, error) {
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
	case "/start", "/help", "/support":
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
	path, source := "", ""
	if command[0] == "/support" {
		path = "/support"
	}
	if len(fields) == 2 {
		source = fields[1]
	}
	return true, c.send(ctx, m.Chat.ID, lang, path, source)
}
