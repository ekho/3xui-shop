import logging

from aiogram import F, Router
from aiogram.exceptions import TelegramAPIError
from aiogram.filters import BaseFilter, Command
from aiogram.types import CallbackQuery, Message
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.models import ServicesContainer
from app.bot.services.approval import ApprovalCallback
from app.bot.utils.constants import ApprovalStatus, SupportTicketStatus
from app.config import Config
from app.db.models import SupportTicket, User
from app.support_bot.service import SupportProxyService

logger = logging.getLogger(__name__)
router = Router(name=__name__)


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
    message: Message, session: AsyncSession, support: SupportProxyService
) -> None:
    ticket = await _ticket_for(message, session)
    if not ticket:
        await message.reply("⚠️ Тикет для этого топика не найден.")
        return

    user = await User.get(session=session, tg_id=ticket.tg_id)
    if not user:
        await message.reply(f"⚠️ Юзер <code>{ticket.tg_id}</code> не найден в БД магазина.")
        return
    await support.send_user_card(user=user, session=session, thread_id=ticket.thread_id)


# ── Кнопки approve/reject на карточках заявок на регистрацию ─────────────────
# Карточки в General шлёт ApprovalService; решение применяет он же — общий сервис
# с основным ботом. Хендлер только парсит колбэк и даёт обратную связь оператору.
# Колбэки не анонимизируются (в отличие от сообщений) — from_user всегда живой оператор;
# право решать = членство в закрытой группе поддержки.


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

    applied = await services.approval.apply_decision(session, target, new_status)
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
