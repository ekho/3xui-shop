import asyncio
import logging
from urllib.parse import urljoin

from aiogram import Bot, Dispatcher
from aiogram.client.default import DefaultBotProperties
from aiogram.client.session.aiohttp import AiohttpSession
from aiogram.client.telegram import TelegramAPIServer
from aiogram.enums import ParseMode
from aiogram.fsm.storage.memory import MemoryStorage
from aiogram.fsm.storage.redis import RedisStorage
from aiogram.utils.i18n import I18n
from aiogram.webhook.aiohttp_server import SimpleRequestHandler, setup_application
from aiohttp.web import Application, AppRunner, TCPSite, _run_app
from redis.asyncio.client import Redis

from app import logger, support_bot
from app.bot import filters, middlewares, routers, services, tasks
from app.bot.middlewares import MaintenanceMiddleware
from app.bot.models import ServicesContainer
from app.bot.payment_gateways import GatewayFactory
from app.bot.utils import commands
from app.bot.utils.py3xui_compat import apply_py3xui_patches
from app.bot.utils.constants import (
    BOT_STARTED_TAG,
    BOT_STOPPED_TAG,
    DEFAULT_LANGUAGE,
    I18N_DOMAIN,
    TELEGRAM_WEBHOOK,
)
from app.config import DEFAULT_BOT_HOST, DEFAULT_LOCALES_DIR, Config, load_config
from app.db.database import Database


async def on_shutdown(db: Database, bot: Bot, services: ServicesContainer) -> None:
    await services.notification.notify_developer(BOT_STOPPED_TAG)
    await commands.delete(bot)
    await bot.delete_webhook()
    await bot.session.close()
    await db.close()
    logging.info("Bot stopped.")


async def on_startup(
    config: Config,
    bot: Bot,
    services: ServicesContainer,
    db: Database,
    redis: Redis,
    i18n: I18n,
) -> None:
    # Long-polling: вебхук не нужен — снимаем его (иначе getUpdates вернёт ошибку) и выходим.
    if not config.bot.USE_WEBHOOK:
        await bot.delete_webhook()
        logging.info("Polling mode: webhook removed, receiving updates via getUpdates.")
        await services.notification.notify_developer(BOT_STARTED_TAG)
        logging.info("Bot started.")
        _start_schedulers(config, db, redis, i18n, services)
        return

    webhook_url = urljoin(config.bot.DOMAIN, TELEGRAM_WEBHOOK)

    # B7: сравнивать .url (раньше сравнивался объект WebhookInfo со строкой → всегда True).
    #     Пересоздаём вебхук при смене URL; secret_token аутентифицирует входящие апдейты.
    try:
        current_webhook = await bot.get_webhook_info()
        if current_webhook.url != webhook_url:
            await bot.set_webhook(webhook_url, secret_token=config.bot.WEBHOOK_SECRET or None)
            current_webhook = await bot.get_webhook_info()
        logging.info(f"Current webhook URL: {current_webhook.url}")
    except Exception as exception:
        # Частая причина: BOT_DOMAIN не публичный/не резолвится/без валидного TLS, или бот
        # не проксируется на этот домен. Логируем понятно и закрываем сессию, чтобы не текла
        # (иначе — "Unclosed client session" и рестарт-луп с невнятным трейсом).
        hint = (
            f"Проверьте BOT_DOMAIN: это должен быть ПУБЛИЧНЫЙ домен с валидным TLS, "
            f"который DNS-резолвится и проксируется вашим reverse-proxy на бота (порт {config.bot.PORT})."
        )
        if config.bot.API_URL:
            hint += (
                f"\nИспользуется кастомный Telegram API '{config.bot.API_URL}' — именно ЭТОТ сервер "
                f"должен уметь резолвить и достучаться до домена вебхука. 'Failed to resolve host' "
                f"обычно значит, что у API-сервера сломан/изолирован DNS."
            )
        logging.critical(f"Не удалось настроить вебхук '{webhook_url}': {exception}\n{hint}")
        await bot.session.close()
        raise

    await services.notification.notify_developer(BOT_STARTED_TAG)
    logging.info("Bot started.")
    _start_schedulers(config, db, redis, i18n, services)


def _start_schedulers(
    config: Config, db: Database, redis: Redis, i18n: I18n, services: ServicesContainer
) -> None:
    tasks.transactions.start_scheduler(db.session)
    if config.shop.REFERRER_REWARD_ENABLED:
        tasks.referral.start_scheduler(
            session_factory=db.session, referral_service=services.referral
        )
    tasks.subscription_expiry.start_scheduler(
        session_factory=db.session,
        redis=redis,
        i18n=i18n,
        vpn_service=services.vpn,
        notification_service=services.notification,
    )
    tasks.inbound_reconcile.start_scheduler(
        session_factory=db.session,
        redis=redis,
        server_pool_service=services.server_pool,
        inbound_group_service=services.inbound_groups,
        notification_service=services.notification,
    )
    tasks.unlimited_reset.start_scheduler(
        session_factory=db.session,
        vpn_service=services.vpn,
        timezone_name=config.bot.TIMEZONE,
        audit_service=services.audit,
    )
    tasks.audit_prune.start_scheduler(
        audit_service=services.audit,
        timezone_name=config.bot.TIMEZONE,
    )
    if config.shop.APPROVAL_REQUIRED:
        tasks.approval_reminder.start_scheduler(
            session_factory=db.session,
            approval_service=services.approval,
            redis=redis,
        )


def _log_support_task_done(task: "asyncio.Task") -> None:
    """Фоновый polling умирает молча (fire-and-forget) — без этого колбэка магазин
    работал бы днями с лежащей поддержкой и без единой строчки в логах."""
    if task.cancelled():
        return
    exception = task.exception()
    if exception:
        logging.critical(
            f"Support bot polling crashed: {exception!r}. Поддержка НЕ работает; "
            "основной бот продолжает работу. Проверьте SUPPORT_BOT_TOKEN/SUPPORT_GROUP_ID."
        )


async def _stop_support_task(task: "asyncio.Task | None") -> None:
    """Останавливает polling support-бота; cancel прерывает start_polling, его
    finally эмитит shutdown диспетчера (закрытие сессии — в support_bot._on_shutdown).
    Никогда не поднимает исключение: за ним в finally идёт runner.cleanup()."""
    if task is None:
        return
    if task.done():
        return  # упавший таск уже отлогирован done-колбэком; await пере-поднял бы его исключение
    task.cancel()
    try:
        await task
    except asyncio.CancelledError:
        pass
    except Exception as exception:
        logging.error(f"Support bot polling stopped with error: {exception!r}")


async def main() -> None:
    # Create web application
    app = Application()

    # Load configuration
    config = load_config()

    # Set up logging
    logger.setup_logging(config.logging)

    # P2: пропатчить модели py3xui под 3x-ui v3.1+ (иначе inbound.get_list падает на инбаундах без security)
    apply_py3xui_patches()

    # Initialize database
    db = Database(config.database)
    await db.initialize()

    # Set up storage for FSM (Finite State Machine)
    storage = RedisStorage.from_url(url=config.redis.url())
    # storage = MemoryStorage()

    # Кастомный Telegram Bot API (локальный сервер/зеркало) — если задан TELEGRAM_API_URL,
    # иначе aiogram использует https://api.telegram.org по умолчанию.
    session = None
    if config.bot.API_URL:
        api_server = TelegramAPIServer.from_base(
            config.bot.API_URL, is_local=config.bot.API_IS_LOCAL
        )
        session = AiohttpSession(api=api_server)
        logging.info(f"Using custom Telegram API server: {config.bot.API_URL}")

    # Initialize the bot with the token and default properties
    bot = Bot(
        token=config.bot.TOKEN,
        session=session,
        default=DefaultBotProperties(
            parse_mode=ParseMode.HTML, link_preview_is_disabled=True
        ),
    )

    # Set up internationalization (i18n)
    i18n = I18n(
        path=DEFAULT_LOCALES_DIR,
        default_locale=DEFAULT_LANGUAGE,
        domain=I18N_DOMAIN,
    )
    I18n.set_current(i18n)

    # Support-прокси: второй бот в этом же процессе (см. app/support_bot/__init__.py).
    # Собираем ДО сервисов: при включённой фиче ApprovalService шлёт карточки заявок
    # на регистрацию в персональные топики группы поддержки через SupportProxyService.
    # Polling стартует ниже.
    support_bot_instance: Bot | None = None
    support_dispatcher: Dispatcher | None = None
    support_service = None
    if config.bot.SUPPORT_BOT_TOKEN and config.bot.SUPPORT_GROUP_ID:
        support_bot_instance, support_dispatcher = support_bot.create(
            config=config, db=db, i18n=i18n
        )
        support_service = support_dispatcher["support"]  # создан внутри create()

    # Initialize services
    services_container = await services.initialize(
        config=config,
        session=db.session,
        bot=bot,
        i18n=i18n,
        support=support_service,
    )

    # Общие сервисы доступны и хендлерам support-бота (кнопки approve/reject в группе);
    # redis — для антиспама карточек при /pending (тот же, что у напоминаний).
    if support_dispatcher is not None:
        support_dispatcher["services"] = services_container
        support_dispatcher["redis"] = storage.redis

    # Sync servers
    await services_container.server_pool.sync_servers()

    # Register payment gateways
    gateway_factory = GatewayFactory()
    gateway_factory.register_gateways(
        app=app,
        config=config,
        session=db.session,
        storage=storage,
        bot=bot,
        i18n=i18n,
        services=services_container,
    )

    # Карточке юзера в /info support-бота нужен маппинг «шлюз -> валюта» (суммы платежей).
    if support_dispatcher is not None:
        support_dispatcher["gateway_factory"] = gateway_factory

    # Create the dispatcher
    dispatcher = Dispatcher(
        db=db,
        storage=storage,
        config=config,
        bot=bot,
        services=services_container,
        gateway_factory=gateway_factory,
        redis=storage.redis,
        i18n=i18n,
    )

    # Register event handlers
    dispatcher.startup.register(on_startup)
    dispatcher.shutdown.register(on_shutdown)

    # Enable Maintenance mode for developing # WARNING: remove before production
    MaintenanceMiddleware.set_mode(False)

    # Register middlewares
    middlewares.register(dispatcher=dispatcher, i18n=i18n, session=db.session, config=config)

    # Register filters
    filters.register(
        dispatcher=dispatcher,
        developer_id=config.bot.DEV_ID,
        admins_ids=config.bot.ADMINS,
    )

    # Include bot routers
    routers.include(app=app, dispatcher=dispatcher)

    # Set up bot commands
    await commands.setup(bot)

    # Support-прокси: всегда long-polling — независимо от режима основного бота
    # (разные токены, конфликтов нет).
    support_task: asyncio.Task | None = None
    if support_dispatcher is not None:
        # handle_signals=False: сигналами управляет основной цикл (start_polling основного
        # бота или _run_app); второй набор signal-хендлеров затёр бы первый.
        support_task = asyncio.create_task(
            support_dispatcher.start_polling(support_bot_instance, handle_signals=False)
        )
        support_task.add_done_callback(_log_support_task_done)

    if config.bot.USE_WEBHOOK:
        # Webhook: Telegram шлёт апдейты на /webhook (через ваш reverse-proxy, если используется).
        # B7: secret_token заставляет aiogram сверять заголовок X-Telegram-Bot-Api-Secret-Token
        #     (secrets.compare_digest) и возвращать 401 на подделки. None → проверка отключена.
        webhook_requests_handler = SimpleRequestHandler(
            dispatcher=dispatcher, bot=bot, secret_token=config.bot.WEBHOOK_SECRET or None
        )
        webhook_requests_handler.register(app, path=TELEGRAM_WEBHOOK)

        # Set up application and run
        setup_application(app, dispatcher, bot=bot)
        try:
            await _run_app(app, host=DEFAULT_BOT_HOST, port=config.bot.PORT)
        finally:
            await _stop_support_task(support_task)
    else:
        # Long-polling: апдейты забираем через getUpdates — публичный вебхук/домен не нужны.
        # Веб-сервер всё равно поднимаем для НЕ-Telegram роутов (Cryptomus-вебхук, редирект
        # подключения); start_polling сам управляет startup/shutdown диспетчера и закрывает сессию.
        runner = AppRunner(app)
        await runner.setup()
        site = TCPSite(runner, host=DEFAULT_BOT_HOST, port=config.bot.PORT)
        await site.start()
        try:
            await dispatcher.start_polling(bot)
        finally:
            await _stop_support_task(support_task)
            await runner.cleanup()


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except (KeyboardInterrupt, SystemExit):
        logging.info("Bot stopped.")
