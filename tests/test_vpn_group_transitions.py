import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock, patch

from app.bot.services.inbound_groups import EmptyInboundSetError, InboundGroupService
from app.bot.services.vpn import VPNService, gb_to_bytes
from app.bot.services.xui_clients import ClientView


class VPNProfileSelectionTests(unittest.IsolatedAsyncioTestCase):
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
            get_connection=AsyncMock(return_value=SimpleNamespace(api=object()))
        )
        self.vpn.is_client_exists = AsyncMock(return_value=True)
        self.vpn.update_client = AsyncMock(return_value=True)
        self.vpn.apply_inbound_groups = AsyncMock(return_value=True)
        self.vpn.reset_traffic = AsyncMock(return_value=True)
        self.vpn._persist_groups = AsyncMock()
        self.vpn._enforce_ban = AsyncMock()
        self.vpn._clients = Mock(return_value=SimpleNamespace())

    async def test_select_euru_from_regular_replaces_regular_and_preserves_banned(self) -> None:
        user = SimpleNamespace(tg_id=42, inbound_groups=["regular", "banned"])

        result = await self.vpn.select_access_profile(user, "euru")

        self.assertTrue(result)
        self.vpn.apply_inbound_groups.assert_awaited_once_with(
            user,
            groups=["banned", "euru"],
        )

    async def test_select_regular_from_euru_replaces_euru_and_preserves_banned(self) -> None:
        user = SimpleNamespace(tg_id=42, inbound_groups=["euru", "banned"])

        result = await self.vpn.select_access_profile(user, "regular")

        self.assertTrue(result)
        self.vpn.apply_inbound_groups.assert_awaited_once_with(
            user,
            groups=["banned", "regular"],
        )

    async def test_select_euru_from_regular_persists_without_panel_client(self) -> None:
        user = SimpleNamespace(tg_id=42, inbound_groups=["regular", "banned"])
        self.vpn.is_client_exists.return_value = False

        result = await self.vpn.select_access_profile(user, "euru")

        self.assertTrue(result)
        self.vpn._persist_groups.assert_awaited_once_with(user, ["banned", "euru"])
        self.vpn.apply_inbound_groups.assert_not_awaited()

    async def test_select_regular_from_euru_persists_without_panel_client(self) -> None:
        user = SimpleNamespace(tg_id=42, inbound_groups=["euru"])
        self.vpn.is_client_exists.return_value = False

        result = await self.vpn.select_access_profile(user, "regular")

        self.assertTrue(result)
        self.vpn._persist_groups.assert_awaited_once_with(user, ["regular"])
        self.vpn.apply_inbound_groups.assert_not_awaited()

    async def test_select_euru_from_unlimited_applies_starter_trial_parameters(self) -> None:
        user = SimpleNamespace(
            tg_id=42,
            server_id=1,
            inbound_groups=["unlimited", "banned"],
        )

        result = await self.vpn.select_access_profile(user, "euru")

        self.assertTrue(result)
        self.vpn.update_client.assert_awaited_once_with(
            user=user,
            devices=2,
            duration=3,
            replace_devices=True,
            replace_duration=True,
            total_gb=gb_to_bytes(15),
        )
        self.vpn.apply_inbound_groups.assert_awaited_once_with(
            user,
            groups=["banned", "euru"],
            enforce_enable=True,
        )
        self.vpn.reset_traffic.assert_awaited_once_with(user)

    async def test_select_euru_from_unlimited_persists_without_panel_client(self) -> None:
        user = SimpleNamespace(
            tg_id=42,
            server_id=None,
            inbound_groups=["unlimited", "banned"],
        )
        self.vpn.is_client_exists.return_value = False

        result = await self.vpn.select_access_profile(user, "euru")

        self.assertTrue(result)
        self.vpn._persist_groups.assert_awaited_once_with(user, ["banned", "euru"])
        self.vpn.update_client.assert_not_awaited()
        self.vpn.apply_inbound_groups.assert_not_awaited()

    async def test_select_unlimited_from_euru_uses_authoritative_plan_and_preserves_banned(
        self,
    ) -> None:
        user = SimpleNamespace(tg_id=42, inbound_groups=["euru", "banned"])
        plan = SimpleNamespace(devices=7, traffic_gb=100, inbound_groups=["unlimited"])
        self.vpn.plan_service = SimpleNamespace(get_unlimited_plan=lambda: plan)
        self.vpn.reconcile_from_panel = AsyncMock(return_value=None)
        self.vpn.is_client_exists.return_value = None
        self.vpn.create_client = AsyncMock(return_value=True)

        result = await self.vpn.select_access_profile(user, "unlimited")

        self.assertTrue(result)
        self.vpn.create_client.assert_awaited_once_with(
            user=user,
            devices=7,
            duration=0,
            total_gb=gb_to_bytes(100),
            groups=["banned", "unlimited"],
            expiry_override=0,
        )


class VPNCanonicalBoundaryTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        self.vpn = object.__new__(VPNService)
        self.vpn.inbound_group_service = object.__new__(InboundGroupService)
        self.connection = SimpleNamespace(
            server=SimpleNamespace(name="test-server"),
            api=object(),
        )
        self.vpn.server_pool_service = SimpleNamespace(
            assign_server_to_user=AsyncMock(),
            get_connection=AsyncMock(return_value=self.connection),
        )
        self.vpn._resolve_inbounds = AsyncMock(return_value=[11])
        self.vpn._persist_groups = AsyncMock()
        self.vpn._mirror_group_label = AsyncMock()
        self.clients = SimpleNamespace(
            add=AsyncMock(),
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

    @staticmethod
    def user(groups: list[str]) -> SimpleNamespace:
        return SimpleNamespace(
            tg_id=42,
            vpn_id="vpn-id",
            sub_id="sub-id",
            first_name="Maria",
            last_name=None,
            username=None,
            inbound_groups=groups,
        )

    async def test_create_client_canonicalizes_before_resolve_and_persist(self) -> None:
        user = self.user(["regular"])

        with patch.object(VPNService, "_clients", return_value=self.clients):
            result = await self.vpn.create_client(
                user,
                devices=2,
                duration=30,
                groups=["regular", "euru", "banned"],
            )

        self.assertTrue(result)
        self.vpn._resolve_inbounds.assert_awaited_once_with(
            self.connection,
            ["banned", "euru"],
        )
        self.vpn._persist_groups.assert_awaited_once_with(user, ["banned", "euru"])

    async def test_apply_groups_canonicalizes_before_resolve_and_persist(self) -> None:
        user = self.user(["regular"])
        self.vpn.inbound_group_service.managed_inbound_ids = AsyncMock(return_value={1, 11})

        with patch.object(VPNService, "_clients", return_value=self.clients):
            result = await self.vpn.apply_inbound_groups(
                user,
                groups=["regular", "euru", "banned"],
            )

        self.assertTrue(result)
        self.vpn._resolve_inbounds.assert_awaited_once_with(
            self.connection,
            ["banned", "euru"],
        )
        self.vpn._persist_groups.assert_awaited_once_with(user, ["banned", "euru"])

    async def test_empty_resolution_does_not_persist_canonical_groups(self) -> None:
        user = self.user(["regular"])
        self.vpn._resolve_inbounds.side_effect = EmptyInboundSetError(
            ["euru"],
            "test-server",
        )

        with self.assertRaises(EmptyInboundSetError):
            await self.vpn.apply_inbound_groups(
                user,
                groups=["regular", "euru"],
            )

        self.vpn._persist_groups.assert_not_awaited()


class VPNPaidPlanGroupTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        self.vpn = object.__new__(VPNService)
        self.vpn.inbound_group_service = object.__new__(InboundGroupService)
        self.user = SimpleNamespace(tg_id=42, inbound_groups=["euru", "banned"])
        self.plan = SimpleNamespace(inbound_groups=["regular"])
        self.vpn.plan_service = SimpleNamespace(get_plan=lambda devices: self.plan)
        self.vpn.reconcile_from_panel = AsyncMock(return_value=None)
        self.vpn.is_client_exists = AsyncMock(return_value=None)
        self.vpn.create_client = AsyncMock(return_value=True)
        self.vpn.update_client = AsyncMock(return_value=True)
        self.vpn.apply_inbound_groups = AsyncMock(return_value=True)
        self.vpn.reset_traffic = AsyncMock(return_value=True)
        self.vpn.server_pool_service = SimpleNamespace(get_connection=AsyncMock(return_value=None))

    async def test_create_regular_subscription_removes_euru_and_preserves_banned(self) -> None:
        result = await self.vpn.create_subscription(self.user, 2, 30, 100)

        self.assertTrue(result)
        self.vpn.create_client.assert_awaited_once_with(
            user=self.user,
            devices=2,
            duration=30,
            total_gb=gb_to_bytes(100),
            groups=["banned", "regular"],
        )

    async def test_extend_stops_when_authoritative_plan_groups_fail_to_apply(self) -> None:
        self.vpn.apply_inbound_groups.return_value = False

        result = await self.vpn.extend_subscription(self.user, 2, 30, 100)

        self.assertFalse(result)
        self.vpn.apply_inbound_groups.assert_awaited_once_with(
            self.user,
            groups=["banned", "regular"],
        )
        self.vpn.reset_traffic.assert_not_awaited()


if __name__ == "__main__":
    unittest.main()
