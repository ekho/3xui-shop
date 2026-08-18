import unittest
from types import SimpleNamespace
from unittest.mock import ANY, AsyncMock, patch

from app.bot.routers.admin_tools.group_handler import callback_toggle_user_group
from app.bot.services.inbound_groups import InboundGroupService
from app.bot.utils.navigation import NavAdminTools


class AdminGroupHandlerTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        self.target = SimpleNamespace(tg_id=42, inbound_groups=["regular"], server_id=1)
        self.admin = SimpleNamespace(tg_id=7)
        self.callback = SimpleNamespace(
            data="",
            bot=object(),
            message=SimpleNamespace(edit_text=AsyncMock()),
        )
        self.services = SimpleNamespace(
            inbound_groups=SimpleNamespace(
                effective_groups=InboundGroupService.effective_groups,
                access_groups=InboundGroupService.access_groups,
            ),
            vpn=SimpleNamespace(
                select_access_profile=AsyncMock(return_value=True),
                grant_unlimited=AsyncMock(return_value=True),
                revoke_unlimited=AsyncMock(return_value=True),
                apply_inbound_groups=AsyncMock(return_value=True),
            ),
            notification=SimpleNamespace(show_popup=AsyncMock()),
        )

    async def invoke(self, group: str) -> None:
        self.callback.data = NavAdminTools.TOGGLE_USER_GROUP + f"_{self.target.tg_id}_{group}"
        with (
            patch(
                "app.bot.routers.admin_tools.group_handler.User.get",
                new=AsyncMock(return_value=self.target),
            ),
            patch(
                "app.bot.routers.admin_tools.group_handler._render_user_groups",
                new=AsyncMock(return_value=("groups", None)),
            ),
            patch("app.bot.routers.admin_tools.group_handler._", lambda key: key),
        ):
            await callback_toggle_user_group(
                callback=self.callback,
                user=self.admin,
                session=object(),
                services=self.services,
            )

    async def test_regular_to_euru_routes_through_profile_selection(self) -> None:
        await self.invoke("euru")

        self.services.vpn.select_access_profile.assert_awaited_once_with(self.target, "euru")
        self.services.vpn.apply_inbound_groups.assert_not_awaited()

    async def test_unlimited_to_euru_routes_through_profile_selection(self) -> None:
        self.target.inbound_groups = ["unlimited"]

        await self.invoke("euru")

        self.services.vpn.select_access_profile.assert_awaited_once_with(self.target, "euru")
        self.services.vpn.revoke_unlimited.assert_not_awaited()

    async def test_euru_to_unlimited_routes_through_profile_selection(self) -> None:
        self.target.inbound_groups = ["euru"]

        await self.invoke("unlimited")

        self.services.vpn.select_access_profile.assert_awaited_once_with(
            self.target,
            "unlimited",
        )
        self.services.vpn.grant_unlimited.assert_not_awaited()

    async def test_active_unlimited_still_revokes_to_starter_trial(self) -> None:
        self.target.inbound_groups = ["unlimited"]

        await self.invoke("unlimited")

        self.services.vpn.revoke_unlimited.assert_awaited_once_with(self.target)
        self.services.vpn.select_access_profile.assert_not_awaited()

    async def test_removing_only_euru_is_refused(self) -> None:
        self.target.inbound_groups = ["euru"]

        await self.invoke("euru")

        self.services.vpn.select_access_profile.assert_not_awaited()
        self.services.notification.show_popup.assert_awaited_once()

    async def test_removing_only_regular_is_refused(self) -> None:
        await self.invoke("regular")

        self.services.vpn.select_access_profile.assert_not_awaited()
        self.services.notification.show_popup.assert_awaited_once()

    async def test_banning_euru_preserves_profile_and_cancels_stars(self) -> None:
        self.target.inbound_groups = ["euru"]

        with patch(
            "app.bot.routers.admin_tools.group_handler.cancel_stars_auto_renew",
            new=AsyncMock(),
        ) as cancel:
            await self.invoke("banned")

        self.services.vpn.apply_inbound_groups.assert_awaited_once_with(
            self.target,
            groups=["banned", "euru"],
            enforce_enable=True,
        )
        cancel.assert_awaited_once_with(
            self.callback.bot,
            ANY,
            self.target,
            reason="banned by admin",
        )
        self.services.vpn.select_access_profile.assert_not_awaited()

    async def test_no_client_profile_change_still_routes_through_vpn_service(self) -> None:
        self.target.server_id = None

        with patch(
            "app.bot.routers.admin_tools.group_handler.User.update",
            new=AsyncMock(),
        ) as update:
            await self.invoke("euru")

        self.services.vpn.select_access_profile.assert_awaited_once_with(self.target, "euru")
        update.assert_not_awaited()

    async def test_unknown_group_is_rejected_without_persistence(self) -> None:
        with patch(
            "app.bot.routers.admin_tools.group_handler.User.update",
            new=AsyncMock(),
        ) as update:
            await self.invoke("unknown")

        self.services.vpn.select_access_profile.assert_not_awaited()
        self.services.vpn.apply_inbound_groups.assert_not_awaited()
        update.assert_not_awaited()
        self.services.notification.show_popup.assert_awaited_once()


if __name__ == "__main__":
    unittest.main()
