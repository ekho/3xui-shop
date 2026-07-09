from __future__ import annotations

import logging
from datetime import timedelta
from typing import TYPE_CHECKING

from aiogram import Bot
from aiogram.exceptions import TelegramAPIError, TelegramBadRequest, TelegramForbiddenError
from aiogram.filters.callback_data import CallbackData
from aiogram.types import InlineKeyboardMarkup
from aiogram.utils.i18n import I18n
from aiogram.utils.i18n import gettext as _
from aiogram.utils.keyboard import InlineKeyboardBuilder
from redis.asyncio.client import Redis
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.bot.utils.constants import DEFAULT_LANGUAGE, ApprovalStatus
from app.config import BotConfig, Config
from app.db.models import User

if TYPE_CHECKING:
    # Только для аннотаций: runtime-импорт notification замкнул бы цикл
    # approval → notification → routers → admin_tools.approval_handler → approval
    # (approval_handler импортирует отсюда ApprovalCallback на уровне модуля).
    from app.support_bot.service import SupportProxyService

    from .notification import NotificationService

logger = logging.getLogger(__name__)

# Ключ храним заметно дольше интервала напоминаний, чтобы id предыдущего сообщения
# дожил до следующего рана; у решённых заявок (approved/rejected) ран их пропускает,
# а ключ сам истечёт по TTL.
REMINDER_MSG_TTL = timedelta(days=30)


class ApprovalCallback(CallbackData, prefix="approval"):
    action: str  # "approve" | "reject"
    user_id: int


def approval_keyboard(user_id: int) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    builder.button(
        text=_("approval:button:approve"),
        callback_data=ApprovalCallback(action="approve", user_id=user_id),
    )
    builder.button(
        text=_("approval:button:reject"),
        callback_data=ApprovalCallback(action="reject", user_id=user_id),
    )
    return builder.as_markup()


def rejected_contact_keyboard(bot_config: BotConfig) -> InlineKeyboardMarkup:
    # Импорт внутри функции — избегаем циклической зависимости services → routers
    # на уровне модуля.
    from app.bot.routers.support.keyboard import contact_button

    builder = InlineKeyboardBuilder()
    builder.row(contact_button(bot_config))
    return builder.as_markup()


# Redis для напоминаний — вспомогательный слой антиспама, не источник истины. Сбой Redis
# (failover/таймаут) НЕ должен ни ронять весь прогон (иначе остальные адресаты не получат
# напоминание в этот час), ни пробрасываться наружу. Best-effort, как delete_message/notify_by_id.
# Остаточный риск: если set упадёт уже ПОСЛЕ отправки — id потеряется и следующий прогон
# пришлёт один дубль (самоизлечивается по восстановлении Redis).
async def _redis_get(redis: Redis, key: str) -> bytes | None:
    try:
        return await redis.get(key)
    except Exception as exception:
        logger.warning(f"[approval reminder] redis.get {key} failed: {exception}")
        return None


async def _redis_set(redis: Redis, key: str, value: str) -> None:
    try:
        await redis.set(key, value, ex=REMINDER_MSG_TTL)
    except Exception as exception:
        logger.warning(f"[approval reminder] redis.set {key} failed: {exception}")


async def _redis_delete(redis: Redis, key: str) -> None:
    try:
        await redis.delete(key)
    except Exception as exception:
        logger.warning(f"[approval reminder] redis.delete {key} failed: {exception}")


class ApprovalService:
    """Заявки на регистрацию: решение, карточки с кнопками, напоминания.

    Один сервис на оба бота. Карточка новой заявки создаёт персональный топик юзера
    в группе поддержки (тот же, что для тикетов — SupportProxyService.send_to_topic)
    и падает туда; напоминания — в тот же топик. Если фича выключена — в личку
    каждому админу через основного бота (историческое поведение). apply_decision —
    единый источник правды для всех поверхностей: кнопки в карточке (оба бота) и
    экран «Ожидающие» в админке.

    Уведомления юзеру всегда шлёт ОСНОВНОЙ бот: support-бота юзер мог не стартовать.
    """

    def __init__(
        self,
        config: Config,
        bot: Bot,
        i18n: I18n,
        notification_service: NotificationService,
        support: SupportProxyService | None = None,
    ) -> None:
        self.config = config
        self.bot = bot  # основной бот
        self.i18n = i18n
        self.notification = notification_service
        self.support = support

    @property
    def group_channel_enabled(self) -> bool:
        return self.support is not None

    # region: решение по заявке

    async def apply_decision(
        self,
        session: AsyncSession,
        target: User,
        new_status: ApprovalStatus,
    ) -> bool:
        """Применяет решение по заявке (approve/reject) и уведомляет пользователя.

        Инкапсулирует: идемпотентность (повторный тап по «протухшей» кнопке), сброс
        `approval_requested_at`, отмену Stars-подписки при reject и отправку юзеру
        granted/denied в ЕГО локали.

        Возвращает False, если статус уже был установлен (повторный тап — no-op),
        иначе True.
        """
        # Идемпотентность: повторный тап по «протухшей» кнопке (напоминание/исходное уведомление
        # в другом чате, оставшиеся после решения) не должен заново прогонять уже решённую
        # заявку и повторно слать юзеру granted/denied. Блокируем только повторное применение ТОГО
        # ЖЕ статуса — смену решения (approve↔reject) и повторную заявку (статус снова PENDING)
        # пропускаем.
        if target.approval_status == new_status:
            return False

        # M6: сбрасываем метку заявки, чтобы повторный запрос (rejected → снова) отправил новое уведомление
        await User.update(
            session,
            tg_id=target.tg_id,
            approval_status=new_status,
            approval_requested_at=None,
        )

        # B1: reject при активном Stars-рекурренте обязан отменить подписку, иначе Telegram
        #     продолжит списывать звёзды. Поля появляются на Этапе 5 — getattr для forward-совместимости.
        if new_status == ApprovalStatus.REJECTED and getattr(target, "is_stars_auto_renew", False):
            charge_id = getattr(target, "stars_charge_id", None)
            if charge_id:
                try:
                    await self.bot.edit_user_star_subscription(
                        user_id=target.tg_id,
                        telegram_payment_charge_id=charge_id,
                        is_canceled=True,
                    )
                except TelegramBadRequest as exception:
                    logger.warning(f"Cancel stars sub on reject for {target.tg_id}: {exception}")
                await User.update(session, tg_id=target.tg_id, is_stars_auto_renew=False)

        # M5: текст юзеру — в ЕГО локали (i18n-middleware ставит локаль инициатора решения)
        locale = (target.language_code if target.language_code else None) or DEFAULT_LANGUAGE
        with self.i18n.use_locale(locale):
            user_text = (
                _("approval:user:granted")
                if new_status == ApprovalStatus.APPROVED
                else _("approval:user:denied")
            )
            # Отказанному юзеру — стоп-лист: единственный оставшийся канал это связь с админом напрямую.
            user_markup = (
                rejected_contact_keyboard(self.config.bot)
                if new_status == ApprovalStatus.REJECTED
                else None
            )
        try:
            await self.bot.send_message(target.tg_id, user_text, reply_markup=user_markup)
        except TelegramForbiddenError:
            logger.info(f"User {target.tg_id} blocked the bot; approval notice skipped.")

        return True

    # endregion

    # region: карточки заявок

    async def announce_new_request(self, session: AsyncSession, user: User) -> None:
        """Карточка новой заявки с кнопками approve/reject.

        В персональный топик юзера в группе поддержки (создаётся сразу) при
        включённой фиче; при сбое отправки или выключенной фиче — в личку каждому
        админу (заявка не должна потеряться)."""
        # Апдейт пришёл в локали нового юзера → карточку рендерим на дефолтной локали.
        with self.i18n.use_locale(DEFAULT_LANGUAGE):
            text = _("approval:admin:new_request").format(
                name=user.first_name, username=user.username or "-", tg_id=user.tg_id
            )
            keyboard = approval_keyboard(user.tg_id)

        if self.group_channel_enabled:
            sent = await self.support.send_to_topic(
                session=session, user=user, text=text, reply_markup=keyboard
            )
            if sent:
                return
            logger.error(
                f"Approval card for {user.tg_id} not delivered to support group; "
                "falling back to admins' PM."
            )
        await self._notify_each_admin(text, keyboard)

    async def _notify_each_admin(self, text: str, keyboard: InlineKeyboardMarkup) -> None:
        admin_ids = set(self.config.bot.ADMINS) | {self.config.bot.DEV_ID}
        for admin_id in admin_ids:
            await self.notification.notify_by_id(chat_id=admin_id, text=text, reply_markup=keyboard)

    # endregion

    # region: напоминания

    async def remind_pending(
        self, session_factory: async_sessionmaker, redis: Redis | None = None
    ) -> None:
        """Периодическое напоминание о висящих заявках.

        Антиспам: предыдущее напоминание по юзеру удаляется перед отправкой нового
        (id живёт в Redis; ключи группы и лички независимы)."""
        # Сессия открыта на весь прогон: отправка в топик может создавать тикет/топик.
        session: AsyncSession
        async with session_factory() as session:
            stmt = select(User).where(User.approval_status == ApprovalStatus.PENDING)
            result = await session.execute(stmt)
            pending_users = result.scalars().all()

            if not pending_users:
                logger.info("[approval reminder] No pending users to remind about.")
                return

            destination = "support topics" if self.group_channel_enabled else "admins"
            logger.info(
                f"[approval reminder] Reminding {destination} "
                f"about {len(pending_users)} pending users."
            )

            # Апдейт фонового таска не привязан к локали юзера → рендерим на дефолтной.
            with self.i18n.use_locale(DEFAULT_LANGUAGE):
                for user in pending_users:
                    text = _("approval:admin:reminder").format(
                        name=user.first_name, username=user.username or "-", tg_id=user.tg_id
                    )
                    keyboard = approval_keyboard(user.tg_id)

                    if self.group_channel_enabled and await self._remind_topic(
                        session, user, text, keyboard, redis
                    ):
                        continue
                    # Группа недоступна (кик/права) или фича выключена — заявка не должна
                    # потеряться: личка админов; её антиспам-ключи независимы от групповых.
                    await self._remind_admins(user.tg_id, text, keyboard, redis)

    async def _remind_topic(
        self,
        session: AsyncSession,
        user: User,
        text: str,
        keyboard: InlineKeyboardMarkup,
        redis: Redis | None,
    ) -> bool:
        key = f"approval:reminder:msg:group:{user.tg_id}"
        if redis is not None:
            previous_id = await _redis_get(redis, key)
            if previous_id:
                await self._delete_group_message(int(previous_id))

        notification = await self.support.send_to_topic(
            session=session, user=user, text=text, reply_markup=keyboard
        )

        if redis is not None:
            if notification:
                await _redis_set(redis, key, str(notification.message_id))
            else:
                # Отправка не удалась — предыдущее уже удалено, не тянем мёртвый id.
                await _redis_delete(redis, key)
        return notification is not None

    async def _delete_group_message(self, message_id: int) -> None:
        # message_id уникален в рамках чата — thread_id для удаления не нужен.
        try:
            await self.support.bot.delete_message(
                chat_id=self.support.group_id, message_id=message_id
            )
        except TelegramAPIError as exception:
            logger.debug(
                f"[approval reminder] delete group message {message_id} failed: {exception}"
            )

    async def _remind_admins(
        self, tg_id: int, text: str, keyboard: InlineKeyboardMarkup, redis: Redis | None
    ) -> None:
        admin_ids = set(self.config.bot.ADMINS) | {self.config.bot.DEV_ID}
        for admin_id in admin_ids:
            # Антиспам: перед новым напоминанием удаляем предыдущее по этому юзеру
            # в чате этого админа (id хранится в Redis per (admin, user)).
            key = f"approval:reminder:msg:{admin_id}:{tg_id}"
            if redis is not None:
                previous_id = await _redis_get(redis, key)
                if previous_id:
                    await self.notification.delete_message(
                        chat_id=admin_id, message_id=int(previous_id)
                    )

            notification = await self.notification.notify_by_id(
                chat_id=admin_id, text=text, reply_markup=keyboard
            )

            if redis is not None:
                if notification:
                    await _redis_set(redis, key, str(notification.message_id))
                else:
                    # Отправка не удалась — предыдущее уже удалено, не тянем мёртвый id.
                    await _redis_delete(redis, key)

    # endregion
