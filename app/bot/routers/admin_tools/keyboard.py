from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup
from aiogram.utils.i18n import gettext as _
from aiogram.utils.keyboard import InlineKeyboardBuilder

from app.bot.models.plan import Plan
from app.bot.routers.misc.keyboard import (
    back_button,
    back_to_main_menu_button,
    cancel_button,
)
from app.bot.utils.formatting import format_device_count
from app.bot.utils.navigation import NavAdminTools
from app.db.models import Server, User
from app.db.models.invite import Invite


def admin_tools_keyboard(is_dev: bool) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    if is_dev:
        builder.row(
            InlineKeyboardButton(
                text=_("admin_tools:button:server_management"),
                callback_data=NavAdminTools.SERVER_MANAGEMENT,
            )
        )

    builder.row(
        InlineKeyboardButton(
            text=_("admin_tools:button:statistics"),
            callback_data=NavAdminTools.STATISTICS,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("admin_tools:button:user_editor"),
            callback_data=NavAdminTools.USER_EDITOR,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("admin_tools:button:registration_requests"),
            callback_data=NavAdminTools.REGISTRATION_REQUESTS,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("admin_tools:button:invite_editor"),
            callback_data=NavAdminTools.INVITE_EDITOR,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("admin_tools:button:promocode_editor"),
            callback_data=NavAdminTools.PROMOCODE_EDITOR,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("admin_tools:button:plan_editor"),
            callback_data=NavAdminTools.PLAN_EDITOR,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("admin_tools:button:group_management"),
            callback_data=NavAdminTools.GROUP_MANAGEMENT,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("admin_tools:button:notification"),
            callback_data=NavAdminTools.NOTIFICATION,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("admin_tools:button:create_backup"),
            callback_data=NavAdminTools.CREATE_BACKUP,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("admin_tools:button:maintenance_mode"),
            callback_data=NavAdminTools.MAINTENANCE_MODE,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("admin_tools:button:restart_bot"),
            callback_data=NavAdminTools.RESTART_BOT,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("admin_tools:button:test_button"),
            callback_data=NavAdminTools.TEST,
        )
    )

    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def promocode_editor_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("promocode_editor:button:create"),
            callback_data=NavAdminTools.CREATE_PROMOCODE,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("promocode_editor:button:delete"),
            callback_data=NavAdminTools.DELETE_PROMOCODE,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("promocode_editor:button:edit"),
            callback_data=NavAdminTools.EDIT_PROMOCODE,
        )
    )

    builder.adjust(3)
    builder.row(back_button(NavAdminTools.MAIN))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def promocode_duration_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    duration_options = [1, 7, 30, 90, 365]

    for duration in duration_options:
        duration_text = _("1 day", "{} days", duration).format(duration)
        button = InlineKeyboardButton(
            text=duration_text,
            callback_data=f"{duration}",
        )
        builder.row(button)

    builder.adjust(2)
    builder.row(back_button(NavAdminTools.PROMOCODE_EDITOR))
    return builder.as_markup()


def maintenance_mode_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    from app.bot.middlewares import MaintenanceMiddleware

    if MaintenanceMiddleware.active:
        builder.row(
            InlineKeyboardButton(
                text=_("maintenance_mode:button:disable"),
                callback_data=NavAdminTools.MAINTENANCE_MODE_DISABLE,
            )
        )
    else:
        builder.row(
            InlineKeyboardButton(
                text=_("maintenance_mode:button:enable"),
                callback_data=NavAdminTools.MAINTENANCE_MODE_ENABLE,
            )
        )

    builder.adjust(2)
    builder.row(back_button(NavAdminTools.MAIN))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def servers_keyboard(servers: list) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.add(
        InlineKeyboardButton(
            text=_("server_management:button:sync"),
            callback_data=NavAdminTools.SYNC_SERVERS,
        )
    )

    builder.add(
        InlineKeyboardButton(
            text=_("server_management:button:add"),
            callback_data=NavAdminTools.ADD_SERVER,
        )
    )

    server: Server
    for server in servers:
        status = "🟢" if server.online else "🔴"
        builder.row(
            InlineKeyboardButton(
                text=f"{status} {server.name}",
                callback_data=NavAdminTools.SHOW_SERVER + f"_{server.name}",
            )
        )

    builder.row(back_button(NavAdminTools.MAIN))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def server_keyboard(server_name: str) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("server_management:button:ping"),
            callback_data=NavAdminTools.PING_SERVER + f"_{server_name}",
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("server_management:button:delete"),
            callback_data=NavAdminTools.DELETE_SERVER + f"_{server_name}",
        )
    )

    builder.adjust(2)
    builder.row(back_button(NavAdminTools.SERVER_MANAGEMENT))
    return builder.as_markup()


def confirm_add_server_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("server_management:button:confirm"),
            callback_data=NavAdminTools.СONFIRM_ADD_SERVER,
        )
    )

    builder.adjust(2)
    builder.row(back_button(NavAdminTools.ADD_SERVER_BACK))
    return builder.as_markup()


def notification_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("notification:button:send_to_user"),
            callback_data=NavAdminTools.SEND_NOTIFICATION_USER,
        ),
        InlineKeyboardButton(
            text=_("notification:button:send_to_all"),
            callback_data=NavAdminTools.SEND_NOTIFICATION_ALL,
        ),
    )

    builder.row(
        InlineKeyboardButton(
            text=_("notification:button:last_notification"),
            callback_data=NavAdminTools.LAST_NOTIFICATION,
        )
    )

    builder.row(back_button(NavAdminTools.MAIN))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def last_notification_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.add(
        InlineKeyboardButton(
            text=_("notification:button:edit"),
            callback_data=NavAdminTools.EDIT_NOTIFICATION,
        )
    )

    builder.add(
        InlineKeyboardButton(
            text=_("notification:button:delete"),
            callback_data=NavAdminTools.DELETE_NOTIFICATION,
        )
    )

    builder.row(back_button(NavAdminTools.NOTIFICATION))
    return builder.as_markup()


def confirm_send_notification_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("notification:button:confirm"),
            callback_data=NavAdminTools.CONFIRM_SEND_NOTIFICATION,
        )
    )
    builder.row(cancel_button(NavAdminTools.NOTIFICATION))
    return builder.as_markup()


def invite_editor_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("invite_editor:button:create_invite"),
            callback_data=NavAdminTools.CREATE_INVITE,
        )
    )

    builder.row(
        InlineKeyboardButton(
            text=_("invite_editor:button:list_invites"),
            callback_data=NavAdminTools.LIST_INVITES,
        )
    )

    builder.row(back_button(NavAdminTools.MAIN))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def invite_list_keyboard(
    invites: list[Invite], page: int = 0, limit: int = 5
) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    total_invites = len(invites)
    start_idx = page * limit
    end_idx = min(start_idx + limit, total_invites)

    for i in range(start_idx, end_idx):
        invite = invites[i]
        builder.row(
            InlineKeyboardButton(
                text=f"{invite.name} ({invite.clicks} clicks)",
                callback_data=NavAdminTools.SHOW_INVITE_DETAILS + f"_{invite.id}",
            )
        )

    row = []
    if page > 0:
        row.append(
            InlineKeyboardButton(
                text=_("invite_editor:button:previous_page"),
                callback_data=NavAdminTools.SHOW_INVITE_PAGE + f"_{page-1}",
            )
        )

    if (page + 1) * limit < total_invites:
        row.append(
            InlineKeyboardButton(
                text=_("invite_editor:button:next_page"),
                callback_data=NavAdminTools.SHOW_INVITE_PAGE + f"_{page+1}",
            )
        )

    if row:
        builder.row(*row)

    builder.row(back_button(NavAdminTools.INVITE_EDITOR))

    return builder.as_markup()


def invite_details_keyboard(invite: Invite) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    if invite.is_active:
        builder.row(
            InlineKeyboardButton(
                text=_("invite_editor:button:disable"),
                callback_data=NavAdminTools.TOGGLE_INVITE_STATUS + f"_{invite.id}",
            )
        )
    else:
        builder.row(
            InlineKeyboardButton(
                text=_("invite_editor:button:enable"),
                callback_data=NavAdminTools.TOGGLE_INVITE_STATUS + f"_{invite.id}",
            )
        )

    builder.row(
        InlineKeyboardButton(
            text=_("invite_editor:button:delete"),
            callback_data=NavAdminTools.CONFIRM_DELETE_INVITE + f"_{invite.id}",
        )
    )

    builder.row(back_button(NavAdminTools.LIST_INVITES))

    return builder.as_markup()


def confirm_delete_invite_keyboard(invite_id: int) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("invite_editor:button:confirm_delete"),
            callback_data=NavAdminTools.DELETE_INVITE + f"_{invite_id}",
        ),
    )
    builder.row(cancel_button(NavAdminTools.SHOW_INVITE_DETAILS + f"_{invite_id}"))
    return builder.as_markup()


def plan_editor_keyboard(plans: list[Plan]) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    for plan in plans:
        # Скрытый (невыкупаемый) тариф помечаем 🔒, чтобы админ не путал его с обычным.
        prefix = "🔒" if plan.hidden else "📱"
        builder.row(
            InlineKeyboardButton(
                text=f"{prefix} {format_device_count(plan.devices)}",
                callback_data=NavAdminTools.SHOW_PLAN + f"_{plan.devices}",
            )
        )

    builder.row(
        InlineKeyboardButton(
            text=_("plan_editor:button:create"),
            callback_data=NavAdminTools.CREATE_PLAN,
        )
    )
    builder.row(back_button(NavAdminTools.MAIN))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def plan_details_keyboard(devices: int) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("plan_editor:button:edit_prices"),
            callback_data=NavAdminTools.EDIT_PLAN_PRICES + f"_{devices}",
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("plan_editor:button:edit_traffic"),
            callback_data=NavAdminTools.EDIT_PLAN_TRAFFIC + f"_{devices}",
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("plan_editor:button:delete"),
            callback_data=NavAdminTools.CONFIRM_DELETE_PLAN + f"_{devices}",
        )
    )
    builder.row(back_button(NavAdminTools.PLAN_EDITOR))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def confirm_create_plan_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("plan_editor:button:confirm_create"),
            callback_data=NavAdminTools.CONFIRM_CREATE_PLAN,
        )
    )
    builder.row(cancel_button(NavAdminTools.PLAN_EDITOR))
    return builder.as_markup()


def confirm_delete_plan_keyboard(devices: int) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("plan_editor:button:confirm_delete"),
            callback_data=NavAdminTools.DELETE_PLAN + f"_{devices}",
        )
    )
    builder.row(cancel_button(NavAdminTools.SHOW_PLAN + f"_{devices}"))
    return builder.as_markup()


def group_management_keyboard() -> InlineKeyboardMarkup:
    """Обзор групп read-only: группы создаются/редактируются в панели 3x-ui,
    из бота управляется только связка пользователь<->группы."""
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("group_mgmt:button:user_groups"),
            callback_data=NavAdminTools.USER_GROUPS,
        )
    )
    builder.row(back_button(NavAdminTools.MAIN))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def user_groups_users_keyboard(
    users: list[User], page: int = 0, limit: int = 8
) -> InlineKeyboardMarkup:
    """Пагинированный выбор пользователя для редактирования его групп."""
    builder = InlineKeyboardBuilder()
    total_users = len(users)
    start_idx = page * limit
    end_idx = min(start_idx + limit, total_users)

    for i in range(start_idx, end_idx):
        target = users[i]
        label = (
            f"{target.first_name} (@{target.username})"
            if target.username
            else f"{target.first_name} ({target.tg_id})"
        )
        builder.row(
            InlineKeyboardButton(
                text=label,
                callback_data=NavAdminTools.PICK_USER_GROUPS + f"_{target.tg_id}",
            )
        )

    row = []
    if page > 0:
        row.append(
            InlineKeyboardButton(
                text=_("group_mgmt:button:previous_page"),
                callback_data=NavAdminTools.USER_GROUPS_PAGE + f"_{page-1}",
            )
        )
    if (page + 1) * limit < total_users:
        row.append(
            InlineKeyboardButton(
                text=_("group_mgmt:button:next_page"),
                callback_data=NavAdminTools.USER_GROUPS_PAGE + f"_{page+1}",
            )
        )
    if row:
        builder.row(*row)

    builder.row(back_button(NavAdminTools.GROUP_MANAGEMENT))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def user_groups_keyboard(
    tg_id: int, group_names: list[str], member: set[str]
) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    for name in group_names:
        mark = "✅" if name in member else "⬜️"
        builder.row(
            InlineKeyboardButton(
                text=f"{mark} {name}",
                callback_data=NavAdminTools.TOGGLE_USER_GROUP + f"_{tg_id}_{name}",
            )
        )

    # Назад — к списку выбора пользователя (а не в обзор групп).
    builder.row(back_button(NavAdminTools.USER_GROUPS))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def registration_requests_keyboard() -> InlineKeyboardMarkup:
    """Раздел «Запросы на регистрацию»: заявки в ожидании и уже отклонённые."""
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("registration_requests:button:pending"),
            callback_data=NavAdminTools.PENDING_USERS,
        )
    )
    builder.row(
        InlineKeyboardButton(
            text=_("registration_requests:button:rejected"),
            callback_data=NavAdminTools.REJECTED_USERS,
        )
    )
    builder.row(back_button(NavAdminTools.MAIN))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def pending_users_keyboard(
    users: list[User], page: int = 0, limit: int = 5
) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    total_users = len(users)
    start_idx = page * limit
    end_idx = min(start_idx + limit, total_users)

    for i in range(start_idx, end_idx):
        pending_user = users[i]
        label = (
            f"{pending_user.first_name} (@{pending_user.username})"
            if pending_user.username
            else f"{pending_user.first_name} ({pending_user.tg_id})"
        )
        builder.row(
            InlineKeyboardButton(
                text=label,
                callback_data=NavAdminTools.SHOW_PENDING_DETAILS + f"_{pending_user.tg_id}",
            )
        )

    row = []
    if page > 0:
        row.append(
            InlineKeyboardButton(
                text=_("pending_users:button:previous_page"),
                callback_data=NavAdminTools.SHOW_PENDING_PAGE + f"_{page-1}",
            )
        )

    if (page + 1) * limit < total_users:
        row.append(
            InlineKeyboardButton(
                text=_("pending_users:button:next_page"),
                callback_data=NavAdminTools.SHOW_PENDING_PAGE + f"_{page+1}",
            )
        )

    if row:
        builder.row(*row)

    builder.row(back_button(NavAdminTools.REGISTRATION_REQUESTS))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def pending_user_details_keyboard(tg_id: int) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("approval:button:approve"),
            callback_data=NavAdminTools.PENDING_APPROVE + f"_{tg_id}",
        ),
        InlineKeyboardButton(
            text=_("approval:button:reject"),
            callback_data=NavAdminTools.PENDING_REJECT + f"_{tg_id}",
        ),
    )
    builder.row(back_button(NavAdminTools.PENDING_USERS))
    return builder.as_markup()


def rejected_users_keyboard(
    users: list[User], page: int = 0, limit: int = 5
) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    total_users = len(users)
    start_idx = page * limit
    end_idx = min(start_idx + limit, total_users)

    for i in range(start_idx, end_idx):
        rejected_user = users[i]
        label = (
            f"{rejected_user.first_name} (@{rejected_user.username})"
            if rejected_user.username
            else f"{rejected_user.first_name} ({rejected_user.tg_id})"
        )
        builder.row(
            InlineKeyboardButton(
                text=label,
                callback_data=NavAdminTools.SHOW_REJECTED_DETAILS + f"_{rejected_user.tg_id}",
            )
        )

    row = []
    if page > 0:
        row.append(
            InlineKeyboardButton(
                text=_("rejected_users:button:previous_page"),
                callback_data=NavAdminTools.SHOW_REJECTED_PAGE + f"_{page-1}",
            )
        )

    if (page + 1) * limit < total_users:
        row.append(
            InlineKeyboardButton(
                text=_("rejected_users:button:next_page"),
                callback_data=NavAdminTools.SHOW_REJECTED_PAGE + f"_{page+1}",
            )
        )

    if row:
        builder.row(*row)

    builder.row(back_button(NavAdminTools.REGISTRATION_REQUESTS))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()


def rejected_user_details_keyboard(tg_id: int) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("rejected_users:button:unreject"),
            callback_data=NavAdminTools.CONFIRM_UNREJECT_USER + f"_{tg_id}",
        )
    )
    builder.row(back_button(NavAdminTools.REJECTED_USERS))
    return builder.as_markup()


def confirm_unreject_user_keyboard(tg_id: int) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("rejected_users:button:confirm_unreject"),
            callback_data=NavAdminTools.UNREJECT_USER + f"_{tg_id}",
        )
    )
    builder.row(cancel_button(NavAdminTools.SHOW_REJECTED_DETAILS + f"_{tg_id}"))
    return builder.as_markup()


def statistics_keyboard() -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()

    builder.row(
        InlineKeyboardButton(
            text=_("statistics:button:refresh"),
            callback_data=NavAdminTools.STATISTICS,
        )
    )
    builder.row(back_button(NavAdminTools.MAIN))
    builder.row(back_to_main_menu_button())
    return builder.as_markup()
