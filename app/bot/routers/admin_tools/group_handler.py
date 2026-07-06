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
from app.bot.utils.constants import INBOUND_GROUP_NAME_KEY, MAIN_MESSAGE_ID_KEY
from app.bot.utils.navigation import NavAdminTools
from app.db.models import User

from .keyboard import (
    confirm_delete_group_keyboard,
    group_details_keyboard,
    group_management_keyboard,
    user_groups_keyboard,
)

logger = logging.getLogger(__name__)
router = Router(name=__name__)


class CreateGroupStates(StatesGroup):
    name = State()


class RenameGroupStates(StatesGroup):
    name = State()


class UserGroupsStates(StatesGroup):
    tg_id = State()


# Коды ошибок сервисного слоя -> ключи локализации попапов.
_ERROR_KEYS = {
    "invalid_name": "group_mgmt:popup:invalid_name",
    "exists": "group_mgmt:popup:exists",
    "not_found": "group_mgmt:popup:not_found",
    "unknown_group": "group_mgmt:popup:not_found",
    "inbound_not_found": "group_mgmt:popup:api_error",
    "referenced_by_plans": "group_mgmt:popup:referenced_by_plans",
    "referenced_by_users": "group_mgmt:popup:referenced_by_users",
    "has_inbounds": "group_mgmt:popup:has_inbounds",
    "api_error": "group_mgmt:popup:api_error",
}


def _error_text(code: str) -> str:
    return _(_ERROR_KEYS.get(code, "group_mgmt:popup:api_error"))


async def _collect_group_entries(
    services: ServicesContainer, group: str
) -> list[tuple[int, int, str, bool]]:
    """[(server_id, inbound_id, подпись, в_группе)] по всем серверам пула."""
    entries: list[tuple[int, int, str, bool]] = []
    for connection in services.server_pool.all_connections():
        for inbound in await services.inbound_groups.all_inbounds(connection.api):
            in_group = services.inbound_groups.parse_group(inbound.tag or "") == group
            label = f"{connection.server.name}: {inbound.remark or inbound.tag}"
            entries.append((connection.server.id, inbound.id, label, in_group))
    return entries


# region Groups list


@router.callback_query(F.data == NavAdminTools.GROUP_MANAGEMENT, IsAdmin())
async def callback_group_management(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    logger.info(f"Admin {user.tg_id} opened group management.")
    await state.set_state(None)

    names = sorted(await services.inbound_groups.known_groups())

    lines = []
    for name in names:
        inbound_count = 0
        for connection in services.server_pool.all_connections():
            for inbound in await services.inbound_groups.all_inbounds(connection.api):
                if services.inbound_groups.parse_group(inbound.tag or "") == name:
                    inbound_count += 1
        user_refs, plan_refs = await services.inbound_groups.references(name)
        lines.append(
            _("group_mgmt:message:group_line").format(
                name=name, inbounds=inbound_count, users=user_refs, plans=plan_refs
            )
        )

    text = _("group_mgmt:message:main")
    text += "\n".join(lines) if lines else _("group_mgmt:message:empty")

    await callback.message.edit_text(text=text, reply_markup=group_management_keyboard(names))


# endregion


# region Group card + composition toggles


async def _render_group_details(
    services: ServicesContainer, group: str
) -> tuple[str, object] | None:
    if group not in await services.inbound_groups.known_groups():
        return None
    user_refs, plan_refs = await services.inbound_groups.references(group)
    entries = await _collect_group_entries(services, group)
    text = _("group_mgmt:message:details").format(
        name=group, users=user_refs, plans=plan_refs
    )
    return text, group_details_keyboard(group, entries)


@router.callback_query(F.data.startswith(NavAdminTools.SHOW_GROUP), IsAdmin())
async def callback_show_group(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    group = callback.data[len(NavAdminTools.SHOW_GROUP) + 1 :]
    logger.info(f"Admin {user.tg_id} opened group '{group}'.")
    await state.set_state(None)

    rendered = await _render_group_details(services, group)
    if rendered is None:
        await services.notification.show_popup(callback=callback, text=_("group_mgmt:popup:not_found"))
        await callback_group_management(callback=callback, user=user, state=state, services=services)
        return

    text, keyboard = rendered
    await callback.message.edit_text(text=text, reply_markup=keyboard)


@router.callback_query(F.data.startswith(NavAdminTools.TOGGLE_GROUP_INBOUND), IsAdmin())
async def callback_toggle_group_inbound(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    # tgl_grp_ib_{group}_{server_id}_{inbound_id}
    payload = callback.data[len(NavAdminTools.TOGGLE_GROUP_INBOUND) + 1 :]
    group, server_id, inbound_id = payload.split("_")
    server_id, inbound_id = int(server_id), int(inbound_id)

    connection = next(
        (c for c in services.server_pool.all_connections() if c.server.id == server_id), None
    )
    if connection is None:
        await services.notification.show_popup(callback=callback, text=_("group_mgmt:popup:api_error"))
        return

    inbound = next(
        (i for i in await services.inbound_groups.all_inbounds(connection.api) if i.id == inbound_id),
        None,
    )
    if inbound is None:
        await services.notification.show_popup(callback=callback, text=_("group_mgmt:popup:api_error"))
        return

    in_group = services.inbound_groups.parse_group(inbound.tag or "") == group
    logger.info(
        f"Admin {user.tg_id} toggles inbound {inbound_id} (server {server_id}) "
        f"{'out of' if in_group else 'into'} group '{group}'."
    )
    if in_group:
        error = await services.inbound_groups.remove_inbound_from_group(
            connection.api, inbound_id, group
        )
    else:
        error = await services.inbound_groups.add_inbound_to_group(
            connection.api, inbound_id, group
        )

    if error:
        await services.notification.show_popup(callback=callback, text=_error_text(error))
        return

    rendered = await _render_group_details(services, group)
    if rendered:
        text, keyboard = rendered
        await callback.message.edit_text(text=text, reply_markup=keyboard)
    # Ретег меняет состав — членства юзеров доведёт reconciler (или админ руками сейчас).
    await services.notification.show_popup(
        callback=callback, text=_("group_mgmt:popup:retagged"), cache_time=1
    )


# endregion


# region Create group


@router.callback_query(F.data == NavAdminTools.CREATE_GROUP, IsAdmin())
async def callback_create_group(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
) -> None:
    logger.info(f"Admin {user.tg_id} started creating inbound group.")
    await state.set_state(CreateGroupStates.name)
    await state.update_data({MAIN_MESSAGE_ID_KEY: callback.message.message_id})
    await callback.message.edit_text(
        text=_("group_mgmt:message:enter_name"),
        reply_markup=back_keyboard(NavAdminTools.GROUP_MANAGEMENT),
    )


@router.message(CreateGroupStates.name, IsAdmin())
async def message_create_group_name(
    message: Message,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    name = (message.text or "").strip().lower()
    logger.info(f"Admin {user.tg_id} entered new group name: {name}")

    error = await services.inbound_groups.create_group_registered(services.server_pool, name)
    if error:
        await services.notification.notify_by_message(
            message=message, text=_error_text(error), duration=5
        )
        return

    await state.set_state(None)
    main_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)
    names = sorted(await services.inbound_groups.known_groups())
    await message.bot.edit_message_text(
        text=_("group_mgmt:message:main"),
        chat_id=message.chat.id,
        message_id=main_message_id,
        reply_markup=group_management_keyboard(names),
    )
    await services.notification.notify_by_message(
        message=message, text=_("group_mgmt:ntf:created_success").format(name=name), duration=5
    )


# endregion


# region Rename group


@router.callback_query(F.data.startswith(NavAdminTools.RENAME_GROUP), IsAdmin())
async def callback_rename_group(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
) -> None:
    group = callback.data[len(NavAdminTools.RENAME_GROUP) + 1 :]
    logger.info(f"Admin {user.tg_id} started renaming group '{group}'.")
    await state.set_state(RenameGroupStates.name)
    await state.update_data(
        {MAIN_MESSAGE_ID_KEY: callback.message.message_id, INBOUND_GROUP_NAME_KEY: group}
    )
    await callback.message.edit_text(
        text=_("group_mgmt:message:enter_new_name").format(name=group),
        reply_markup=back_keyboard(NavAdminTools.SHOW_GROUP + f"_{group}"),
    )


@router.message(RenameGroupStates.name, IsAdmin())
async def message_rename_group(
    message: Message,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    new_name = (message.text or "").strip().lower()
    old_name = await state.get_value(INBOUND_GROUP_NAME_KEY)
    logger.info(f"Admin {user.tg_id} renames group '{old_name}' -> '{new_name}'.")

    error = await services.inbound_groups.rename_group_cascade(
        services.server_pool, old_name, new_name
    )
    if error:
        await services.notification.notify_by_message(
            message=message, text=_error_text(error), duration=5
        )
        return

    await state.set_state(None)
    main_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)
    names = sorted(await services.inbound_groups.known_groups())
    await message.bot.edit_message_text(
        text=_("group_mgmt:message:main"),
        chat_id=message.chat.id,
        message_id=main_message_id,
        reply_markup=group_management_keyboard(names),
    )
    await services.notification.notify_by_message(
        message=message,
        text=_("group_mgmt:ntf:renamed_success").format(old=old_name, new=new_name),
        duration=5,
    )


# endregion


# region Delete group


@router.callback_query(F.data.startswith(NavAdminTools.CONFIRM_DELETE_GROUP), IsAdmin())
async def callback_confirm_delete_group(
    callback: CallbackQuery,
    user: User,
) -> None:
    group = callback.data[len(NavAdminTools.CONFIRM_DELETE_GROUP) + 1 :]
    logger.info(f"Admin {user.tg_id} requested deletion of group '{group}'.")
    await callback.message.edit_text(
        text=_("group_mgmt:message:confirm_delete").format(name=group),
        reply_markup=confirm_delete_group_keyboard(group),
    )


@router.callback_query(F.data.startswith(NavAdminTools.DELETE_GROUP), IsAdmin())
async def callback_delete_group(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    group = callback.data[len(NavAdminTools.DELETE_GROUP) + 1 :]

    error = await services.inbound_groups.delete_group_guarded(services.server_pool, group)
    logger.info(f"Admin {user.tg_id} deleted group '{group}': {error or 'ok'}.")

    if error:
        await services.notification.show_popup(callback=callback, text=_error_text(error))
        return

    await callback_group_management(callback=callback, user=user, state=state, services=services)
    await services.notification.show_popup(
        callback=callback, text=_("group_mgmt:popup:deleted_success")
    )


# endregion


# region User groups (назначение набора юзеру)


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
    names = sorted(await services.inbound_groups.known_groups())
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
    tg_id_raw, group = payload.split("_")
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
