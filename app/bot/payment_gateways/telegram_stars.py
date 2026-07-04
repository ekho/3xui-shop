import logging

from aiogram import Bot
from aiogram.fsm.storage.redis import RedisStorage
from aiogram.types import LabeledPrice
from aiogram.utils.i18n import I18n
from aiogram.utils.i18n import gettext as _
from aiogram.utils.i18n import lazy_gettext as __
from aiohttp.web import Application
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.filters.is_dev import IsDev
from app.bot.models import ServicesContainer, SubscriptionData
from app.bot.payment_gateways import PaymentGateway
from app.bot.utils.constants import Currency
from app.bot.utils.formatting import format_device_count, format_subscription_period
from app.bot.utils.navigation import NavSubscription
from app.config import Config

logger = logging.getLogger(__name__)

# Telegram допускает единственное значение периода подписки Stars — 30 дней.
STARS_SUBSCRIPTION_PERIOD = 2592000


class TelegramStars(PaymentGateway):
    name = ""
    currency = Currency.XTR
    callback = NavSubscription.PAY_TELEGRAM_STARS

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
        self.name = __("payment:gateway:telegram_stars")
        self.app = app
        self.config = config
        self.session = session
        self.storage = storage
        self.bot = bot
        self.services = services
        self.i18n = i18n
        logger.info("TelegramStars payment gateway initialized.")

    async def create_payment(self, data: SubscriptionData) -> str:
        if await IsDev()(user_id=data.user_id):
            amount = 1
        else:
            amount = int(data.price)

        prices = [LabeledPrice(label=self.currency.code, amount=amount)]
        devices = format_device_count(data.devices)
        duration = format_subscription_period(data.duration)
        title = _("payment:invoice:title").format(devices=devices, duration=duration)
        description = _("payment:invoice:description").format(devices=devices, duration=duration)

        kwargs = dict(
            title=title,
            description=description,
            prices=prices,
            payload=data.pack(),
            currency=self.currency.code,
        )
        # G4: авто-рекуррент только для 30-дн. тарифа и только на «свежей» покупке
        # (не продление/смена). Продления/прочие сроки — разовые платежи.
        if data.duration == 30 and not data.is_extend and not data.is_change:
            kwargs["subscription_period"] = STARS_SUBSCRIPTION_PERIOD

        pay_url = await self.bot.create_invoice_link(**kwargs)
        logger.info(f"Payment link created for user {data.user_id}: {pay_url}")
        return pay_url

    async def handle_payment_succeeded(self, payment_id: str) -> None:
        # B2: транзакция Stars создаётся сразу COMPLETED в successful_payment (дедуп там же),
        # поэтому без PENDING-CAS — иначе первый же легитимный вызов был бы отброшен.
        await self._on_payment_succeeded(payment_id, expected_status=None)

    async def handle_payment_canceled(self, payment_id: str) -> None:
        await self._on_payment_canceled(payment_id)
