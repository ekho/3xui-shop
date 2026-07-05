import asyncio
import logging

from aiogram import F, Router
from aiogram.fsm.context import FSMContext
from aiogram.types import BufferedInputFile, CallbackQuery
from aiogram.utils.i18n import gettext as _
from aiohttp.web import HTTPFound, Request, Response

from app.bot.models import ServicesContainer
from app.bot.utils.constants import (
    APP_ANDROID_SCHEME,
    APP_IOS_SCHEME,
    APP_WINDOWS_SCHEME,
    MAIN_MESSAGE_ID_KEY,
    PREVIOUS_CALLBACK_KEY,
    QR_CODE_AUTO_DELETE_SECONDS,
)
from app.bot.utils.navigation import NavDownload, NavMain
from app.bot.utils.network import parse_redirect_url
from app.bot.utils.qrcode import generate_qr_png
from app.config import Config
from app.db.models import User

from .keyboard import download_keyboard, platforms_keyboard

logger = logging.getLogger(__name__)
router = Router(name=__name__)


async def redirect_to_connection(request: Request) -> Response:
    query_string = request.query_string

    if not query_string:
        return Response(status=400, reason="Missing query string.")

    params = parse_redirect_url(query_string)
    scheme = params.get("scheme")
    key = params.get("key")

    if not scheme or not key:
        raise Response(status=400, reason="Invalid parameters.")

    redirect_url = f"{scheme}{key}"  # TODO: #namevpn
    if scheme in {
        APP_IOS_SCHEME,
        APP_ANDROID_SCHEME,
        APP_WINDOWS_SCHEME,
    }:
        raise HTTPFound(redirect_url)

    return Response(status=400, reason="Unsupported application.")


@router.callback_query(F.data == NavDownload.MAIN)
async def callback_download(callback: CallbackQuery, user: User, state: FSMContext) -> None:
    logger.info(f"User {user.tg_id} opened download apps page.")

    main_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)
    previous_callback = await state.get_value(PREVIOUS_CALLBACK_KEY)

    logger.debug("--------------------------------")
    logger.debug(f"callback.message.message_id: {callback.message.message_id}")
    logger.debug(f"main_message_id: {main_message_id}")
    logger.debug(f"previous_callback: {previous_callback}")
    logger.debug("--------------------------------")
    if callback.message.message_id != main_message_id:
        await state.update_data({PREVIOUS_CALLBACK_KEY: NavMain.MAIN_MENU})
        previous_callback = NavMain.MAIN_MENU
        await callback.bot.edit_message_text(
            text=_("download:message:choose_platform"),
            chat_id=user.tg_id,
            message_id=main_message_id,
            reply_markup=platforms_keyboard(previous_callback),
        )
    else:
        await callback.message.edit_text(
            text=_("download:message:choose_platform"),
            reply_markup=platforms_keyboard(previous_callback),
        )


@router.callback_query(F.data.startswith(NavDownload.PLATFORM))
async def callback_platform(
    callback: CallbackQuery,
    user: User,
    services: ServicesContainer,
    config: Config,
) -> None:
    logger.info(f"User {user.tg_id} selected platform: {callback.data}")
    key = await services.vpn.get_key(user)

    match callback.data:
        case NavDownload.PLATFORM_IOS:
            platform = _("download:message:platform_ios")
        case NavDownload.PLATFORM_ANDROID:
            platform = _("download:message:platform_android")
        case _:
            platform = _("download:message:platform_windows")

    await callback.message.edit_text(
        text=_("download:message:connect_to_vpn").format(platform=platform),
        reply_markup=download_keyboard(platform=callback.data, key=key, url=config.bot.DOMAIN),
    )


@router.callback_query(F.data == NavDownload.SHOW_QR)
async def callback_show_qr(
    callback: CallbackQuery,
    user: User,
    services: ServicesContainer,
) -> None:
    logger.info(f"User {user.tg_id} requested subscription QR code.")
    key = await services.vpn.get_key(user)

    if not key:
        await services.notification.show_popup(
            callback=callback,
            text=_("download:popup:qr_unavailable"),
        )
        return

    photo = BufferedInputFile(generate_qr_png(key), filename="subscription_qr.png")
    message = await callback.message.answer_photo(
        photo=photo,
        caption=_("download:message:qr_caption").format(seconds=QR_CODE_AUTO_DELETE_SECONDS),
    )

    # Ключ в QR — тот же секрет, что и в profile:show_key: не оставляем его висеть в чате.
    await asyncio.sleep(QR_CODE_AUTO_DELETE_SECONDS)
    try:
        await message.delete()
    except Exception as exception:
        logger.error(f"Failed to delete QR message for {user.tg_id}: {exception}")
