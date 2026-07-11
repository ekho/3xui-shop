import html
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

from .keyboard import pending_user_details_keyboard, pending_users_keyboard

logger = logging.getLogger(__name__)
router = Router(name=__name__)


async def _get_pending_users(session: AsyncSession) -> list[User]:
    # Старейшие заявки — первыми: у админа перед глазами те, кто ждёт дольше всех.
    stmt = (
        select(User)
        .where(User.approval_status == ApprovalStatus.PENDING)
        .order_by(User.approval_requested_at)
    )
    result = await session.execute(stmt)
    return list(result.scalars().all())


@router.callback_query(F.data == NavAdminTools.PENDING_USERS, IsAdmin())
async def callback_pending_users(
    callback: CallbackQuery, user: User, session: AsyncSession
) -> None:
    logger.info(f"Admin {user.tg_id} is listing pending users.")
    pending_users = await _get_pending_users(session)

    if pending_users:
        await callback.message.edit_text(
            text=_("pending_users:message:list"),
            reply_markup=pending_users_keyboard(pending_users),
        )
    else:
        await callback.message.edit_text(
            text=_("pending_users:message:no_pending"),
            reply_markup=back_keyboard(NavAdminTools.REGISTRATION_REQUESTS),
        )


@router.callback_query(F.data.startswith(NavAdminTools.SHOW_PENDING_PAGE), IsAdmin())
async def callback_pending_page(
    callback: CallbackQuery, user: User, session: AsyncSession
) -> None:
    page = int(callback.data.split("_")[3])
    pending_users = await _get_pending_users(session)

    logger.info(f"Admin {user.tg_id} is now on page #{page + 1} of pending users.")

    await callback.message.edit_text(
        text=_("pending_users:message:list"),
        reply_markup=pending_users_keyboard(pending_users, page=page),
    )


@router.callback_query(F.data.startswith(NavAdminTools.SHOW_PENDING_DETAILS), IsAdmin())
async def callback_pending_details(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    tg_id = int(callback.data.split("_")[3])
    target = await User.get(session=session, tg_id=tg_id)

    if not target or target.approval_status != ApprovalStatus.PENDING:
        await services.notification.show_popup(
            callback=callback,
            text=_("pending_users:popup:not_found"),
        )
        await callback_pending_users(callback=callback, user=user, session=session)
        return

    logger.info(f"Admin {user.tg_id} is checking pending user {target.tg_id}.")

    # approval_requested_at может быть None (юзер возвращён из отказа в очередь без нового /start).
    requested_at = (
        target.approval_requested_at.strftime("%Y-%m-%d %H:%M")
        if target.approval_requested_at
        else "-"
    )

    await callback.message.edit_text(
        text=_("pending_users:message:details").format(
            name=html.escape(target.first_name),
            username=target.username or "-",
            tg_id=target.tg_id,
            requested_at=requested_at,
        ),
        reply_markup=pending_user_details_keyboard(target.tg_id),
    )


async def _decide(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
    new_status: ApprovalStatus,
) -> None:
    """Применяет решение админа по ожидающей заявке и обновляет экран.

    Парсинг tg_id и guard (заявка ещё в статусе PENDING) уже сделаны ниже — остаётся
    только применить решение и дать обратную связь. `new_status` — ApprovalStatus.APPROVED
    для «✅ Одобрить» или ApprovalStatus.REJECTED для «🚫 Отклонить».
    """
    tg_id = int(callback.data.split("_")[2])
    target = await User.get(session=session, tg_id=tg_id)

    if target is None:
        await services.notification.show_popup(
            callback=callback,
            text=_("pending_users:popup:not_found"),
        )
        await callback_pending_users(callback=callback, user=user, session=session)
        return

    if target.approval_status != ApprovalStatus.PENDING:
        # Заявку успели решить в другом канале (кнопки в самом уведомлении/напоминании) до
        # того, как админ тапнул в списке — свежая сессия видит уже не-PENDING статус.
        await services.notification.show_popup(
            callback=callback,
            text=_("pending_users:popup:already"),
        )
        await callback_pending_users(callback=callback, user=user, session=session)
        return

    logger.info(f"Admin {user.tg_id} set {new_status.value} for pending user {target.tg_id}.")

    # Единый сервис: он же уведомляет юзера в его локали и снимает Stars-рекуррент при reject.
    # target только что прошёл guard как PENDING (та же сессия, без рефетча), поэтому решение
    # здесь применяется всегда; гонку «уже обработано» ловит guard выше на свежей сессии.
    await services.approval.apply_decision(session, target, new_status)

    if new_status == ApprovalStatus.APPROVED:
        text = _("pending_users:popup:approved").format(name=target.first_name)
    else:
        text = _("pending_users:popup:rejected").format(name=target.first_name)

    await services.notification.show_popup(callback=callback, text=text)
    # Возврат к обновлённому списку: только что решённая заявка из него исчезнет.
    await callback_pending_users(callback=callback, user=user, session=session)


@router.callback_query(F.data.startswith(NavAdminTools.PENDING_APPROVE), IsAdmin())
async def callback_pending_approve(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    await _decide(callback, user, session, services, ApprovalStatus.APPROVED)


@router.callback_query(F.data.startswith(NavAdminTools.PENDING_REJECT), IsAdmin())
async def callback_pending_reject(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    await _decide(callback, user, session, services, ApprovalStatus.REJECTED)
