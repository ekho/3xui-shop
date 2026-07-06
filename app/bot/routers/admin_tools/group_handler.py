import logging

from aiogram import F, Router
from aiogram.fsm.context import FSMContext
from aiogram.fsm.state import State, StatesGroup
from aiogram.types import CallbackQuery, Message
from aiogram.utils.i18n import gettext as _
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.filters import IsAdmin
from app.bot.models import ServicesContainer
from app.bot.routers.misc.keyboard import back_keyboard
from app.bot.services.inbound_groups import EmptyInboundSetError
from app.bot.utils.constants import MAIN_MESSAGE_ID_KEY
from app.bot.utils.navigation import NavAdminTools
from app.db.models import User

from .keyboard import group_management_keyboard, user_groups_keyboard

logger = logging.getLogger(__name__)
router = Router(name=__name__)


class UserGroupsStates(StatesGroup):
    tg_id = State()


# region Groups overview (read-only: группы создаются и редактируются в панели)


@router.callback_query(F.data == NavAdminTools.GROUP_MANAGEMENT, IsAdmin())
async def callback_group_management(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    logger.info(f"Admin {user.tg_id} opened group overview.")
    await state.set_state(None)

    # Синк с панелей: список групп + маппинг инбаундов по сегментам тегов.
    inbound_counts: dict[str, int] = {}
    for connection in services.server_pool.all_connections():
        known = await services.inbound_groups.known_groups(connection.api)
        for name in known:
            inbound_counts.setdefault(name, 0)
        for inbound in await services.inbound_groups.all_inbounds(connection.api):
            for name in services.inbound_groups.groups_of(inbound.tag or "", known):
                inbound_counts[name] = inbound_counts.get(name, 0) + 1

    lines = []
    for name in sorted(inbound_counts):
        user_refs, plan_refs = await services.inbound_groups.references(name)
        lines.append(
            _("group_mgmt:message:group_line").format(
                name=name, inbounds=inbound_counts[name], users=user_refs, plans=plan_refs
            )
        )

    text = _("group_mgmt:message:main")
    text += "\n".join(lines) if lines else _("group_mgmt:message:empty")

    await callback.message.edit_text(text=text, reply_markup=group_management_keyboard())


# endregion


# region User groups (связка пользователь<->группы — единственная запись из бота)


@router.callback_query(F.data == NavAdminTools.USER_GROUPS, IsAdmin())
async def callback_user_groups(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
) -> None:
    logger.info(f"Admin {user.tg_id} started editing user groups.")
    await state.set_state(UserGroupsStates.tg_id)
    await state.update_data({MAIN_MESSAGE_ID_KEY: callback.message.message_id})
    await callback.message.edit_text(
        text=_("group_mgmt:message:enter_user_id"),
        reply_markup=back_keyboard(NavAdminTools.GROUP_MANAGEMENT),
    )


async def _render_user_groups(
    services: ServicesContainer, target: User
) -> tuple[str, object]:
    names = sorted(await services.inbound_groups.known_groups_union(services.server_pool))
    member = set(services.inbound_groups.effective_groups(target))
    text = _("group_mgmt:message:user_groups").format(
        tg_id=target.tg_id, groups=", ".join(sorted(member)) or "—"
    )
    return text, user_groups_keyboard(target.tg_id, names, member)


@router.message(UserGroupsStates.tg_id, IsAdmin())
async def message_user_groups_tg_id(
    message: Message,
    user: User,
    session: AsyncSession,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    raw = (message.text or "").strip()
    logger.info(f"Admin {user.tg_id} entered user id for group editing: {raw}")

    target = await User.get(session=session, tg_id=int(raw)) if raw.isdigit() else None
    if target is None:
        await services.notification.notify_by_message(
            message=message, text=_("group_mgmt:ntf:user_not_found"), duration=5
        )
        return

    await state.set_state(None)
    main_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)
    text, keyboard = await _render_user_groups(services, target)
    await message.bot.edit_message_text(
        text=text, chat_id=message.chat.id, message_id=main_message_id, reply_markup=keyboard
    )


@router.callback_query(F.data.startswith(NavAdminTools.TOGGLE_USER_GROUP), IsAdmin())
async def callback_toggle_user_group(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    # tgl_usr_grp_{tg_id}_{group}
    payload = callback.data[len(NavAdminTools.TOGGLE_USER_GROUP) + 1 :]
    tg_id_raw, group = payload.split("_", 1)
    target = await User.get(session=session, tg_id=int(tg_id_raw))
    if target is None:
        await services.notification.show_popup(callback=callback, text=_("group_mgmt:ntf:user_not_found"))
        return

    current = set(services.inbound_groups.effective_groups(target))
    new_groups = sorted(current - {group} if group in current else current | {group})
    if not new_groups:
        # Политика «не до нуля»: у юзера всегда минимум одна группа.
        await services.notification.show_popup(
            callback=callback, text=_("group_mgmt:popup:empty_set_refused")
        )
        return

    logger.info(f"Admin {user.tg_id} sets groups {new_groups} for user {target.tg_id}.")

    if target.server_id:
        # Немедленная сходимость: attach/detach + запись набора + зеркало метки.
        try:
            applied = await services.vpn.apply_inbound_groups(target, groups=new_groups)
        except EmptyInboundSetError:
            await services.notification.show_popup(
                callback=callback, text=_("group_mgmt:popup:empty_resolve")
            )
            return
        if not applied:
            await services.notification.show_popup(
                callback=callback, text=_("group_mgmt:popup:api_error")
            )
            return
    else:
        # Клиента на панели ещё нет — просто сохранить набор (применится при выдаче).
        await User.update(session=session, tg_id=target.tg_id, inbound_groups=new_groups)

    target.inbound_groups = new_groups
    text, keyboard = await _render_user_groups(services, target)
    await callback.message.edit_text(text=text, reply_markup=keyboard)


# endregion
