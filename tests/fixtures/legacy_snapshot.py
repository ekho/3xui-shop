"""Synthetic latest-shape SQLite source for the full migration rehearsal."""

import json
from contextlib import closing
import secrets
import sqlite3
import sys
import uuid
from pathlib import Path


SCHEMA = """
CREATE TABLE servers (id INTEGER PRIMARY KEY, name VARCHAR(255) NOT NULL, host VARCHAR(255) NOT NULL, max_clients INTEGER NOT NULL, location VARCHAR(32), online BOOLEAN NOT NULL, subscription_url VARCHAR(512));
CREATE TABLE users (id INTEGER PRIMARY KEY, tg_id INTEGER NOT NULL UNIQUE, vpn_id VARCHAR(36) NOT NULL UNIQUE, sub_id VARCHAR(36) NOT NULL UNIQUE, server_id INTEGER REFERENCES servers(id) ON DELETE SET NULL, first_name VARCHAR(32) NOT NULL, last_name VARCHAR(64), username VARCHAR(32), language_code VARCHAR(5) NOT NULL, created_at DATETIME NOT NULL, is_trial_used BOOLEAN NOT NULL, approval_status VARCHAR(8) NOT NULL, approval_requested_at DATETIME, approval_decided_at DATETIME, approval_decided_by INTEGER, stars_charge_id VARCHAR(64), is_stars_auto_renew BOOLEAN NOT NULL, stars_expires_at BIGINT, inbound_groups JSON, source_invite_name VARCHAR(100));
CREATE TABLE transactions (id INTEGER PRIMARY KEY, tg_id INTEGER NOT NULL REFERENCES users(tg_id), payment_id VARCHAR(64) NOT NULL UNIQUE, subscription VARCHAR(255) NOT NULL, status VARCHAR(9) NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);
CREATE TABLE plans (id INTEGER PRIMARY KEY, devices INTEGER NOT NULL UNIQUE, traffic_gb INTEGER NOT NULL, prices JSON NOT NULL, inbound_groups JSON NOT NULL, hidden BOOLEAN NOT NULL);
CREATE TABLE plan_durations (id INTEGER PRIMARY KEY, days INTEGER NOT NULL UNIQUE);
CREATE TABLE referrals (id INTEGER PRIMARY KEY, referred_tg_id INTEGER NOT NULL UNIQUE REFERENCES users(tg_id) ON DELETE CASCADE, referrer_tg_id INTEGER NOT NULL REFERENCES users(tg_id) ON DELETE CASCADE, created_at DATETIME NOT NULL, referred_rewarded_at DATETIME, referred_bonus_days INTEGER);
CREATE TABLE referrer_rewards (id INTEGER PRIMARY KEY, user_tg_id INTEGER NOT NULL REFERENCES users(tg_id) ON DELETE CASCADE, reward_type VARCHAR(5) NOT NULL, reward_level VARCHAR(12), amount NUMERIC(38,18) NOT NULL, created_at DATETIME NOT NULL, rewarded_at DATETIME, payment_id VARCHAR(64) NOT NULL, UNIQUE(user_tg_id,payment_id));
CREATE TABLE promocodes (id INTEGER PRIMARY KEY, code VARCHAR(32) NOT NULL UNIQUE, duration INTEGER NOT NULL, is_activated BOOLEAN NOT NULL, activated_by INTEGER REFERENCES users(tg_id), created_at DATETIME NOT NULL);
CREATE TABLE invites (id INTEGER PRIMARY KEY, name VARCHAR NOT NULL UNIQUE, hash_code VARCHAR NOT NULL UNIQUE, clicks INTEGER, created_at DATETIME, is_active BOOLEAN);
CREATE TABLE support_tickets (id INTEGER PRIMARY KEY, tg_id INTEGER NOT NULL UNIQUE, thread_id INTEGER, status VARCHAR(6) NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);
CREATE TABLE audit_log (id INTEGER PRIMARY KEY, created_at DATETIME NOT NULL, actor_type VARCHAR NOT NULL, actor_id INTEGER, actor_name VARCHAR, action VARCHAR NOT NULL, target_id INTEGER, source VARCHAR NOT NULL, payload JSON);
"""


def create(path):
    """Create only the explicit path; return dynamic expected identifiers for assertions."""
    path = Path(path)
    if not path.is_absolute() or path.exists():
        raise ValueError("fixture path must be new and absolute")
    ids = [secrets.randbelow(10**10) + 10**10 for _ in range(3)]
    vpn = [str(uuid.uuid4()) for _ in ids]
    sub = [secrets.token_hex(8) for _ in ids]
    charge = secrets.token_hex(20)
    stamp = "2026-10-10 12:13:14.123456"
    with closing(sqlite3.connect(path)) as db, db:
        db.execute("PRAGMA foreign_keys=ON")
        db.executescript(SCHEMA)
        db.execute("INSERT INTO servers VALUES (1,?,?,?,?,?,?)", ("synthetic", "https://example.invalid", 100, None, 1, None))
        for i, tg in enumerate(ids):
            db.execute("INSERT INTO users VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", (
                i + 1, tg, vpn[i], sub[i], 1 if i != 2 else None, "Synthetic", None, None, "en", stamp,
                1 if i == 0 else 0, ("approved", "pending", "rejected")[i], stamp if i != 1 else None,
                stamp if i != 1 else None, ids[0] if i != 1 else None,
                charge if i == 0 else None, 1 if i == 0 else 0, 1790000000 if i == 0 else None,
                json.dumps(["regular"]) if i == 0 else None, "synthetic-invite" if i == 1 else None))
        for i, status in enumerate(("pending", "completed", "canceled", "refunded")):
            db.execute("INSERT INTO transactions VALUES (?,?,?,?,?,?,?)", (i + 1, ids[i % 3], secrets.token_hex(16), "synthetic", status, stamp, stamp))
        db.execute("INSERT INTO plan_durations VALUES (?,?)", (1, 30))
        db.execute("INSERT INTO plans VALUES (?,?,?,?,?,?)", (1, 2, 100, '{"RUB":{"30":120.50},"USD":{"30":2.25},"XTR":{"30":42}}', '["regular"]', 0))
        db.execute("INSERT INTO referrals VALUES (?,?,?,?,?,?)", (1, ids[1], ids[0], stamp, None, None))
        for i, kind in enumerate(("DAYS", "MONEY")):
            db.execute("INSERT INTO referrer_rewards VALUES (?,?,?,?,?,?,?,?)", (i + 1, ids[0], kind, "FIRST_LEVEL" if i == 0 else None, 10 if i == 0 else 2.25, stamp, stamp if i == 0 else None, secrets.token_hex(12)))
        db.execute("INSERT INTO promocodes VALUES (?,?,?,?,?,?)", (1, secrets.token_hex(4), 30, 1, ids[1], stamp))
        db.execute("INSERT INTO promocodes VALUES (?,?,?,?,?,?)", (2, secrets.token_hex(4), 7, 0, None, stamp))
        db.execute("INSERT INTO invites VALUES (?,?,?,?,?,?)", (1, "synthetic-invite", secrets.token_hex(8), 2, None, 1))
        db.execute("INSERT INTO support_tickets VALUES (?,?,?,?,?,?)", (1, ids[0], 42, "open", stamp, stamp))
        db.execute("INSERT INTO support_tickets VALUES (?,?,?,?,?,?)", (2, secrets.randbelow(10**10) + 10**10, None, "closed", stamp, stamp))
        db.execute("INSERT INTO audit_log VALUES (?,?,?,?,?,?,?,?,?)", (1, stamp, "admin", ids[0], "Synthetic", "approval.approve", ids[0], "main_bot", json.dumps({"nonce": secrets.token_hex(8)})))
        db.execute("INSERT INTO audit_log VALUES (?,?,?,?,?,?,?,?,?)", (2, stamp, "system", None, None, "system.audit_prune", None, "job", None))
    path.chmod(0o600)
    return {"tg_ids": ids, "vpn_ids": vpn, "sub_ids": sub, "charge": charge}


if __name__ == "__main__":
    create(sys.argv[1])
    print("SYNTHETIC_SOURCE_READY")
