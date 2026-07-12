from __future__ import annotations

from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from app.bot.services import (
        ApprovalService,
        AuditService,
        InboundGroupService,
        NotificationService,
        PlanService,
        ServerPoolService,
        VPNService,
        ReferralService,
        SubscriptionService,
        PaymentStatsService,
        InviteStatsService,
    )

from dataclasses import dataclass


@dataclass
class ServicesContainer:
    server_pool: ServerPoolService
    plan: PlanService
    inbound_groups: InboundGroupService
    vpn: VPNService
    notification: NotificationService
    referral: ReferralService
    subscription: SubscriptionService
    payment_stats: PaymentStatsService
    invite_stats: InviteStatsService
    approval: ApprovalService
    audit: AuditService
