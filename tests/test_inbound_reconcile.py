import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, patch

from app.bot.services import NotificationService, ServerPoolService
from app.bot.services.inbound_groups import InboundGroupService
from app.bot.services.xui_clients import ClientView
from app.bot.tasks.inbound_reconcile import reconcile_inbound_groups
from app.db.models import User


class InboundReconciliationTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        self.user = User(tg_id=42, server_id=7, inbound_groups=["euru"])
        self.connection = SimpleNamespace(
            api=SimpleNamespace(),
            server=SimpleNamespace(name="test-server"),
        )
        self.clients = SimpleNamespace(
            export=AsyncMock(
                return_value=[
                    ClientView(
                        email="42",
                        inbound_ids=[10, 20],
                        raw={"enable": True},
                    )
                ]
            ),
            attach=AsyncMock(),
            detach=AsyncMock(),
            set_clients_enabled=AsyncMock(),
        )
        self.redis = SimpleNamespace(get=AsyncMock(return_value=None), set=AsyncMock())
        self.server_pool = MagicMock(spec=ServerPoolService)
        self.server_pool.get_connection = AsyncMock(return_value=self.connection)
        self.notifications = MagicMock(spec=NotificationService)
        self.notifications.notify_developer = AsyncMock()
        self.session_factory = MagicMock()
        self.session_factory.return_value.__aenter__ = AsyncMock()
        self.session_factory.return_value.__aexit__ = AsyncMock()
        self.groups = object.__new__(InboundGroupService)
        self.groups.known_groups = AsyncMock(
            return_value={"banned", "regular", "unlimited", "euru"}
        )
        self.groups.resolve = AsyncMock(return_value={"regular": [10], "unlimited": [20], "euru": [30]})
        self.groups.managed_inbound_ids = AsyncMock(return_value={10, 20, 30})

    async def run_reconcile(self) -> None:
        with (
            patch.object(User, "get_all", new=AsyncMock(return_value=[self.user])),
            patch("app.bot.tasks.inbound_reconcile.XuiClientsApi", return_value=self.clients),
        ):
            await reconcile_inbound_groups(
                self.session_factory,
                self.redis,
                self.server_pool,
                self.groups,
                self.notifications,
            )

    async def test_euru_detaches_managed_regular_only(self) -> None:
        await self.run_reconcile()

        self.clients.attach.assert_awaited_once_with("42", [30])
        self.clients.detach.assert_awaited_once_with("42", [10, 20])

    async def test_euru_plus_banned_preserves_euru_reconciliation(self) -> None:
        self.user.inbound_groups = ["banned", "euru"]

        await self.run_reconcile()

        self.clients.attach.assert_awaited_once_with("42", [30])
        self.clients.detach.assert_awaited_once_with("42", [10, 20])
        self.clients.set_clients_enabled.assert_awaited_once_with(["42"], False)

    async def test_malformed_profiles_use_euru_precedence(self) -> None:
        self.user.inbound_groups = ["regular", "euru"]

        await self.run_reconcile()

        self.clients.attach.assert_awaited_once_with("42", [30])
        self.clients.detach.assert_awaited_once_with("42", [10, 20])

    async def test_unknown_manual_inbounds_are_preserved(self) -> None:
        self.clients.export = AsyncMock(return_value=[
            ClientView(email="42", inbound_ids=[30, 99], raw={"enable": True})
        ])

        await self.run_reconcile()

        self.clients.attach.assert_not_awaited()
        self.clients.detach.assert_not_awaited()

    async def test_empty_euru_resolution_alerts_and_skips_membership_changes(self) -> None:
        self.groups.resolve = AsyncMock(return_value={"regular": [10], "unlimited": [20], "euru": []})

        await self.run_reconcile()

        self.notifications.notify_developer.assert_awaited_once()
        self.clients.attach.assert_not_awaited()
        self.clients.detach.assert_not_awaited()

    async def test_unlimited_inherits_regular_inbounds(self) -> None:
        self.user.inbound_groups = ["unlimited"]
        self.clients.export = AsyncMock(
            return_value=[ClientView(email="42", inbound_ids=[30], raw={"enable": True})]
        )

        await self.run_reconcile()

        self.clients.attach.assert_awaited_once_with("42", [10, 20])
        self.clients.detach.assert_awaited_once_with("42", [30])


if __name__ == "__main__":
    unittest.main()
