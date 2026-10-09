package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

type supportBridge struct {
	cfg       SupportConfig
	botID     int64
	origin    string
	username  string
	authority *accounts.Service
	owner     *support.Service
	access    *subscriptions.Service
	api       *botapi.Client
}

func NewSupport(cfg SupportConfig, client *http.Client, origin string, authority *accounts.Service, owner *support.Service, access *subscriptions.Service) (*Runtime, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	r := &Runtime{enabled: cfg.Enabled, token: cfg.Token}
	if !cfg.Enabled {
		return r, nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || authority == nil || owner == nil || access == nil {
		return nil, errors.New("invalid support domain contracts")
	}
	id, _ := strconv.ParseInt(strings.SplitN(cfg.Token, ":", 2)[0], 10, 64)
	if err = owner.ConfigureTelegram(id, cfg.GroupID); err != nil {
		return nil, err
	}
	r.api = botapi.New(cfg.Token, client)
	r.support = &supportBridge{cfg: cfg, botID: id, origin: strings.TrimRight(origin, "/"), authority: authority, owner: owner, access: access, api: r.api}
	return r, nil
}

func (b *supportBridge) start(ctx context.Context) error {
	me, err := b.api.GetMe(ctx)
	if err != nil {
		return err
	}
	if me.ID != b.botID {
		return &ActionError{Code: "SUPPORT_BOT_MISMATCH"}
	}
	chat, err := b.api.GetChat(ctx, b.cfg.GroupID)
	if err != nil {
		return err
	}
	if chat.Type != "supergroup" || !chat.IsForum {
		return &ActionError{Code: "SUPPORT_GROUP_NOT_FORUM"}
	}
	member, err := b.api.GetChatMember(ctx, b.cfg.GroupID, b.botID)
	if err != nil {
		return err
	}
	if member.Status != "administrator" || !member.CanManageTopics {
		return &ActionError{Code: "SUPPORT_ADMIN_REQUIRED"}
	}
	b.username = me.Username
	return nil
}

func (b *supportBridge) handle(ctx context.Context, u botapi.Update) error {
	if present(u.Subscription) || present(u.PreCheckout) || u.Message != nil && (present(u.Message.SuccessfulPayment) || present(u.Message.RefundedPayment)) {
		return &ActionError{Code: "UNSUPPORTED_PAYMENT"}
	}
	if u.Callback != nil {
		return b.handleSupportCallback(ctx, *u.Callback, u.ID)
	}
	m := u.Message
	if m == nil || m.ID <= 0 || m.ID > 1<<52-1 {
		return nil
	}
	if m.Chat.ID == b.cfg.GroupID && m.Chat.Type == "supergroup" && m.ThreadID > 1 && m.ThreadID <= 1<<52-1 {
		var fields map[string]json.RawMessage
		if len(m.Raw) > 0 && json.Unmarshal(m.Raw, &fields) == nil {
			_, closed := fields["forum_topic_closed"]
			_, reopened := fields["forum_topic_reopened"]
			if closed || reopened {
				if closed && reopened {
					return nil
				}
				digest, err := m.SupportDigest()
				if err != nil {
					return err
				}
				actor := int64(0)
				if m.From != nil && m.From.ID > 0 && m.From.ID <= 1<<52-1 {
					actor = m.From.ID
				}
				action := "topic_reopened"
				if closed {
					action = "topic_closed"
				}
				return supportHandlingError(b.owner.ObserveTelegramTopic(ctx, support.TelegramSource{BotID: b.botID, GroupID: b.cfg.GroupID, ChatID: m.Chat.ID, MessageID: m.ID, UpdateID: u.ID, ActorID: actor, ThreadID: m.ThreadID, Action: action, Digest: digest}, closed))
			}
		}
	}
	if m.From == nil || m.From.IsBot || m.From.ID <= 0 || m.From.ID > 1<<52-1 {
		return nil
	}
	if m.Chat.ID != m.From.ID || m.Chat.Type != "private" || m.ThreadID != 0 {
		if m.Chat.ID != b.cfg.GroupID || m.Chat.Type != "supergroup" || m.ThreadID < 0 || m.ThreadID > 1<<52-1 {
			return nil
		}
	}
	if m.Chat.Type == "private" && (m.Text == "/start" || strings.EqualFold(m.Text, "/start@"+b.username)) {
		lang := clientLang(*m.From)
		_, err := b.api.SendMessage(ctx, m.Chat.ID, clientText(lang, "Напишите сообщение в этот чат, чтобы обратиться в поддержку.", "Send a message here to contact support."), &botapi.InlineKeyboard{Rows: [][]botapi.Button{{{Text: clientText(lang, "Открыть кабинет", "Open cabinet"), URL: b.origin + "/cabinet/support?lang=" + lang}}}})
		return err
	}
	sourceCtx, actor, err := b.authority.ResolveTelegramContext(ctx, m.From.ID)
	if err != nil {
		return supportHandlingError(err)
	}
	if m.Chat.ID == b.cfg.GroupID {
		if actor == nil {
			return nil
		}
		if err = b.authority.RequireOperator(sourceCtx, actor.ID); err != nil {
			return supportHandlingError(err)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(m.Text), "/") {
		if m.Chat.ID != b.cfg.GroupID || actor == nil {
			return nil
		}
		in, topic, ok := parseSupportCommand(m.Text, b.username)
		if !ok {
			return nil
		}
		digest, e := m.SupportDigest()
		if e != nil {
			return e
		}
		return supportHandlingError(b.runSupportCommand(sourceCtx, support.TelegramSource{BotID: b.botID, GroupID: b.cfg.GroupID, ChatID: m.Chat.ID, MessageID: m.ID, UpdateID: u.ID, ActorID: m.From.ID, ThreadID: m.ThreadID, Action: "command", Digest: digest}, topic, in))
	}
	if m.Chat.ID == b.cfg.GroupID && m.ThreadID <= 1 {
		return nil
	}
	if utf8.RuneCountInString(m.Text) > 4000 {
		_, err = b.api.SendSupportText(ctx, m.Chat.ID, m.ThreadID, clientText(clientLang(*m.From), "Сообщение слишком длинное: отправьте до 4000 символов или приложите файл.", "Message is too long: send up to 4000 characters or attach a file."))
		return err
	}
	in, err := b.messageInput(ctx, *m, u.ID, actor != nil)
	if err != nil {
		return err
	}
	_, err = b.owner.ReceiveTelegramMessage(sourceCtx, in)
	return supportHandlingError(err)
}

func supportHandlingError(err error) error {
	var account *accounts.Error
	var owner *support.Error
	var access *subscriptions.Error
	if errors.As(err, &account) && account.Status < 500 || errors.As(err, &owner) && owner.Status < 500 || errors.As(err, &access) && access.Status < 500 {
		return nil
	}
	return err
}

func (b *supportBridge) send(ctx context.Context, part support.TelegramPart) (support.TelegramOutcome, error) {
	var out support.TelegramOutcome
	var err error
	switch part.Kind {
	case "create_topic":
		out.ThreadID, err = b.api.CreateForumTopic(ctx, part.ChatID, part.Text)
	case "text":
		out.MessageID, err = b.api.SendSupportText(ctx, part.ChatID, part.ThreadID, part.Text)
	case "copy":
		out.MessageID, err = b.api.CopySupportMessage(ctx, part.ChatID, part.ThreadID, part.CopyChatID, part.CopyMessageID)
	case "document":
		out.MessageID, err = b.api.SendSupportDocument(ctx, part.ChatID, part.ThreadID, part.Name, part.Bytes, part.Text)
	case "close_topic", "reopen_topic":
		out.Acknowledged, err = b.api.SetForumTopicClosed(ctx, part.ChatID, part.ThreadID, part.Kind == "close_topic")
	case "general", "control", "confirmation":
		keyboard := &botapi.InlineKeyboard{Rows: [][]botapi.Button{{{Text: "Open cabinet", URL: b.origin + "/admin/clients"}}}}
		for _, link := range part.Links {
			if link.AccountID == uuid.Nil {
				return support.TelegramOutcome{Status: "failed", Code: "BAD_REQUEST"}, nil
			}
			keyboard.Rows = append(keyboard.Rows, []botapi.Button{{Text: link.AccountID.String(), URL: b.origin + "/admin/clients/" + link.AccountID.String() + "/show"}})
		}
		if part.Kind == "confirmation" {
			keyboard.Rows = append(keyboard.Rows, []botapi.Button{{Text: "Confirm", Data: "sp1:c:" + part.ConfirmationID.String()}, {Text: "Cancel", Data: "sp1:x:" + part.ConfirmationID.String()}})
		}
		out.MessageID, err = b.api.SendSupportCard(ctx, part.ChatID, part.ThreadID, part.Text, keyboard)
	default:
		return support.TelegramOutcome{Status: "failed", Code: "BAD_REQUEST"}, nil
	}
	if err == nil {
		out.Status = "sent"
		return out, nil
	}
	var api *botapi.APIError
	if errors.As(err, &api) {
		switch api.Code {
		case "BAD_REQUEST", "FORBIDDEN", "UNAUTHORIZED", "CONFLICT", "THREAD_NOT_FOUND":
			return support.TelegramOutcome{Status: "failed", Code: api.Code}, err
		case "RATE_LIMITED":
			return support.TelegramOutcome{Status: "retry", Code: api.Code, RetryAfter: api.RetryAfter}, err
		}
	}
	return support.TelegramOutcome{Status: "unknown", Code: "ACK_UNKNOWN"}, err
}

type SupportConfig struct {
	Enabled bool
	Token   string
	GroupID int64
}

func (c SupportConfig) validate() error {
	if !c.Enabled {
		return nil
	}
	id, err := strconv.ParseInt(strings.SplitN(c.Token, ":", 2)[0], 10, 64)
	if !tokenPattern.MatchString(c.Token) || err != nil || id <= 0 || id > 1<<52-1 || c.GroupID >= 0 || c.GroupID < -(1<<52-1) {
		return errors.New("invalid support Telegram configuration")
	}
	return nil
}

func LoadSupportConfig() (SupportConfig, error) {
	var c SupportConfig
	if value := os.Getenv("SUPPORT_TELEGRAM_ENABLED"); value != "" {
		var err error
		c.Enabled, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid SUPPORT_TELEGRAM_ENABLED")
		}
	}
	if !c.Enabled {
		return c, nil
	}
	var err error
	c.Token, err = loadTokenFile("SUPPORT_BOT_TOKEN")
	if err != nil {
		return SupportConfig{}, err
	}
	group := os.Getenv("SUPPORT_GROUP_ID")
	c.GroupID, err = strconv.ParseInt(group, 10, 64)
	if err != nil || strconv.FormatInt(c.GroupID, 10) != group {
		return SupportConfig{}, errors.New("invalid SUPPORT_GROUP_ID")
	}
	return c, c.validate()
}

func ValidatePollingBots(main Config, support SupportConfig) error {
	if err := main.validate(); err != nil {
		return err
	}
	if err := support.validate(); err != nil {
		return err
	}
	if main.Enabled && support.Enabled && strings.SplitN(main.Token, ":", 2)[0] == strings.SplitN(support.Token, ":", 2)[0] {
		return errors.New("main and support require distinct bot identities")
	}
	return nil
}
