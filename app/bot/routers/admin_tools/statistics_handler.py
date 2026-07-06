import logging

from aiogram import F, Router
from aiogram.types import CallbackQuery
from aiogram.utils.i18n import gettext as _
from sqlalchemy.ext.asyncio import AsyncSession

from app.bot.filters import IsAdmin
from app.bot.models import ServicesContainer
from app.bot.payment_gateways import GatewayFactory
from app.bot.utils.constants import Currency
from app.bot.utils.navigation import NavAdminTools
from app.db.models import Server, User

from .keyboard import statistics_keyboard

logger = logging.getLogger(__name__)
router = Router(name=__name__)


def _percent(part: int, total: int) -> str:
    return f"{round(part / total * 100)}%" if total else "—"


def _format_revenue(revenue: dict[str, float]) -> str:
    if not revenue:
        return "• " + _("statistics:revenue:none")
    lines = []
    for code, amount in revenue.items():
        symbol = Currency.from_code(code).symbol
        lines.append(f"• {amount:.2f} {symbol}")
    return "\n".join(lines)


@router.callback_query(F.data == NavAdminTools.STATISTICS, IsAdmin())
async def callback_statistics(
    callback: CallbackQuery,
    user: User,
    session: AsyncSession,
    services: ServicesContainer,
    gateway_factory: GatewayFactory,
) -> None:
    logger.info(f"Admin {user.tg_id} opened statistics.")

    payment_method_currencies = {
        gateway.callback: gateway.currency.code for gateway in gateway_factory.get_gateways()
    }
    stats = await services.invite_stats.get_global_stats(
        session=session, payment_method_currencies=payment_method_currencies
    )

    servers = await Server.get_all(session)
    online_servers_count = sum(1 for server in servers if server.online)
    total_clients = sum(server.current_clients for server in servers)

    text = _("statistics:message:main").format(
        revenue=_format_revenue(stats.revenue),
        users_count=stats.users_count,
        trial_users_count=stats.trial_users_count,
        trial_percent=_percent(stats.trial_users_count, stats.users_count),
        paid_users_count=stats.paid_users_count,
        signup_to_paid_percent=_percent(stats.paid_users_count, stats.users_count),
        repeat_customers_count=stats.repeat_customers_count,
        repeat_percent=_percent(stats.repeat_customers_count, stats.paid_users_count),
        active_subscriptions_count=stats.active_subscriptions_count,
        servers_count=len(servers),
        online_servers_count=online_servers_count,
        total_clients=total_clients,
    )

    await callback.message.edit_text(text=text, reply_markup=statistics_keyboard())
