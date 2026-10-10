package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"github.com/google/uuid"
)

type ClientPromocodeActions interface {
	ActivatePromocode(context.Context, uuid.UUID, uuid.UUID, bonuses.ActivatePromocodeInput) (bonuses.PromocodeActivation, error)
	GetPromocodeActivation(context.Context, uuid.UUID, uuid.UUID) (bonuses.PromocodeActivation, error)
}

type ClientPromocodeActorResolver interface {
	ResolveTelegramContext(context.Context, int64) (context.Context, *accounts.Snapshot, error)
}

type clientPromocodeBridge struct {
	authority ClientPromocodeActorResolver
	owner     ClientPromocodeActions
	api       *botapi.Client
	origin    string
	botID     string
}

func (r *Runtime) ConfigureClientPromocode(authority ClientPromocodeActorResolver, owner ClientPromocodeActions, origin string) error {
	u, err := url.Parse(origin)
	if r.api == nil || authority == nil || owner == nil || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("invalid Telegram client promocode configuration")
	}
	r.activation = &clientPromocodeBridge{authority: authority, owner: owner, api: r.api, origin: strings.TrimSuffix(origin, "/"), botID: strings.SplitN(r.token, ":", 2)[0]}
	return nil
}

type clientPromocodeCommand struct {
	action, value string
	valid         bool
}

func parseClientPromocodeCommand(text, username string) (clientPromocodeCommand, bool) {
	if !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') || len(text) > 4096 {
		return clientPromocodeCommand{}, false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return clientPromocodeCommand{}, false
	}
	name, mention, addressed := strings.Cut(fields[0], "@")
	if addressed && (username == "" || !strings.EqualFold(mention, username)) {
		return clientPromocodeCommand{}, false
	}
	cmd := clientPromocodeCommand{}
	switch name {
	case "/promocode":
		cmd.action = "activate"
		cmd.valid = len(fields) == 2 && len(fields[1]) <= 128
		if cmd.valid {
			cmd.value = fields[1]
		}
	case "/promocode_status":
		cmd.action = "status"
		if len(fields) == 2 {
			id, err := uuid.Parse(fields[1])
			cmd.valid = err == nil && id != uuid.Nil && id.String() == fields[1]
			if cmd.valid {
				cmd.value = fields[1]
			}
		}
	default:
		return clientPromocodeCommand{}, false
	}
	return cmd, true
}

func clientPromocodeButton(origin, lang string) *botapi.InlineKeyboard {
	return &botapi.InlineKeyboard{Rows: [][]botapi.Button{{{Text: clientText(lang, "Открыть кабинет", "Open cabinet"), URL: origin + "/cabinet?lang=" + lang}}}}
}

func (b *clientPromocodeBridge) send(ctx context.Context, chat int64, lang, ru, en string) error {
	_, err := b.api.SendMessage(ctx, chat, clientText(lang, ru, en), clientPromocodeButton(b.origin, lang))
	if safeCode(err) == "FORBIDDEN" || safeCode(err) == "BAD_REQUEST" {
		return nil
	}
	return err
}

func clientPromocodeStatus(status, lang string) string {
	switch status {
	case "pending":
		return clientText(lang, "ожидает", "pending")
	case "provisioning":
		return clientText(lang, "применяется", "provisioning")
	case "applied":
		return clientText(lang, "применён", "applied")
	case "needs_review":
		return clientText(lang, "требует проверки", "needs review")
	default:
		return clientText(lang, "неизвестен", "unknown")
	}
}

func (b *clientPromocodeBridge) handle(ctx context.Context, update botapi.Update, username string) (bool, error) {
	m := update.Message
	if m == nil {
		return false, nil
	}
	command, handled := parseClientPromocodeCommand(m.Text, username)
	if !handled {
		return false, nil
	}
	if !serverMessageActor(m) || update.ID < 0 {
		return true, nil
	}
	lang := clientLang(*m.From)
	if !command.valid {
		return true, b.send(ctx, m.Chat.ID, lang, "Отправьте /promocode КОД или откройте кабинет.", "Send /promocode CODE or open the cabinet.")
	}
	proof, actor, err := b.authority.ResolveTelegramContext(ctx, m.From.ID)
	if err != nil {
		return true, b.failure(ctx, m.Chat.ID, lang, err)
	}
	if actor == nil {
		return true, b.send(ctx, m.Chat.ID, lang, "Аккаунт Telegram не подключён. Войдите в кабинет.", "Telegram account is not linked. Sign in to the cabinet.")
	}
	if actor.Locale == "ru" || actor.Locale == "en" {
		lang = actor.Locale
	}
	var result bonuses.PromocodeActivation
	if command.action == "activate" {
		key := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("client-promocode:%s:%d:%d:%d", b.botID, update.ID, m.Chat.ID, m.ID)))
		result, err = b.owner.ActivatePromocode(proof, actor.ID, key, bonuses.ActivatePromocodeInput{Code: command.value})
	} else {
		id, _ := uuid.Parse(command.value)
		result, err = b.owner.GetPromocodeActivation(proof, actor.ID, id)
	}
	if err != nil {
		return true, b.failure(ctx, m.Chat.ID, lang, err)
	}
	return true, b.send(ctx, m.Chat.ID, lang,
		fmt.Sprintf("Промокод: %d дн. Статус: %s. Проверить: /promocode_status %s", result.DurationDays, clientPromocodeStatus(result.Status, "ru"), result.OperationID),
		fmt.Sprintf("Promocode: %d days. Status: %s. Check: /promocode_status %s", result.DurationDays, clientPromocodeStatus(result.Status, "en"), result.OperationID))
}

func (b *clientPromocodeBridge) failure(ctx context.Context, chat int64, lang string, err error) error {
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
	case "PROMOCODE_INVALID":
		return b.send(ctx, chat, lang, "Промокод не найден.", "Promocode not found.")
	case "PROMOCODE_USED":
		return b.send(ctx, chat, lang, "Промокод уже использован.", "Promocode has already been used.")
	case "IDEMPOTENCY_CONFLICT":
		return b.send(ctx, chat, lang, "Данные изменились. Проверьте статус в кабинете.", "Request changed. Check its status in the cabinet.")
	}
	return b.send(ctx, chat, lang, "Активация недоступна. Проверьте кабинет.", "Activation is unavailable. Check the cabinet.")
}

func (c *Client) sendPromocodeInstruction(ctx context.Context, chat int64, lang string) error {
	_, err := c.api.SendMessage(ctx, chat, clientText(lang,
		"Отправьте /promocode КОД или откройте форму в кабинете.",
		"Send /promocode CODE or open the form in your cabinet."), clientPromocodeButton(c.origin, lang))
	return cosmetic(err)
}
