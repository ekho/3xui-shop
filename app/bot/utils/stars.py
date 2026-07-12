"""Stars-рекуррент: общая отмена автопродления (reject заявки, бан юзера)."""

import logging

from aiogram import Bot
from aiogram.exceptions import TelegramBadRequest
from sqlalchemy.ext.asyncio import AsyncSession

from app.db.models import User

logger = logging.getLogger(__name__)


async def cancel_stars_auto_renew(
    bot: Bot, session: AsyncSession, target: User, reason: str
) -> None:
    """Отменить Stars-автопродление юзера. Идемпотентно и best-effort.

    Telegram продолжает списывать звёзды, пока подписку не отменить явно, поэтому
    отмена обязательна при reject заявки (B1) и при бане (бан = стоп-продление,
    иначе деньги списываются за выключенный VPN). charge_id — от ПЕРВОГО платежа
    подписки (B4). Подписка могла лапснуть/быть отменённой самим юзером —
    TelegramBadRequest не фатален; флаг в БД сбрасывается в любом случае,
    иначе залипнет True.
    """
    if not getattr(target, "is_stars_auto_renew", False):
        return

    charge_id = getattr(target, "stars_charge_id", None)
    if charge_id:
        try:
            await bot.edit_user_star_subscription(
                user_id=target.tg_id,
                telegram_payment_charge_id=charge_id,
                is_canceled=True,
            )
            logger.info(f"Stars auto-renew canceled for {target.tg_id} ({reason}).")
        except TelegramBadRequest as exception:
            logger.warning(f"Cancel stars sub for {target.tg_id} ({reason}): {exception}")

    await User.update(session, tg_id=target.tg_id, is_stars_auto_renew=False)
    target.is_stars_auto_renew = False
