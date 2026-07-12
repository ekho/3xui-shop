import logging
from typing import TYPE_CHECKING

from aiogram.filters import BaseFilter
from aiogram.types import TelegramObject
from aiogram.types import User as TelegramUser

from app.bot.utils.constants import AuditSource

from .is_dev import IsDev

if TYPE_CHECKING:
    from app.bot.models import ServicesContainer

logger = logging.getLogger(__name__)


class IsAdmin(BaseFilter):
    admins_ids: list[int] = []

    async def __call__(
        self,
        event: TelegramObject | None = None,
        user_id: int | None = None,
        services: "ServicesContainer | None" = None,
    ) -> bool:
        if user_id:
            is_dev = await IsDev()(user_id=user_id)
            return user_id in self.admins_ids or is_dev

        user: TelegramUser | None = event.from_user

        if not user:
            return False

        is_dev = await IsDev()(event)
        allowed = user.id in self.admins_ids or is_dev

        # Отказ в доступе — сигнал безопасности в аудит-лог. Фильтр стоит ПОСЛЕ
        # data/state-фильтров хендлера, поэтому сюда с allowed=False доходит только
        # не-админ, чей апдейт УЖЕ совпал с админским контролом (не шум обычных юзеров).
        if not allowed:
            await self._audit_denied(event, user, services)

        return allowed

    @staticmethod
    async def _audit_denied(
        event: TelegramObject, user: TelegramUser, services: "ServicesContainer | None"
    ) -> None:
        audit = getattr(services, "audit", None)
        if audit is None:  # services в фильтр не пришли / аудит недоступен — молча выходим
            return
        attempted = getattr(event, "data", None) or getattr(event, "text", None)
        await audit.access_denied(user, source=AuditSource.MAIN_BOT, attempted=attempted)

    @classmethod
    def set_admins(cls, admins_ids: list[int]) -> None:
        cls.admins_ids = admins_ids
        logger.info(f"Admins set: {admins_ids}")
