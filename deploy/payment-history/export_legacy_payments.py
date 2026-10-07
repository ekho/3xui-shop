#!/usr/bin/env python3
"""Export a stopped, controlled SQLite payment snapshot without changing it."""

import argparse
import json
import sqlite3
import sys
from pathlib import Path
from zoneinfo import ZoneInfo

# The existing standalone exporter has the same explicit-timezone conversion.
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "account-restrictions"))
from export_legacy_approval import instant


def export(path, source_zone):
    uri = Path(path).resolve(strict=True).as_uri() + "?mode=ro"
    with sqlite3.connect(uri, uri=True) as db:
        db.execute("PRAGMA query_only=ON")
        users = [{"source_legacy_user_id": row[0], "source_tg_id": row[1]}
                 for row in db.execute("SELECT id,tg_id FROM users WHERE tg_id IN (SELECT tg_id FROM transactions) ORDER BY id")]
        transactions = [{"source_id": row[0], "source_tg_id": row[1],
                         "payment_id": row[2], "subscription": row[3], "status": row[4],
                         "created_at": instant(row[5], source_zone),
                         "updated_at": instant(row[6], source_zone)}
                        for row in db.execute("SELECT id,tg_id,payment_id,subscription,status,created_at,updated_at FROM transactions ORDER BY id")]
    return {"version": 1, "users": users, "transactions": transactions}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("snapshot")
    parser.add_argument("--timezone", required=True, help="IANA timezone for naive legacy timestamps")
    args = parser.parse_args()
    try:
        payload = json.dumps(export(args.snapshot, ZoneInfo(args.timezone)), ensure_ascii=False,
                             separators=(",", ":")).encode("utf-8")
        if len(payload) + 1 > 32 << 20:
            raise ValueError("package too large")
    except (OSError, ValueError, TypeError, UnicodeError, sqlite3.Error, KeyError):
        print("EXPORT_FAILED", file=sys.stderr)
        return 1
    sys.stdout.buffer.write(payload + b"\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
