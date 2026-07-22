import unittest
from contextlib import asynccontextmanager
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from app.bot.services.subscription import AdminTrialStatus, SubscriptionService


class CreateAdminTrialTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        @asynccontextmanager
        async def session_factory():
            yield object()

        self.vpn = SimpleNamespace(
            get_available_server=AsyncMock(return_value=object()),
            is_client_exists=AsyncMock(return_value=None),
        )
        self.service = SubscriptionService(
            config=SimpleNamespace(shop=SimpleNamespace(TRIAL_ENABLED=True)),
            session_factory=session_factory,
            vpn_service=self.vpn,
        )

    @patch("app.bot.services.subscription.User.create", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.User.get", new_callable=AsyncMock, return_value=None)
    async def test_creates_approved_user_then_uses_standard_trial(self, get, create) -> None:
        created = SimpleNamespace(tg_id=42)
        create.return_value = created
        self.service.gift_trial = AsyncMock(return_value=True)

        result = await self.service.create_admin_trial(42, "Мария", approved_by=7)

        self.assertEqual(result.status, AdminTrialStatus.CREATED)
        self.assertIs(result.user, created)
        self.assertEqual(create.await_args.kwargs["approval_decided_by"], 7)
        self.service.gift_trial.assert_awaited_once_with(created)

    @patch("app.bot.services.subscription.User.get", new_callable=AsyncMock)
    async def test_refuses_existing_telegram_id(self, get) -> None:
        get.return_value = SimpleNamespace(tg_id=42)

        result = await self.service.create_admin_trial(42, "Мария", approved_by=7)

        self.assertEqual(result.status, AdminTrialStatus.ALREADY_EXISTS)
        self.vpn.get_available_server.assert_not_awaited()

    async def test_refuses_when_trial_is_disabled(self) -> None:
        self.service.config.shop.TRIAL_ENABLED = False

        result = await self.service.create_admin_trial(42, "Мария", approved_by=7)

        self.assertEqual(result.status, AdminTrialStatus.TRIAL_DISABLED)
        self.vpn.get_available_server.assert_not_awaited()

    @patch("app.bot.services.subscription.User.get", new_callable=AsyncMock, return_value=None)
    async def test_refuses_when_no_server_is_available(self, get) -> None:
        self.vpn.get_available_server.return_value = None

        result = await self.service.create_admin_trial(42, "Мария", approved_by=7)

        self.assertEqual(result.status, AdminTrialStatus.NO_SERVER)

    @patch("app.bot.services.subscription.User.delete", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.User.create", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.User.get", new_callable=AsyncMock, return_value=None)
    async def test_removes_new_user_when_provisioning_fails(self, get, create, delete) -> None:
        create.return_value = SimpleNamespace(tg_id=42)
        self.service.gift_trial = AsyncMock(return_value=False)

        result = await self.service.create_admin_trial(42, "Мария", approved_by=7)

        self.assertEqual(result.status, AdminTrialStatus.PROVISION_FAILED)
        delete.assert_awaited_once_with(self.service.session_factory, 42)

    @patch("app.bot.services.subscription.User.update_trial_status", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.User.delete", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.User.create", new_callable=AsyncMock)
    @patch("app.bot.services.subscription.User.get", new_callable=AsyncMock, return_value=None)
    async def test_preserves_partial_panel_client(self, get, create, delete, update_trial_status) -> None:
        created = SimpleNamespace(tg_id=42)
        create.return_value = created
        self.service.gift_trial = AsyncMock(return_value=False)
        self.vpn.is_client_exists.return_value = object()

        result = await self.service.create_admin_trial(42, "Мария", approved_by=7)

        self.assertEqual(result.status, AdminTrialStatus.PARTIAL_PROVISION)
        delete.assert_not_awaited()
        self.assertEqual(update_trial_status.await_args.kwargs["tg_id"], 42)
        self.assertTrue(update_trial_status.await_args.kwargs["used"])
