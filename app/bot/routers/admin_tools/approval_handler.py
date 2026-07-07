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
from app.config import Config
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


def rejected_contact_keyboard(support_id: int) -> InlineKeyboardMarkup:
    # Импорт внутри функции — избегаем циклической зависимости на уровне модуля
    # (support роутер грузится позже admin_tools в routers/__init__.py).
    from app.bot.routers.support.keyboard import contact_button

    builder = InlineKeyboardBuilder()
    builder.row(contact_button(support_id))
    return builder.as_markup()


async def apply_approval_decision(
    session: AsyncSession,
    bot: Bot,
    i18n: I18n,
    config: Config,
    target: User,
    new_status: ApprovalStatus,
) -> bool:
    """Применяет решение по заявке (approve/reject) и уведомляет пользователя.

    Единый источник правды для обоих каналов: реактивных кнопок в уведомлении
    (`on_approval`) и экрана «Ожидающие» в разделе запросов на регистрацию.

    Инкапсулирует: идемпотентность (повторный тап по «протухшей» кнопке), сброс
    `approval_requested_at`, отмену Stars-подписки при reject и отправку юзеру
    granted/denied в ЕГО локали.

    Возвращает False, если статус уже был установлен (повторный тап — no-op),
    иначе True.
    """
    # Идемпотентность: повторный тап по «протухшей» кнопке (напоминание/исходное уведомление
    # у другого админа, оставшиеся после решения) не должен заново прогонять уже решённую
    # заявку и повторно слать юзеру granted/denied. Блокируем только повторное применение ТОГО
    # ЖЕ статуса — смену решения (approve↔reject) и повторную заявку (статус снова PENDING)
    # пропускаем.
    if target.approval_status == new_status:
        return False

    # M6: сбрасываем метку заявки, чтобы повторный запрос (rejected → снова) отправил новое уведомление
    await User.update(
        session,
        tg_id=target.tg_id,
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
    # Отказанному юзеру — стоп-лист: единственный оставшийся канал это связь с админом напрямую.
    user_markup = (
        rejected_contact_keyboard(config.bot.SUPPORT_ID)
        if new_status == ApprovalStatus.REJECTED
        else None
    )
    try:
        await bot.send_message(target.tg_id, user_text, reply_markup=user_markup)
    except TelegramForbiddenError:
        logger.info(f"User {target.tg_id} blocked the bot; approval notice skipped.")

    return True


@router.callback_query(ApprovalCallback.filter(), IsAdmin())
async def on_approval(
    callback: CallbackQuery,
    callback_data: ApprovalCallback,
    session: AsyncSession,
    bot: Bot,
    i18n: I18n,
    config: Config,
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

    applied = await apply_approval_decision(session, bot, i18n, config, target, new_status)
    if not applied:
        await callback.answer(_("approval:admin:already_processed"), show_alert=True)
        return

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
