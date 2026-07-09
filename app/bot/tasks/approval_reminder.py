import logging

from apscheduler.schedulers.asyncio import AsyncIOScheduler
from redis.asyncio.client import Redis
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.services.approval import ApprovalService

logger = logging.getLogger(__name__)

REMINDER_INTERVAL_HOURS = 1

# Логика напоминаний (выбор канала группа/личка, Redis-антиспам) — в ApprovalService;
# здесь только расписание.


def start_scheduler(
    session_factory: async_sessionmaker,
    approval_service: ApprovalService,
    redis: Redis | None = None,
) -> None:
    scheduler = AsyncIOScheduler()
    scheduler.add_job(
        approval_service.remind_pending,
        "interval",
        hours=REMINDER_INTERVAL_HOURS,
        # Без next_run_time=now(): в отличие от других тасков, первый ран не должен быть
        # немедленным — иначе каждый рестарт бота дублирует уже отправленное admin:new_request.
        args=[session_factory, redis],
        coalesce=True,
        misfire_grace_time=300,
        max_instances=1,
    )
    scheduler.start()
