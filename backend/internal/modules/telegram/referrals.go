package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"github.com/jackc/pgx/v5"
)

func (c *Client) ConfigureReferrals(owner *bonuses.Service) { c.bonuses = owner }

func (c *Client) sendReferrals(ctx context.Context, chat int64, lang string) error {
	if c.bonuses == nil {
		return c.send(ctx, chat, lang, "/referrals", "")
	}
	ctx, actor, err := c.accounts.ResolveTelegramContext(ctx, chat)
	var denied *accounts.Error
	if errors.As(err, &denied) && (denied.Status == 400 || denied.Status == 403) || err == nil && actor == nil {
		return c.send(ctx, chat, lang, "/referrals", "")
	}
	if err != nil {
		return err
	}
	out, err := c.bonuses.ReadReferrals(ctx, actor.ID, c.origin)
	if err != nil {
		var domain *bonuses.Error
		if errors.As(err, &domain) {
			text := clientText(lang, "Приглашения сейчас недоступны. Откройте кабинет для проверки статуса.", "Invitations are unavailable. Open the cabinet to check your status.")
			_, err = c.api.SendMessage(ctx, chat, text, nil)
			return cosmetic(err)
		}
		return err
	}
	text := clientText(lang, "Приглашения\nВаша ссылка: ", "Invitations\nYour link: ") + html.EscapeString(out.WebURL)
	for _, level := range out.Levels {
		format := clientText(lang, "\n\nСтепень %d: приглашено %d\nВыдано дней: %s (%d записей)\nОжидает дней: %s (%d записей)\nРасчётные MONEY-записи: %d, ожидают: %d", "\n\nLevel %d: invited %d\nGranted days: %s (%d records)\nPending days: %s (%d records)\nCalculated MONEY records: %d, pending: %d")
		text += fmt.Sprintf(format, level.Level, level.Invited, level.GrantedDays, level.GrantedRewards, level.PendingDays, level.PendingRewards, level.MoneyRecords, level.PendingMoneyRecords)
	}
	text += clientText(lang, "\n\nMONEY-записи не являются балансом или выплатой.", "\n\nMONEY records do not represent a balance or payout.")
	if out.UnclassifiedRecords > 0 {
		text += fmt.Sprintf(clientText(lang, "\nЗаписи без известной степени: %d", "\nRecords with an unknown level: %d"), out.UnclassifiedRecords)
	}
	keyboard := &botapi.InlineKeyboard{Rows: [][]botapi.Button{{{Text: clientText(lang, "Открыть приглашения", "Open invitations"), WebApp: &botapi.WebAppInfo{URL: c.route("/referrals", lang)}}}}}
	_, err = c.accounts.WithTelegramDelivery(ctx, actor.ID, chat, actor.CredentialVersion, func(pgx.Tx) error {
		_, sendErr := c.api.SendMessage(ctx, chat, text, keyboard)
		return cosmetic(sendErr)
	})
	return err
}
