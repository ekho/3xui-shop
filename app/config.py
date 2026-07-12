import logging
import os
from dataclasses import dataclass
from logging.handlers import MemoryHandler
from pathlib import Path

from environs import Env
from marshmallow.validate import OneOf, Range

from app.bot.utils.constants import (
    DB_FORMAT,
    LOG_GZ_ARCHIVE_FORMAT,
    LOG_ZIP_ARCHIVE_FORMAT,
    Currency,
    ReferrerRewardType,
)

BASE_DIR = Path(__file__).resolve().parent
DEFAULT_DATA_DIR = BASE_DIR / "data"
DEFAULT_LOCALES_DIR = BASE_DIR / "locales"
DEFAULT_PLANS_DIR = DEFAULT_DATA_DIR / "plans.json"

DEFAULT_BOT_HOST = "0.0.0.0"
DEFAULT_BOT_PORT = 8080
DEFAULT_BOT_TIMEZONE = "UTC"  # часовой пояс для календарных джобов (месячный сброс безлимита)
DEFAULT_AUDIT_RETENTION_DAYS = 365  # хранить события аудит-лога год; прун-джоб чистит старше

DEFAULT_SHOP_APPROVAL_REQUIRED = True
DEFAULT_SHOP_EMAIL = "support@3xui-shop.com"
DEFAULT_SHOP_CURRENCY = Currency.RUB.code
DEFAULT_SHOP_TRIAL_ENABLED = True
DEFAULT_SHOP_TRIAL_PERIOD = 3
DEFAULT_SHOP_TRIAL_TRAFFIC_GB = 15  # лимит трафика триала в ГБ (0 = безлимит)
DEFAULT_SHOP_REFERRED_TRIAL_ENABLED = False
DEFAULT_SHOP_REFERRED_TRIAL_PERIOD = 7
DEFAULT_SHOP_REFERRER_REWARD_ENABLED = True
DEFAULT_SHOP_REFERRER_REWARD_TYPE = ReferrerRewardType.DAYS.value
DEFAULT_SHOP_REFERRER_LEVEL_ONE_PERIOD = 10
DEFAULT_SHOP_REFERRER_LEVEL_TWO_PERIOD = 3
DEFAULT_SHOP_REFERRER_LEVEL_ONE_RATE = 50
DEFAULT_SHOP_REFERRER_LEVEL_TWO_RATE = 5
DEFAULT_SHOP_BONUS_DEVICES_COUNT = 1
DEFAULT_SHOP_PAYMENT_STARS_ENABLED = True
DEFAULT_SHOP_PAYMENT_CRYPTOMUS_ENABLED = False
DEFAULT_SHOP_PAYMENT_HELEKET_ENABLED = False
DEFAULT_SHOP_PAYMENT_YOOKASSA_ENABLED = False
DEFAULT_SHOP_PAYMENT_YOOMONEY_ENABLED = False
DEFAULT_SHOP_PAYMENT_MANUAL_ENABLED = False
DEFAULT_DB_NAME = "bot_database"

DEFAULT_REDIS_DB_NAME = "0"
DEFAULT_REDIS_HOST = "3xui-shop-redis"
DEFAULT_REDIS_PORT = 6379


DEFAULT_LOG_LEVEL = "DEBUG"
DEFAULT_LOG_FORMAT = "%(asctime)s | %(name)s | %(levelname)s | %(message)s"
DEFAULT_LOG_ARCHIVE_FORMAT = LOG_ZIP_ARCHIVE_FORMAT

logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)
memory_handler = MemoryHandler(capacity=100, flushLevel=logging.ERROR)
memory_handler.setFormatter(logging.Formatter(DEFAULT_LOG_FORMAT))
logger.addHandler(memory_handler)


def env_or_file(env: Env, name: str, default: str | None = None, required: bool = False) -> str | None:
    """Read a STRING secret from a file via the ``<NAME>_FILE`` convention (docker compose
    secrets mounted at /run/secrets/<name>), falling back to the plain ``<NAME>`` env var.

    Only for string secrets (BOT_TOKEN, XUI_PASSWORD, *_API_KEY, WEBHOOK_SECRET, ...).
    Numeric/list vars keep typed reading via env.int/env.list to preserve subcast/validation.
    """
    path = env.str(f"{name}_FILE", default=None)
    if path and os.path.isfile(path):
        with open(path, encoding="utf-8") as file:
            return file.read().strip()
    if required:
        return env.str(name)  # raises if missing — preserves previous required semantics
    return env.str(name, default=default)


@dataclass
class BotConfig:
    TOKEN: str
    ADMINS: list[int]
    DEV_ID: int
    SUPPORT_ID: int
    # Прокси-поддержка: отдельный бот-«прокси» между юзером и админами. Включается,
    # только когда заданы ОБА значения; иначе кнопка поддержки ведёт в личку SUPPORT_ID.
    SUPPORT_BOT_TOKEN: str | None
    SUPPORT_GROUP_ID: int | None  # супергруппа-форум (Topics включены, бот — админ с can_manage_topics)
    SUPPORT_BOT_USERNAME: str | None  # заполняется на старте из get_me(); в env не задаётся
    DOMAIN: str
    PORT: int
    WEBHOOK_SECRET: str | None
    API_URL: str | None  # кастомный Telegram Bot API (напр. локальный/зеркало); None → api.telegram.org
    API_IS_LOCAL: bool  # True для self-hosted telegram-bot-api в режиме --local (файлы на диске сервера)
    USE_WEBHOOK: bool  # True → вебхук (нужен домен + TLS reverse-proxy); False → long-polling (getUpdates, домен/вебхук не нужны)
    TIMEZONE: str  # IANA-имя (напр. Europe/Moscow) для календарных джобов; резолвится pytz
    AUDIT_RETENTION_DAYS: int  # хранить события аудит-лога N дней; прун-джоб чистит старше
    # Зеркало аудита пишется в топик General группы поддержки (SUPPORT_GROUP_ID) —
    # отдельного канала/переменной нет; без support-бота остаётся только БД.


@dataclass
class ShopConfig:
    APPROVAL_REQUIRED: bool
    EMAIL: str
    CURRENCY: str
    TRIAL_ENABLED: bool
    TRIAL_PERIOD: int
    TRIAL_TRAFFIC_GB: int
    REFERRED_TRIAL_ENABLED: bool
    REFERRED_TRIAL_PERIOD: int
    REFERRER_REWARD_ENABLED: bool
    REFERRER_REWARD_TYPE: str
    REFERRER_LEVEL_ONE_PERIOD: int
    REFERRER_LEVEL_TWO_PERIOD: int
    REFERRER_LEVEL_ONE_RATE: int
    REFERRER_LEVEL_TWO_RATE: int
    BONUS_DEVICES_COUNT: int
    PAYMENT_STARS_ENABLED: bool
    PAYMENT_CRYPTOMUS_ENABLED: bool
    PAYMENT_HELEKET_ENABLED: bool
    PAYMENT_YOOKASSA_ENABLED: bool
    PAYMENT_YOOMONEY_ENABLED: bool
    PAYMENT_MANUAL_ENABLED: bool  # G3: ручная карта
    MANUAL_CARD_DETAILS: str | None


@dataclass
class XUIConfig:
    USERNAME: str | None
    PASSWORD: str | None
    TOKEN: str | None


@dataclass
class CryptomusConfig:
    API_KEY: str | None
    MERCHANT_ID: str | None

@dataclass
class HeleketConfig:
    API_KEY: str | None
    MERCHANT_ID: str | None

@dataclass
class YooKassaConfig:
    TOKEN: str | None
    SHOP_ID: int | None


@dataclass
class YooMoneyConfig:
    NOTIFICATION_SECRET: str | None
    WALLET_ID: str | None


@dataclass
class DatabaseConfig:
    HOST: str | None
    PORT: int | None
    NAME: str
    USERNAME: str | None
    PASSWORD: str | None

    def url(self, driver: str = "sqlite+aiosqlite") -> str:
        if driver.startswith("sqlite"):
            return f"{driver}:////{DEFAULT_DATA_DIR}/{self.NAME}.{DB_FORMAT}"
        return f"{driver}://{self.USERNAME}:{self.PASSWORD}@{self.HOST}:{self.PORT}/{self.NAME}"


@dataclass
class RedisConfig:
    HOST: str
    PORT: int
    DB_NAME: str
    USERNAME: str | None
    PASSWORD: str | None

    def url(self) -> str:
        if self.USERNAME and self.PASSWORD:
            return f"redis://{self.USERNAME}:{self.PASSWORD}@{self.HOST}:{self.PORT}/{self.DB_NAME}"
        return f"redis://{self.HOST}:{self.PORT}/{self.DB_NAME}"


@dataclass
class LoggingConfig:
    LEVEL: str
    FORMAT: str
    ARCHIVE_FORMAT: str


@dataclass
class Config:
    bot: BotConfig
    shop: ShopConfig
    xui: XUIConfig
    cryptomus: CryptomusConfig
    heleket: HeleketConfig
    yookassa: YooKassaConfig
    yoomoney: YooMoneyConfig
    database: DatabaseConfig
    redis: RedisConfig
    logging: LoggingConfig


def load_config() -> Config:
    env = Env()
    env.read_env()

    bot_admins = env.list("BOT_ADMINS", subcast=int, default=[], required=False)
    if not bot_admins:
        logger.warning("BOT_ADMINS list is empty.")

    # Нормализуем BOT_DOMAIN: убираем случайно указанную схему и хвостовые слэши,
    # иначе f"https://{...}" даёт https://https://... → Telegram: "bad webhook: Failed to resolve host".
    bot_domain = env.str("BOT_DOMAIN").strip().rstrip("/")
    for _scheme in ("https://", "http://"):
        if bot_domain.lower().startswith(_scheme):
            bot_domain = bot_domain[len(_scheme):]
    if not bot_domain or "/" in bot_domain or "." not in bot_domain:
        logger.warning(
            f"BOT_DOMAIN looks invalid: '{bot_domain}'. Ожидается публичный хост, напр. bot.example.com."
        )

    # P1: авторизация в 3x-ui — по токену ЛИБО username+password. py3xui 0.7.0 при заданном
    # token не требует (и не допускает) login по паролю; при токен-авторизации логин/пароль опциональны.
    xui_token = env_or_file(env, "XUI_TOKEN", default=None)
    xui_username = env.str("XUI_USERNAME", default=None)
    xui_password = env_or_file(env, "XUI_PASSWORD", default=None)
    if not xui_token and not (xui_username and xui_password):
        logger.error("XUI auth is not configured: set XUI_TOKEN or XUI_USERNAME + XUI_PASSWORD.")

    # Прокси-поддержка: нужны и токен второго бота, и id супергруппы-форума.
    # Полуконфигурация — это ошибка оператора: громко предупреждаем и выключаем фичу.
    support_bot_token = env_or_file(env, "SUPPORT_BOT_TOKEN", default=None)
    support_group_id = env.int("SUPPORT_GROUP_ID", default=None)
    if bool(support_bot_token) != bool(support_group_id):
        logger.warning(
            "Support proxy bot is disabled: set BOTH SUPPORT_BOT_TOKEN and SUPPORT_GROUP_ID "
            "(only one of them is configured)."
        )
        support_bot_token = None
        support_group_id = None

    payment_stars_enabled = env.bool(
        "SHOP_PAYMENT_STARS_ENABLED",
        default=DEFAULT_SHOP_PAYMENT_STARS_ENABLED,
    )

    payment_cryptomus_enabled = env.bool(
        "SHOP_PAYMENT_CRYPTOMUS_ENABLED",
        default=DEFAULT_SHOP_PAYMENT_CRYPTOMUS_ENABLED,
    )
    if payment_cryptomus_enabled:
        cryptomus_api_key = env_or_file(env, "CRYPTOMUS_API_KEY", default=None)
        cryptomus_merchant_id = env_or_file(env, "CRYPTOMUS_MERCHANT_ID", default=None)
        if not cryptomus_api_key or not cryptomus_merchant_id:
            logger.error(
                "CRYPTOMUS_API_KEY or CRYPTOMUS_MERCHANT_ID is not set. Payment Cryptomus is disabled."
            )
            payment_cryptomus_enabled = False

    payment_heleket_enabled = env.bool(
        "SHOP_PAYMENT_HELEKET_ENABLED",
        default=DEFAULT_SHOP_PAYMENT_HELEKET_ENABLED,
    )
    if payment_heleket_enabled:
        heleket_api_key = env.str("HELEKET_API_KEY", default=None)
        heleket_merchant_id = env.str("HELEKET_MERCHANT_ID", default=None)
        if not heleket_api_key or not heleket_merchant_id:
            logger.error(
                "HELEKET_API_KEY or HELEKET_MERCHANT_ID is not set. Payment Heleket is disabled."
            )
            payment_heleket_enabled = False

    payment_yookassa_enabled = env.bool(
        "SHOP_PAYMENT_YOOKASSA_ENABLED",
        default=DEFAULT_SHOP_PAYMENT_YOOKASSA_ENABLED,
    )
    if payment_yookassa_enabled:
        yookassa_token = env.str("YOOKASSA_TOKEN", default=None)
        yookassa_shop_id = env.int("YOOKASSA_SHOP_ID", default=None)
        if not yookassa_token or not yookassa_shop_id:
            logger.error(
                "YOOKASSA_TOKEN or YOOKASSA_SHOP_ID is not set. Payment YooKassa is disabled."
            )
            payment_yookassa_enabled = False

    payment_yoomoney_enabled = env.bool(
        "SHOP_PAYMENT_YOOMONEY_ENABLED",
        default=DEFAULT_SHOP_PAYMENT_YOOMONEY_ENABLED,
    )
    if payment_yoomoney_enabled:
        yoomoney_notification_secret = env_or_file(env, "YOOMONEY_NOTIFICATION_SECRET", default=None)
        yoomoney_wallet_id = env.str("YOOMONEY_WALLET_ID", default=None)
        if not yoomoney_notification_secret or not yoomoney_wallet_id:
            logger.error(
                "YOOMONEY_NOTIFICATION_SECRET or YOOMONEY_WALLET_ID is not set. Payment YooMoney is disabled."
            )
            payment_yoomoney_enabled = False

    payment_manual_enabled = env.bool(
        "SHOP_PAYMENT_MANUAL_ENABLED",
        default=DEFAULT_SHOP_PAYMENT_MANUAL_ENABLED,
    )
    manual_card_details = env_or_file(env, "MANUAL_CARD_DETAILS", default=None)
    if payment_manual_enabled and not manual_card_details:
        logger.error("MANUAL_CARD_DETAILS is not set. Manual card payment is disabled.")
        payment_manual_enabled = False

    if (
        not payment_stars_enabled
        and not payment_cryptomus_enabled
        and not payment_heleket_enabled
        and not payment_yookassa_enabled
        and not payment_yoomoney_enabled
        and not payment_manual_enabled
    ):
        logger.warning("No payment methods are enabled. Enabling Stars payment method.")
        payment_stars_enabled = True

    referrer_reward_type = env.str(
        "SHOP_REFERRED_REWARD_TYPE",
        default=DEFAULT_SHOP_REFERRER_REWARD_TYPE,
        validate=OneOf(
            [reward_type.value for reward_type in ReferrerRewardType],
            error="SHOP_REFERRER_REWARD_TYPE must be one of: {choices}",
        ),
    )
    referrer_reward_enabled = env.bool(
        "SHOP_REFERRER_REWARD_ENABLED", default=DEFAULT_SHOP_REFERRER_REWARD_ENABLED
    )
    if referrer_reward_type != ReferrerRewardType.DAYS.value:
        logger.error(
            "Only 'days' option is now available for SHOP_REFERRER_REWARD_TYPE. Referrer reward disabled."
        )
        referrer_reward_enabled = False

    return Config(
        bot=BotConfig(
            TOKEN=env_or_file(env, "BOT_TOKEN", required=True),
            ADMINS=bot_admins,
            DEV_ID=env.int("BOT_DEV_ID"),
            SUPPORT_ID=env.int("BOT_SUPPORT_ID"),
            SUPPORT_BOT_TOKEN=support_bot_token,
            SUPPORT_GROUP_ID=support_group_id,
            SUPPORT_BOT_USERNAME=None,
            DOMAIN=f"https://{bot_domain}",
            PORT=env.int("BOT_PORT", default=DEFAULT_BOT_PORT),
            WEBHOOK_SECRET=env_or_file(env, "WEBHOOK_SECRET", default=None),  # B7
            API_URL=env.str("TELEGRAM_API_URL", default=None),
            API_IS_LOCAL=env.bool("TELEGRAM_API_IS_LOCAL", default=False),
            USE_WEBHOOK=env.bool("BOT_USE_WEBHOOK", default=True),
            TIMEZONE=env.str("BOT_TIMEZONE", default=DEFAULT_BOT_TIMEZONE),
            AUDIT_RETENTION_DAYS=env.int(
                "AUDIT_RETENTION_DAYS", default=DEFAULT_AUDIT_RETENTION_DAYS
            ),
        ),
        shop=ShopConfig(
            APPROVAL_REQUIRED=env.bool(
                "SHOP_APPROVAL_REQUIRED", default=DEFAULT_SHOP_APPROVAL_REQUIRED
            ),
            EMAIL=env.str("SHOP_EMAIL", default=DEFAULT_SHOP_EMAIL),
            CURRENCY=env.str(
                "SHOP_CURRENCY",
                default=DEFAULT_SHOP_CURRENCY,
                validate=OneOf(
                    [currency.code for currency in Currency]
                    + [currency.code.lower() for currency in Currency],
                    error="SHOP_CURRENCY must be one of: {choices}",
                ),
            ).upper(),
            TRIAL_ENABLED=env.bool("SHOP_TRIAL_ENABLED", default=DEFAULT_SHOP_TRIAL_ENABLED),
            TRIAL_PERIOD=env.int("SHOP_TRIAL_PERIOD", default=DEFAULT_SHOP_TRIAL_PERIOD),
            TRIAL_TRAFFIC_GB=env.int(
                "SHOP_TRIAL_TRAFFIC_GB",
                default=DEFAULT_SHOP_TRIAL_TRAFFIC_GB,
                validate=Range(min=0, error="SHOP_TRIAL_TRAFFIC_GB must be >= 0"),
            ),
            REFERRED_TRIAL_ENABLED=env.bool(
                "SHOP_REFERRED_TRIAL_ENABLED", default=DEFAULT_SHOP_REFERRED_TRIAL_ENABLED
            ),
            REFERRED_TRIAL_PERIOD=env.int(
                "SHOP_REFERRED_TRIAL_PERIOD",
                default=DEFAULT_SHOP_REFERRED_TRIAL_PERIOD,
                validate=Range(min=1, error="SHOP_REFERRED_TRIAL_PERIOD must be >= 1"),
            ),
            REFERRER_REWARD_ENABLED=referrer_reward_enabled,
            REFERRER_REWARD_TYPE=referrer_reward_type,
            REFERRER_LEVEL_ONE_PERIOD=env.int(
                "SHOP_REFERRER_LEVEL_ONE_PERIOD",
                default=DEFAULT_SHOP_REFERRER_LEVEL_ONE_PERIOD,
                validate=Range(min=1, error="SHOP_REFERRER_LEVEL_ONE_PERIOD must be >= 1"),
            ),
            REFERRER_LEVEL_TWO_PERIOD=env.int(
                "SHOP_REFERRER_LEVEL_TWO_PERIOD",
                default=DEFAULT_SHOP_REFERRER_LEVEL_TWO_PERIOD,
                validate=Range(min=1, error="SHOP_REFERRER_LEVEL_TWO_PERIOD must be >= 1"),
            ),
            REFERRER_LEVEL_ONE_RATE=env.int(
                "SHOP_REFERRER_LEVEL_ONE_RATE",
                default=DEFAULT_SHOP_REFERRER_LEVEL_ONE_RATE,
                validate=Range(
                    min=1,
                    max=100,
                    error="SHOP_REFERRER_LEVEL_ONE_RATE must be between 1 and 100",
                ),
            ),
            REFERRER_LEVEL_TWO_RATE=env.int(
                "SHOP_REFERRER_LEVEL_TWO_RATE",
                default=DEFAULT_SHOP_REFERRER_LEVEL_TWO_RATE,
                validate=Range(
                    min=1,
                    max=100,
                    error="SHOP_REFERRER_LEVEL_TWO_RATE must be between 1 and 100",
                ),
            ),
            BONUS_DEVICES_COUNT=env.int(
                "SHOP_BONUS_DEVICES_COUNT", default=DEFAULT_SHOP_BONUS_DEVICES_COUNT
            ),
            PAYMENT_STARS_ENABLED=payment_stars_enabled,
            PAYMENT_CRYPTOMUS_ENABLED=payment_cryptomus_enabled,
            PAYMENT_HELEKET_ENABLED=payment_heleket_enabled,
            PAYMENT_YOOKASSA_ENABLED=payment_yookassa_enabled,
            PAYMENT_YOOMONEY_ENABLED=payment_yoomoney_enabled,
            PAYMENT_MANUAL_ENABLED=payment_manual_enabled,
            MANUAL_CARD_DETAILS=manual_card_details,
        ),
        xui=XUIConfig(
            USERNAME=xui_username,
            PASSWORD=xui_password,
            TOKEN=xui_token,
        ),
        cryptomus=CryptomusConfig(
            API_KEY=env_or_file(env, "CRYPTOMUS_API_KEY", default=None),
            MERCHANT_ID=env_or_file(env, "CRYPTOMUS_MERCHANT_ID", default=None),
        ),
        heleket=HeleketConfig(
            API_KEY=env.str("HELEKET_API_KEY", default=None),
            MERCHANT_ID=env.str("HELEKET_MERCHANT_ID", default=None),
        ),
        yookassa=YooKassaConfig(
            TOKEN=env.str("YOOKASSA_TOKEN", default=None),
            SHOP_ID=env.int("YOOKASSA_SHOP_ID", default=None),
        ),
        yoomoney=YooMoneyConfig(
            NOTIFICATION_SECRET=env_or_file(env, "YOOMONEY_NOTIFICATION_SECRET", default=None),
            WALLET_ID=env.str("YOOMONEY_WALLET_ID", default=None),
        ),
        database=DatabaseConfig(
            HOST=env.str("DB_HOST", default=None),
            PORT=env.int("DB_PORT", default=None),
            USERNAME=env.str("DB_USERNAME", default=None),
            PASSWORD=env.str("DB_PASSWORD", default=None),
            NAME=env.str("DB_NAME", default=DEFAULT_DB_NAME),
        ),
        redis=RedisConfig(
            HOST=env.str("REDIS_HOST", default=DEFAULT_REDIS_HOST),
            PORT=env.int("REDIS_PORT", default=DEFAULT_REDIS_PORT),
            DB_NAME=env.str("REDIS_DB_NAME", default=DEFAULT_REDIS_DB_NAME),
            USERNAME=env.str("REDIS_USERNAME", default=None),
            PASSWORD=env.str("REDIS_PASSWORD", default=None),
        ),
        logging=LoggingConfig(
            LEVEL=env.str("LOG_LEVEL", default=DEFAULT_LOG_LEVEL),
            FORMAT=env.str("LOG_FORMAT", default=DEFAULT_LOG_FORMAT),
            ARCHIVE_FORMAT=env.str(
                "LOG_ARCHIVE_FORMAT",
                default=DEFAULT_LOG_ARCHIVE_FORMAT,
                validate=OneOf(
                    [LOG_ZIP_ARCHIVE_FORMAT, LOG_GZ_ARCHIVE_FORMAT],
                    error="LOG_ARCHIVE_FORMAT must be one of: {choices}",
                ),
            ),
        ),
    )
