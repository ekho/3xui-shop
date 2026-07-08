"""Support-прокси: отдельный бот (второй токен) пересылает сообщения юзеров в
топики супергруппы-форума и ответы операторов обратно. Живёт в том же процессе,
что и основной бот, но со СВОИМ Dispatcher:

- без глобального IsPrivate (нужны апдейты из группы);
- без Throttling (дропал бы быстрые серии сообщений, включая альбомы);
- без Garbage (удалял бы текстовые сообщения юзера — они здесь и есть полезная нагрузка);
- без Maintenance/Approval (поддержка должна работать в обслуживание и до апрува).

Апдейты всегда получает long-polling'ом независимо от режима основного бота:
трафик мал, домен/вебхук не нужны, токены разные — конфликтов нет.
"""

import logging

from aiogram import Bot, Dispatcher
from aiogram.client.default import DefaultBotProperties
from aiogram.client.session.aiohttp import AiohttpSession
from aiogram.client.telegram import TelegramAPIServer
from aiogram.enums import ParseMode
from aiogram.exceptions import TelegramAPIError
from aiogram.fsm.storage.memory import MemoryStorage
from aiogram.types import BotCommand, BotCommandScopeAllPrivateChats, BotCommandScopeChat
from aiogram.utils.i18n import I18n, SimpleI18nMiddleware

from app.config import Config
from app.db.database import Database

from . import routers
from .middleware import SupportDBSessionMiddleware
from .service import SupportProxyService

logger = logging.getLogger(__name__)


def create(config: Config, db: Database, i18n: I18n) -> tuple[Bot, Dispatcher]:
    """Собирает support-бота и его диспетчер. Вызывать только при включённой фиче."""
    session = None
    if config.bot.API_URL:
        api_server = TelegramAPIServer.from_base(config.bot.API_URL, is_local=config.bot.API_IS_LOCAL)
        session = AiohttpSession(api=api_server)

    bot = Bot(
        token=config.bot.SUPPORT_BOT_TOKEN,
        session=session,
        default=DefaultBotProperties(parse_mode=ParseMode.HTML, link_preview_is_disabled=True),
    )

    service = SupportProxyService(config=config, bot=bot, i18n=i18n)

    # FSM не используется — MemoryStorage, чтобы не делить Redis-ключи с основным ботом.
    dispatcher = Dispatcher(
        storage=MemoryStorage(),
        config=config,
        db=db,
        i18n=i18n,
        support=service,
    )

    dispatcher.update.middleware.register(SimpleI18nMiddleware(i18n))
    dispatcher.update.middleware.register(SupportDBSessionMiddleware(db.session))

    routers.include(dispatcher)

    dispatcher.startup.register(_on_startup)
    dispatcher.shutdown.register(_on_shutdown)

    return bot, dispatcher


async def _on_startup(config: Config, bot: Bot) -> None:
    await bot.delete_webhook()

    me = await bot.get_me()
    # Кнопка «Связаться» в основном боте строит deep-link из username — берём его из
    # get_me(), а не из env: нечему рассинхронизироваться.
    config.bot.SUPPORT_BOT_USERNAME = me.username
    logger.info(f"Support bot @{me.username} started (group {config.bot.SUPPORT_GROUP_ID}).")

    try:
        chat = await bot.get_chat(config.bot.SUPPORT_GROUP_ID)
        if not chat.is_forum:
            logger.critical(
                f"SUPPORT_GROUP_ID={config.bot.SUPPORT_GROUP_ID}: у группы НЕ включены Topics — "
                "создание тикетов будет падать. Включите Topics в настройках группы."
            )
    except TelegramAPIError as exception:
        logger.critical(
            f"Support bot не видит группу {config.bot.SUPPORT_GROUP_ID}: {exception}. "
            "Добавьте бота в группу админом с правом can_manage_topics."
        )

    try:
        await bot.set_my_commands(
            commands=[BotCommand(command="start", description="Начать диалог с поддержкой")],
            scope=BotCommandScopeAllPrivateChats(),
        )
        await bot.set_my_commands(
            commands=[
                BotCommand(command="close", description="Закрыть тикет"),
                BotCommand(command="info", description="Карточка пользователя"),
                BotCommand(command="ban", description="Заблокировать в поддержке"),
                BotCommand(command="unban", description="Разблокировать"),
            ],
            scope=BotCommandScopeChat(chat_id=config.bot.SUPPORT_GROUP_ID),
        )
    except TelegramAPIError as exception:
        logger.warning(f"Support bot commands setup failed: {exception}")


async def _on_shutdown(bot: Bot) -> None:
    await bot.session.close()
    logger.info("Support bot stopped.")
