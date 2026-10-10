import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from environs import Env
from app.config import env_or_file, load_config


class SecretConfigTests(unittest.TestCase):
    def test_legacy_yookassa_requires_deployment_receipt_email(self):
        env = {"BOT_DOMAIN": "bot.example.test", "SHOP_PAYMENT_YOOKASSA_ENABLED": "true",
               "YOOKASSA_TOKEN": "token-fixture", "YOOKASSA_SHOP_ID": "100001"}
        for email in ("", "private-invalid-fixture", "Name <receipts@example.test>", "a@example.test\r\nBcc:b@example.test"):
            with self.subTest(email=email), patch.dict(os.environ, {**env, "SHOP_EMAIL": email}, clear=True), patch.object(Env, "read_env"):
                with self.assertRaisesRegex(ValueError, '^Valid SHOP_EMAIL required for YooKassa$'):
                    load_config()

    def test_legacy_payment_and_database_callers_use_files(self):
        with tempfile.TemporaryDirectory() as directory:
            values = {"HELEKET_API_KEY": "heleket-fixture", "HELEKET_MERCHANT_ID": "merchant-fixture",
                      "YOOKASSA_TOKEN": "yookassa-fixture", "DB_PASSWORD": "db-fixture", "REDIS_PASSWORD": "redis-fixture"}
            env = {"BOT_DOMAIN": "bot.example.test", "BOT_TOKEN": "bot-fixture", "BOT_DEV_ID": "101", "BOT_SUPPORT_ID": "102",
                   "SHOP_PAYMENT_HELEKET_ENABLED": "true", "SHOP_PAYMENT_YOOKASSA_ENABLED": "true", "YOOKASSA_SHOP_ID": "100001",
                   "SHOP_EMAIL": "receipts@example.test"}
            for name, value in values.items():
                path = Path(directory) / name.lower()
                path.write_text(value)
                env[name + "_FILE"] = str(path)
            with patch.dict(os.environ, env, clear=True), patch.object(Env, "read_env"):
                config = load_config()
            self.assertTrue(config.shop.PAYMENT_HELEKET_ENABLED)
            self.assertTrue(config.shop.PAYMENT_YOOKASSA_ENABLED)
            self.assertEqual(config.heleket.API_KEY, values["HELEKET_API_KEY"])
            self.assertEqual(config.heleket.MERCHANT_ID, values["HELEKET_MERCHANT_ID"])
            self.assertEqual(config.yookassa.TOKEN, values["YOOKASSA_TOKEN"])
            self.assertEqual(config.database.PASSWORD, values["DB_PASSWORD"])
            self.assertEqual(config.redis.PASSWORD, values["REDIS_PASSWORD"])

    def test_explicit_file_wins_over_legacy_environment(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "credential"
            path.write_text("file-fixture\n")
            with patch.dict(os.environ, {"BOT_TOKEN": "env-fixture", "BOT_TOKEN_FILE": str(path)}, clear=True):
                self.assertEqual(env_or_file(Env(), "BOT_TOKEN", required=True), "file-fixture")
            with patch.dict(os.environ, {"BOT_TOKEN": "env-fixture"}, clear=True):
                self.assertEqual(env_or_file(Env(), "BOT_TOKEN", required=True), "env-fixture")

    def test_bad_explicit_file_never_falls_back_or_discloses_input(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "private-path-fixture"
            for mode in ("missing", "directory", "empty", "encoding"):
                with self.subTest(mode=mode):
                    if path.exists():
                        path.rmdir() if path.is_dir() else path.unlink()
                    if mode == "directory":
                        path.mkdir()
                    elif mode == "empty":
                        path.write_text(" \n ")
                    elif mode == "encoding":
                        path.write_bytes(b"\xff")
                    with patch.dict(os.environ, {"BOT_TOKEN": "private-env-fixture", "BOT_TOKEN_FILE": str(path)}, clear=True):
                        with self.assertRaises(ValueError) as raised:
                            env_or_file(Env(), "BOT_TOKEN", required=True)
                        self.assertNotIn(str(path), str(raised.exception))
                        self.assertNotIn("private-env-fixture", str(raised.exception))


if __name__ == "__main__":
    unittest.main()
