import logging
from datetime import datetime, timezone

from aiogram import F, Router
from aiogram.exceptions import TelegramAPIError
from aiogram.filters import BaseFilter, Command, CommandObject
from aiogram.types import CallbackQuery, InlineKeyboardButton, InlineKeyboardMarkup, Message
from aiogram.utils.i18n import I18n
from redis.asyncio.client import Redis
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.models import ServicesContainer
from app.bot.services.approval import ApprovalCallback
from app.bot.utils.constants import DEFAULT_LANGUAGE, ApprovalStatus, SupportTicketStatus
from app.bot.utils.user_card import build_user_card
from app.config import Config
from app.db.database import Database
from app.db.models import SupportTicket, User
from app.support_bot.service import SupportProxyService

logger = logging.getLogger(__name__)
router = Router(name=__name__)

# Компенсация: лимит на разовое начисление и префикс колбэка кнопки сброса трафика.
MAX_COMP_DAYS = 365
COMP_RESET_PREFIX = "comp_reset"


class InSupportGroup(BaseFilter):
    async def __call__(self, message: Message, config: Config) -> bool:
        return message.chat.id == config.bot.SUPPORT_GROUP_ID


class CallbackInSupportGroup(BaseFilter):
    async def __call__(self, callback: CallbackQuery, config: Config) -> bool:
        return (
            callback.message is not None
            and callback.message.chat.id == config.bot.SUPPORT_GROUP_ID
        )


router.message.filter(InSupportGroup())
router.callback_query.filter(CallbackInSupportGroup())

# Анонимные админы пишут как GroupAnonymousBot — сопоставить с оператором нельзя,
# и user-контекста у таких апдейтов нет; им отвечает anonymous_admin_hint.
_human = F.from_user & ~F.from_user.is_bot

# m5: F.message_thread_id ставится и у ответов в General (id корня цепочки) —
# тикеты живут только в настоящих топиках, где есть is_topic_message.
_in_topic = F.message_thread_id & F.is_topic_message


async def _ticket_for(message: Message, session: AsyncSession) -> SupportTicket | None:
    if message.message_thread_id is None:
        return None
    return await SupportTicket.get_by_thread_id(
        session=session, thread_id=message.message_thread_id
    )


# ── Команды оператора в топике ────────────────────────────────────────────────


@router.message(Command("close"), _human, _in_topic)
async def command_close(
    message: Message, session: AsyncSession, support: SupportProxyService
) -> None:
    ticket = await _ticket_for(message, session)
    if not ticket:
        await message.reply("⚠️ Тикет для этого топика не найден.")
        return
    if ticket.status == SupportTicketStatus.CLOSED:
        await message.reply("ℹ️ Тикет уже закрыт.")
        return

    await SupportTicket.update(
        session=session, tg_id=ticket.tg_id, status=SupportTicketStatus.CLOSED
    )
    try:
        await support.bot.close_forum_topic(
            chat_id=message.chat.id, message_thread_id=message.message_thread_id
        )
    except TelegramAPIError as exception:
        logger.debug(f"close_forum_topic failed: {exception}")
    await support.notify_user(session, ticket, "support_bot:message:closed_by_operator")
    logger.info(f"Ticket {ticket.tg_id} closed by operator {message.from_user.id}.")


@router.message(Command("ban"), _human, _in_topic)
async def command_ban(message: Message, session: AsyncSession) -> None:
    ticket = await _ticket_for(message, session)
    if not ticket:
        await message.reply("⚠️ Тикет для этого топика не найден.")
        return

    await SupportTicket.update(
        session=session, tg_id=ticket.tg_id, status=SupportTicketStatus.BANNED
    )
    await message.reply("🚫 Пользователь заблокирован в поддержке (/unban — разблокировать).")
    logger.info(f"Ticket {ticket.tg_id} banned by operator {message.from_user.id}.")


@router.message(Command("unban"), _human, _in_topic)
async def command_unban(message: Message, session: AsyncSession) -> None:
    ticket = await _ticket_for(message, session)
    if not ticket:
        await message.reply("⚠️ Тикет для этого топика не найден.")
        return

    await SupportTicket.update(
        session=session, tg_id=ticket.tg_id, status=SupportTicketStatus.OPEN
    )
    await message.reply("✅ Пользователь разблокирован.")
    logger.info(f"Ticket {ticket.tg_id} unbanned by operator {message.from_user.id}.")


@router.message(Command("info"), _human, _in_topic)
async def command_info(
    message: Message,
    session: AsyncSession,
    services: ServicesContainer,
    gateway_factory=None,
) -> None:
    ticket = await _ticket_for(message, session)
    if not ticket:
        await message.reply("⚠️ Тикет для этого топика не найден.")
        return

    user = await User.get(session=session, tg_id=ticket.tg_id)
    if not user:
        await message.reply(f"⚠️ Юзер <code>{ticket.tg_id}</code> не найден в БД магазина.")
        return

    # Полная карточка — тот же рендерер, что в админ-меню (БД + live-панель + платежи).
    payment_method_currencies = (
        {gateway.callback: gateway.currency.code for gateway in gateway_factory.get_gateways()}
        if gateway_factory
        else {}
    )
    text, _client_data = await build_user_card(
        target=user,
        session=session,
        services=services,
        payment_method_currencies=payment_method_currencies,
    )
    await message.reply(f"{text}\n\n⌨️ /close · /ban · /unban · /comp N · /approve · /reject")


# ── Компенсация: /comp N — начислить дни юзеру этого топика ──────────────────
# Тот же примитив, что у промокодов/рефералки (process_bonus_days): продлевает от
# max(текущий срок, сейчас), не трогает девайсы и платный лимит трафика. Юзера
# уведомляет ОСНОВНОЙ бот в его локали (support-бота юзер мог не стартовать).


@router.message(Command("comp"), _human, _in_topic)
async def command_comp(
    message: Message,
    command: CommandObject,
    session: AsyncSession,
    services: ServicesContainer,
    config: Config,
    i18n: I18n,
) -> None:
    ticket = await _ticket_for(message, session)
    if not ticket:
        await message.reply("⚠️ Тикет для этого топика не найден.")
        return

    target = await User.get(session=session, tg_id=ticket.tg_id)
    if not target:
        await message.reply(f"⚠️ Юзер <code>{ticket.tg_id}</code> не найден в БД магазина.")
        return

    args = (command.args or "").strip()
    if not args.isdigit() or not 1 <= int(args) <= MAX_COMP_DAYS:
        await message.reply(
            f"⚠️ Использование: <code>/comp N</code> — начислить N дней (1–{MAX_COMP_DAYS})."
        )
        return
    days = int(args)

    # Гварды (зеркало админ-меню): безлимиту дни превратили бы бессрочный
    # expiryTime=0 в конечную дату; у забаненного дни тихо сгорали бы.
    if services.inbound_groups.is_unlimited(target):
        await message.reply("⛔️ У пользователя безлимит — дни не применимы (бессрочный срок).")
        return
    if services.inbound_groups.is_banned(target):
        await message.reply(
            "⛔️ Пользователь забанен (VPN отключён) — дни сгорят. Сначала снимите бан "
            "(админ-меню → Пользователи)."
        )
        return

    # Клиента в панели нет — понадобится сервер со свободными местами
    # (reconcile мог усыновить клиента, заведённого в панели руками).
    if not target.server_id and not await services.vpn.reconcile_from_panel(target):
        if await services.server_pool.get_available_server() is None:
            await message.reply("⚠️ Нет сервера со свободными местами — клиента не создать.")
            return

    try:
        granted = await services.vpn.process_bonus_days(
            user=target, duration=days, devices=config.shop.BONUS_DEVICES_COUNT
        )
    except Exception as exception:  # noqa: BLE001 — операторский флоу: отказ видим
        logger.error(f"/comp {days}d for {target.tg_id} failed: {exception}")
        granted = False

    if not granted:
        await message.reply("⚠️ Не удалось начислить дни — подробности в логах бота.")
        return

    logger.info(f"Operator {message.from_user.id} granted {days} bonus days to {target.tg_id}.")

    # Уведомление юзеру — основным ботом, в его локали.
    locale = (
        target.language_code
        if target.language_code in i18n.available_locales
        else DEFAULT_LANGUAGE
    )
    user_text = i18n.gettext("compensation:user:granted", locale=locale).format(days=days)
    await services.notification.notify_by_id(chat_id=target.tg_id, text=user_text)

    # Отчёт оператору: новый срок + предупреждение об исчерпанном трафике с кнопкой сброса.
    refreshed = await User.get(session=session, tg_id=target.tg_id)
    client_data = (
        await services.vpn.get_client_data(refreshed) if refreshed and refreshed.server_id else None
    )
    expiry_note = ""
    if client_data and client_data._expiry_time != -1:
        expiry_date = datetime.fromtimestamp(
            client_data._expiry_time / 1000, timezone.utc
        ).strftime("%Y-%m-%d %H:%M")
        expiry_note = f"\nПодписка до: <code>{expiry_date} UTC</code>"

    exhausted_note = ""
    reply_markup = None
    if client_data and client_data.has_traffic_exhausted:
        exhausted_note = "\n⚠️ Лимит трафика исчерпан — доступ появится после сброса счётчика."
        reply_markup = InlineKeyboardMarkup(
            inline_keyboard=[
                [
                    InlineKeyboardButton(
                        text="♻️ Сбросить трафик",
                        callback_data=f"{COMP_RESET_PREFIX}_{target.tg_id}",
                    )
                ]
            ]
        )

    await message.reply(
        f"✅ Начислено {days} дн. пользователю <code>{target.tg_id}</code>."
        f"{expiry_note}{exhausted_note}",
        reply_markup=reply_markup,
    )


@router.callback_query(F.data.startswith(COMP_RESET_PREFIX))
async def on_comp_reset(
    callback: CallbackQuery,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    tg_id = int(callback.data.rsplit("_", 1)[-1])
    target = await User.get(session=session, tg_id=tg_id)
    if not target:
        await callback.answer("⚠️ Юзер не найден в БД магазина.", show_alert=True)
        return

    # resetTraffic на панели заодно включает клиента — забаненному запрещаем.
    if services.inbound_groups.is_banned(target):
        await callback.answer("⛔️ Пользователь забанен — сброс стал бы тихим разбаном.", show_alert=True)
        return

    ok = await services.vpn.reset_traffic(target)
    if not ok:
        await callback.answer("⚠️ Не удалось сбросить трафик.", show_alert=True)
        return

    logger.info(f"Operator {callback.from_user.id} reset traffic of {tg_id} (after /comp).")
    await callback.answer("✅ Трафик сброшен.")
    try:
        await callback.message.edit_text(
            f"{callback.message.html_text}\n♻️ Трафик сброшен.", reply_markup=None
        )
    except TelegramAPIError:
        pass  # сообщение недоступно/не изменилось — сброс уже сделан


# ── Управление заявками на регистрацию командами ─────────────────────────────
# /approve и /reject решают заявку юзера ЭТОГО топика — независимо от того, есть ли
# в топике карточка с кнопками (карточек нет у заявок, поданных до включения фичи).
# /pending — обзор очереди из любого места группы + раскладка карточек по топикам.


async def _decide_registration(
    message: Message,
    session: AsyncSession,
    services: ServicesContainer,
    new_status: ApprovalStatus,
) -> None:
    ticket = await _ticket_for(message, session)
    if not ticket:
        await message.reply("⚠️ Тикет для этого топика не найден.")
        return

    target = await User.get(session=session, tg_id=ticket.tg_id)
    if not target:
        await message.reply(f"⚠️ Юзер <code>{ticket.tg_id}</code> не найден в БД магазина.")
        return

    applied = await services.approval.apply_decision(
        session, target, new_status, decided_by=message.from_user.id
    )
    if not applied:
        await message.reply(f"ℹ️ Заявка уже в статусе «{new_status.value}».")
        return

    verdict = "✅ Одобрено" if new_status == ApprovalStatus.APPROVED else "🚫 Отклонено"
    await message.reply(verdict)
    logger.info(
        f"Registration {new_status.value} for user {target.tg_id} "
        f"by operator {message.from_user.id} (command)."
    )


@router.message(Command("approve"), _human, _in_topic)
async def command_approve(
    message: Message, session: AsyncSession, services: ServicesContainer
) -> None:
    await _decide_registration(message, session, services, ApprovalStatus.APPROVED)


@router.message(Command("reject"), _human, _in_topic)
async def command_reject(
    message: Message, session: AsyncSession, services: ServicesContainer
) -> None:
    await _decide_registration(message, session, services, ApprovalStatus.REJECTED)


@router.message(Command("pending"), _human)
async def command_pending(
    message: Message,
    session: AsyncSession,
    services: ServicesContainer,
    db: Database,
    redis: Redis,
) -> None:
    stmt = select(User).where(User.approval_status == ApprovalStatus.PENDING)
    pending_users = list((await session.execute(stmt)).scalars().all())

    if not pending_users:
        await message.reply("✅ Заявок на регистрацию в очереди нет.")
        return

    # Полное переиспользование логики напоминаний: карточка с кнопками в топик каждого
    # ожидающего, предыдущее напоминание удаляется (Redis-антиспам).
    await services.approval.remind_pending(db.session, redis)

    lines = "\n".join(
        f"• {u.first_name} · @{u.username or '—'} · <code>{u.tg_id}</code>"
        for u in pending_users
    )
    await message.reply(
        f"⏳ Заявок в очереди: {len(pending_users)}\n{lines}\n\n"
        "Карточки с кнопками разложены по топикам (обновлены)."
    )
    logger.info(
        f"Operator {message.from_user.id} listed {len(pending_users)} pending users (/pending)."
    )


# ── Кнопки approve/reject на карточках заявок на регистрацию ─────────────────
# Карточки в топики юзеров шлёт ApprovalService (send_to_topic); решение применяет
# он же — общий сервис с основным ботом. Хендлер только парсит колбэк и даёт
# обратную связь оператору. Колбэки не анонимизируются (в отличие от сообщений) —
# from_user всегда живой оператор; право решать = членство в закрытой группе поддержки.


@router.callback_query(ApprovalCallback.filter())
async def on_approval(
    callback: CallbackQuery,
    callback_data: ApprovalCallback,
    session: AsyncSession,
    services: ServicesContainer,
) -> None:
    new_status = (
        ApprovalStatus.APPROVED if callback_data.action == "approve" else ApprovalStatus.REJECTED
    )
    target = await User.get(session=session, tg_id=callback_data.user_id)
    if target is None:
        await callback.answer("⚠️ Пользователь не найден в БД магазина.", show_alert=True)
        return

    applied = await services.approval.apply_decision(
        session, target, new_status, decided_by=callback.from_user.id
    )
    if not applied:
        await callback.answer("ℹ️ Заявка уже обработана.", show_alert=True)
        return

    operator = callback.from_user
    verdict = "✅ Одобрено" if new_status == ApprovalStatus.APPROVED else "🚫 Отклонено"
    handle = f"@{operator.username}" if operator.username else str(operator.id)
    try:
        await callback.message.edit_text(f"{callback.message.text}\n\n{verdict} — {handle}")
    except TelegramAPIError:
        pass  # текст не изменился / сообщение недоступно
    await callback.answer()
    logger.info(
        f"Registration {new_status.value} for user {target.tg_id} "
        f"by operator {operator.id} (support group)."
    )


# ── Синк статуса с нативными действиями над топиком. Без _human: session в группе
# инжектится всегда (SupportDBSessionMiddleware), а закрытие анонимным админом тоже
# должно синкаться (m6). Эха от самого бота не бывает: свои сообщения (в т.ч.
# сервисные после /close) боту не доставляются. ──────────────────────────────


@router.message(F.forum_topic_closed)
async def topic_closed_natively(
    message: Message, session: AsyncSession, support: SupportProxyService
) -> None:
    ticket = await _ticket_for(message, session)
    if not ticket or ticket.status != SupportTicketStatus.OPEN:
        return
    await SupportTicket.update(
        session=session, tg_id=ticket.tg_id, status=SupportTicketStatus.CLOSED
    )
    await support.notify_user(session, ticket, "support_bot:message:closed_by_operator")
    logger.info(f"Ticket {ticket.tg_id} closed natively (topic UI).")


@router.message(F.forum_topic_reopened)
async def topic_reopened_natively(message: Message, session: AsyncSession) -> None:
    ticket = await _ticket_for(message, session)
    if not ticket or ticket.status != SupportTicketStatus.CLOSED:
        return
    await SupportTicket.update(
        session=session, tg_id=ticket.tg_id, status=SupportTicketStatus.OPEN
    )
    logger.info(f"Ticket {ticket.tg_id} reopened natively (topic UI).")


# ── Релэй ответа оператора ────────────────────────────────────────────────────


@router.message(
    _in_topic,
    F.from_user.is_bot,
    F.sender_chat,  # анонимный админ пишет как GroupAnonymousBot
    # только контентные сообщения: на сервисные подсказка была бы невпопад
    ~F.forum_topic_created,
    ~F.forum_topic_edited,
    ~F.forum_topic_closed,
    ~F.forum_topic_reopened,
    ~F.pinned_message,
)
async def anonymous_admin_hint(message: Message) -> None:
    await message.reply(
        "⚠️ Вы пишете анонимно (от имени группы) — такие сообщения пользователю не пересылаются. "
        "Отключите «Remain Anonymous» в правах администратора и повторите."
    )


@router.message(
    _human,
    _in_topic,
    ~F.text.startswith("/"),  # m8: опечатки команд (/clsoe) не должны улетать юзеру
    ~F.forum_topic_created,
    ~F.forum_topic_edited,
    ~F.forum_topic_closed,
    ~F.forum_topic_reopened,
    ~F.pinned_message,
)
async def relay_admin_message(
    message: Message, session: AsyncSession, support: SupportProxyService
) -> None:
    ticket = await _ticket_for(message, session)
    if not ticket:
        await message.reply(
            "⚠️ Тикет для этого топика не найден — сообщение пользователю не переслано."
        )
        return

    if ticket.status == SupportTicketStatus.BANNED:
        # M3: заметка оператора в забаненном тикете НЕ снимает бан и не уходит юзеру.
        await message.reply(
            "🚫 Тикет забанен — сообщение не переслано. /unban, чтобы снова отвечать."
        )
        return

    if ticket.status == SupportTicketStatus.CLOSED:
        # Ответ в закрытом тикете возвращает его в работу.
        await SupportTicket.update(
            session=session, tg_id=ticket.tg_id, status=SupportTicketStatus.OPEN
        )

    await support.relay_from_admin(message=message, ticket=ticket)
