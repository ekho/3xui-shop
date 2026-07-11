import asyncio
import html
import logging

from aiogram import Bot
from aiogram.exceptions import TelegramAPIError, TelegramBadRequest, TelegramForbiddenError
from aiogram.types import InlineKeyboardMarkup, Message
from aiogram.utils.i18n import I18n
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.utils.constants import DEFAULT_LANGUAGE, SupportTicketStatus
from app.config import Config
from app.db.models import SupportTicket, User

logger = logging.getLogger(__name__)

# Подстроки ошибок Bot API, означающие «топика больше нет» (удалён вручную/группа пересоздана).
# Telegram не даёт машиночитаемого кода — матчимся по тексту, как это принято с TelegramBadRequest.
_THREAD_GONE_MARKERS = ("thread not found", "topic_deleted", "topic deleted")


class SupportProxyService:
    """Прокси между личкой support-бота и топиками супергруппы-форума.

    Инварианты: один юзер — один тикет (uq по tg_id); thread_id может быть None
    (топик создаётся лениво и пересоздаётся после ручного удаления админом).
    """

    def __init__(self, config: Config, bot: Bot, i18n: I18n) -> None:
        self.config = config
        self.bot = bot  # support-бот, НЕ основной
        self.i18n = i18n
        self.group_id: int = config.bot.SUPPORT_GROUP_ID  # type: ignore[assignment]
        # Гонка первого контакта: polling обрабатывает апдейты конкурентно (handle_as_tasks),
        # альбом/бурст от нового юзера без лока создал бы два топика. Лок на tg_id;
        # словарь не чистим — int-ключей на юзера немного, память несущественна.
        self._relay_locks: dict[int, asyncio.Lock] = {}

    # region: user -> group

    async def relay_from_user(self, message: Message, session: AsyncSession, user: User) -> bool:
        """Копирует сообщение юзера в его топик. Возвращает успех доставки.

        Весь фидбэк юзеру о недоставке (бан/сбой) отправляется отсюда же — вызывающему
        остаётся только подтвердить успех (реакцией).
        """
        lock = self._relay_locks.setdefault(user.tg_id, asyncio.Lock())
        async with lock:
            delivered = await self._relay_from_user(message=message, session=session, user=user)
        if not delivered:
            ticket = await SupportTicket.get_by_tg_id(session=session, tg_id=user.tg_id)
            if not (ticket and ticket.status == SupportTicketStatus.BANNED):
                await message.answer(self._user_text("support_bot:message:delivery_failed", user))
        return delivered

    async def _relay_from_user(self, message: Message, session: AsyncSession, user: User) -> bool:
        ticket = await SupportTicket.get_by_tg_id(session=session, tg_id=user.tg_id)

        if not ticket:
            ticket = await SupportTicket.create(session=session, tg_id=user.tg_id)
            if not ticket:
                return False

        if ticket.status == SupportTicketStatus.BANNED:
            await message.answer(self._user_text("support_bot:message:banned", user))
            return False

        if ticket.status == SupportTicketStatus.CLOSED:
            # Новое сообщение переоткрывает диалог: статус в БД + топик в группе (best-effort).
            ticket = await SupportTicket.update(
                session=session, tg_id=user.tg_id, status=SupportTicketStatus.OPEN
            )
            if ticket.thread_id is not None:
                try:
                    await self.bot.reopen_forum_topic(
                        chat_id=self.group_id, message_thread_id=ticket.thread_id
                    )
                except TelegramAPIError:
                    logger.debug(f"Could not reopen topic {ticket.thread_id}; will recreate on demand.")

        if ticket.thread_id is None:
            thread_id = await self._create_topic(session=session, user=user)
            if thread_id is None:
                return False
            ticket = await SupportTicket.update(
                session=session, tg_id=user.tg_id, thread_id=thread_id
            )

        try:
            await self._copy_to_topic(message, ticket.thread_id)
            return True
        except TelegramBadRequest as exception:
            if not self._is_thread_gone(exception):
                logger.error(f"Failed to relay message from user {user.tg_id}: {exception}")
                return False

        # Топик удалили руками — пересоздаём и ретраим ровно один раз.
        logger.warning(f"Topic {ticket.thread_id} for user {user.tg_id} is gone; recreating.")
        thread_id = await self._create_topic(session=session, user=user)
        if thread_id is None:
            return False
        await SupportTicket.update(session=session, tg_id=user.tg_id, thread_id=thread_id)

        try:
            await self._copy_to_topic(message, thread_id)
            return True
        except TelegramAPIError as exception:
            logger.error(f"Retry relay for user {user.tg_id} failed: {exception}")
            return False

    async def _copy_to_topic(self, message: Message, thread_id: int) -> None:
        await message.copy_to(chat_id=self.group_id, message_thread_id=thread_id)

    async def send_to_topic(
        self,
        session: AsyncSession,
        user: User,
        text: str,
        reply_markup: InlineKeyboardMarkup | None = None,
    ) -> Message | None:
        """Сообщение от имени бота в персональный топик юзера.

        Тикет и топик создаются при необходимости; удалённый вручную топик
        пересоздаётся ровно один раз — та же семантика, что у relay_from_user.
        Используется ApprovalService'ом для карточек заявок на регистрацию.
        В отличие от relay_from_user статус тикета не проверяется: карточка —
        служебное сообщение операторам, бан юзера в поддержке её не блокирует.
        """
        lock = self._relay_locks.setdefault(user.tg_id, asyncio.Lock())
        async with lock:
            return await self._send_to_topic(
                session=session, user=user, text=text, reply_markup=reply_markup
            )

    async def _send_to_topic(
        self,
        session: AsyncSession,
        user: User,
        text: str,
        reply_markup: InlineKeyboardMarkup | None,
    ) -> Message | None:
        ticket = await SupportTicket.get_by_tg_id(session=session, tg_id=user.tg_id)

        if not ticket:
            ticket = await SupportTicket.create(session=session, tg_id=user.tg_id)
            if not ticket:
                return None

        if ticket.thread_id is None:
            thread_id = await self._create_topic(session=session, user=user)
            if thread_id is None:
                return None
            ticket = await SupportTicket.update(
                session=session, tg_id=user.tg_id, thread_id=thread_id
            )

        try:
            return await self.bot.send_message(
                chat_id=self.group_id,
                message_thread_id=ticket.thread_id,
                text=text,
                reply_markup=reply_markup,
            )
        except TelegramBadRequest as exception:
            if not self._is_thread_gone(exception):
                logger.error(f"Failed to send to topic of user {user.tg_id}: {exception}")
                return None
        except TelegramAPIError as exception:
            logger.error(f"Failed to send to topic of user {user.tg_id}: {exception}")
            return None

        # Топик удалили руками — пересоздаём и ретраим ровно один раз.
        logger.warning(f"Topic {ticket.thread_id} for user {user.tg_id} is gone; recreating.")
        thread_id = await self._create_topic(session=session, user=user)
        if thread_id is None:
            return None
        await SupportTicket.update(session=session, tg_id=user.tg_id, thread_id=thread_id)

        try:
            return await self.bot.send_message(
                chat_id=self.group_id,
                message_thread_id=thread_id,
                text=text,
                reply_markup=reply_markup,
            )
        except TelegramAPIError as exception:
            logger.error(f"Retry send to topic of user {user.tg_id} failed: {exception}")
            return None

    async def _create_topic(self, session: AsyncSession, user: User) -> int | None:
        try:
            topic = await self.bot.create_forum_topic(
                chat_id=self.group_id, name=self._topic_name(user)
            )
        except TelegramAPIError as exception:
            logger.critical(
                f"Failed to create forum topic for user {user.tg_id}: {exception}. "
                "Проверьте, что SUPPORT_GROUP_ID — супергруппа с включёнными Topics, "
                "а support-бот — админ с правом can_manage_topics."
            )
            return None

        await self.send_user_card(user=user, session=session, thread_id=topic.message_thread_id)
        return topic.message_thread_id

    def _topic_name(self, user: User) -> str:
        username = f"@{user.username}" if user.username else "—"
        return f"{user.first_name} · {username} · {user.tg_id}"[:128]

    async def send_user_card(self, user: User, session: AsyncSession, thread_id: int) -> None:
        """Карточка юзера первым сообщением топика — контекст для оператора."""
        username = f"@{user.username}" if user.username else "—"
        card = (
            f"👤 <a href='tg://user?id={user.tg_id}'>{html.escape(user.first_name)}</a>\n"
            f"🆔 <code>{user.tg_id}</code> · {username}\n"
            f"🌐 {user.language_code} · 🚀 триал использован: {'да' if user.is_trial_used else 'нет'}\n"
            f"📅 в магазине с: {user.created_at:%Y-%m-%d}\n\n"
            f"⌨️ /close · /ban · /unban · /info · /comp N"
        )
        try:
            await self.bot.send_message(chat_id=self.group_id, message_thread_id=thread_id, text=card)
        except TelegramAPIError as exception:
            logger.error(f"Failed to send user card for {user.tg_id}: {exception}")

    # endregion

    # region: group -> user

    async def relay_from_admin(self, message: Message, ticket: SupportTicket) -> bool:
        """Копирует сообщение оператора из топика юзеру. Возвращает успех доставки."""
        try:
            await message.copy_to(chat_id=ticket.tg_id)
            return True
        except TelegramForbiddenError:
            await message.reply(
                "⚠️ Пользователь заблокировал support-бота — сообщение не доставлено."
            )
            return False
        except TelegramAPIError as exception:
            logger.error(f"Failed to relay admin reply to user {ticket.tg_id}: {exception}")
            await message.reply(f"⚠️ Не доставлено: {html.escape(str(exception))}")
            return False

    async def notify_user(self, session: AsyncSession, ticket: SupportTicket, key: str) -> None:
        """Сервисное уведомление юзеру на ЕГО локали (не на локали админа)."""
        user = await User.get(session=session, tg_id=ticket.tg_id)
        locale = self._locale_for(user.language_code if user else None)
        try:
            await self.bot.send_message(
                chat_id=ticket.tg_id, text=self.i18n.gettext(key, locale=locale)
            )
        except TelegramAPIError:
            logger.debug(f"Could not notify user {ticket.tg_id} ({key}).")

    # endregion

    def _locale_for(self, language_code: str | None) -> str:
        # gettext с locale вне available_locales вернул бы сырой msgid (у юзера
        # language_code бывает de/uk/... — каталогов только en/ru).
        if language_code in self.i18n.available_locales:
            return language_code
        return DEFAULT_LANGUAGE

    def _user_text(self, key: str, user: User) -> str:
        return self.i18n.gettext(key, locale=self._locale_for(user.language_code))

    @staticmethod
    def _is_thread_gone(exception: TelegramBadRequest) -> bool:
        text = str(exception).lower()
        return any(marker in text for marker in _THREAD_GONE_MARKERS)
