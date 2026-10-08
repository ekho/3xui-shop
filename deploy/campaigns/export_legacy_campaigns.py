#!/usr/bin/env python3
"""Export only campaign metadata from a stopped, owned SQLite snapshot."""
import argparse
import json
import sqlite3
import sys
from pathlib import Path
from zoneinfo import ZoneInfo

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "account-restrictions"))
from export_legacy_approval import instant


def boolean(value):
    if type(value) is not int or value not in (0, 1):
        raise ValueError("invalid historical flag")
    return bool(value)


def export(path, source_zone, source):
    uri = Path(path).resolve(strict=True).as_uri() + "?mode=ro"
    with sqlite3.connect(uri, uri=True) as db:
        db.execute("PRAGMA query_only=ON")
        campaigns = [{"source_id": r[0], "name": r[1], "hash_code": r[2], "clicks": r[3],
                      "is_active": boolean(r[4]), "created_at": instant(r[5], source_zone)}
                     for r in db.execute("SELECT id,name,hash_code,clicks,is_active,created_at FROM invites ORDER BY id LIMIT 10001")]
        users = [{"source_legacy_user_id": r[0], "source_tg_id": r[1], "source_invite_name": r[2], "is_trial_used": boolean(r[3])}
                 for r in db.execute("SELECT id,tg_id,source_invite_name,is_trial_used FROM users WHERE source_invite_name IS NOT NULL ORDER BY id LIMIT 100001")]
    if len(campaigns) > 10000 or len(users) > 100000:
        raise ValueError("source exceeds package bound")
    return {"version": 1, "source": source, "campaigns": campaigns, "users": users}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("snapshot")
    parser.add_argument("--timezone", required=True)
    parser.add_argument("--source", required=True, help="Stable non-secret identifier of this legacy source")
    args = parser.parse_args()
    try:
        payload = json.dumps(export(args.snapshot, ZoneInfo(args.timezone), args.source), ensure_ascii=False,
                             separators=(",", ":")).encode("utf-8")
        if len(payload) + 1 > 32 << 20:
            raise ValueError("package exceeds decoder bound")
    except (OSError, ValueError, TypeError, UnicodeError, sqlite3.Error, KeyError):
        print("EXPORT_FAILED", file=sys.stderr)
        return 1
    sys.stdout.buffer.write(payload + b"\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
