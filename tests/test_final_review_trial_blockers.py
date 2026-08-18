import unittest
from contextlib import asynccontextmanager
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from app.bot.services.inbound_groups import EmptyInboundSetError, InboundGroupService
from app.bot.services.subscription import SubscriptionService
from app.bot.services.vpn import VPNService
from app.bot.services.xui_clients import ClientView


class SelfServiceTrialProfileTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        @asynccontextmanager
        async def session_factory():
            yield object()

        self.vpn = SimpleNamespace(
            inbound_group_service=object.__new__(InboundGroupService),
            create_client=AsyncMock(return_value=True),
            select_access_profile=AsyncMock(),
            process_bonus_days=AsyncMock(),
        )
        self.service = SubscriptionService(
            config=SimpleNamespace(
                shop=SimpleNamespace(
                    TRIAL_ENABLED=True,
                    REFERRED_TRIAL_ENABLED=True,
                    TRIAL_PERIOD=3,
                    REFERRED_TRIAL_PERIOD=7,
                    BONUS_DEVICES_COUNT=2,
                    TRIAL_TRAFFIC_GB=15,
                )
            ),
            session_factory=session_factory,
            vpn_service=self.vpn,
        )
        self.user = SimpleNamespace(
            tg_id=42,
            server_id=None,
            is_trial_used=False,
            inbound_groups=["euru", "banned"],
        )

    @patch("app.bot.services.subscription.User.update_trial_status", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.Referral.get_referral", new_callable=AsyncMock)
    async def test_regular_trial_creates_client_with_regular_profile_and_preserves_banned(
        self,
        get_referral,
        update_trial_status,
    ) -> None:
        get_referral.return_value = None
        update_trial_status.return_value = True

        result = await self.service.gift_trial(self.user)

        self.assertTrue(result)
        self.vpn.create_client.assert_awaited_once_with(
            user=self.user,
            duration=3,
            devices=2,
            total_gb=16106127360,
            groups=["banned", "regular"],
        )
        self.vpn.select_access_profile.assert_not_awaited()
        self.vpn.process_bonus_days.assert_not_awaited()

    @patch("app.bot.services.subscription.User.update_trial_status", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.Referral.get_referral", new_callable=AsyncMock)
    async def test_regular_trial_status_failure_does_not_change_profile(
        self,
        get_referral,
        update_trial_status,
    ) -> None:
        get_referral.return_value = None
        update_trial_status.return_value = False

        result = await self.service.gift_trial(self.user)

        self.assertFalse(result)
        self.assertEqual(self.user.inbound_groups, ["euru", "banned"])
        self.vpn.create_client.assert_not_awaited()
        self.vpn.select_access_profile.assert_not_awaited()
        self.vpn.process_bonus_days.assert_not_awaited()

    @patch("app.bot.services.subscription.User.update_trial_status", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.Referral.get_referral", new_callable=AsyncMock)
    async def test_failed_regular_trial_creation_rolls_back_without_changing_profile(
        self,
        get_referral,
        update_trial_status,
    ) -> None:
        get_referral.return_value = None
        update_trial_status.return_value = True
        self.vpn.create_client.return_value = False

        result = await self.service.gift_trial(self.user)

        self.assertFalse(result)
        self.vpn.create_client.assert_awaited_once_with(
            user=self.user,
            duration=3,
            devices=2,
            total_gb=16106127360,
            groups=["banned", "regular"],
        )
        self.assertEqual(
            [awaited.kwargs["used"] for awaited in update_trial_status.await_args_list],
            [True, False],
        )
        self.assertEqual(self.user.inbound_groups, ["euru", "banned"])
        self.vpn.select_access_profile.assert_not_awaited()
        self.vpn.process_bonus_days.assert_not_awaited()

    @patch("app.bot.services.subscription.User.update_trial_status", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.Referral.get_referral", new_callable=AsyncMock)
    async def test_empty_regular_inbound_rolls_back_without_escaping(
        self,
        get_referral,
        update_trial_status,
    ) -> None:
        get_referral.return_value = None
        update_trial_status.return_value = True
        self.vpn.create_client.side_effect = EmptyInboundSetError(
            ["banned", "regular"],
            "test-server",
        )

        result = await self.service.gift_trial(self.user)

        self.assertFalse(result)
        self.assertEqual(
            [awaited.kwargs["used"] for awaited in update_trial_status.await_args_list],
            [True, False],
        )
        self.assertEqual(self.user.inbound_groups, ["euru", "banned"])
        self.vpn.select_access_profile.assert_not_awaited()
        self.vpn.process_bonus_days.assert_not_awaited()


class StarterTrialPersistenceTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        self.vpn = object.__new__(VPNService)
        self.vpn.config = SimpleNamespace(
            shop=SimpleNamespace(
                BONUS_DEVICES_COUNT=2,
                TRIAL_PERIOD=3,
                TRIAL_TRAFFIC_GB=15,
            )
        )
        self.vpn.inbound_group_service = object.__new__(InboundGroupService)
        self.vpn.server_pool_service = SimpleNamespace(
            get_connection=AsyncMock(return_value=None),
        )
        self.vpn.is_client_exists = AsyncMock(return_value=None)
        self.vpn.update_client = AsyncMock(return_value=True)
        self.vpn.apply_inbound_groups = AsyncMock(return_value=True)
        self.vpn.reset_traffic = AsyncMock(return_value=True)
        self.vpn._persist_groups = AsyncMock()
        self.vpn._enforce_ban = AsyncMock()

    async def test_unlimited_to_euru_refuses_assigned_server_outage(self) -> None:
        user = SimpleNamespace(
            tg_id=42,
            server_id=9,
            inbound_groups=["unlimited", "banned"],
        )

        result = await self.vpn.select_access_profile(user, "euru")

        self.assertFalse(result)
        self.vpn._persist_groups.assert_not_awaited()

    async def test_unlimited_to_regular_refuses_assigned_server_outage(self) -> None:
        user = SimpleNamespace(
            tg_id=42,
            server_id=9,
            inbound_groups=["unlimited", "banned"],
        )

        result = await self.vpn.revoke_unlimited(user)

        self.assertFalse(result)
        self.vpn._persist_groups.assert_not_awaited()

    async def test_unlimited_to_euru_persists_for_unassigned_user_without_client(self) -> None:
        user = SimpleNamespace(
            tg_id=42,
            server_id=None,
            inbound_groups=["unlimited", "banned"],
        )

        result = await self.vpn.select_access_profile(user, "euru")

        self.assertTrue(result)
        self.vpn._persist_groups.assert_awaited_once_with(user, ["banned", "euru"])


class PanelMembershipPersistenceTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        self.vpn = object.__new__(VPNService)
        self.vpn.inbound_group_service = object.__new__(InboundGroupService)
        self.connection = SimpleNamespace(api=object())
        self.vpn.server_pool_service = SimpleNamespace(
            get_connection=AsyncMock(return_value=self.connection),
        )
        self.vpn._resolve_inbounds = AsyncMock(return_value=[11])
        self.vpn.inbound_group_service.managed_inbound_ids = AsyncMock(return_value={1, 11})
        self.vpn._persist_groups = AsyncMock()
        self.vpn._mirror_group_label = AsyncMock()
        self.clients = SimpleNamespace(
            get=AsyncMock(
                return_value=ClientView(
                    email="42",
                    inbound_ids=[1],
                    raw={"enable": True},
                )
            ),
            attach=AsyncMock(),
            detach=AsyncMock(),
            set_clients_enabled=AsyncMock(),
        )
        self.user = SimpleNamespace(tg_id=42, inbound_groups=["euru"])

    async def test_attach_failure_does_not_persist_groups(self) -> None:
        self.clients.attach.side_effect = RuntimeError("panel unavailable")

        with patch.object(VPNService, "_clients", return_value=self.clients):
            result = await self.vpn.apply_inbound_groups(self.user, groups=["regular"])

        self.assertFalse(result)
        self.vpn._persist_groups.assert_not_awaited()

    async def test_detach_failure_does_not_persist_groups(self) -> None:
        self.clients.detach.side_effect = RuntimeError("panel unavailable")

        with patch.object(VPNService, "_clients", return_value=self.clients):
            result = await self.vpn.apply_inbound_groups(self.user, groups=["regular"])

        self.assertFalse(result)
        self.vpn._persist_groups.assert_not_awaited()


if __name__ == "__main__":
    unittest.main()
