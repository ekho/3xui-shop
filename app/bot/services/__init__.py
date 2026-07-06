from aiogram import Bot
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.models import ServicesContainer
from app.config import Config

from .inbound_groups import InboundGroupService
from .invite_stats import InviteStatsService
from .notification import NotificationService
from .payment_stats import PaymentStatsService
from .plan import PlanService
from .referral import ReferralService
from .server_pool import ServerPoolService
from .subscription import SubscriptionService
from .vpn import VPNService


async def initialize(
    config: Config,
    session: async_sessionmaker,
    bot: Bot,
) -> ServicesContainer:
    server_pool = ServerPoolService(config=config, session=session)
    plan = PlanService(session_factory=session)
    await plan.load()
    # Реестра групп в боте нет: список групп живёт в панели (страница Groups)
    # и синкается сервисом по требованию.
    inbound_groups = InboundGroupService(session_factory=session)
    vpn = VPNService(
        config=config,
        session=session,
        server_pool_service=server_pool,
        plan_service=plan,
        inbound_group_service=inbound_groups,
    )
    notification = NotificationService(config=config, bot=bot)
    referral = ReferralService(config=config, session_factory=session, vpn_service=vpn)
    subscription = SubscriptionService(config=config, session_factory=session, vpn_service=vpn)
    payment_stats = PaymentStatsService(session_factory=session)
    invite_stats = InviteStatsService(session_factory=session, payment_stats_service=payment_stats)

    return ServicesContainer(
        server_pool=server_pool,
        plan=plan,
        inbound_groups=inbound_groups,
        vpn=vpn,
        notification=notification,
        referral=referral,
        subscription=subscription,
        payment_stats=payment_stats,
        invite_stats=invite_stats,
    )
