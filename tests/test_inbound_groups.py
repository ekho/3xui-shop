import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from py3xui import AsyncApi

from app.bot.services.inbound_groups import InboundGroupService
from app.bot.utils import constants
from app.db.models import User


class InboundGroupProfileTests(unittest.TestCase):
    def test_fixed_group_set_contains_all_four_known_groups(self) -> None:
        self.assertEqual(
            constants.INBOUND_GROUPS,
            ("banned", "regular", "unlimited", "euru"),
        )
        self.assertEqual(constants.EURU_INBOUND_GROUP, "euru")
        self.assertEqual(
            constants.ACCESS_INBOUND_GROUPS,
            ("regular", "unlimited", "euru"),
        )

    def test_euru_tag_matching_requires_an_exact_hyphen_segment(self) -> None:
        known: set[str] = set(constants.INBOUND_GROUPS)

        self.assertEqual(
            InboundGroupService.groups_of("n2-euru-in-8443", known),
            {"euru"},
        )
        self.assertEqual(
            InboundGroupService.groups_of("n2-eurus-in-8443", known),
            set(),
        )

    def test_euru_does_not_inherit_regular(self) -> None:
        self.assertEqual(
            InboundGroupService.expand_access_groups(["euru"]),
            ["euru"],
        )

    def test_unlimited_inheritance_remains_unchanged(self) -> None:
        self.assertEqual(
            InboundGroupService.expand_access_groups(["unlimited"]),
            ["regular", "unlimited"],
        )

    def test_transition_regular_to_euru_preserves_banned(self) -> None:
        self.assertEqual(
            InboundGroupService.transition_access_profile(
                ["regular", "banned"],
                "euru",
            ),
            ["banned", "euru"],
        )

    def test_transition_euru_to_regular_replaces_euru(self) -> None:
        self.assertEqual(
            InboundGroupService.transition_access_profile(["euru"], "regular"),
            ["regular"],
        )

    def test_transition_unlimited_to_euru_replaces_unlimited(self) -> None:
        self.assertEqual(
            InboundGroupService.transition_access_profile(["unlimited"], "euru"),
            ["euru"],
        )

    def test_transition_euru_to_unlimited_replaces_euru(self) -> None:
        self.assertEqual(
            InboundGroupService.transition_access_profile(["euru"], "unlimited"),
            ["unlimited"],
        )

    def test_transition_from_empty_state_uses_selected_profile(self) -> None:
        self.assertEqual(
            InboundGroupService.transition_access_profile(None, "euru"),
            ["euru"],
        )

    def test_transition_rejects_non_access_group(self) -> None:
        with self.assertRaises(ValueError):
            InboundGroupService.transition_access_profile(["regular"], "banned")

    def test_canonical_groups_defaults_empty_state_to_regular(self) -> None:
        self.assertEqual(InboundGroupService.canonical_groups(None), ["regular"])
        self.assertEqual(InboundGroupService.canonical_groups([]), ["regular"])

    def test_effective_groups_uses_canonical_default(self) -> None:
        user = User(inbound_groups=None)

        self.assertEqual(InboundGroupService.effective_groups(user), ["regular"])

    def test_canonical_groups_use_profile_precedence(self) -> None:
        cases = (
            (["regular", "euru"], ["euru"]),
            (["regular", "unlimited"], ["unlimited"]),
            (["regular", "euru", "unlimited"], ["unlimited"]),
            (["banned", "regular", "euru"], ["banned", "euru"]),
        )

        for groups, expected in cases:
            with self.subTest(groups=groups):
                self.assertEqual(
                    InboundGroupService.canonical_groups(groups),
                    expected,
                )


class InboundGroupResolutionTests(unittest.IsolatedAsyncioTestCase):
    async def test_resolve_matches_enabled_euru_inbound_only(self) -> None:
        api = AsyncApi("https://example.invalid", token="test-token")
        service = object.__new__(InboundGroupService)

        with patch.object(
            api.inbound,
            "get_list",
            new=AsyncMock(
                return_value=[
                    SimpleNamespace(id=11, tag="euru-n2-in-8443", enable=True),
                    SimpleNamespace(id=12, tag="euru-n3-in-8443", enable=False),
                    SimpleNamespace(id=13, tag="eurus-n4-in-8443", enable=True),
                ]
            ),
        ):
            self.assertEqual(await service.resolve(api, ["euru"]), {"euru": [11]})


if __name__ == "__main__":
    unittest.main()
