import logging

from aiogram import F, Router
from aiogram.enums import ChatType
from aiogram.exceptions import TelegramAPIError
from aiogram.filters import CommandStart
from aiogram.types import Message, ReactionTypeEmoji
from aiogram.utils.i18n import gettext as _
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import User
from app.support_bot.service import SupportProxyService

logger = logging.getLogger(__name__)
router = Router(name=__name__)
router.message.filter(F.chat.type == ChatType.PRIVATE)


@router.message(CommandStart())
async def command_start(message: Message, user: User) -> None:
    logger.info(f"User {user.tg_id} started support bot.")
    await message.answer(text=_("support_bot:message:welcome"))


@router.message(~F.pinned_message)  # пин в личке — сервисное сообщение, его не копируют
async def relay_user_message(
    message: Message,
    user: User,
    session: AsyncSession,
    support: SupportProxyService,
) -> None:
    # Весь пользовательский фидбэк об ошибках (бан/сбой доставки) отправляет сервис.
    delivered = await support.relay_from_user(message=message, session=session, user=user)

    if delivered:
        # Тихое подтверждение реакцией вместо спама «отправлено» под каждым сообщением.
        try:
            await message.react([ReactionTypeEmoji(emoji="👌")])
        except TelegramAPIError:
            pass
