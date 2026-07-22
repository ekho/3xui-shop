import unittest
from contextlib import asynccontextmanager
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from app.bot.services.audit import AuditActor, AuditService
from app.bot.services.subscription import AdminTrialStatus, SubscriptionService
from app.bot.services.vpn import VPNService, gb_to_bytes
from app.bot.utils.constants import AuditAction


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


class ForcedTrialTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        self.config = SimpleNamespace(
            shop=SimpleNamespace(
                BONUS_DEVICES_COUNT=2,
                TRIAL_PERIOD=3,
                TRIAL_TRAFFIC_GB=15,
            )
        )
        self.user = SimpleNamespace(tg_id=42, inbound_groups=["regular", "banned"])
        self.vpn = object.__new__(VPNService)
        self.vpn.config = self.config
        self.vpn.inbound_group_service = SimpleNamespace(
            is_banned=lambda user: "banned" in (user.inbound_groups or [])
        )
        self.vpn.server_pool_service = SimpleNamespace(get_connection=AsyncMock(return_value=None))
        self.vpn.is_client_exists = AsyncMock(return_value=True)
        self.vpn.update_client = AsyncMock(return_value=True)
        self.vpn.apply_inbound_groups = AsyncMock(return_value=True)
        self.vpn.reset_traffic = AsyncMock(return_value=True)
        self.vpn._enforce_ban = AsyncMock()

    async def test_force_trial_replaces_limits_and_preserves_ban(self) -> None:
        self.assertTrue(await self.vpn.force_trial(self.user))

        self.vpn.update_client.assert_awaited_once_with(
            user=self.user,
            devices=self.config.shop.BONUS_DEVICES_COUNT,
            duration=self.config.shop.TRIAL_PERIOD,
            replace_devices=True,
            replace_duration=True,
            total_gb=gb_to_bytes(self.config.shop.TRIAL_TRAFFIC_GB),
        )
        self.vpn.apply_inbound_groups.assert_awaited_once_with(
            self.user,
            groups=["regular", "banned"],
            enforce_enable=True,
        )
        self.vpn.reset_traffic.assert_awaited_once_with(self.user)

    async def test_force_trial_refuses_without_panel_client(self) -> None:
        self.vpn.is_client_exists.return_value = False

        self.assertFalse(await self.vpn.force_trial(self.user))

        self.vpn.update_client.assert_not_awaited()
        self.vpn.apply_inbound_groups.assert_not_awaited()

    async def test_force_trial_fails_when_traffic_reset_fails(self) -> None:
        self.vpn.reset_traffic.return_value = False

        self.assertFalse(await self.vpn.force_trial(self.user))

        self.vpn._enforce_ban.assert_not_awaited()

    async def test_change_subscription_fails_when_traffic_reset_fails(self) -> None:
        self.vpn._plan_groups = lambda devices: ["regular"]
        self.vpn.reset_traffic.return_value = False

        self.assertFalse(await self.vpn.change_subscription(self.user, 2, 30, 100))

        self.vpn._enforce_ban.assert_not_awaited()

    async def test_change_subscription_fails_when_group_apply_fails(self) -> None:
        self.vpn._plan_groups = lambda devices: ["regular"]
        self.vpn.apply_inbound_groups.return_value = False

        self.assertFalse(await self.vpn.change_subscription(self.user, 2, 30, 100))

        self.vpn.reset_traffic.assert_not_awaited()


class AdminTrialAuditTests(unittest.IsolatedAsyncioTestCase):
    async def test_records_trial_parameters_for_successful_admin_creation(self) -> None:
        service = object.__new__(AuditService)
        service.record = AsyncMock()
        actor = AuditActor.system()
        target = SimpleNamespace(tg_id=42, first_name="Мария")

        await service.admin_trial_created(actor, target, duration=3, devices=2, traffic_gb=15)

        service.record.assert_awaited_once_with(
            AuditAction.USER_CREATE_TRIAL,
            actor,
            target=target,
            payload={"duration": 3, "devices": 2, "traffic_gb": 15},
            channel_note="триал: 3 дн.",
        )

    async def test_records_forced_trial_change_parameters(self) -> None:
        service = object.__new__(AuditService)
        service.record = AsyncMock()
        actor = AuditActor.system()
        target = SimpleNamespace(tg_id=42, first_name="Мария")

        await service.admin_plan_changed(
            actor,
            target,
            mode="trial",
            duration=3,
            devices=2,
            traffic_gb=15,
        )

        service.record.assert_awaited_once_with(
            AuditAction.USER_CHANGE_PLAN,
            actor,
            target=target,
            payload={"mode": "trial", "duration": 3, "devices": 2, "traffic_gb": 15},
            channel_note="триал: 3 дн.",
        )

    def test_formats_plan_change_in_history(self) -> None:
        from app.bot.services.audit import _entry_detail

        self.assertEqual(
            _entry_detail(
                AuditAction.USER_CHANGE_PLAN,
                {"mode": "plan", "duration": 30, "devices": 2, "traffic_gb": 100},
            ),
            "📱 тариф · 30 дн. · 2 устр. · 100 ГБ",
        )


class AdminTrialInputTests(unittest.TestCase):
    def test_accepts_signed_64_bit_positive_telegram_id(self) -> None:
        from app.bot.routers.admin_tools.user_handler import parse_new_client_tg_id

        self.assertEqual(parse_new_client_tg_id("9223372036854775807"), 9223372036854775807)

    def test_rejects_invalid_telegram_ids(self) -> None:
        from app.bot.routers.admin_tools.user_handler import parse_new_client_tg_id

        for raw in ("", "-1", "0", "9223372036854775808", "4.2"):
            self.assertIsNone(parse_new_client_tg_id(raw))

    def test_trims_and_bounds_display_name(self) -> None:
        from app.bot.routers.admin_tools.user_handler import normalize_new_client_name

        self.assertEqual(normalize_new_client_name("  Мария  "), "Мария")
        self.assertIsNone(normalize_new_client_name("   "))
        self.assertIsNone(normalize_new_client_name("x" * 33))


class AdminTrialKeyboardTests(unittest.TestCase):
    def test_user_editor_starts_creation_from_a_dedicated_button(self) -> None:
        from app.bot.routers.admin_tools.keyboard import user_editor_users_keyboard
        from app.bot.utils.navigation import NavAdminTools

        with (
            patch("app.bot.routers.admin_tools.keyboard._", lambda text: text),
            patch("app.bot.routers.misc.keyboard._", lambda text: text),
        ):
            callbacks = [
                button.callback_data
                for row in user_editor_users_keyboard([]).inline_keyboard
                for button in row
            ]

        self.assertIn(NavAdminTools.CREATE_TRIAL_CLIENT, callbacks)

    def test_confirmation_keyboard_uses_a_callback_without_user_payload(self) -> None:
        from app.bot.routers.admin_tools.keyboard import user_create_trial_confirm_keyboard
        from app.bot.utils.navigation import NavAdminTools

        with (
            patch("app.bot.routers.admin_tools.keyboard._", lambda text: text),
            patch("app.bot.routers.misc.keyboard._", lambda text: text),
        ):
            callbacks = [
                button.callback_data
                for row in user_create_trial_confirm_keyboard().inline_keyboard
                for button in row
            ]

        self.assertIn(NavAdminTools.CONFIRM_CREATE_TRIAL_CLIENT, callbacks)
