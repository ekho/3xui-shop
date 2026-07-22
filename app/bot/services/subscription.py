from __future__ import annotations

import logging
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from enum import StrEnum
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from app.bot.services import VPNService

from sqlalchemy.ext.asyncio import async_sessionmaker

from app.bot.utils.constants import DEFAULT_LANGUAGE, ApprovalStatus
from app.bot.utils.misc import generate_sub_id
from app.config import Config
from app.db.models import Referral, User

logger = logging.getLogger(__name__)


class AdminTrialStatus(StrEnum):
    CREATED = "created"
    ALREADY_EXISTS = "already_exists"
    TRIAL_DISABLED = "trial_disabled"
    NO_SERVER = "no_server"
    PROVISION_FAILED = "provision_failed"
    PARTIAL_PROVISION = "partial_provision"


@dataclass(frozen=True)
class AdminTrialResult:
    status: AdminTrialStatus
    user: User | None = None


class SubscriptionService:
    def __init__(
        self,
        config: Config,
        session_factory: async_sessionmaker,
        vpn_service: VPNService,
    ) -> None:
        self.config = config
        self.session_factory = session_factory
        self.vpn_service = vpn_service
        logger.info("Subscription Service initialized")

    async def create_admin_trial(
        self, tg_id: int, first_name: str, *, approved_by: int
    ) -> AdminTrialResult:
        """Создать нового одобренного пользователя и выдать ему обычный триал.

        Этот путь предназначен для администратора: клиент может ещё не открывать
        бота, но получает те же параметры и provisioning, что и при самостоятельном
        получении триала. При неясном результате панели запись сохраняется и триал
        отмечается использованным, чтобы повтор не выдал второй доступ.
        """
        if not self.config.shop.TRIAL_ENABLED:
            return AdminTrialResult(AdminTrialStatus.TRIAL_DISABLED)

        async with self.session_factory() as session:
            if await User.get(session=session, tg_id=tg_id):
                return AdminTrialResult(AdminTrialStatus.ALREADY_EXISTS)

        if await self.vpn_service.get_available_server() is None:
            return AdminTrialResult(AdminTrialStatus.NO_SERVER)

        async with self.session_factory() as session:
            user = await User.create(
                session=session,
                tg_id=tg_id,
                vpn_id=str(uuid.uuid4()),
                sub_id=generate_sub_id(),
                first_name=first_name,
                language_code=DEFAULT_LANGUAGE,
                approval_status=ApprovalStatus.APPROVED,
                approval_decided_at=datetime.now(timezone.utc),
                approval_decided_by=approved_by,
            )

        if user is None:
            logger.warning(f"Admin trial user {tg_id} was created concurrently.")
            return AdminTrialResult(AdminTrialStatus.ALREADY_EXISTS)

        if await self.gift_trial(user):
            return AdminTrialResult(AdminTrialStatus.CREATED, user)

        try:
            panel_client = await self.vpn_service.is_client_exists(user)
        except Exception as exception:  # noqa: BLE001 — сохраним запись при неизвестном ответе панели
            logger.error(f"Unable to confirm failed admin trial provisioning for {tg_id}: {exception}")
            panel_client = True

        if panel_client:
            async with self.session_factory() as session:
                await User.update_trial_status(session=session, tg_id=tg_id, used=True)
            return AdminTrialResult(AdminTrialStatus.PARTIAL_PROVISION, user)

        await User.delete(self.session_factory, tg_id)
        return AdminTrialResult(AdminTrialStatus.PROVISION_FAILED)

    async def is_trial_available(self, user: User) -> bool:
        is_first_check_ok = (
            self.config.shop.TRIAL_ENABLED and not user.server_id and not user.is_trial_used
        )

        if not is_first_check_ok:
            return False

        async with self.session_factory() as session:
            referral = await Referral.get_referral(session, user.tg_id)

        return not referral or (referral and not self.config.shop.REFERRED_TRIAL_ENABLED)

    async def gift_trial(self, user: User) -> bool:
        if not await self.is_trial_available(user=user):
            logger.warning(
                f"Failed to activate trial for user {user.tg_id}. Trial period is not available."
            )
            return False

        async with self.session_factory() as session:
            trial_used = await User.update_trial_status(
                session=session, tg_id=user.tg_id, used=True
            )

        if not trial_used:
            logger.critical(f"Failed to activate trial for user {user.tg_id}.")
            return False

        logger.info(f"Begun giving trial period for user {user.tg_id}.")
        trial_success = await self.vpn_service.process_bonus_days(
            user,
            duration=self.config.shop.TRIAL_PERIOD,
            devices=self.config.shop.BONUS_DEVICES_COUNT,
            traffic_gb=self.config.shop.TRIAL_TRAFFIC_GB,
        )

        if trial_success:
            logger.info(
                f"Successfully gave {self.config.shop.TRIAL_PERIOD} days to a user {user.tg_id}"
            )
            return True

        async with self.session_factory() as session:
            await User.update_trial_status(session=session, tg_id=user.tg_id, used=False)

        logger.warning(f"Failed to apply trial period for user {user.tg_id} due to failure.")
        return False
