import json
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "deploy/payment-history/export_legacy_payments.py"


class LegacyPaymentExportTest(unittest.TestCase):
    def test_original_raw_statuses_and_explicit_timezone(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "legacy.sqlite"
            with sqlite3.connect(source) as db:
                db.executescript("CREATE TABLE users(id INTEGER PRIMARY KEY,tg_id INTEGER);"
                                 "CREATE TABLE transactions(id INTEGER PRIMARY KEY,tg_id INTEGER,payment_id TEXT,subscription TEXT,status TEXT,created_at TEXT,updated_at TEXT);"
                                 "INSERT INTO users VALUES(5,701),(6,702);")
                for i, status in enumerate(("pending", "completed", "canceled", "refunded"), 1):
                    db.execute("INSERT INTO transactions VALUES(?,?,?,?,?,?,?)", (i, 701, "synthetic-long-charge-" * 200 + status, "unknown:keep-original-✨", status,
                               "2026-10-01 03:04:05.123456", "2026-10-01T00:04:04.123456+00:00"))
            before = source.read_bytes()
            proc = subprocess.run([sys.executable, str(SCRIPT), str(source), "--timezone", "Europe/Moscow"], capture_output=True, text=True)
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertEqual(source.read_bytes(), before)
            packet = json.loads(proc.stdout)
            self.assertEqual(packet["version"], 1)
            self.assertEqual(packet["users"], [{"source_legacy_user_id": 5, "source_tg_id": 701}])
            self.assertEqual([r["status"] for r in packet["transactions"]], ["pending", "completed", "canceled", "refunded"])
            self.assertEqual(packet["transactions"][0]["payment_id"], "synthetic-long-charge-" * 200 + "pending")
            self.assertEqual(packet["transactions"][0]["subscription"], "unknown:keep-original-✨")
            self.assertEqual(packet["transactions"][0]["created_at"], "2026-10-01T00:04:05.123456Z")
            self.assertEqual(packet["transactions"][0]["updated_at"], "2026-10-01T00:04:04.123456Z")
            no_zone = subprocess.run([sys.executable, str(SCRIPT), str(source)], capture_output=True, text=True)
            self.assertNotEqual(no_zone.returncode, 0)
            self.assertEqual(no_zone.stdout, "")

    def test_corrupt_copy_has_safe_error(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "broken.sqlite"
            source.write_bytes(b"private source not a database")
            proc = subprocess.run([sys.executable, str(SCRIPT), str(source), "--timezone", "UTC"], capture_output=True, text=True)
            self.assertEqual(proc.returncode, 1)
            self.assertEqual(proc.stderr.strip(), "EXPORT_FAILED")
            self.assertEqual(proc.stdout, "")


if __name__ == "__main__":
    unittest.main()
