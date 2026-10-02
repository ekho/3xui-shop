#!/usr/bin/env python3
"""Export only legacy registration approval metadata from a controlled SQLite copy."""

import argparse
import json
import sqlite3
import sys
from datetime import datetime, timezone
from pathlib import Path
from zoneinfo import ZoneInfo


def instant(value, source_zone):
    if value is None:
        return None
    parsed = datetime.fromisoformat(value)
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=source_zone)
    return parsed.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def export(path, source_zone):
    uri = Path(path).resolve(strict=True).as_uri() + "?mode=ro"
    with sqlite3.connect(uri, uri=True) as db:
        db.execute("PRAGMA query_only=ON")
        users = [
            {
                "source_legacy_user_id": row[0],
                "source_tg_id": row[1],
                "status": row[2],
                "requested_at": instant(row[3], source_zone),
                "decided_at": instant(row[4], source_zone),
                "decided_by": row[5],
            }
            for row in db.execute(
                "SELECT id,tg_id,approval_status,approval_requested_at,approval_decided_at,approval_decided_by FROM users ORDER BY id"
            )
        ]
        events = [
            {
                "source_id": row[0],
                "created_at": instant(row[1], source_zone),
                "actor_type": row[2],
                "actor_id": row[3],
                "actor_name": row[4],
                "action": row[5],
                "target_tg_id": row[6],
                "source": row[7],
            }
            for row in db.execute(
                "SELECT id,created_at,actor_type,actor_id,actor_name,action,target_id,source "
                "FROM audit_log WHERE action IN ('approval.approve','approval.reject') ORDER BY id"
            )
        ]
    return {"version": 1, "users": users, "approval_events": events}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("snapshot")
    parser.add_argument("--timezone", required=True, help="IANA timezone for naive legacy timestamps")
    args = parser.parse_args()
    try:
        package = export(args.snapshot, ZoneInfo(args.timezone))
    except (OSError, ValueError, TypeError, sqlite3.Error, KeyError):
        print("EXPORT_FAILED", file=sys.stderr)
        return 1
    json.dump(package, sys.stdout, ensure_ascii=False, separators=(",", ":"))
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
