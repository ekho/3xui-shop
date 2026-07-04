import logging
from typing import Any, Awaitable, Callable

from aiogram import BaseMiddleware
from aiogram.types import CallbackQuery, Message, PreCheckoutQuery, TelegramObject, Update
from aiogram.utils.i18n import gettext as _

from app.bot.filters import IsAdmin
from app.bot.services import NotificationService
from app.bot.utils.constants import ApprovalStatus
from app.config import Config

logger = logging.getLogger(__name__)

PENDING_NOTICE_TTL = 45  # сек: троттлинг ответа "ожидайте" на обычные сообщения (m3)


class ApprovalMiddleware(BaseMiddleware):
    """G1: пропускает только approved-юзеров и админов; остальным блокирует действия.

    Ставится ПОСЛЕ DBSessionMiddleware (нужен загруженный data['user']).
    B1: платёжные апдейты (pre_checkout, successful_payment) НЕ глотаем молча —
    иначе Telegram отменит платёж (10 с) или потеряется рекуррентное списание Stars.
    """

    def __init__(self, config: Config) -> None:
        self.config = config
        logger.debug("Approval Middleware initialized.")

    async def __call__(
        self,
        handler: Callable[[TelegramObject, dict[str, Any]], Awaitable[Any]],
        event: TelegramObject,
        data: dict[str, Any],
    ) -> Any:
        if not isinstance(event, Update):
            return await handler(event, data)

        user = data.get("user")
        if not self.config.shop.APPROVAL_REQUIRED or user is None:
            return await handler(event, data)
        if user.approval_status == ApprovalStatus.APPROVED or await IsAdmin()(user_id=user.tg_id):
            return await handler(event, data)

        ev = event.event

        # B1: деньги уже списаны — successful_payment пропускаем ВСЕГДА (рефанд/аудит в хендлере)
        if isinstance(ev, Message) and ev.successful_payment:
            return await handler(event, data)

        # B1: pre_checkout нельзя ронять молча (error_message обязателен при ok=False)
        if isinstance(ev, PreCheckoutQuery):
            await ev.answer(ok=False, error_message=_("approval:notice:pending"))
            return None

        if isinstance(ev, Message):
            # m3: точный разбор /start (Command не матчит /startXXX; диплинк /start ref сохраняем)
            if (ev.text or "").split(maxsplit=1)[:1] == ["/start"]:
                return await handler(event, data)
            # m3: не оставлять юзера в тишине — показать статус (с redis-троттлингом от спама)
            redis = data.get("redis")
            throttle_key = f"approval:pending:notice:{user.tg_id}"
            if redis is None or not await redis.get(throttle_key):
                await NotificationService.notify_by_message(
                    message=ev, text=_("approval:notice:pending"), duration=5
                )
                if redis is not None:
                    await redis.set(throttle_key, "1", ex=PENDING_NOTICE_TTL)
            return None

        if isinstance(ev, CallbackQuery):
            await ev.answer(_("approval:notice:pending"), show_alert=True)
            return None

        return None
