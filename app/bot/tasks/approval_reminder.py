import logging

from aiogram.utils.i18n import I18n
from aiogram.utils.i18n import gettext as _
from apscheduler.schedulers.asyncio import AsyncIOScheduler
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.bot.services import NotificationService
from app.bot.utils.constants import DEFAULT_LANGUAGE, ApprovalStatus
from app.config import Config
from app.db.models import User

logger = logging.getLogger(__name__)

REMINDER_INTERVAL_HOURS = 1


async def remind_admins_of_pending_users(
    session_factory: async_sessionmaker,
    config: Config,
    i18n: I18n,
    notification_service: NotificationService,
) -> None:
    # Импорт внутри функции — та же причина, что в main_menu/handler.py: избегаем
    # циклической зависимости на уровне модуля.
    from app.bot.routers.admin_tools.approval_handler import approval_keyboard

    session: AsyncSession
    async with session_factory() as session:
        stmt = select(User).where(User.approval_status == ApprovalStatus.PENDING)
        result = await session.execute(stmt)
        pending_users = result.scalars().all()

    if not pending_users:
        logger.info("[approval reminder] No pending users to remind about.")
        return

    logger.info(f"[approval reminder] Reminding admins about {len(pending_users)} pending users.")
    admin_ids = set(config.bot.ADMINS) | {config.bot.DEV_ID}

    # Апдейт фонового таска не привязан к локали конкретного юзера → рендерим на дефолтной.
    with i18n.use_locale(DEFAULT_LANGUAGE):
        for user in pending_users:
            text = _("approval:admin:reminder").format(
                name=user.first_name, username=user.username or "-", tg_id=user.tg_id
            )
            keyboard = approval_keyboard(user.tg_id)
            for admin_id in admin_ids:
                await notification_service.notify_by_id(
                    chat_id=admin_id, text=text, reply_markup=keyboard
                )


def start_scheduler(
    session_factory: async_sessionmaker,
    config: Config,
    i18n: I18n,
    notification_service: NotificationService,
) -> None:
    scheduler = AsyncIOScheduler()
    scheduler.add_job(
        remind_admins_of_pending_users,
        "interval",
        hours=REMINDER_INTERVAL_HOURS,
        # Без next_run_time=now(): в отличие от других тасков, первый ран не должен быть
        # немедленным — иначе каждый рестарт бота дублирует уже отправленное admin:new_request.
        args=[session_factory, config, i18n, notification_service],
        coalesce=True,
        misfire_grace_time=300,
        max_instances=1,
    )
    scheduler.start()
