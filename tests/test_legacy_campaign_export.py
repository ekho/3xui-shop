import json
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "deploy/campaigns/export_legacy_campaigns.py"


class LegacyCampaignExportTest(unittest.TestCase):
    def test_raw_source_orphans_timezone_and_read_only(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "owned.sqlite"
            with sqlite3.connect(source) as db:
                db.executescript("CREATE TABLE invites(id INTEGER PRIMARY KEY,name TEXT,hash_code TEXT,clicks INTEGER,is_active INTEGER,created_at TEXT);"
                                 "CREATE TABLE users(id INTEGER PRIMARY KEY,tg_id INTEGER,source_invite_name TEXT,is_trial_used INTEGER);"
                                 "INSERT INTO invites VALUES(9007199254740993,' Exact campaign ✨ ','oldhashx',17,0,'2026-10-01 03:04:05.123456');"
                                 "INSERT INTO users VALUES(5,701,' Exact campaign ✨ ',1),(6,702,'Deleted name',0),(7,703,NULL,0);")
            before = source.read_bytes()
            proc = subprocess.run([sys.executable, str(SCRIPT), str(source), "--timezone", "Europe/Moscow", "--source", "owned-copy"], capture_output=True, text=True)
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertEqual(source.read_bytes(), before)
            packet = json.loads(proc.stdout)
            self.assertEqual(packet["version"], 1)
            self.assertEqual(packet["source"], "owned-copy")
            self.assertEqual(packet["campaigns"], [{"source_id": 9007199254740993, "name": " Exact campaign ✨ ", "hash_code": "oldhashx", "clicks": 17, "is_active": False, "created_at": "2026-10-01T00:04:05.123456Z"}])
            self.assertEqual(packet["users"], [{"source_legacy_user_id": 5, "source_tg_id": 701, "source_invite_name": " Exact campaign ✨ ", "is_trial_used": True}, {"source_legacy_user_id": 6, "source_tg_id": 702, "source_invite_name": "Deleted name", "is_trial_used": False}])
            missing_zone = subprocess.run([sys.executable, str(SCRIPT), str(source), "--source", "owned-copy"], capture_output=True, text=True)
            self.assertNotEqual(missing_zone.returncode, 0)
            self.assertEqual(missing_zone.stdout, "")

    def test_corrupt_copy_has_safe_error(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "broken.sqlite"
            source.write_bytes(b"private source not a database")
            proc = subprocess.run([sys.executable, str(SCRIPT), str(source), "--timezone", "UTC", "--source", "owned-copy"], capture_output=True, text=True)
            self.assertEqual(proc.returncode, 1)
            self.assertEqual(proc.stderr.strip(), "EXPORT_FAILED")
            self.assertEqual(proc.stdout, "")


if __name__ == "__main__":
    unittest.main()
