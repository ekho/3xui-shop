import logging

from aiogram import Bot, F, Router
from aiogram.fsm.context import FSMContext
from aiogram.fsm.state import State, StatesGroup
from aiogram.types import CallbackQuery, Message, PreCheckoutQuery
from aiogram.utils.i18n import gettext as _
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.filters import IsAdmin
from app.bot.filters.is_dev import IsDev
from app.bot.models import ServicesContainer, SubscriptionData
from app.bot.payment_gateways import GatewayFactory
from app.bot.utils.constants import ApprovalStatus, TransactionStatus
from app.bot.utils.formatting import format_subscription_period
from app.bot.utils.navigation import NavSubscription
from app.config import Config
from app.db.models import Transaction, User

from .keyboard import pay_keyboard

logger = logging.getLogger(__name__)
router = Router(name=__name__)


class PaymentState(StatesGroup):
    processing = State()


@router.callback_query(
    SubscriptionData.filter(
        F.state.startswith(NavSubscription.PAY) & (F.state != NavSubscription.PAY_MANUAL)
    )
)  # M3: PAY_MANUAL уходит в manual_handler (у ручной оплаты нет URL для pay_keyboard)
async def callback_payment_method_selected(
    callback: CallbackQuery,
    user: User,
    callback_data: SubscriptionData,
    services: ServicesContainer,
    bot: Bot,
    gateway_factory: GatewayFactory,
    state: FSMContext,
) -> None:
    if await state.get_state() == PaymentState.processing:
        logger.debug(f"User {user.tg_id} is already processing payment.")
        return

    await state.set_state(PaymentState.processing)

    try:
        method = callback_data.state
        devices = callback_data.devices
        duration = callback_data.duration
        logger.info(f"User {user.tg_id} selected payment method: {method}")
        logger.info(f"User {user.tg_id} selected {devices} devices and {duration} days.")
        gateway = gateway_factory.get_gateway(method)
        plan = services.plan.get_plan(devices)
        price = plan.get_price(currency=gateway.currency, duration=duration)
        callback_data.price = price
        callback_data.traffic = plan.traffic_gb  # G2: лимит трафика тарифа → в payload транзакции

        pay_url = await gateway.create_payment(callback_data)

        if callback_data.is_extend:
            text = _("payment:message:order_extend")
        elif callback_data.is_change:
            text = _("payment:message:order_change")
        else:
            text = _("payment:message:order")

        await callback.message.edit_text(
            text=text.format(
                devices=devices,
                duration=format_subscription_period(duration),
                price=price,
                currency=gateway.currency.symbol,
            ),
            reply_markup=pay_keyboard(pay_url=pay_url, callback_data=callback_data),
        )
    except Exception as exception:
        logger.error(f"Error processing payment: {exception}")
        await services.notification.show_popup(callback=callback, text=_("payment:popup:error"))
    finally:
        await state.set_state(None)


@router.pre_checkout_query()
async def pre_checkout_handler(pre_checkout_query: PreCheckoutQuery, user: User) -> None:
    logger.info(f"Pre-checkout query received from user {user.tg_id}")
    if pre_checkout_query.invoice_payload:
        await pre_checkout_query.answer(ok=True)
    else:
        await pre_checkout_query.answer(ok=False)


@router.message(F.successful_payment)
async def successful_payment(
    message: Message,
    user: User,
    session: AsyncSession,
    bot: Bot,
    config: Config,
    services: ServicesContainer,
    gateway_factory: GatewayFactory,
) -> None:
    sp = message.successful_payment
    charge_id = sp.telegram_payment_charge_id
    data = SubscriptionData.unpack(sp.invoice_payload)

    # B1: рекуррентное списание Stars приходит АВТОМАТИЧЕСКИ даже у не-approved юзера
    # (например, апрув сняли после покупки) — деньги списаны, услуги нет. Рефанд (для рекуррента
    # он же отменяет подписку) + аудит-транзакция + снять флаг автопродления.
    if (
        config.shop.APPROVAL_REQUIRED
        and user.approval_status != ApprovalStatus.APPROVED
        and not await IsAdmin()(user_id=user.tg_id)
    ):
        txn = await Transaction.create(
            session=session, tg_id=user.tg_id, subscription=data.pack(),
            payment_id=charge_id, status=TransactionStatus.CANCELED,
        )
        if txn is None:  # дубликат — уже обработали, повторно не рефандим
            return
        try:
            await bot.refund_star_payment(user_id=user.tg_id, telegram_payment_charge_id=charge_id)
        except Exception as exception:
            logger.error(f"Refund failed for non-approved {user.tg_id}: {exception}")
        await User.update(session, tg_id=user.tg_id, is_stars_auto_renew=False)
        await services.notification.notify_developer(
            text=f"Stars payment from non-approved user {user.tg_id} refunded ({charge_id})."
        )
        return

    # dev-рефанд только для разовых тестов, НЕ для рекуррента (иначе отменит подписку)
    if await IsDev()(user_id=user.tg_id) and not sp.is_recurring:
        await bot.refund_star_payment(user_id=user.tg_id, telegram_payment_charge_id=charge_id)

    # авто-продление: рекуррентное списание несёт исходный payload (is_extend=False) → форсим extend
    if sp.is_recurring and not sp.is_first_recurring:
        data.is_extend = True

    transaction = await Transaction.create(
        session=session,
        tg_id=user.tg_id,
        subscription=data.pack(),
        payment_id=charge_id,
        status=TransactionStatus.COMPLETED,
    )
    # B2: дубликат successful_payment (повторная доставка апдейта) → create вернёт None.
    if transaction is None:
        logger.warning(f"Duplicate Stars payment {charge_id} for user {user.tg_id}; skip.")
        return

    # B4: charge_id принимает editUserStarSubscription ТОЛЬКО от первого платежа подписки;
    #     рекурренты/разовые его НЕ трогают (иначе отмена из профиля упадёт CHARGE_ID_INVALID).
    if sp.is_first_recurring:
        await User.update(session, tg_id=user.tg_id, stars_charge_id=charge_id)
    if sp.is_recurring:  # первый И рекурренты — подписка жива (самовосстановление флага)
        await User.update(session, tg_id=user.tg_id, is_stars_auto_renew=True)
    if sp.subscription_expiration_date:  # B5: дата следующего списания — сигнал для крона
        await User.update(session, tg_id=user.tg_id, stars_expires_at=sp.subscription_expiration_date)

    gateway = gateway_factory.get_gateway(NavSubscription.PAY_TELEGRAM_STARS)
    await gateway.handle_payment_succeeded(payment_id=transaction.payment_id)
