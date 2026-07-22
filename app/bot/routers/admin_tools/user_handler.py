"""Раздел «Пользователи»: список -> карточка -> действия.

Карточка агрегирует БД + live-данные панели (общий рендерер app/bot/utils/user_card.py,
он же используется /info support-бота). Действия: начислить дни (компенсация через
process_bonus_days — тот же примитив, что у промокодов), бан/разбан (тумблер группы
banned), сброс трафика, прыжки в существующие флоу групп (PICK_USER_GROUPS) и
уведомлений (MESSAGE_USER -> NotificationStates.message_to_user).
"""

import html
import logging

from aiogram import F, Router
from aiogram.fsm.context import FSMContext
from aiogram.fsm.state import State, StatesGroup
from aiogram.types import CallbackQuery, InlineKeyboardMarkup, Message
from aiogram.utils.i18n import I18n
from aiogram.utils.i18n import gettext as _
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.filters import IsAdmin
from app.bot.models import ServicesContainer
from app.bot.payment_gateways import GatewayFactory
from app.bot.routers.misc.keyboard import back_keyboard
from app.bot.services.audit import AuditActor, format_audit_entry
from app.bot.services.inbound_groups import EmptyInboundSetError
from app.bot.services.subscription import AdminTrialStatus
from app.bot.utils.constants import (
    BANNED_INBOUND_GROUP,
    DEFAULT_LANGUAGE,
    MAIN_MESSAGE_ID_KEY,
    NOTIFICATION_PENDING_CHAT_IDS_KEY,
    NOTIFICATION_RETURN_TO_KEY,
)
from app.bot.utils.navigation import NavAdminTools
from app.bot.utils.stars import cancel_stars_auto_renew
from app.bot.utils.user_card import build_user_card
from app.config import Config
from app.db.models import AuditLog, SupportTicket, User

from .keyboard import (
    user_ban_confirm_keyboard,
    user_card_keyboard,
    user_create_trial_confirm_keyboard,
    user_editor_users_keyboard,
    user_extend_confirm_keyboard,
    user_history_keyboard,
    user_reset_traffic_confirm_keyboard,
)
from .notification_handler import NotificationStates

logger = logging.getLogger(__name__)
router = Router(name=__name__)

MAX_EXTEND_DAYS = 365
MAX_TELEGRAM_ID = 2**63 - 1
MAX_CLIENT_NAME_LENGTH = 32

# FSM-данные флоу продления (одноимённый модуль — ключи локальные).
USER_EDITOR_TARGET_KEY = "user_editor_target"
USER_EDITOR_DAYS_KEY = "user_editor_days"
NEW_CLIENT_TG_ID_KEY = "new_client_tg_id"
NEW_CLIENT_NAME_KEY = "new_client_name"


class UserEditorStates(StatesGroup):
    extend_days = State()
    create_trial_tg_id = State()
    create_trial_name = State()
    confirm_create_trial = State()


def _tg_id_from(callback: CallbackQuery) -> int:
    return int(callback.data.rsplit("_", 1)[-1])


def parse_new_client_tg_id(raw: str) -> int | None:
    value = raw.strip()
    if not value.isdigit():
        return None
    tg_id = int(value)
    return tg_id if 1 <= tg_id <= MAX_TELEGRAM_ID else None


def normalize_new_client_name(raw: str) -> str | None:
    value = raw.strip()
    return value if 1 <= len(value) <= MAX_CLIENT_NAME_LENGTH else None


async def _get_target(
    callback: CallbackQuery,
    session: AsyncSession,
    services: ServicesContainer,
    tg_id: int,
) -> User | None:
    target = await User.get(session=session, tg_id=tg_id)
    if target is None:
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:user_not_found")
        )
    return target


async def _render_card(
    session: AsyncSession,
    services: ServicesContainer,
    gateway_factory: GatewayFactory,
    config: Config,
    target: User,
) -> tuple[str, InlineKeyboardMarkup]:
    payment_method_currencies = {
        gateway.callback: gateway.currency.code for gateway in gateway_factory.get_gateways()
    }
    text, client_data = await build_user_card(
        target=target,
        session=session,
        services=services,
        payment_method_currencies=payment_method_currencies,
    )

    # Прямая ссылка в персональный топик юзера в группе поддержки (если фича включена
    # и топик уже создан). Супергруппа -100XXXXXXXXXX -> t.me/c/XXXXXXXXXX/{thread_id}.
    topic_url = None
    if config.bot.SUPPORT_GROUP_ID:
        ticket = await SupportTicket.get_by_tg_id(session=session, tg_id=target.tg_id)
        if ticket and ticket.thread_id:
            internal_id = str(config.bot.SUPPORT_GROUP_ID).removeprefix("-100")
            topic_url = f"https://t.me/c/{internal_id}/{ticket.thread_id}"

    keyboard = user_card_keyboard(
        target.tg_id,
        is_banned=services.inbound_groups.is_banned(target),
        show_reset_traffic=bool(client_data and client_data.has_traffic_exhausted),
        show_subscription_key=bool(target.server and target.server.subscription_url),
        topic_url=topic_url,
    )
    return text, keyboard


async def _show_card(
    callback: CallbackQuery,
    session: AsyncSession,
    services: ServicesContainer,
    gateway_factory: GatewayFactory,
    config: Config,
    target: User,
) -> None:
    text, keyboard = await _render_card(session, services, gateway_factory, config, target)
    await callback.message.edit_text(text=text, reply_markup=keyboard)


# region: список


@router.callback_query(F.data == NavAdminTools.USER_EDITOR, IsAdmin())
async def callback_user_editor(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    state: FSMContext,
) -> None:
    logger.info(f"Admin {user.tg_id} opened user editor.")
    await state.set_state(None)
    users = await User.get_all(session=session)
    await callback.message.edit_text(
        text=_("user_editor:message:select_user"),
        reply_markup=user_editor_users_keyboard(users, page=0),
    )


@router.callback_query(F.data.startswith(NavAdminTools.USER_EDITOR_PAGE), IsAdmin())
async def callback_user_editor_page(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
) -> None:
    page = int(callback.data.rsplit("_", 1)[-1])
    users = await User.get_all(session=session)
    await callback.message.edit_text(
        text=_("user_editor:message:select_user"),
        reply_markup=user_editor_users_keyboard(users, page=page),
    )


# endregion


# region: создание клиента с триалом


@router.callback_query(F.data == NavAdminTools.CREATE_TRIAL_CLIENT, IsAdmin())
async def callback_create_trial_client(
    callback: CallbackQuery,
    user: User,
    state: FSMContext,
) -> None:
    logger.info(f"Admin {user.tg_id} started creating a trial client.")
    await state.set_state(UserEditorStates.create_trial_tg_id)
    await state.update_data({NEW_CLIENT_TG_ID_KEY: None, NEW_CLIENT_NAME_KEY: None})
    await callback.message.edit_text(
        text=_("user_editor:message:create_trial_client_tg_id"),
        reply_markup=back_keyboard(NavAdminTools.USER_EDITOR),
    )


@router.message(UserEditorStates.create_trial_tg_id, IsAdmin())
async def message_create_trial_client_tg_id(
    message: Message,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    tg_id = parse_new_client_tg_id(message.text or "")
    if tg_id is None:
        await services.notification.notify_by_message(
            message=message,
            text=_("user_editor:ntf:invalid_client_tg_id"),
            duration=5,
        )
        return

    await state.set_state(UserEditorStates.create_trial_name)
    await state.update_data({NEW_CLIENT_TG_ID_KEY: tg_id})
    main_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)
    await message.bot.edit_message_text(
        text=_("user_editor:message:create_trial_client_name").format(tg_id=tg_id),
        chat_id=message.chat.id,
        message_id=main_message_id,
        reply_markup=back_keyboard(NavAdminTools.USER_EDITOR),
    )


@router.message(UserEditorStates.create_trial_name, IsAdmin())
async def message_create_trial_client_name(
    message: Message,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    name = normalize_new_client_name(message.text or "")
    if name is None:
        await services.notification.notify_by_message(
            message=message,
            text=_("user_editor:ntf:invalid_client_name").format(max_length=MAX_CLIENT_NAME_LENGTH),
            duration=5,
        )
        return

    tg_id = await state.get_value(NEW_CLIENT_TG_ID_KEY)
    if not isinstance(tg_id, int):
        await state.set_state(None)
        await services.notification.notify_by_message(
            message=message,
            text=_("user_editor:popup:create_trial_client_stale"),
            duration=5,
        )
        return

    await state.set_state(UserEditorStates.confirm_create_trial)
    await state.update_data({NEW_CLIENT_NAME_KEY: name})
    main_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)
    await message.bot.edit_message_text(
        text=_("user_editor:message:confirm_create_trial_client").format(
            name=html.escape(name), tg_id=tg_id
        ),
        chat_id=message.chat.id,
        message_id=main_message_id,
        reply_markup=user_create_trial_confirm_keyboard(),
    )


@router.callback_query(
    F.data == NavAdminTools.CONFIRM_CREATE_TRIAL_CLIENT,
    UserEditorStates.confirm_create_trial,
    IsAdmin(),
)
async def callback_confirm_create_trial_client(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    state: FSMContext,
    services: ServicesContainer,
    gateway_factory: GatewayFactory,
    config: Config,
) -> None:
    tg_id = await state.get_value(NEW_CLIENT_TG_ID_KEY)
    name = await state.get_value(NEW_CLIENT_NAME_KEY)
    if not isinstance(tg_id, int) or not isinstance(name, str):
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:create_trial_client_stale")
        )
        return

    # Съедаем состояние перед сетевым provisioning: второй тап должен попасть в
    # fallback, а не создать клиент дважды.
    await state.set_state(None)
    await state.update_data({NEW_CLIENT_TG_ID_KEY: None, NEW_CLIENT_NAME_KEY: None})

    try:
        result = await services.subscription.create_admin_trial(
            tg_id, name, approved_by=user.tg_id
        )
    except Exception as exception:  # noqa: BLE001 — сбой пула/БД виден администратору
        logger.error(f"Admin {user.tg_id} failed to create trial client {tg_id}: {exception}")
        result = None

    if result is None or result.status is AdminTrialStatus.PROVISION_FAILED:
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:create_trial_client_failed")
        )
        return
    if result.status is AdminTrialStatus.ALREADY_EXISTS:
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:create_trial_client_exists")
        )
        return
    if result.status is AdminTrialStatus.TRIAL_DISABLED:
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:create_trial_client_trial_disabled")
        )
        return
    if result.status is AdminTrialStatus.NO_SERVER:
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:no_server")
        )
        return

    target = result.user
    if target is None:
        logger.critical(f"Admin trial result {result.status} for {tg_id} has no user.")
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:create_trial_client_failed")
        )
        return

    if result.status is AdminTrialStatus.PARTIAL_PROVISION:
        refreshed = await User.get(session=session, tg_id=tg_id)
        if refreshed:
            await _show_card(callback, session, services, gateway_factory, config, refreshed)
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:create_trial_client_partial")
        )
        return

    await services.audit.admin_trial_created(
        AuditActor.admin(callback.from_user),
        target,
        duration=config.shop.TRIAL_PERIOD,
        devices=config.shop.BONUS_DEVICES_COUNT,
        traffic_gb=config.shop.TRIAL_TRAFFIC_GB,
    )
    try:
        key = await services.vpn.get_key(target)
    except Exception as exception:  # noqa: BLE001 — доступ уже создан, ключ можно взять из карточки
        logger.error(f"Failed to get subscription key for new client {tg_id}: {exception}")
        key = None

    if key:
        text = _("user_editor:message:create_trial_client_success").format(
            name=html.escape(name), tg_id=tg_id, key=html.escape(key)
        )
    else:
        text = _("user_editor:message:create_trial_client_success_without_key").format(
            name=html.escape(name), tg_id=tg_id
        )
    await callback.message.edit_text(
        text=text,
        reply_markup=back_keyboard(NavAdminTools.SHOW_USER + f"_{tg_id}"),
    )


@router.callback_query(F.data == NavAdminTools.CONFIRM_CREATE_TRIAL_CLIENT, IsAdmin())
async def callback_confirm_create_trial_client_stale(
    callback: CallbackQuery,
    services: ServicesContainer,
) -> None:
    await services.notification.show_popup(
        callback=callback, text=_("user_editor:popup:create_trial_client_stale")
    )


# endregion


# region: карточка


@router.callback_query(F.data.startswith(NavAdminTools.SHOW_USER), IsAdmin())
async def callback_show_user(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    state: FSMContext,
    services: ServicesContainer,
    gateway_factory: GatewayFactory,
    config: Config,
) -> None:
    tg_id = _tg_id_from(callback)
    logger.info(f"Admin {user.tg_id} opened card of user {tg_id}.")
    await state.set_state(None)

    target = await _get_target(callback, session, services, tg_id)
    if target is None:
        return
    await _show_card(callback, session, services, gateway_factory, config, target)


@router.callback_query(F.data.startswith(NavAdminTools.SHOW_USER_KEY), IsAdmin())
async def callback_show_user_key(
    callback: CallbackQuery,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    tg_id = _tg_id_from(callback)
    target = await _get_target(callback, session, services, tg_id)
    if target is None:
        return
    try:
        key = await services.vpn.get_key(target)
    except Exception as exception:  # noqa: BLE001 — карточка не должна падать из-за URL
        logger.error(f"Failed to get subscription key for {tg_id}: {exception}")
        key = None
    if not key:
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:subscription_key_unavailable")
        )
        return
    await callback.message.edit_text(
        text=_("user_editor:message:subscription_key").format(
            name=html.escape(target.first_name), tg_id=tg_id, key=html.escape(key)
        ),
        reply_markup=back_keyboard(NavAdminTools.SHOW_USER + f"_{tg_id}"),
    )


# endregion


# region: продление (компенсация)


@router.callback_query(F.data.startswith(NavAdminTools.EXTEND_USER), IsAdmin())
async def callback_extend_user(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    tg_id = _tg_id_from(callback)
    target = await _get_target(callback, session, services, tg_id)
    if target is None:
        return

    # Гварды компенсации: у безлимита дни превратили бы бессрочный expiryTime=0
    # в конечную дату; забаненному дни тихо сгорали бы при выключенном клиенте.
    if services.inbound_groups.is_unlimited(target):
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:unlimited_refused")
        )
        return
    if services.inbound_groups.is_banned(target):
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:banned_refused")
        )
        return

    logger.info(f"Admin {user.tg_id} started extending subscription of user {tg_id}.")
    await state.set_state(UserEditorStates.extend_days)
    await state.update_data({USER_EDITOR_TARGET_KEY: tg_id, USER_EDITOR_DAYS_KEY: None})
    await callback.message.edit_text(
        text=_("user_editor:message:extend_prompt").format(
            name=html.escape(target.first_name), tg_id=tg_id, max_days=MAX_EXTEND_DAYS
        ),
        reply_markup=back_keyboard(NavAdminTools.SHOW_USER + f"_{tg_id}"),
    )


@router.message(UserEditorStates.extend_days)
async def message_extend_days(
    message: Message,
    user: User,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    raw = (message.text or "").strip()
    if not raw.isdigit() or not 1 <= int(raw) <= MAX_EXTEND_DAYS:
        await services.notification.notify_by_message(
            message=message,
            text=_("user_editor:ntf:invalid_days").format(max_days=MAX_EXTEND_DAYS),
            duration=5,
        )
        return

    days = int(raw)
    tg_id = await state.get_value(USER_EDITOR_TARGET_KEY)
    await state.update_data({USER_EDITOR_DAYS_KEY: days})
    logger.info(f"Admin {user.tg_id} entered {days} bonus days for user {tg_id}.")

    main_message_id = await state.get_value(MAIN_MESSAGE_ID_KEY)
    await message.bot.edit_message_text(
        text=_("user_editor:message:extend_confirm").format(days=days, tg_id=tg_id),
        chat_id=message.chat.id,
        message_id=main_message_id,
        reply_markup=user_extend_confirm_keyboard(tg_id),
    )


@router.callback_query(
    F.data.startswith(NavAdminTools.CONFIRM_EXTEND_USER),
    UserEditorStates.extend_days,
    IsAdmin(),
)
async def callback_confirm_extend_user(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    state: FSMContext,
    services: ServicesContainer,
    gateway_factory: GatewayFactory,
    config: Config,
    i18n: I18n,
) -> None:
    tg_id = _tg_id_from(callback)
    days = await state.get_value(USER_EDITOR_DAYS_KEY)
    stored_tg_id = await state.get_value(USER_EDITOR_TARGET_KEY)

    if not days or stored_tg_id != tg_id:
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:extend_expired")
        )
        return

    target = await _get_target(callback, session, services, tg_id)
    if target is None:
        return

    # Гварды на свежих данных панели/БД (unlimited/banned могли смениться, пока
    # админ вводил число; perpetual/недоступный сервер на входе не проверялись).
    blocker = await services.vpn.compensation_blocker(target)
    if blocker in ("unlimited", "banned"):
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:extend_expired")
        )
        return
    if blocker == "perpetual":
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:unlimited_refused")
        )
        return
    if blocker == "server_unreachable":
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:server_unreachable")
        )
        return
    if blocker == "no_server":
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:no_server")
        )
        return

    # Идемпотентность: состояние потребляется ДО похода в панель — апдейты
    # обрабатываются конкурентно, и двойной тап иначе начислил бы дни дважды.
    # Второй тап уходит в fallback-хендлер ниже (попап «флоу устарел»).
    await state.set_state(None)
    await state.update_data({USER_EDITOR_DAYS_KEY: None})

    logger.info(f"Admin {user.tg_id} grants {days} bonus days to user {tg_id}.")
    try:
        granted = await services.vpn.process_bonus_days(
            user=target,
            duration=days,
            devices=config.shop.BONUS_DEVICES_COUNT,
        )
    except Exception as exception:  # noqa: BLE001 — админ-флоу: любой сбой -> видимый отказ
        logger.error(f"Extend {days}d for {tg_id} failed: {exception}")
        granted = False

    if not granted:
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:extend_failed")
        )
        return

    await services.audit.compensation(AuditActor.admin(callback.from_user), target, days)

    # Юзеру — уведомление в ЕГО локали через основного бота (как ApprovalService).
    locale = (
        target.language_code
        if target.language_code in i18n.available_locales
        else DEFAULT_LANGUAGE
    )
    with i18n.use_locale(locale):
        user_text = _("compensation:user:granted").format(days=days)
    await services.notification.notify_by_id(chat_id=target.tg_id, text=user_text)

    # Свежая карточка + подсказка про исчерпанный трафик: дни начислены, но доступ
    # появится только после сброса счётчика (кнопка на карточке).
    refreshed = await User.get(session=session, tg_id=tg_id)
    text, keyboard = await _render_card(session, services, gateway_factory, config, refreshed)
    await callback.message.edit_text(text=text, reply_markup=keyboard)

    client_data = await services.vpn.get_client_data(refreshed) if refreshed.server_id else None
    if client_data and client_data.has_traffic_exhausted:
        popup_text = _("user_editor:popup:extend_success_traffic_exhausted").format(days=days)
    else:
        popup_text = _("user_editor:popup:extend_success").format(days=days)
    await services.notification.show_popup(callback=callback, text=popup_text)


@router.callback_query(F.data.startswith(NavAdminTools.CONFIRM_EXTEND_USER), IsAdmin())
async def callback_confirm_extend_stale(
    callback: CallbackQuery,
    user: User,
    services: ServicesContainer,
) -> None:
    # Fallback без state-фильтра (регистрируется ПОСЛЕ основного): тап по confirm
    # вне флоу продления — двойной тап или протухший экран — получает попап, а не тишину.
    await services.notification.show_popup(
        callback=callback, text=_("user_editor:popup:extend_expired")
    )


# endregion


# region: бан/разбан


@router.callback_query(F.data.startswith(NavAdminTools.CONFIRM_BAN_USER), IsAdmin())
async def callback_confirm_ban_user(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
    gateway_factory: GatewayFactory,
    config: Config,
) -> None:
    # cnf_ban_user_{tg_id}_{want}: want — ЦЕЛЕВОЕ состояние бана, зашитое при рендере
    # подтверждения. Не тумблер: двойной тап/протухший экран не разворачивают действие.
    payload = callback.data[len(NavAdminTools.CONFIRM_BAN_USER) + 1 :]
    tg_id_raw, want_raw = payload.split("_", 1)
    tg_id, want_banned = int(tg_id_raw), bool(int(want_raw))

    target = await _get_target(callback, session, services, tg_id)
    if target is None:
        return

    current = set(services.inbound_groups.effective_groups(target))
    banning = want_banned
    if (BANNED_INBOUND_GROUP in current) == want_banned:
        # Состояние уже такое, как просили (двойной тап/другая поверхность успела).
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:ban_stale")
        )
        await _show_card(callback, session, services, gateway_factory, config, target)
        return
    new_groups = sorted(
        current | {BANNED_INBOUND_GROUP} if banning else current - {BANNED_INBOUND_GROUP}
    )

    # Политика «не до нуля» (как в редакторе групп): помимо banned должна
    # оставаться хотя бы одна группа — иначе после разбана нечего восстанавливать.
    if not services.inbound_groups.access_groups(new_groups):
        await services.notification.show_popup(
            callback=callback, text=_("group_mgmt:popup:empty_set_refused")
        )
        return

    logger.info(
        f"Admin {user.tg_id} {'bans' if banning else 'unbans'} user {tg_id} "
        f"(groups -> {new_groups})."
    )

    # Решение должно долететь до панели: клиент мог существовать без привязки в БД
    # (усыновляется reconcile'ом, мутирует target) — иначе бан остался бы на бумаге,
    # а живой клиент панели продолжил бы работать.
    if not target.server_id:
        await services.vpn.reconcile_from_panel(target)

    if target.server_id:
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

    # Бан = стоп-продление: активный Stars-рекуррент отменяем, иначе Telegram
    # продолжит списывать звёзды за выключенный VPN (зеркало reject-а заявки).
    if banning:
        await cancel_stars_auto_renew(callback.bot, session, target, reason="banned by admin")

    actor = AuditActor.admin(callback.from_user)
    before = sorted(current)
    if banning:
        await services.audit.ban(actor, target, before=before, after=new_groups)
    else:
        await services.audit.unban(actor, target, before=before, after=new_groups)

    await services.notification.show_popup(
        callback=callback,
        text=_("user_editor:popup:banned") if banning else _("user_editor:popup:unbanned"),
    )
    await _show_card(callback, session, services, gateway_factory, config, target)


@router.callback_query(F.data.startswith(NavAdminTools.BAN_USER), IsAdmin())
async def callback_ban_user(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    tg_id = _tg_id_from(callback)
    target = await _get_target(callback, session, services, tg_id)
    if target is None:
        return

    is_banned = services.inbound_groups.is_banned(target)
    # Ключи — литералами (не через переменную): pybabel extract видит только литералы.
    text = (
        _("user_editor:message:unban_confirm")
        if is_banned
        else _("user_editor:message:ban_confirm")
    ).format(name=html.escape(target.first_name), tg_id=tg_id)
    await callback.message.edit_text(
        text=text,
        reply_markup=user_ban_confirm_keyboard(tg_id, is_banned=is_banned),
    )


# endregion


# region: сброс трафика


@router.callback_query(F.data.startswith(NavAdminTools.CONFIRM_RESET_USER_TRAFFIC), IsAdmin())
async def callback_confirm_reset_traffic(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
    gateway_factory: GatewayFactory,
    config: Config,
) -> None:
    tg_id = _tg_id_from(callback)
    target = await _get_target(callback, session, services, tg_id)
    if target is None:
        return

    # resetTraffic на панели заодно ВКЛЮЧАЕТ клиента — забаненному запрещаем,
    # чтобы сброс не превратился в тихий разбан.
    if services.inbound_groups.is_banned(target):
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:banned_refused")
        )
        return

    logger.info(f"Admin {user.tg_id} resets traffic of user {tg_id}.")
    ok = await services.vpn.reset_traffic(target)
    if ok:
        await services.audit.traffic_reset(AuditActor.admin(callback.from_user), target)
    await services.notification.show_popup(
        callback=callback,
        text=(
            _("user_editor:popup:traffic_reset")
            if ok
            else _("user_editor:popup:traffic_reset_failed")
        ),
    )
    if ok:
        await _show_card(callback, session, services, gateway_factory, config, target)


@router.callback_query(F.data.startswith(NavAdminTools.RESET_USER_TRAFFIC), IsAdmin())
async def callback_reset_traffic(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    tg_id = _tg_id_from(callback)
    target = await _get_target(callback, session, services, tg_id)
    if target is None:
        return

    # Отказ сразу на входе (симметрично «Начислить дни»), не только на confirm.
    if services.inbound_groups.is_banned(target):
        await services.notification.show_popup(
            callback=callback, text=_("user_editor:popup:banned_refused")
        )
        return

    await callback.message.edit_text(
        text=_("user_editor:message:reset_traffic_confirm").format(
            name=html.escape(target.first_name), tg_id=tg_id
        ),
        reply_markup=user_reset_traffic_confirm_keyboard(tg_id),
    )


# endregion


# region: написать пользователю (вход в существующий флоу уведомлений)


@router.callback_query(F.data.startswith(NavAdminTools.MESSAGE_USER), IsAdmin())
async def callback_message_user(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    state: FSMContext,
    services: ServicesContainer,
) -> None:
    tg_id = _tg_id_from(callback)
    target = await _get_target(callback, session, services, tg_id)
    if target is None:
        return

    logger.info(f"Admin {user.tg_id} writes a message to user {tg_id} from the card.")
    await state.update_data(
        {
            NOTIFICATION_PENDING_CHAT_IDS_KEY: [target.tg_id],
            # После отправки/отмены вернуться в карточку, а не в раздел уведомлений.
            NOTIFICATION_RETURN_TO_KEY: NavAdminTools.SHOW_USER + f"_{tg_id}",
        }
    )
    await state.set_state(NotificationStates.message_to_user)
    await callback.message.edit_text(
        text=_("notification:message:send_message_for_user").format(
            user_id=target.tg_id,
            first_name=html.escape(target.first_name),
        ),
        reply_markup=back_keyboard(NavAdminTools.SHOW_USER + f"_{tg_id}"),
    )


# endregion


# region: история действий над пользователем (аудит-лог)

# Сколько событий на страницу. Тянем на 1 больше — лишнее сигналит «есть следующая».
_HISTORY_PAGE_SIZE = 7


@router.callback_query(F.data.startswith(NavAdminTools.USER_HISTORY), IsAdmin())
async def callback_user_history(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
) -> None:
    # callback: user_history_{tg_id}_{page}
    payload = callback.data[len(NavAdminTools.USER_HISTORY) + 1 :]
    tg_id_raw, page_raw = payload.rsplit("_", 1)
    tg_id, page = int(tg_id_raw), int(page_raw)

    entries = await AuditLog.get_page(
        session=session,
        target_id=tg_id,
        limit=_HISTORY_PAGE_SIZE + 1,
        offset=page * _HISTORY_PAGE_SIZE,
    )
    has_next = len(entries) > _HISTORY_PAGE_SIZE
    entries = entries[:_HISTORY_PAGE_SIZE]

    if not entries:
        body = _("user_editor:history:empty")
    else:
        body = "\n\n".join(format_audit_entry(entry) for entry in entries)

    text = _("user_editor:history:title").format(user_id=tg_id, entries=body)
    await callback.message.edit_text(
        text=text,
        reply_markup=user_history_keyboard(
            tg_id, page, has_prev=page > 0, has_next=has_next
        ),
    )


# endregion
