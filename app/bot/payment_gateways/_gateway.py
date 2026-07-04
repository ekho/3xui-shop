import logging
from abc import ABC, abstractmethod

from aiogram import Bot
from aiogram.fsm.storage.redis import RedisStorage
from aiogram.utils.i18n import I18n
from aiogram.utils.i18n import gettext as _
from aiogram.utils.i18n import lazy_gettext as __
from aiohttp.web import Application
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.models import ServicesContainer, SubscriptionData
from app.bot.routers.main_menu.handler import redirect_to_main_menu
from app.bot.utils.constants import (
    DEFAULT_LANGUAGE,
    EVENT_PAYMENT_CANCELED_TAG,
    EVENT_PAYMENT_SUCCEEDED_TAG,
    Currency,
    TransactionStatus,
)
from app.bot.utils.formatting import format_device_count, format_subscription_period
from app.config import Config
from app.db.models import Transaction, User

logger = logging.getLogger(__name__)

from app.bot.models import SubscriptionData
from app.bot.utils.constants import Currency


class PaymentGateway(ABC):
    name: str
    currency: Currency
    callback: str

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
        self.app = app
        self.config = config
        self.session = session
        self.storage = storage
        self.bot = bot
        self.i18n = i18n
        self.services = services

    @abstractmethod
    async def create_payment(self, data: SubscriptionData) -> str:
        pass

    @abstractmethod
    async def handle_payment_succeeded(self, payment_id: str) -> None:
        pass

    @abstractmethod
    async def handle_payment_canceled(self, payment_id: str) -> None:
        pass

    async def _on_payment_succeeded(
        self,
        payment_id: str,
        expected_status: TransactionStatus | None = TransactionStatus.PENDING,
    ) -> None:
        """Провижининг после оплаты.

        B2 — идемпотентность: для шлюзов с PENDING-жизненным циклом (Cryptomus/Heleket/YooKassa/
        YooMoney/manual) переход PENDING→COMPLETED делается атомарным CAS; повторная доставка
        вебхука проигрывает гонку и выходит без повторного провижининга.
        Для Stars транзакция создаётся сразу COMPLETED в successful_payment (там же дедуп по
        charge_id через Transaction.create), поэтому вызывается с expected_status=None.
        """
        logger.info(f"Payment succeeded {payment_id}")

        async with self.session() as session:
            transaction = await Transaction.get_by_id(session=session, payment_id=payment_id)
            if transaction is None:
                logger.error(f"Payment {payment_id}: transaction not found; skip provisioning.")
                await self.services.notification.notify_developer(
                    text=f"{EVENT_PAYMENT_SUCCEEDED_TAG}\n\nTransaction {payment_id} not found."
                )
                return

            data = SubscriptionData.unpack(transaction.subscription)
            logger.debug(f"Subscription data unpacked: {data}")
            user = await User.get(session=session, tg_id=data.user_id)
            if user is None:
                # M7/B2: деньги списаны, но провижинить некому — алерт админу, не тихий крэш.
                logger.error(f"Payment {payment_id}: user {data.user_id} not found; manual action required.")
                await self.services.notification.notify_developer(
                    text=f"{EVENT_PAYMENT_SUCCEEDED_TAG}\n\nUser {data.user_id} not found for paid "
                    f"{payment_id}. Manual re-provision required."
                )
                return

            if expected_status is not None:
                won = await Transaction.set_status_atomic(
                    session, payment_id, expected_status, TransactionStatus.COMPLETED
                )
                if not won:
                    logger.info(
                        f"Payment {payment_id} already processed (status != {expected_status.value}); skip."
                    )
                    return
            else:
                await Transaction.update(
                    session=session,
                    payment_id=payment_id,
                    status=TransactionStatus.COMPLETED,
                )

        if self.config.shop.REFERRER_REWARD_ENABLED:
            await self.services.referral.add_referrers_rewards_on_payment(
                referred_tg_id=data.user_id,
                payment_amount=data.price,  # TODO: (!) add currency unified processing
                payment_id=payment_id,
            )

        await self.services.notification.notify_developer(
            text=EVENT_PAYMENT_SUCCEEDED_TAG
            + "\n\n"
            + _("payment:event:payment_succeeded").format(
                payment_id=payment_id,
                user_id=user.tg_id,
                devices=format_device_count(data.devices),
                duration=format_subscription_period(data.duration),
            ),
        )

        locale = user.language_code if user else DEFAULT_LANGUAGE
        with self.i18n.use_locale(locale):
            await redirect_to_main_menu(
                bot=self.bot,
                user=user,
                services=self.services,
                config=self.config,
                storage=self.storage,
            )

            # B2: провижининг после CAS. Если упадёт — статус уже COMPLETED, повтор вебхука
            # будет отброшен как обработанный, поэтому НЕ проглатываем молча: алерт разработчику
            # с пометкой "ручной re-provision", иначе получим "оплачено, но не выдано".
            try:
                if data.is_extend:
                    await self.services.vpn.extend_subscription(
                        user=user,
                        devices=data.devices,
                        duration=data.duration,
                        traffic_gb=data.traffic,  # G2
                    )
                    logger.info(f"Subscription extended for user {user.tg_id}")
                    await self.services.notification.notify_extend_success(
                        user_id=user.tg_id,
                        data=data,
                    )
                elif data.is_change:
                    await self.services.vpn.change_subscription(
                        user=user,
                        devices=data.devices,
                        duration=data.duration,
                        traffic_gb=data.traffic,  # G2
                    )
                    logger.info(f"Subscription changed for user {user.tg_id}")
                    await self.services.notification.notify_change_success(
                        user_id=user.tg_id,
                        data=data,
                    )
                else:
                    await self.services.vpn.create_subscription(
                        user=user,
                        devices=data.devices,
                        duration=data.duration,
                        traffic_gb=data.traffic,  # G2
                    )
                    logger.info(f"Subscription created for user {user.tg_id}")
                    key = await self.services.vpn.get_key(user)
                    await self.services.notification.notify_purchase_success(
                        user_id=user.tg_id,
                        key=key,
                    )
            except Exception as exception:
                logger.error(f"Provisioning failed for paid {payment_id} (user {user.tg_id}): {exception}")
                await self.services.notification.notify_developer(
                    text=f"{EVENT_PAYMENT_SUCCEEDED_TAG}\n\n⚠️ Provisioning FAILED for paid "
                    f"{payment_id} (user {user.tg_id}). Manual re-provision required: {exception}"
                )

    async def _on_payment_canceled(self, payment_id: str) -> None:
        logger.info(f"Payment canceled {payment_id}")
        async with self.session() as session:
            transaction = await Transaction.get_by_id(session=session, payment_id=payment_id)
            if transaction is None:
                logger.warning(f"Payment cancel {payment_id}: transaction not found; skip.")
                return
            data = SubscriptionData.unpack(transaction.subscription)

            # B2: cancel-вебхук после completed НЕ должен перетирать статус и слать ложное уведомление.
            won = await Transaction.set_status_atomic(
                session, payment_id, TransactionStatus.PENDING, TransactionStatus.CANCELED
            )
            if not won:
                logger.info(f"Payment {payment_id} already finalized; ignore cancel.")
                return

        await self.services.notification.notify_developer(
            text=EVENT_PAYMENT_CANCELED_TAG
            + "\n\n"
            + _("payment:event:payment_canceled").format(
                payment_id=payment_id,
                user_id=data.user_id,
                devices=format_device_count(data.devices),
                duration=format_subscription_period(data.duration),
            ),
        )
