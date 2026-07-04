import logging
import uuid

from aiogram import Bot
from aiogram.fsm.storage.redis import RedisStorage
from aiogram.utils.i18n import I18n
from aiogram.utils.i18n import lazy_gettext as __
from aiohttp.web import Application
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.models import ServicesContainer, SubscriptionData
from app.bot.payment_gateways import PaymentGateway
from app.bot.utils.constants import Currency, TransactionStatus
from app.bot.utils.navigation import NavSubscription
from app.config import Config
from app.db.models import Transaction

logger = logging.getLogger(__name__)


class ManualCard(PaymentGateway):
    """G3: ручная оплата «карта→карта». Вебхука нет — подтверждает админ (Этап 4).

    Нужен в фабрике, чтобы (а) появиться кнопкой способа оплаты и (б) переиспользовать
    наследуемый _on_payment_succeeded/_on_payment_canceled (с идемпотентностью B2).
    create_payment возвращает НЕ URL, а payment_id — его использует manual_handler.
    """

    name = ""
    currency = Currency.RUB
    callback = NavSubscription.PAY_MANUAL

    def __init__(
        self,
        app: Application,
        config: Config,
        session: async_sessionmaker,
        storage: RedisStorage,
        bot: Bot,
        i18n: I18n,
        services: ServicesContainer,
    ) -> None:
        self.name = __("payment:gateway:manual")
        self.app = app
        self.config = config
        self.session = session
        self.storage = storage
        self.bot = bot
        self.i18n = i18n
        self.services = services
        logger.info("Manual card payment gateway initialized.")

    async def create_payment(self, data: SubscriptionData) -> str:
        # M12: максимум одна активная PENDING-заявка на юзера — иначе юзер плодит неотличимые
        # заявки и два админа могут заапрувить одну оплату (payment_id разные, дедуп по id не спасёт).
        async with self.session() as session:
            for txn in await Transaction.get_by_user(session=session, tg_id=data.user_id):
                if txn.status != TransactionStatus.PENDING:
                    continue
                try:
                    if SubscriptionData.unpack(txn.subscription).state == NavSubscription.PAY_MANUAL:
                        logger.info(f"Reusing pending manual transaction {txn.payment_id} for {data.user_id}.")
                        return txn.payment_id
                except Exception:
                    continue

            payment_id = str(uuid.uuid4())
            await Transaction.create(
                session=session,
                tg_id=data.user_id,
                subscription=data.pack(),
                payment_id=payment_id,
                status=TransactionStatus.PENDING,
            )
        return payment_id  # не URL: используется в manual_handler

    async def handle_payment_succeeded(self, payment_id: str) -> None:
        await self._on_payment_succeeded(payment_id)

    async def handle_payment_canceled(self, payment_id: str) -> None:
        await self._on_payment_canceled(payment_id)
