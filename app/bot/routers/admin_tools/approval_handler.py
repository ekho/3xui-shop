import logging

from aiogram import Bot, Router
from aiogram.exceptions import TelegramBadRequest, TelegramForbiddenError
from aiogram.filters.callback_data import CallbackData
from aiogram.types import CallbackQuery, InlineKeyboardMarkup
from aiogram.utils.i18n import I18n
from aiogram.utils.i18n import gettext as _
from aiogram.utils.keyboard import InlineKeyboardBuilder
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.filters import IsAdmin
from app.bot.utils.constants import DEFAULT_LANGUAGE, ApprovalStatus
from app.db.models import User

logger = logging.getLogger(__name__)
router = Router(name=__name__)


class ApprovalCallback(CallbackData, prefix="approval"):
    action: str  # "approve" | "reject"
    user_id: int


def approval_keyboard(user_id: int) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    builder.button(
        text=_("approval:button:approve"),
        callback_data=ApprovalCallback(action="approve", user_id=user_id),
    )
    builder.button(
        text=_("approval:button:reject"),
        callback_data=ApprovalCallback(action="reject", user_id=user_id),
    )
    return builder.as_markup()


@router.callback_query(ApprovalCallback.filter(), IsAdmin())
async def on_approval(
    callback: CallbackQuery,
    callback_data: ApprovalCallback,
    session: AsyncSession,
    bot: Bot,
    i18n: I18n,
) -> None:
    new_status = (
        ApprovalStatus.APPROVED
        if callback_data.action == "approve"
        else ApprovalStatus.REJECTED
    )
    target = await User.get(session, tg_id=callback_data.user_id)
    if target is None:
        await callback.answer(_("approval:admin:user_not_found"), show_alert=True)
        return

    # M6: сбрасываем метку заявки, чтобы повторный запрос (rejected → снова) отправил новое уведомление
    await User.update(
        session,
        tg_id=callback_data.user_id,
        approval_status=new_status,
        approval_requested_at=None,
    )

    # B1: reject при активном Stars-рекурренте обязан отменить подписку, иначе Telegram
    #     продолжит списывать звёзды. Поля появляются на Этапе 5 — getattr для forward-совместимости.
    if new_status == ApprovalStatus.REJECTED and getattr(target, "is_stars_auto_renew", False):
        charge_id = getattr(target, "stars_charge_id", None)
        if charge_id:
            try:
                await bot.edit_user_star_subscription(
                    user_id=target.tg_id, telegram_payment_charge_id=charge_id, is_canceled=True
                )
            except TelegramBadRequest as exception:
                logger.warning(f"Cancel stars sub on reject for {target.tg_id}: {exception}")
            await User.update(session, tg_id=target.tg_id, is_stars_auto_renew=False)

    # M5: текст юзеру — в ЕГО локали (SimpleI18nMiddleware ставит локаль админа-инициатора)
    locale = (target.language_code if target.language_code else None) or DEFAULT_LANGUAGE
    with i18n.use_locale(locale):
        user_text = (
            _("approval:user:granted")
            if new_status == ApprovalStatus.APPROVED
            else _("approval:user:denied")
        )
    try:
        await bot.send_message(callback_data.user_id, user_text)
    except TelegramForbiddenError:
        logger.info(f"User {callback_data.user_id} blocked the bot; approval notice skipped.")

    # сообщение админу — в локали текущего апдейта (админа), это корректно
    try:
        await callback.message.edit_text(
            callback.message.text
            + "\n\n"
            + _("approval:admin:done").format(status=new_status.value)
        )
    except TelegramBadRequest:
        pass  # текст не изменился / сообщение недоступно
    await callback.answer()
