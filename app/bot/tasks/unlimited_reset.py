import logging

from apscheduler.schedulers.asyncio import AsyncIOScheduler
from apscheduler.triggers.cron import CronTrigger
from apscheduler.util import astimezone
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.bot.services import AuditService, VPNService
from app.bot.utils.constants import UNLIMITED_INBOUND_GROUP, AuditAction
from app.db.models import User

logger = logging.getLogger(__name__)


async def reset_unlimited_traffic(
    session_factory: async_sessionmaker,
    vpn_service: VPNService,
    audit_service: AuditService | None = None,
) -> None:
    """Обнулить использованный трафик всем клиентам группы `unlimited`.

    Безлимит-план: totalGB=100ГБ (кап), expiryTime=0 (бессрочно). «100ГБ в месяц»
    обеспечивается этим сбросом 1-го числа: reset_traffic зануляет up/down клиента
    сразу по ВСЕМ его инбаундам (и unlimited, и унаследованным regular — см.
    INBOUND_GROUP_INCLUDES), поэтому кап отсчитывается заново с начала месяца.

    Забаненных пропускаем: панельный resetTraffic заодно включает клиента в xray,
    т.е. сбросил бы бан; доступа у них нет — обнулять нечего.
    """
    session: AsyncSession
    async with session_factory() as session:
        users = await User.get_all(session=session)

    groups = vpn_service.inbound_group_service
    targets = [
        user
        for user in users
        if user.server_id
        and UNLIMITED_INBOUND_GROUP in groups.effective_groups(user)
        and not groups.is_banned(user)
    ]

    logger.info(f"[unlimited-reset] Monthly traffic reset for {len(targets)} unlimited user(s).")
    reset = failed = 0
    for user in targets:
        try:
            if await vpn_service.reset_traffic(user):
                reset += 1
            else:
                failed += 1
        except Exception as exception:
            logger.error(f"[unlimited-reset] Failed for {user.tg_id}: {exception}")
            failed += 1

    logger.info(f"[unlimited-reset] Done: {reset} reset, {failed} failed.")

    # Системное событие в аудит-лог: джоб меняет трафик юзеров без человека-инициатора.
    if audit_service is not None:
        await audit_service.system_event(
            AuditAction.SYSTEM_UNLIMITED_RESET,
            payload={"targets": len(targets), "reset": reset, "failed": failed},
            channel_note=f"безлимит: сброшено {reset}, ошибок {failed} (из {len(targets)})",
        )


def start_scheduler(
    session_factory: async_sessionmaker,
    vpn_service: VPNService,
    timezone_name: str,
    audit_service: AuditService | None = None,
) -> None:
    # Календарный месяц: 1-го числа в 00:00 по timezone_name (BOT_TIMEZONE, дефолт UTC).
    # Резолвим через APScheduler-util (pytz, не зависит от системной tzdata); при
    # опечатке в BOT_TIMEZONE не роняем старт бота — откатываемся на UTC с логом.
    try:
        tz = astimezone(timezone_name)
    except Exception as exception:
        logger.error(
            f"[unlimited-reset] Invalid BOT_TIMEZONE '{timezone_name}': {exception}. "
            "Falling back to UTC."
        )
        tz = astimezone("UTC")
        timezone_name = "UTC"
    scheduler = AsyncIOScheduler(timezone=tz)
    scheduler.add_job(
        reset_unlimited_traffic,
        CronTrigger(day=1, hour=0, minute=0),
        args=[session_factory, vpn_service, audit_service],
        # Пропущенный из-за простоя прогон не копим и не теряем: coalesce + грейс 1ч
        # (если бот лежал ровно в полночь 1-го, сброс догонит при старте в пределах часа).
        coalesce=True,
        misfire_grace_time=3600,
        max_instances=1,
    )
    scheduler.start()
    logger.info(
        f"[unlimited-reset] Scheduler started (monthly, 1st 00:00 {timezone_name})."
    )
