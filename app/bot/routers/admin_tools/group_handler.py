import logging

from aiogram import F, Router
from aiogram.fsm.context import FSMContext
from aiogram.types import CallbackQuery
from aiogram.utils.i18n import gettext as _
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.filters import IsAdmin
from app.bot.models import ServicesContainer
from app.bot.services.inbound_groups import EmptyInboundSetError
from app.bot.utils.constants import UNLIMITED_INBOUND_GROUP
from app.bot.utils.navigation import NavAdminTools
from app.db.models import User

from .keyboard import (
    group_management_keyboard,
    user_groups_keyboard,
    user_groups_users_keyboard,
)

logger = logging.getLogger(__name__)
router = Router(name=__name__)


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
    session: AsyncSession,
    state: FSMContext,
) -> None:
    logger.info(f"Admin {user.tg_id} opened the user-groups picker.")
    await state.set_state(None)
    users = await User.get_all(session=session)
    await callback.message.edit_text(
        text=_("group_mgmt:message:select_user"),
        reply_markup=user_groups_users_keyboard(users, page=0),
    )


@router.callback_query(F.data.startswith(NavAdminTools.USER_GROUPS_PAGE), IsAdmin())
async def callback_user_groups_page(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
) -> None:
    page = int(callback.data.rsplit("_", 1)[-1])
    users = await User.get_all(session=session)
    await callback.message.edit_text(
        text=_("group_mgmt:message:select_user"),
        reply_markup=user_groups_users_keyboard(users, page=page),
    )


async def _render_user_groups(
    services: ServicesContainer, target: User
) -> tuple[str, object]:
    names = sorted(await services.inbound_groups.known_groups_union(services.server_pool))
    member = set(services.inbound_groups.effective_groups(target))
    text = _("group_mgmt:message:user_groups").format(
        tg_id=target.tg_id, groups=", ".join(sorted(member)) or "—"
    )
    if services.inbound_groups.is_banned(target):
        text += _("group_mgmt:message:user_banned")
    return text, user_groups_keyboard(target.tg_id, names, member)


@router.callback_query(F.data.startswith(NavAdminTools.PICK_USER_GROUPS), IsAdmin())
async def callback_pick_user_groups(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    tg_id = int(callback.data.rsplit("_", 1)[-1])
    logger.info(f"Admin {user.tg_id} picked user {tg_id} for group editing.")

    target = await User.get(session=session, tg_id=tg_id)
    if target is None:
        await services.notification.show_popup(
            callback=callback, text=_("group_mgmt:ntf:user_not_found")
        )
        return

    text, keyboard = await _render_user_groups(services, target)
    await callback.message.edit_text(text=text, reply_markup=keyboard)


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

    # Спец-группа `unlimited` — не обычный attach/detach, а смена тарифного состояния:
    #   • включение -> грант скрытого безлимит-плана (7 устройств, 100ГБ-кап, бессрочно);
    #   • снятие    -> откат на СТАРТОВЫЙ ТРИАЛ (regular, TRIAL_PERIOD дней, BONUS_DEVICES_COUNT).
    # Обрабатываем ДО проверки «не до нуля»: итоговый набор задаёт сам grant/revoke
    # (а не new_groups), поэтому снятие даже единственной группы unlimited корректно.
    if group == UNLIMITED_INBOUND_GROUP:
        if group not in current:
            logger.info(f"Admin {user.tg_id} grants unlimited plan to user {target.tg_id}.")
            ok = await services.vpn.grant_unlimited(target)
            fail_key = "group_mgmt:popup:unlimited_failed"
        else:
            logger.info(
                f"Admin {user.tg_id} revokes unlimited (-> starter trial) for user {target.tg_id}."
            )
            ok = await services.vpn.revoke_unlimited(target)
            fail_key = "group_mgmt:popup:unlimited_revoke_failed"
        if not ok:
            await services.notification.show_popup(callback=callback, text=_(fail_key))
            return
        # grant/revoke сами записали набор групп (и, при гранте, сервер) — перечитываем.
        refreshed = await User.get(session=session, tg_id=target.tg_id)
        text, keyboard = await _render_user_groups(services, refreshed)
        await callback.message.edit_text(text=text, reply_markup=keyboard)
        return

    new_groups = sorted(current - {group} if group in current else current | {group})
    if not services.inbound_groups.access_groups(new_groups):
        # Политика «не до нуля»: минимум одна группа помимо banned — иначе после
        # разбана нечего восстанавливать.
        await services.notification.show_popup(
            callback=callback, text=_("group_mgmt:popup:empty_set_refused")
        )
        return

    logger.info(f"Admin {user.tg_id} sets groups {new_groups} for user {target.tg_id}.")

    if target.server_id:
        # Немедленная сходимость: attach/detach + бан/разбан + запись набора + метка.
        # enforce_enable=True: явное действие админа — единственный путь разбана.
        try:
            applied = await services.vpn.apply_inbound_groups(
                target, groups=new_groups, enforce_enable=True
            )
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
