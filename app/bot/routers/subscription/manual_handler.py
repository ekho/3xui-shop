import logging

from aiogram import Bot, F, Router
from aiogram.exceptions import TelegramBadRequest
from aiogram.filters.callback_data import CallbackData
from aiogram.types import CallbackQuery, InlineKeyboardButton, InlineKeyboardMarkup
from aiogram.utils.i18n import I18n
from aiogram.utils.i18n import gettext as _
from aiogram.utils.keyboard import InlineKeyboardBuilder
from redis.asyncio import Redis
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.filters import IsAdmin
from app.bot.models import ServicesContainer, SubscriptionData
from app.bot.payment_gateways import GatewayFactory
from app.bot.routers.misc.keyboard import back_to_main_menu_button
from app.bot.utils.constants import DEFAULT_LANGUAGE, Currency, TransactionStatus
from app.bot.utils.formatting import format_device_count, format_subscription_period
from app.bot.utils.navigation import NavSubscription
from app.config import Config
from app.db.models import Transaction, User

logger = logging.getLogger(__name__)
router = Router(name=__name__)

MANUAL_PAID_THROTTLE = 600  # сек (M12): одна заявка = одно уведомление админам


class ManualPaidCallback(CallbackData, prefix="manpaid"):
    payment_id: str


class ManualModerationCallback(CallbackData, prefix="manmod"):
    action: str  # "approve" | "reject"
    payment_id: str


def manual_paid_kb(payment_id: str) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    builder.row(InlineKeyboardButton(
        text=_("payment:manual:button:paid"),
        callback_data=ManualPaidCallback(payment_id=payment_id).pack(),
    ))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def manual_moderation_kb(payment_id: str) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    builder.row(
        InlineKeyboardButton(
            text=_("payment:manual:button:approve"),
            callback_data=ManualModerationCallback(action="approve", payment_id=payment_id).pack(),
        ),
        InlineKeyboardButton(
            text=_("payment:manual:button:reject"),
            callback_data=ManualModerationCallback(action="reject", payment_id=payment_id).pack(),
        ),
    )
    return builder.as_markup()


# G3/M3: ловим PAY_MANUAL здесь (в универсальном payment-хендлере он ИСКЛЮЧён из фильтра).
@router.callback_query(SubscriptionData.filter(F.state == NavSubscription.PAY_MANUAL))
async def manual_payment(
    callback: CallbackQuery,
    user: User,
    callback_data: SubscriptionData,
    services: ServicesContainer,
    gateway_factory: GatewayFactory,
    config: Config,
    bot: Bot,
) -> None:
    # M9: активный Stars-рекуррент + ручная оплата → двойной биллинг (Telegram спишет звёзды сам).
    if getattr(user, "is_stars_auto_renew", False):
        await callback.answer(_("payment:manual:cancel_autorenew_first"), show_alert=True)
        return

    plan = services.plan.get_plan(callback_data.devices)
    callback_data.price = plan.get_price(currency=Currency.RUB, duration=callback_data.duration)
    callback_data.traffic = plan.traffic_gb  # G2
    gateway = gateway_factory.get_gateway(NavSubscription.PAY_MANUAL)
    payment_id = await gateway.create_payment(callback_data)  # создаёт/переиспользует PENDING (M12)

    await callback.message.edit_text(
        _("payment:manual:instructions").format(
            price=callback_data.price,
            currency=Currency.RUB.symbol,
            details=config.shop.MANUAL_CARD_DETAILS,
        ),
        reply_markup=manual_paid_kb(payment_id),
    )


@router.callback_query(ManualPaidCallback.filter())
async def manual_paid(
    callback: CallbackQuery,
    callback_data: ManualPaidCallback,
    bot: Bot,
    config: Config,
    session: AsyncSession,
    services: ServicesContainer,
    redis: Redis,
) -> None:
    # M12: троттлинг — одна заявка = максимум одно уведомление админам.
    throttle_key = f"manual:paid:{callback.from_user.id}:{callback_data.payment_id}"
    if await redis.get(throttle_key):
        await callback.answer(_("payment:manual:already_sent"))
        return
    await redis.set(throttle_key, "1", ex=MANUAL_PAID_THROTTLE)

    # M-no-amount: обогащаем заявку админу суммой/тарифом из транзакции (в callback только payment_id).
    txn = await Transaction.get_by_id(session, payment_id=callback_data.payment_id)
    if txn is None:
        await callback.answer(_("payment:manual:already_processed"))
        return
    data = SubscriptionData.unpack(txn.subscription)
    plan = services.plan.get_plan(data.devices)
    admin_text = _("payment:manual:admin_request").format(
        user_id=callback.from_user.id,
        username=callback.from_user.username or "-",
        payment_id=callback_data.payment_id,
        price=data.price,
        currency=Currency.RUB.symbol,
        devices=format_device_count(data.devices),
        duration=format_subscription_period(data.duration),
        traffic=(plan.traffic_gb if plan else 0),
    )
    admin_ids = set(config.bot.ADMINS) | {config.bot.DEV_ID}
    for admin_id in admin_ids:
        try:
            await bot.send_message(
                admin_id, admin_text, reply_markup=manual_moderation_kb(callback_data.payment_id)
            )
        except Exception as exception:
            logger.warning(f"Failed to notify admin {admin_id}: {exception}")
    await callback.message.edit_text(_("payment:manual:awaiting_admin"))


@router.callback_query(ManualModerationCallback.filter(), IsAdmin())
async def manual_moderation(
    callback: CallbackQuery,
    callback_data: ManualModerationCallback,
    session: AsyncSession,
    gateway_factory: GatewayFactory,
    services: ServicesContainer,
    bot: Bot,
    i18n: I18n,
) -> None:
    pid = callback_data.payment_id
    # B3: read-check для UX. Реальную атомарность (Approve/Approve, Reject->Approve, двойной Reject)
    # обеспечивает CAS PENDING->терминальный статус ВНУТРИ _on_payment_succeeded/_canceled шлюза (B2):
    # проигравший гонку вызов просто пропустит провижининг/отмену.
    txn = await Transaction.get_by_id(session, payment_id=pid)
    if txn is None or txn.status != TransactionStatus.PENDING:
        await callback.answer(_("payment:manual:already_processed"))
        return

    gateway = gateway_factory.get_gateway(NavSubscription.PAY_MANUAL)
    if callback_data.action == "approve":
        await gateway.handle_payment_succeeded(pid)  # CAS + провижининг + уведомление юзеру
    else:
        await gateway.handle_payment_canceled(pid)  # CAS PENDING->CANCELED
        # M5: уведомить юзера об отказе в ЕГО локали (notify_by_id глотает TelegramForbiddenError)
        target = await User.get(session, tg_id=txn.tg_id)
        locale = (target.language_code if target else None) or DEFAULT_LANGUAGE
        with i18n.use_locale(locale):
            await services.notification.notify_by_id(chat_id=txn.tg_id, text=_("payment:manual:rejected"))

    try:
        await callback.message.edit_text(
            callback.message.text + "\n\n" + _("payment:manual:done").format(action=callback_data.action)
        )
    except TelegramBadRequest:
        pass
    await callback.answer()
