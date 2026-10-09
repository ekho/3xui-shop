package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
)

type ServerActorResolver interface {
	ResolveTelegramContext(context.Context, int64) (context.Context, *accounts.Snapshot, error)
	RequireInfrastructure(context.Context, uuid.UUID) error
}

type ServerActions interface {
	ListManagedServers(context.Context, uuid.UUID) ([]vpn.Server, error)
	GetManagedServer(context.Context, uuid.UUID, string) (vpn.Server, error)
	CreateManagedServer(context.Context, uuid.UUID, uuid.UUID, vpn.ServerInput, string) (vpn.Server, bool, error)
	PingManagedServer(context.Context, uuid.UUID, string, uuid.UUID, string) (vpn.Server, error)
	SyncManagedServers(context.Context, uuid.UUID, uuid.UUID, string) ([]vpn.Server, error)
	DeleteManagedServer(context.Context, uuid.UUID, string, uuid.UUID, string) (vpn.Server, error)
}

type serverBridge struct {
	authority ServerActorResolver
	owner     ServerActions
	origin    string
	api       *botapi.Client
	botID     string
}

func (r *Runtime) ConfigureServerManagement(authority ServerActorResolver, owner ServerActions, origin string) error {
	u, err := url.Parse(origin)
	if r.api == nil || authority == nil || owner == nil || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("invalid Telegram server management configuration")
	}
	r.servers = &serverBridge{authority: authority, owner: owner, origin: strings.TrimSuffix(origin, "/"), api: r.api, botID: strings.SplitN(r.token, ":", 2)[0]}
	return nil
}

func serverMessageActor(m *botapi.Message) bool {
	if m == nil || !clientActor(m.From, m) {
		return false
	}
	if len(m.Raw) == 0 {
		return true
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(m.Raw, &raw) != nil {
		return false
	}
	for _, field := range []string{"forward_origin", "forward_from", "forward_from_chat", "forward_date", "forward_sender_name", "sender_chat", "via_bot", "is_automatic_forward", "sender_business_bot", "business_connection_id"} {
		if present(raw[field]) {
			return false
		}
	}
	return true
}

func (b *serverBridge) handle(ctx context.Context, update botapi.Update, username string) (bool, error) {
	m := update.Message
	if m == nil {
		return false, nil
	}
	command, handled := parseServerCommand(m.Text, username)
	if !handled {
		return false, nil
	}
	if !serverMessageActor(m) || update.ID < 0 {
		return true, nil
	}
	proofCtx, actor, err := b.authority.ResolveTelegramContext(ctx, m.From.ID)
	if err != nil {
		return true, b.serverFailure(ctx, m.Chat.ID, "ru", err)
	}
	if actor == nil {
		return true, b.send(ctx, m.Chat.ID, "Действие недоступно.", "Action unavailable.", "ru", nil)
	}
	lang := "en"
	if actor.Locale == "ru" {
		lang = "ru"
	}
	if err = b.authority.RequireInfrastructure(proofCtx, actor.ID); err != nil {
		return true, b.serverFailure(ctx, m.Chat.ID, lang, err)
	}
	if !command.valid {
		if command.action == "delete" && command.id != "" {
			return true, b.send(ctx, m.Chat.ID, "Для удаления отправьте /server_delete ID confirm.", "To delete, send /server_delete ID confirm.", lang, nil)
		}
		return true, b.send(ctx, m.Chat.ID, "Неверная команда. Используйте /servers для списка действий.", "Invalid command. Use /servers for available actions.", lang, nil)
	}
	key := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("server-management:%s:%d:%d:%d", b.botID, update.ID, m.Chat.ID, m.ID)))
	var rows []vpn.Server
	var server vpn.Server
	switch command.action {
	case "list":
		rows, err = b.owner.ListManagedServers(proofCtx, actor.ID)
	case "card":
		server, err = b.owner.GetManagedServer(proofCtx, actor.ID, command.id)
	case "add":
		server, _, err = b.owner.CreateManagedServer(proofCtx, actor.ID, key, vpn.ServerInput{Name: command.name, Host: command.host, MaxClients: command.maxClients}, "telegram")
	case "ping":
		server, err = b.owner.PingManagedServer(proofCtx, actor.ID, command.id, key, "telegram")
	case "sync":
		rows, err = b.owner.SyncManagedServers(proofCtx, actor.ID, key, "telegram")
	case "delete":
		server, err = b.owner.DeleteManagedServer(proofCtx, actor.ID, command.id, key, "telegram")
	}
	if err != nil {
		return true, b.serverFailure(ctx, m.Chat.ID, lang, err)
	}
	if command.action == "delete" {
		return true, b.send(ctx, m.Chat.ID, "Сервер удалён: "+html.EscapeString(shortServerText(server.ID, 128)), "Server deleted: "+html.EscapeString(shortServerText(server.ID, 128)), lang, nil)
	}
	if command.action == "list" || command.action == "sync" {
		text := serverList(rows, lang)
		return true, b.send(ctx, m.Chat.ID, text, text, lang, b.cabinetButton(lang))
	}
	text := serverCard(server, lang)
	return true, b.send(ctx, m.Chat.ID, text, text, lang, b.cabinetButton(lang))
}

func (b *serverBridge) send(ctx context.Context, chat int64, ru, en, lang string, keyboard *botapi.InlineKeyboard) error {
	_, err := b.api.SendMessage(ctx, chat, clientText(lang, ru, en), keyboard)
	return cosmetic(err)
}

func (b *serverBridge) serverFailure(ctx context.Context, chat int64, lang string, err error) error {
	status := 0
	var account *accounts.Error
	var server *vpn.Error
	if errors.As(err, &account) {
		status = account.Status
	}
	if errors.As(err, &server) {
		status = server.Status
	}
	if status == 0 || status >= 500 {
		return err
	}
	switch status {
	case 404:
		return b.send(ctx, chat, "Сервер не найден.", "Server not found.", lang, nil)
	case 409:
		return b.send(ctx, chat, "Действие конфликтует с состоянием сервера. Проверьте его карточку.", "Action conflicts with the server state. Check its card.", lang, nil)
	case 400:
		return b.send(ctx, chat, "Проверьте параметры команды.", "Check the command parameters.", lang, nil)
	default:
		return b.send(ctx, chat, "Действие недоступно.", "Action unavailable.", lang, nil)
	}
}

func (b *serverBridge) cabinetButton(lang string) *botapi.InlineKeyboard {
	return &botapi.InlineKeyboard{Rows: [][]botapi.Button{{{Text: clientText(lang, "Открыть кабинет", "Open cabinet"), URL: b.origin + "/admin/servers?lang=" + lang}}}}
}

func shortServerText(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

func serverCard(s vpn.Server, lang string) string {
	limit := "—"
	if s.MaxClients != nil {
		limit = strconv.FormatInt(*s.MaxClients, 10)
	}
	state := clientText(lang, "неизвестно", "unknown")
	if s.ObservedAt != nil {
		state = clientText(lang, "офлайн", "offline")
		if s.Online {
			state = clientText(lang, "онлайн", "online")
		}
		state += " (" + s.ObservedAt.UTC().Format("2006-01-02 15:04 UTC") + ")"
	}
	id := html.EscapeString(shortServerText(s.ID, 128))
	return fmt.Sprintf("<b>%s</b>\nID: <code>%s</code>\n%s: <code>%s</code>\n%s: %s\n%s: %s\n%s: %d / %d\n/server_ping %s\n/server_delete %s confirm\n/servers_sync",
		html.EscapeString(shortServerText(s.Name, 100)), id, clientText(lang, "Хост", "Host"), html.EscapeString(shortServerText(s.Host, 200)), clientText(lang, "Лимит", "Limit"), limit, clientText(lang, "Статус", "Status"), state, clientText(lang, "Назначено / резерв", "Assigned / reserved"), s.AssignedClients, s.ReservedClients, id, id)
}

func serverList(rows []vpn.Server, lang string) string {
	text := clientText(lang, "<b>Серверы</b>", "<b>Servers</b>")
	if len(rows) == 0 {
		text += clientText(lang, "\nПока нет серверов.", "\nNo servers yet.")
	}
	for i, row := range rows {
		card := serverCard(row, lang)
		if i == 8 || len(text)+len(card) > 3500 {
			text += clientText(lang, "\n… Остальные серверы откройте в кабинете.", "\n… Open the cabinet for more servers.")
			break
		}
		text += "\n\n" + card
	}
	text += "\n\n/server ID · /server_add name | https://host | max_clients · /server_ping ID · /servers_sync · /server_delete ID confirm"
	return text
}
