import logging
from typing import Any, Awaitable, Callable

from aiogram.enums import ChatType
from aiogram.types import TelegramObject

from app.bot.middlewares import DBSessionMiddleware

logger = logging.getLogger(__name__)


class SupportDBSessionMiddleware(DBSessionMiddleware):
    """DBSession для support-бота.

    В группе поддержки инжектим ТОЛЬКО session (без создания User): иначе каждый
    оператор/болтовня в General плодили бы shop-юзеров с vpn_id/sub_id (m10), а
    сервисные апдейты без «живого» from_user (анонимное закрытие топика — m6)
    оставались бы вовсе без сессии. В личке — родительское поведение:
    session + user (+ автосоздание, как в основном боте).
    """

    async def __call__(
        self,
        handler: Callable[[TelegramObject, dict[str, Any]], Awaitable[Any]],
        event: TelegramObject,
        data: dict[str, Any],
    ) -> Any:
        chat = data.get("event_chat")
        if chat is not None and chat.type != ChatType.PRIVATE:
            async with self.session() as session:
                data["session"] = session
                return await handler(event, data)
        return await super().__call__(handler=handler, event=event, data=data)
