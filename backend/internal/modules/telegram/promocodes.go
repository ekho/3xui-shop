package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"github.com/google/uuid"
)

type PromocodeActorResolver interface {
	ResolveTelegramContext(context.Context, int64) (context.Context, *accounts.Snapshot, error)
	RequireOperator(context.Context, uuid.UUID) error
}

type PromocodeActions interface {
	ListPromocodes(context.Context, uuid.UUID, int, int) (bonuses.PromocodeList, error)
	GetPromocode(context.Context, uuid.UUID, uuid.UUID) (bonuses.PromocodeDetail, error)
	CreatePromocode(context.Context, uuid.UUID, uuid.UUID, bonuses.CreatePromocodeInput) (bonuses.Promocode, error)
	EditPromocode(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, bonuses.EditPromocodeInput) (bonuses.Promocode, error)
	DeletePromocode(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, bonuses.DeletePromocodeInput) (bonuses.Promocode, error)
}

type promocodeBridge struct {
	authority PromocodeActorResolver
	owner     PromocodeActions
	origin    string
	api       *botapi.Client
	botID     string
}

func (r *Runtime) ConfigurePromocodes(authority PromocodeActorResolver, owner PromocodeActions, origin string) error {
	u, err := url.Parse(origin)
	if r.api == nil || authority == nil || owner == nil || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("invalid Telegram promocode configuration")
	}
	r.promocodes = &promocodeBridge{authority: authority, owner: owner, origin: strings.TrimSuffix(origin, "/"), api: r.api, botID: strings.SplitN(r.token, ":", 2)[0]}
	return nil
}

type promocodeCommand struct {
	action, reason string
	id             uuid.UUID
	revision       int64
	days           int
	valid          bool
}

func parsePromocodeCommand(text, username string) (promocodeCommand, bool) {
	if !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') || len(text) > 4096 {
		return promocodeCommand{}, false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return promocodeCommand{}, false
	}
	name, mention, addressed := strings.Cut(strings.TrimPrefix(fields[0], "/"), "@")
	if addressed && (username == "" || !strings.EqualFold(mention, username)) {
		return promocodeCommand{}, false
	}
	cmd := promocodeCommand{}
	switch name {
	case "promocodes":
		cmd.action, cmd.valid = "list", len(fields) == 1
	case "promo":
		cmd.action = "card"
		if len(fields) == 2 {
			cmd.id, cmd.valid = parsePromocodeID(fields[1])
		}
	case "promo_create":
		cmd.action = "create"
		if len(fields) >= 3 {
			cmd.days, cmd.valid = parsePromocodeDays(fields[1])
			cmd.reason = strings.Join(fields[2:], " ")
			cmd.valid = cmd.valid && cmd.reason != ""
		}
	case "promo_edit":
		cmd.action = "edit"
		if len(fields) >= 5 {
			cmd.id, cmd.valid = parsePromocodeID(fields[1])
			revision, err := strconv.ParseInt(fields[2], 10, 64)
			cmd.revision = revision
			cmd.valid = cmd.valid && err == nil && revision > 0
			days, valid := parsePromocodeDays(fields[3])
			cmd.days, cmd.valid = days, cmd.valid && valid
			cmd.reason = strings.Join(fields[4:], " ")
			cmd.valid = cmd.valid && cmd.reason != ""
		}
	case "promo_delete":
		cmd.action = "delete"
		if len(fields) >= 5 && fields[3] == "confirm" {
			cmd.id, cmd.valid = parsePromocodeID(fields[1])
			revision, err := strconv.ParseInt(fields[2], 10, 64)
			cmd.revision = revision
			cmd.valid = cmd.valid && err == nil && revision > 0
			cmd.reason = strings.Join(fields[4:], " ")
			cmd.valid = cmd.valid && cmd.reason != ""
		}
	default:
		return promocodeCommand{}, false
	}
	return cmd, true
}

func parsePromocodeID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(s)
	return id, err == nil && id != uuid.Nil
}

func parsePromocodeDays(s string) (int, bool) {
	days, err := strconv.Atoi(s)
	return days, err == nil && days >= 1 && days <= 365
}

func (b *promocodeBridge) handle(ctx context.Context, update botapi.Update, username string) (bool, error) {
	m := update.Message
	if m == nil {
		return false, nil
	}
	command, handled := parsePromocodeCommand(m.Text, username)
	if !handled {
		return false, nil
	}
	if !serverMessageActor(m) || update.ID < 0 {
		return true, nil
	}
	proof, actor, err := b.authority.ResolveTelegramContext(ctx, m.From.ID)
	if err != nil {
		return true, b.failure(ctx, m.Chat.ID, "ru", err)
	}
	if actor == nil {
		return true, b.send(ctx, m.Chat.ID, "Действие недоступно.", "Action unavailable.", "ru", nil)
	}
	lang := "en"
	if actor.Locale == "ru" {
		lang = "ru"
	}
	if err = b.authority.RequireOperator(proof, actor.ID); err != nil {
		return true, b.failure(ctx, m.Chat.ID, lang, err)
	}
	if !command.valid {
		return true, b.send(ctx, m.Chat.ID, "Неверная команда. Используйте /promocodes для списка действий.", "Invalid command. Use /promocodes for available actions.", lang, nil)
	}
	key := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("promocodes:%s:%d:%d:%d", b.botID, update.ID, m.Chat.ID, m.ID)))
	var item bonuses.Promocode
	var list bonuses.PromocodeList
	var detail bonuses.PromocodeDetail
	switch command.action {
	case "list":
		list, err = b.owner.ListPromocodes(proof, actor.ID, 1, 10)
	case "card":
		detail, err = b.owner.GetPromocode(proof, actor.ID, command.id)
	case "create":
		item, err = b.owner.CreatePromocode(proof, actor.ID, key, bonuses.CreatePromocodeInput{DurationDays: command.days, Reason: command.reason})
	case "edit":
		item, err = b.owner.EditPromocode(proof, actor.ID, command.id, key, bonuses.EditPromocodeInput{DurationDays: command.days, ExpectedRevision: command.revision, Reason: command.reason})
	case "delete":
		item, err = b.owner.DeletePromocode(proof, actor.ID, command.id, key, bonuses.DeletePromocodeInput{ExpectedRevision: command.revision, Reason: command.reason})
	}
	if err != nil {
		return true, b.failure(ctx, m.Chat.ID, lang, err)
	}
	switch command.action {
	case "list":
		return true, b.send(ctx, m.Chat.ID, promocodeListText(list, lang), "", lang, b.cabinetButton(lang))
	case "card":
		return true, b.send(ctx, m.Chat.ID, promocodeDetailText(detail, lang), "", lang, b.cabinetButton(lang))
	default:
		return true, b.send(ctx, m.Chat.ID, promocodeCardText(item, lang), "", lang, b.cabinetButton(lang))
	}
}

func (b *promocodeBridge) send(ctx context.Context, chat int64, ru, en, lang string, keyboard *botapi.InlineKeyboard) error {
	message := ru
	if en != "" {
		message = clientText(lang, ru, en)
	}
	_, err := b.api.SendMessage(ctx, chat, message, keyboard)
	return cosmetic(err)
}

func (b *promocodeBridge) failure(ctx context.Context, chat int64, lang string, err error) error {
	status, code := 0, ""
	var account *accounts.Error
	var bonus *bonuses.Error
	if errors.As(err, &account) {
		status, code = account.Status, account.Code
	}
	if errors.As(err, &bonus) {
		status, code = bonus.Status, bonus.Code
	}
	if status == 0 || status >= 500 {
		return err
	}
	switch code {
	case "PROMOCODE_USED":
		return b.send(ctx, chat, "Код уже использован. Обновите карточку.", "This code has been used. Refresh its card.", lang, nil)
	case "PROMOCODE_REVISION_CONFLICT", "IDEMPOTENCY_CONFLICT":
		return b.send(ctx, chat, "Данные изменились. Обновите карточку перед новой командой.", "The record changed. Refresh its card before another command.", lang, nil)
	}
	switch status {
	case 404:
		return b.send(ctx, chat, "Промокод не найден.", "Promocode not found.", lang, nil)
	case 400:
		return b.send(ctx, chat, "Проверьте параметры команды.", "Check the command parameters.", lang, nil)
	default:
		return b.send(ctx, chat, "Действие недоступно.", "Action unavailable.", lang, nil)
	}
}

func (b *promocodeBridge) cabinetButton(lang string) *botapi.InlineKeyboard {
	return &botapi.InlineKeyboard{Rows: [][]botapi.Button{{{Text: clientText(lang, "Промокоды в кабинете", "Promocodes in cabinet"), URL: b.origin + "/admin/promocodes?lang=" + lang}}}}
}

func promoSafe(s string, max int) string {
	return html.EscapeString(shortServerText(s, max))
}

func promocodeCardText(p bonuses.Promocode, lang string) string {
	state := map[string]string{"available": clientText(lang, "доступен", "available"), "activated": clientText(lang, "использован", "used"), "deleted": clientText(lang, "удалён", "deleted")}[p.State]
	if state == "" {
		state = clientText(lang, "неизвестно", "unknown")
	}
	return fmt.Sprintf("<b>%s</b>\nID: <code>%s</code>\n%s: <code>%s</code>\n%s: %d\n%s: %s\nRevision: %d\n/promo %s",
		clientText(lang, "Промокод", "Promocode"), p.PromocodeID, clientText(lang, "Код", "Code"), promoSafe(p.Code, 100),
		clientText(lang, "Дней", "Days"), p.DurationDays, clientText(lang, "Статус", "State"), state, p.Revision, p.PromocodeID)
}

func promocodeListText(list bonuses.PromocodeList, lang string) string {
	text := clientText(lang, "<b>Промокоды</b>", "<b>Promocodes</b>")
	if len(list.Promocodes) == 0 {
		text += clientText(lang, "\nПока нет промокодов.", "\nNo promocodes yet.")
	}
	for i, p := range list.Promocodes {
		card := promocodeCardText(p, lang)
		if i == 10 || len(text)+len(card) > 3500 {
			text += clientText(lang, "\nОстальные откройте в кабинете.", "\nOpen the cabinet for the rest.")
			break
		}
		text += "\n\n" + card
	}
	text += "\n\n/promo ID · /promo_create DAYS REASON · /promo_edit ID REV DAYS REASON · /promo_delete ID REV confirm REASON"
	return text
}

func promocodeDetailText(d bonuses.PromocodeDetail, lang string) string {
	text := promocodeCardText(d.Promocode, lang) + clientText(lang, "\n\n<b>История</b>", "\n\n<b>History</b>")
	if len(d.Events) == 0 {
		text += clientText(lang, "\nПока нет событий.", "\nNo events yet.")
	}
	truncated := false
	for i, event := range d.Events {
		line := "\n" + promoSafe(event.Action, 40)
		if event.Reason != nil {
			line += " · " + promoSafe(*event.Reason, 160)
		}
		if i == 8 || len(text)+len(line) > 3500 {
			truncated = true
			break
		}
		text += line
	}
	if truncated || d.EventsHasMore {
		text += clientText(lang, "\nОстальная история в кабинете.", "\nMore history in the cabinet.")
	}
	return text
}
