import asyncio
import logging

from aiogram import Bot, F, Router
from aiogram.exceptions import TelegramBadRequest
from aiogram.fsm.context import FSMContext
from aiogram.types import CallbackQuery
from aiogram.utils.i18n import gettext as _
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.models import ClientData
from app.bot.services import ServicesContainer
from app.bot.utils.constants import PREVIOUS_CALLBACK_KEY
from app.bot.utils.navigation import NavProfile
from app.db.models import User

from .keyboard import banned_profile_keyboard, buy_subscription_keyboard, profile_keyboard

logger = logging.getLogger(__name__)
router = Router(name=__name__)


async def prepare_message(user: User, client_data: ClientData | None) -> str:
    profile = _("profile:message:main").format(name=user.first_name, id=user.tg_id)

    if not client_data:
        subscription = _("profile:message:subscription_none")
        return profile + subscription

    subscription = _("profile:message:subscription").format(devices=client_data.max_devices)

    subscription += (
        _("profile:message:subscription_expiry_time").format(expiry_time=client_data.expiry_time)
        if not client_data.has_subscription_expired
        else _("profile:message:subscription_expired")
    )

    # G4: статус автопродления Stars
    if getattr(user, "is_stars_auto_renew", False):
        subscription += "\n" + _("profile:message:autorenew_on")

    statistics = _("profile:message:statistics").format(
        total=client_data.traffic_used,
        up=client_data.traffic_up,
        down=client_data.traffic_down,
    )

    # G2: для лимитированных тарифов показываем остаток/лимит (для безлимита _traffic_total == -1)
    if client_data._traffic_total and client_data._traffic_total > 0:
        statistics += "\n" + _("profile:message:traffic_limit").format(
            remaining=client_data.traffic_remaining,
            limit=client_data.traffic_total,
        )

    return profile + subscription + statistics


async def render_profile(
    callback: CallbackQuery, user: User, services: ServicesContainer
) -> None:
    client_data = None
    if user.server_id:
        client_data = await services.vpn.get_client_data(user)
        if not client_data:
            await services.notification.show_popup(
                callback=callback,
                text=_("subscription:popup:error_fetching_data"),
            )
            return

    # Забаненный: только возврат в меню (ни ключа, ни подключения, ни покупки).
    if services.inbound_groups.is_banned(user):
        reply_markup = banned_profile_keyboard()
    elif client_data and not client_data.has_subscription_expired:
        reply_markup = profile_keyboard(user)
    else:
        reply_markup = buy_subscription_keyboard()
    await callback.message.edit_text(
        text=await prepare_message(user=user, client_data=client_data),
        reply_markup=reply_markup,
    )


@router.callback_query(F.data == NavProfile.MAIN)
async def callback_profile(
    callback: CallbackQuery,
    user: User,
    services: ServicesContainer,
    state: FSMContext,
) -> None:
    logger.info(f"User {user.tg_id} opened profile page.")
    await state.update_data({PREVIOUS_CALLBACK_KEY: NavProfile.MAIN})
    await render_profile(callback, user, services)


@router.callback_query(F.data == NavProfile.CANCEL_STARS_SUB)
async def cancel_stars_sub(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    bot: Bot,
    services: ServicesContainer,
) -> None:
    if not getattr(user, "stars_charge_id", None):
        await callback.answer(_("profile:stars:no_sub"), show_alert=True)
        return
    # m5: подписка могла лапснуть/быть отменённой/зарефанженной → edit_user_star_subscription
    # бросит TelegramBadRequest. Флаг сбрасываем ЛОКАЛЬНО в любом случае, иначе он залипнет True.
    try:
        await bot.edit_user_star_subscription(
            user_id=user.tg_id, telegram_payment_charge_id=user.stars_charge_id, is_canceled=True
        )
    except TelegramBadRequest as exception:
        logger.warning(f"Cancel stars sub for {user.tg_id}: {exception}")
    await User.update(session, tg_id=user.tg_id, is_stars_auto_renew=False)
    user.is_stars_auto_renew = False
    await callback.answer(_("profile:stars:canceled"), show_alert=True)
    await render_profile(callback, user, services)


@router.callback_query(F.data == NavProfile.RESUME_STARS_SUB)
async def resume_stars_sub(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    bot: Bot,
    services: ServicesContainer,
) -> None:
    if not getattr(user, "stars_charge_id", None):
        await callback.answer(_("profile:stars:no_sub"), show_alert=True)
        return
    # m4: возобновить можно, пока текущий оплаченный период не истёк. Иначе — TelegramBadRequest.
    try:
        await bot.edit_user_star_subscription(
            user_id=user.tg_id, telegram_payment_charge_id=user.stars_charge_id, is_canceled=False
        )
    except TelegramBadRequest as exception:
        logger.warning(f"Resume stars sub for {user.tg_id}: {exception}")
        await callback.answer(_("profile:stars:resume_failed"), show_alert=True)
        return
    await User.update(session, tg_id=user.tg_id, is_stars_auto_renew=True)
    user.is_stars_auto_renew = True
    await callback.answer(_("profile:stars:resumed"), show_alert=True)
    await render_profile(callback, user, services)


@router.callback_query(F.data == NavProfile.SHOW_KEY)
async def callback_show_key(
    callback: CallbackQuery,
    user: User,
    services: ServicesContainer,
) -> None:
    logger.info(f"User {user.tg_id} looked key.")
    key = await services.vpn.get_key(user)
    key_text = _("profile:message:key")
    message = await callback.message.answer(key_text.format(key=key, seconds_text=_("10 seconds")))

    for seconds in range(9, 0, -1):
        seconds_text = _("1 second", "{} seconds", seconds).format(seconds)
        await asyncio.sleep(1)
        await message.edit_text(text=key_text.format(key=key, seconds_text=seconds_text))
    await message.delete()
