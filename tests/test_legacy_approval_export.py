import json
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "deploy/s48/export_legacy_approval.py"


class LegacyApprovalExportTest(unittest.TestCase):
    def test_readonly_original_events_and_explicit_timezone(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "legacy.sqlite"
            with sqlite3.connect(source) as db:
                db.executescript("""
                    CREATE TABLE users(id INTEGER PRIMARY KEY, tg_id INTEGER, approval_status TEXT,
                      approval_requested_at TEXT, approval_decided_at TEXT, approval_decided_by INTEGER);
                    CREATE TABLE audit_log(id INTEGER PRIMARY KEY, created_at TEXT, actor_type TEXT,
                      actor_id INTEGER, actor_name TEXT, action TEXT, target_id INTEGER,
                      source TEXT, payload TEXT);
                    INSERT INTO users VALUES(5,701,'rejected',NULL,'2024-01-02 03:04:05',NULL);
                    INSERT INTO audit_log VALUES(7,'2024-01-02 03:04:05','admin',NULL,NULL,
                      'approval.reject',701,'main_bot','{"secret":"never-export"}');
                    INSERT INTO audit_log VALUES(8,'2024-01-02 03:04:05','admin',9,'Other',
                      'user.compensate',701,'main_bot','{"secret":"never-export"}');
                """)
            before = source.read_bytes()
            proc = subprocess.run([sys.executable, str(SCRIPT), str(source), "--timezone", "Europe/Moscow"], capture_output=True, text=True)
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertEqual(source.read_bytes(), before)
            self.assertNotIn("never-export", proc.stdout)
            packet = json.loads(proc.stdout)
            self.assertEqual(packet["version"], 1)
            self.assertEqual(packet["users"][0]["decided_at"], "2024-01-02T00:04:05Z")
            self.assertEqual([e["source_id"] for e in packet["approval_events"]], [7])
            self.assertIsNone(packet["approval_events"][0]["actor_id"])


if __name__ == "__main__":
    unittest.main()
