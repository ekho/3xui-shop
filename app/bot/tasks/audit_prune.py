import logging

from apscheduler.schedulers.asyncio import AsyncIOScheduler
from apscheduler.triggers.cron import CronTrigger
from apscheduler.util import astimezone

from app.bot.services import AuditService

logger = logging.getLogger(__name__)


async def prune_audit_log(audit_service: AuditService) -> None:
    """Retention: удалить события аудит-лога старше окна (AUDIT_RETENTION_DAYS).

    Само удаление логируется системным событием внутри AuditService.prune() — иначе
    дыра в логе была бы неотличима от ручной подчистки (важно для работы «безопасность»).
    """
    try:
        deleted = await audit_service.prune()
        logger.info(f"[audit-prune] Done: {deleted} entries removed.")
    except Exception as exception:
        logger.error(f"[audit-prune] Failed: {exception}")


def start_scheduler(audit_service: AuditService, timezone_name: str) -> None:
    # Ежедневно в 03:30 по timezone_name (BOT_TIMEZONE, дефолт UTC) — тихий час, вне
    # месячного сброса безлимита (1-е 00:00) и почасовых джобов.
    try:
        tz = astimezone(timezone_name)
    except Exception as exception:
        logger.error(
            f"[audit-prune] Invalid BOT_TIMEZONE '{timezone_name}': {exception}. Falling back to UTC."
        )
        tz = astimezone("UTC")
        timezone_name = "UTC"
    scheduler = AsyncIOScheduler(timezone=tz)
    scheduler.add_job(
        prune_audit_log,
        CronTrigger(hour=3, minute=30),
        args=[audit_service],
        coalesce=True,
        misfire_grace_time=3600,
        max_instances=1,
    )
    scheduler.start()
    logger.info(f"[audit-prune] Scheduler started (daily, 03:30 {timezone_name}).")
