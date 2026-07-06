import logging

from aiogram import F, Router
from aiogram.types import CallbackQuery
from aiogram.utils.i18n import gettext as _
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.filters import IsAdmin
from app.bot.models import ServicesContainer
from app.bot.routers.misc.keyboard import back_keyboard
from app.bot.utils.constants import ApprovalStatus
from app.bot.utils.navigation import NavAdminTools
from app.db.models import User

from .keyboard import (
    confirm_unreject_user_keyboard,
    rejected_user_details_keyboard,
    rejected_users_keyboard,
)

logger = logging.getLogger(__name__)
router = Router(name=__name__)


async def _get_rejected_users(session: AsyncSession) -> list[User]:
    stmt = select(User).where(User.approval_status == ApprovalStatus.REJECTED)
    result = await session.execute(stmt)
    return list(result.scalars().all())


@router.callback_query(F.data == NavAdminTools.REJECTED_USERS, IsAdmin())
async def callback_rejected_users(
    callback: CallbackQuery, user: User, session: AsyncSession
) -> None:
    logger.info(f"Admin {user.tg_id} is listing rejected users.")
    rejected_users = await _get_rejected_users(session)

    if rejected_users:
        await callback.message.edit_text(
            text=_("rejected_users:message:list"),
            reply_markup=rejected_users_keyboard(rejected_users),
        )
    else:
        await callback.message.edit_text(
            text=_("rejected_users:message:no_rejected"),
            reply_markup=back_keyboard(NavAdminTools.MAIN),
        )


@router.callback_query(F.data.startswith(NavAdminTools.SHOW_REJECTED_PAGE), IsAdmin())
async def callback_rejected_page(
    callback: CallbackQuery, user: User, session: AsyncSession
) -> None:
    page = int(callback.data.split("_")[3])
    rejected_users = await _get_rejected_users(session)

    logger.info(f"Admin {user.tg_id} is now on page #{page + 1} of rejected users.")

    await callback.message.edit_text(
        text=_("rejected_users:message:list"),
        reply_markup=rejected_users_keyboard(rejected_users, page=page),
    )


@router.callback_query(F.data.startswith(NavAdminTools.SHOW_REJECTED_DETAILS), IsAdmin())
async def callback_rejected_details(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    tg_id = int(callback.data.split("_")[3])
    target = await User.get(session=session, tg_id=tg_id)

    if not target or target.approval_status != ApprovalStatus.REJECTED:
        await services.notification.show_popup(
            callback=callback,
            text=_("rejected_users:popup:not_found"),
        )
        await callback_rejected_users(callback=callback, user=user, session=session)
        return

    logger.info(f"Admin {user.tg_id} is checking rejected user {target.tg_id}.")

    await callback.message.edit_text(
        text=_("rejected_users:message:details").format(
            name=target.first_name,
            username=target.username or "-",
            tg_id=target.tg_id,
        ),
        reply_markup=rejected_user_details_keyboard(target.tg_id),
    )


@router.callback_query(F.data.startswith(NavAdminTools.CONFIRM_UNREJECT_USER), IsAdmin())
async def callback_confirm_unreject_prompt(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    tg_id = int(callback.data.split("_")[3])
    target = await User.get(session=session, tg_id=tg_id)

    if not target or target.approval_status != ApprovalStatus.REJECTED:
        await services.notification.show_popup(
            callback=callback,
            text=_("rejected_users:popup:not_found"),
        )
        await callback_rejected_users(callback=callback, user=user, session=session)
        return

    logger.info(f"Admin {user.tg_id} confirmed unreject of user {target.tg_id}.")

    await callback.message.edit_text(
        text=_("rejected_users:message:confirm_unreject").format(
            name=target.first_name, tg_id=target.tg_id
        ),
        reply_markup=confirm_unreject_user_keyboard(target.tg_id),
    )


@router.callback_query(F.data.startswith(NavAdminTools.UNREJECT_USER), IsAdmin())
async def callback_unreject_user(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    tg_id = int(callback.data.split("_")[2])
    target = await User.get(session=session, tg_id=tg_id)

    if not target or target.approval_status != ApprovalStatus.REJECTED:
        await services.notification.show_popup(
            callback=callback,
            text=_("rejected_users:popup:not_found"),
        )
        await callback_rejected_users(callback=callback, user=user, session=session)
        return

    # Возврат в PENDING без approval_requested_at: /start или часовое напоминание
    # заново поставят юзера в очередь на рассмотрение, как для новой заявки.
    await User.update(
        session,
        tg_id=target.tg_id,
        approval_status=ApprovalStatus.PENDING,
        approval_requested_at=None,
    )

    logger.info(f"Admin {user.tg_id} has unrejected user {target.tg_id}.")

    await services.notification.show_popup(
        callback=callback,
        text=_("rejected_users:popup:unrejected").format(name=target.first_name),
    )

    await callback_rejected_users(callback=callback, user=user, session=session)
