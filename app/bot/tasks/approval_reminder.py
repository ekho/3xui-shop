import logging
from datetime import timedelta

from aiogram.utils.i18n import I18n
from aiogram.utils.i18n import gettext as _
from apscheduler.schedulers.asyncio import AsyncIOScheduler
from redis.asyncio.client import Redis
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.bot.services import NotificationService
from app.bot.utils.constants import DEFAULT_LANGUAGE, ApprovalStatus
from app.config import Config
from app.db.models import User

logger = logging.getLogger(__name__)

REMINDER_INTERVAL_HOURS = 1
# Ключ храним заметно дольше интервала, чтобы id предыдущего напоминания дожил до
# следующего рана; у решённых заявок (approved/rejected) ран их пропускает, а ключ
# сам истечёт по TTL.
REMINDER_MSG_TTL = timedelta(days=30)


def _reminder_msg_key(admin_id: int, tg_id: int) -> str:
    return f"approval:reminder:msg:{admin_id}:{tg_id}"


# Redis для этой задачи — вспомогательный слой антиспама, не источник истины. Сбой Redis
# (failover/таймаут) НЕ должен ни ронять весь прогон (иначе остальные админы/юзеры не получат
# напоминание в этот час), ни пробрасываться наружу. Best-effort, как delete_message/notify_by_id.
# Остаточный риск: если set упадёт уже ПОСЛЕ отправки — id потеряется и следующий прогон
# пришлёт один дубль (самоизлечивается по восстановлении Redis).
async def _redis_get(redis: Redis, key: str) -> bytes | None:
    try:
        return await redis.get(key)
    except Exception as exception:
        logger.warning(f"[approval reminder] redis.get {key} failed: {exception}")
        return None


async def _redis_set(redis: Redis, key: str, value: str) -> None:
    try:
        await redis.set(key, value, ex=REMINDER_MSG_TTL)
    except Exception as exception:
        logger.warning(f"[approval reminder] redis.set {key} failed: {exception}")


async def _redis_delete(redis: Redis, key: str) -> None:
    try:
        await redis.delete(key)
    except Exception as exception:
        logger.warning(f"[approval reminder] redis.delete {key} failed: {exception}")


async def remind_admins_of_pending_users(
    session_factory: async_sessionmaker,
    config: Config,
    i18n: I18n,
    notification_service: NotificationService,
    redis: Redis | None = None,
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
                # Антиспам: перед новым напоминанием удаляем предыдущее по этому юзеру
                # в чате этого админа (id хранится в Redis per (admin, user)).
                key = _reminder_msg_key(admin_id, user.tg_id)
                if redis is not None:
                    previous_id = await _redis_get(redis, key)
                    if previous_id:
                        await notification_service.delete_message(
                            chat_id=admin_id, message_id=int(previous_id)
                        )

                notification = await notification_service.notify_by_id(
                    chat_id=admin_id, text=text, reply_markup=keyboard
                )

                if redis is not None:
                    if notification:
                        await _redis_set(redis, key, str(notification.message_id))
                    else:
                        # Отправка не удалась — предыдущее уже удалено, не тянем мёртвый id.
                        await _redis_delete(redis, key)


def start_scheduler(
    session_factory: async_sessionmaker,
    config: Config,
    i18n: I18n,
    notification_service: NotificationService,
    redis: Redis | None = None,
) -> None:
    scheduler = AsyncIOScheduler()
    scheduler.add_job(
        remind_admins_of_pending_users,
        "interval",
        hours=REMINDER_INTERVAL_HOURS,
        # Без next_run_time=now(): в отличие от других тасков, первый ран не должен быть
        # немедленным — иначе каждый рестарт бота дублирует уже отправленное admin:new_request.
        args=[session_factory, config, i18n, notification_service, redis],
        coalesce=True,
        misfire_grace_time=300,
        max_instances=1,
    )
    scheduler.start()
