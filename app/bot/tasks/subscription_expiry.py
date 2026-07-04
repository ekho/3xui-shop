import logging
from datetime import datetime, timedelta, timezone

from aiogram.types import InlineKeyboardMarkup
from aiogram.utils.i18n import I18n
from aiogram.utils.i18n import gettext as _
from aiogram.utils.keyboard import InlineKeyboardBuilder
from apscheduler.schedulers.asyncio import AsyncIOScheduler
from redis.asyncio.client import Redis
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.bot.models import SubscriptionData
from app.bot.services import NotificationService, VPNService
from app.bot.utils.constants import DEFAULT_LANGUAGE
from app.bot.utils.navigation import NavSubscription
from app.db.models import User

logger = logging.getLogger(__name__)

DAYS_THRESHOLDS = [1, 3]          # дни до конца срока (по возрастанию — шлём самый срочный сработавший)
TRAFFIC_THRESHOLDS = [1.0, 0.8]   # доля использования (по убыванию — шлём максимальный сработавший)
# M2: ключи дедупа цикл-привязаны к expiry_time — при продлении expiry меняется → новый ключ →
#     напоминания снова работают в новом периоде. TTL долгий, осиротевшие ключи старых циклов истекут сами.
NOTIFY_TTL = timedelta(days=400)
STARS_LAPSE_GRACE = 24 * 3600  # B5: грейс (сек) после subscription_expiration_date перед снятием флага


def extend_kb(user: User) -> InlineKeyboardMarkup:
    builder = InlineKeyboardBuilder()
    # M7: обязательно user_id — иначе payload уйдёт с user_id=0 и _on_payment_succeeded упадёт на User.get(0).
    builder.button(
        text=_("task:button:extend"),
        callback_data=SubscriptionData(state=NavSubscription.EXTEND, user_id=user.tg_id),
    )
    return builder.as_markup()


async def notify_users_with_expiring_subscription(
    session_factory: async_sessionmaker,
    redis: Redis,
    i18n: I18n,
    vpn_service: VPNService,
    notification_service: NotificationService,
) -> None:
    session: AsyncSession
    async with session_factory() as session:
        users = await User.get_all(session=session)

    logger.info(f"[expiry] Starting subscription check for {len(users)} users.")
    now = datetime.now(timezone.utc)

    for user in users:
        if not user.server_id:  # m6: без подписки — не ходим в панель
            continue

        # P4: get_client_data читает лимит трафика и срок из settings инбаунда (client.total из API
        # v3.4.2 всегда 0). Один inbound.get_list на юзера — цена корректного лимита (m6-оптимизацию
        # «один get_list на прогон» оставляем на потом).
        cd = await vpn_service.get_client_data(user)
        if cd is None:
            continue

        exp_ms = cd._expiry_time if cd._expiry_time and cd._expiry_time > 0 else 0
        locale = user.language_code or DEFAULT_LANGUAGE
        # B5: у юзера с активным Stars-рекуррентом срок продлевается автосписанием — не пугаем «истекает».
        stars_auto_renew = getattr(user, "is_stars_auto_renew", False)
        # B5: вебхука «подписка отменена/не прошла» нет — детектим лапс по дате следующего списания.
        stars_exp = getattr(user, "stars_expires_at", None)
        if stars_auto_renew and stars_exp and now.timestamp() > stars_exp + STARS_LAPSE_GRACE:
            async with session_factory() as s:
                await User.update(s, tg_id=user.tg_id, is_stars_auto_renew=False)
            stars_auto_renew = False  # вернуть юзера в обычные напоминания по сроку

        # --- пороги по сроку ---
        if exp_ms > 0 and not stars_auto_renew:
            days_left = (datetime.fromtimestamp(exp_ms / 1000, timezone.utc) - now).days
            for d in DAYS_THRESHOLDS:  # [1, 3] — сначала самый срочный
                if 0 <= days_left <= d:  # m2: <= d (пропуск рана не теряет порог), не == d
                    key = f"notify:exp:{user.tg_id}:{exp_ms}:{d}"
                    if not await redis.get(key):
                        with i18n.use_locale(locale):  # M8: текст И кнопка в локали юзера
                            await notification_service.notify_by_id(
                                chat_id=user.tg_id,
                                text=_("task:message:expiry").format(days=d),
                                reply_markup=extend_kb(user),
                            )
                        await redis.set(key, "1", ex=NOTIFY_TTL)
                    break  # m2: один (самый срочный) порог за раз

        # --- пороги по трафику ---
        if cd._traffic_total and cd._traffic_total > 0:  # безлимит (-1/0) → пропускаем
            used_ratio = cd._traffic_used / cd._traffic_total
            for t in TRAFFIC_THRESHOLDS:  # [1.0, 0.8] — по убыванию, шлём максимальный
                if used_ratio >= t:
                    key = f"notify:traf:{user.tg_id}:{exp_ms}:{int(t * 100)}"
                    if not await redis.get(key):
                        # m2: 1.0 — доступ уже приостановлен панелью → «исчерпан», а не «заканчивается»
                        msg_key = "task:message:traffic_depleted" if t >= 1.0 else "task:message:traffic_used"
                        with i18n.use_locale(locale):
                            await notification_service.notify_by_id(
                                chat_id=user.tg_id,
                                text=_(msg_key).format(percent=int(t * 100)),
                                reply_markup=extend_kb(user),
                            )
                        await redis.set(key, "1", ex=NOTIFY_TTL)
                    break  # m2: только максимальный сработавший порог за ран

    logger.info("[expiry] Subscription check finished.")


def start_scheduler(
    session_factory: async_sessionmaker,
    redis: Redis,
    i18n: I18n,
    vpn_service: VPNService,
    notification_service: NotificationService,
) -> None:
    scheduler = AsyncIOScheduler()
    scheduler.add_job(
        notify_users_with_expiring_subscription,
        "interval",
        minutes=15,
        args=[session_factory, redis, i18n, vpn_service, notification_service],
        next_run_time=datetime.now(tz=timezone.utc),
        coalesce=True,          # m6: пропущенные раны схлопнуть в один, а не копить
        misfire_grace_time=300,
        max_instances=1,
    )
    scheduler.start()
