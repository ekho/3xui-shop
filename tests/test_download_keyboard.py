import unittest
from unittest.mock import patch

from aiogram.types import InlineKeyboardButton

from app.bot.routers.download.keyboard import download_keyboard, platforms_keyboard
from app.bot.utils.navigation import NavDownload


class DownloadKeyboardTests(unittest.TestCase):
    @staticmethod
    def buttons(keyboard):
        return [button for row in keyboard.inline_keyboard for button in row]

    @patch("app.bot.routers.download.keyboard._", lambda key: key)
    @patch(
        "app.bot.routers.download.keyboard.back_button",
        return_value=InlineKeyboardButton(text="back", callback_data="back"),
    )
    def test_platform_picker_offers_macos_and_other_platforms(self, _back_button) -> None:
        buttons = self.buttons(platforms_keyboard())

        self.assertIn("platform_macos", [button.callback_data for button in buttons])
        self.assertIn(
            "https://github.com/Happ-proxy/happ-desktop/",
            [button.url for button in buttons],
        )

    @patch("app.bot.routers.download.keyboard._", lambda key: key)
    @patch(
        "app.bot.routers.download.keyboard.back_button",
        return_value=InlineKeyboardButton(text="back", callback_data="back"),
    )
    def test_ios_offers_both_app_store_regions(self, _back_button) -> None:
        buttons = self.buttons(download_keyboard(NavDownload.PLATFORM_IOS, "https://vpn/", "key"))

        self.assertIn(
            "https://apps.apple.com/ru/app/happ-lite/id6799917773",
            [button.url for button in buttons],
        )
        self.assertIn(
            "https://apps.apple.com/us/app/happ-proxy-utility/id6504287215",
            [button.url for button in buttons],
        )

    @patch("app.bot.routers.download.keyboard._", lambda key: key)
    @patch(
        "app.bot.routers.download.keyboard.back_button",
        return_value=InlineKeyboardButton(text="back", callback_data="back"),
    )
    def test_windows_download_uses_x64_installer(self, _back_button) -> None:
        buttons = self.buttons(download_keyboard(NavDownload.PLATFORM_WINDOWS, "https://vpn/", "key"))

        self.assertIn(
            "https://github.com/Happ-proxy/happ-desktop/releases/latest/download/setup-Happ.x64.exe",
            [button.url for button in buttons],
        )


if __name__ == "__main__":
    unittest.main()
