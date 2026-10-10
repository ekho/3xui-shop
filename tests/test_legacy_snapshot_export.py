from contextlib import closing
import hashlib
import json
import os
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from tests.fixtures.legacy_snapshot import create


ROOT = Path(__file__).resolve().parents[1]
BACKEND = ROOT / "backend"


class ExportTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.binary_dir = tempfile.TemporaryDirectory(dir=ROOT)
        cls.binary = Path(cls.binary_dir.name) / "server"
        subprocess.run(["go", "build", "-o", str(cls.binary), "./cmd/server"],
                       cwd=BACKEND, check=True, capture_output=True)

    @classmethod
    def tearDownClass(cls):
        cls.binary_dir.cleanup()

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(dir=ROOT)
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / "synthetic.sqlite"
        self.expected = create(self.path)

    def run_export(self, path=None):
        return subprocess.run(
            [str(self.binary), "export-legacy", str(path or self.path), "--source", "synthetic-source",
             "--support-bot-id", "12345", "--support-group-id", "-10012345"],
            capture_output=True, check=False, cwd=BACKEND,
            env={**os.environ, "DATABASE_URL": "invalid", "REDIS_URL": "invalid"},
        )

    def assert_rejected(self):
        result = self.run_export()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, b"")
        self.assertEqual(result.stderr, b"EXPORT_FAILED\n")

    def test_complete_packet_retains_source_facts(self):
        digest = hashlib.sha256(self.path.read_bytes()).digest()
        result = self.run_export()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(hashlib.sha256(self.path.read_bytes()).digest(), digest)
        packet = json.loads(result.stdout)
        self.assertEqual(set(packet), {"version", "source", "users", "servers", "catalogue", "approvals", "payments", "stars", "bonuses", "campaigns", "support", "audit", "catalogue_source"})
        self.assertTrue([u["tg_id"] for u in packet["users"]] == self.expected["tg_ids"], "synthetic telegram ids changed")
        self.assertTrue([u["vpn_id"] for u in packet["users"]] == self.expected["vpn_ids"], "synthetic VPN ids changed")
        self.assertTrue([u["sub_id"] for u in packet["users"]] == self.expected["sub_ids"], "synthetic subscription ids changed")
        self.assertTrue(packet["stars"][0]["stars_charge_id"] == self.expected["charge"], "synthetic charge id changed")
        self.assertEqual((len(packet["stars"]), len(packet["approvals"]["users"]), len(packet["payments"]["users"])), (3, 3, 3))
        self.assertEqual(len(packet["payments"]["transactions"]), 4)
        self.assertEqual({r["reward_type"] for r in packet["bonuses"]["rewards"]}, {"DAYS", "MONEY"})
        self.assertEqual((len(packet["bonuses"]["promocodes"]), len(packet["bonuses"]["referrals"])), (2, 1))
        self.assertEqual((len(packet["campaigns"]["campaigns"]), len(packet["campaigns"]["users"])), (1, 1))
        self.assertEqual(packet["catalogue"]["plans"][0]["prices"]["RUB"]["30"], "120.50")
        self.assertEqual(packet["catalogue_source"]["plans"][0]["prices_json"], '{"RUB":{"30":120.50},"USD":{"30":2.25},"XTR":{"30":42}}')
        self.assertEqual(packet["support"]["tickets"][1]["thread_id"], None)
        self.assertTrue(packet["support"]["tickets"][1]["tg_id"] not in self.expected["tg_ids"], "orphan ticket lost")
        self.assertEqual(len(packet["approvals"]["approval_events"]), 1)
        self.assertEqual(len(packet["catalogue_source"]["durations"]), 1)
        self.assertEqual(packet["audit"]["events"][0]["created_at"], "2026-10-10T12:13:14.123456Z")

    def test_nullable_unknown_flags_are_preserved(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.executescript("PRAGMA foreign_keys=OFF; CREATE TABLE users_new AS SELECT * FROM users; "
                             "DROP TABLE users; ALTER TABLE users_new RENAME TO users; "
                             "CREATE UNIQUE INDEX users_tg_id ON users(tg_id); "
                             "UPDATE users SET is_trial_used=NULL,is_stars_auto_renew=NULL WHERE id=3;")
        result = self.run_export()
        self.assertEqual(result.returncode, 0, result.stderr)
        packet = json.loads(result.stdout)
        self.assertIsNone(packet["users"][2]["is_trial_used"])
        self.assertIsNone(packet["stars"][2]["is_stars_auto_renew"])

    def test_user_with_only_banned_group_is_preserved(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE users SET inbound_groups=? WHERE id=2", ('["banned"]',))
        result = self.run_export()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["users"][1]["inbound_groups"], ["banned"])

    def test_invalid_user_group_sets_are_rejected(self):
        for groups_json in ('["regular","regular"]', '["regular","euru"]', '["unknown"]'):
            with self.subTest(groups_json=groups_json):
                with closing(sqlite3.connect(self.path)) as db, db:
                    db.execute("UPDATE users SET inbound_groups=? WHERE id=2", (groups_json,))
                self.assert_rejected()

    def test_offset_timestamp_keeps_exact_microseconds(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE audit_log SET created_at=? WHERE id=1", ("2026-10-10T15:13:14.123456+03:00",))
        result = self.run_export()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["audit"]["events"][0]["created_at"],
                         "2026-10-10T12:13:14.123456Z")

    def test_support_whole_second_then_fractional_second_is_valid(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE support_tickets SET created_at=?,updated_at=? WHERE id=1",
                       ("2026-10-10 12:13:14", "2026-10-10 12:13:14.100000"))
        result = self.run_export()
        self.assertEqual(result.returncode, 0, result.stderr)
        ticket = json.loads(result.stdout)["support"]["tickets"][0]
        self.assertEqual(ticket["created_at"], "2026-10-10T12:13:14.000000Z")
        self.assertEqual(ticket["updated_at"], "2026-10-10T12:13:14.100000Z")

    def test_support_fractional_second_then_earlier_whole_second_is_rejected(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE support_tickets SET created_at=?,updated_at=? WHERE id=1",
                       ("2026-10-10 12:13:14.100000", "2026-10-10 12:13:14"))
        self.assert_rejected()

    def test_rejects_partial_or_extra_schema(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("ALTER TABLE users ADD COLUMN unexpected TEXT")
        self.assert_rejected()

    def test_rejects_bad_type_and_foreign_key(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE users SET server_id='not-an-id' WHERE id=1")
        self.assert_rejected()

    def test_rejects_duplicate_json_keys(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE plans SET prices=?", ('{"RUB":{"30":1,"30":2}}',))
        self.assert_rejected()

    def test_rejects_lone_surrogate_in_json(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE audit_log SET payload=? WHERE id=1", ('{"x":"\\ud800"}',))
        self.assert_rejected()

    def test_rejects_fractional_day_reward(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE referrer_rewards SET amount=? WHERE reward_type='DAYS'", (1.5,))
        self.assert_rejected()

    def test_rejects_price_that_needs_rounding(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE plans SET prices=?", ('{"RUB":{"30":1.001},"USD":{"30":2.25},"XTR":{"30":42}}',))
        self.assert_rejected()

    def test_rejects_unrecognized_reward_enum(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE referrer_rewards SET reward_type=? WHERE id=1", ("days",))
        self.assert_rejected()

    def test_rejects_unknown_campaign_counters_or_state(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE invites SET clicks=NULL")
        self.assert_rejected()
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE invites SET clicks=2,is_active=NULL")
        self.assert_rejected()

    def test_rejects_missing_source_table(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("DROP TABLE audit_log")
        self.assert_rejected()

    def test_rejects_view_with_exact_table_columns(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            columns = [row[1] for row in db.execute("PRAGMA table_info(audit_log)")]
            db.execute("DROP TABLE audit_log")
            db.execute("CREATE VIEW audit_log AS SELECT " + ",".join(f"NULL AS {column}" for column in columns) + " WHERE 0")
        self.assert_rejected()

    def test_rejects_orphan_transaction_even_without_fk_enforcement(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("PRAGMA foreign_keys=OFF")
            db.execute("UPDATE transactions SET tg_id=? WHERE id=1", (999999999999999,))
        self.assert_rejected()

    def test_rejects_submicrosecond_timestamp(self):
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute("UPDATE audit_log SET created_at=? WHERE id=1", ("2026-10-10 12:13:14.1234567",))
        self.assert_rejected()

    def test_rejects_unowned_or_linked_source(self):
        self.path.chmod(0o644)
        self.assert_rejected()
        self.path.chmod(0o600)
        link = Path(self.tmp.name) / "source-link.sqlite"
        link.symlink_to(self.path)
        result = self.run_export(link)
        self.assertEqual((result.returncode != 0, result.stdout, result.stderr), (True, b"", b"EXPORT_FAILED\n"))


if __name__ == "__main__":
    unittest.main()
