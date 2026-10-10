#!/usr/bin/env python3
"""Read a private current-schema SQLite snapshot and emit one complete C46 packet."""

import argparse
from contextlib import closing
import json
import math
import os
import re
import sqlite3
import stat
import sys
from datetime import datetime, timezone
from decimal import Decimal
from pathlib import Path
from urllib.parse import urlsplit
from uuid import UUID


MAX_PACKET = 32 << 20
INT64 = (1 << 63) - 1
TABLES = {
    "servers": "id name host max_clients location online subscription_url",
    "users": "id tg_id vpn_id sub_id server_id first_name last_name username language_code created_at is_trial_used approval_status approval_requested_at approval_decided_at approval_decided_by stars_charge_id is_stars_auto_renew stars_expires_at inbound_groups source_invite_name",
    "transactions": "id tg_id payment_id subscription status created_at updated_at",
    "plans": "id devices traffic_gb prices inbound_groups hidden",
    "plan_durations": "id days",
    "referrals": "id referred_tg_id referrer_tg_id created_at referred_rewarded_at referred_bonus_days",
    "referrer_rewards": "id user_tg_id reward_type reward_level amount created_at rewarded_at payment_id",
    "promocodes": "id code duration is_activated activated_by created_at",
    "invites": "id name hash_code clicks created_at is_active",
    "support_tickets": "id tg_id thread_id status created_at updated_at",
    "audit_log": "id created_at actor_type actor_id actor_name action target_id source payload",
}
TABLES = {name: fields.split() for name, fields in TABLES.items()}
STAMP = re.compile(r"^\d{4}-\d\d-\d\d[ T]\d\d:\d\d:\d\d(?:\.\d{1,6})?(?:Z|[+-]\d\d:\d\d)?$")
SLUG = re.compile(r"^[a-z0-9][a-z0-9_-]{0,63}$")
PRICE = re.compile(r"^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$")


def fail():
    raise ValueError("invalid source")


def integer(value, *, minimum=0, nullable=False):
    if value is None and nullable:
        return None
    if type(value) is not int or not minimum <= value <= INT64:
        fail()
    return value


def flag(value, *, nullable=False):
    if value is None and nullable:
        return None
    if type(value) is not int or value not in (0, 1):
        fail()
    return bool(value)


def string(value, *, limit=None, nullable=False, nonempty=False):
    if value is None and nullable:
        return None
    if type(value) is not str or (nonempty and not value) or (limit and len(value) > limit):
        fail()
    value.encode("utf-8")  # Lone surrogates cannot enter a UTF-8 packet.
    if "\x00" in value:
        fail()
    return value


def stamp(value, *, nullable=False):
    if value is None and nullable:
        return None
    if type(value) is not str or not STAMP.fullmatch(value):
        fail()  # Check precision before datetime.fromisoformat truncates it.
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)  # SQLite naive DateTime is authoritative UTC.
    return parsed.astimezone(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")


def unique_pairs(pairs):
    out = {}
    for key, value in pairs:
        if key in out:
            fail()
        out[key] = value
    return out


def json_value(raw, *, nullable=False):
    if raw is None and nullable:
        return None
    string(raw)
    value = json.loads(raw, object_pairs_hook=unique_pairs, parse_float=Decimal,
                       parse_constant=lambda _: fail())
    # json.loads accepts escaped lone surrogates; encoding rejects them recursively.
    def check(item):
        if isinstance(item, str):
            item.encode("utf-8")
        elif isinstance(item, dict):
            for key, val in item.items():
                check(key)
                check(val)
        elif isinstance(item, list):
            for val in item:
                check(val)
    check(value)
    return value


def groups(raw, *, nullable=False, allow_empty=False):
    if raw is None and nullable:
        return None
    value = json_value(raw, nullable=nullable)
    if value is None and nullable:
        return None  # SQLAlchemy JSON(None) is stored as JSON null in SQLite.
    if type(value) is not list or not allow_empty and not value or len(value) != len(set(map(str, value))):
        fail()
    for item in value:
        string(item, limit=64, nonempty=True)
    return value


def amount(value, kind):
    if type(value) is int:
        dec = Decimal(value)
    elif type(value) is float:
        if not math.isfinite(value) or 0 < abs(value) < 1e-18:
            fail()
        dec = Decimal(format(value, ".18f"))  # SQLAlchemy Numeric(38,18) observes this scale.
    else:
        fail()
    if dec < 0 or dec >= Decimal(10) ** 20 or len(dec.as_tuple().digits) > 38 or dec.as_tuple().exponent < -18:
        fail()
    if kind == "DAYS" and dec != dec.to_integral_value():
        fail()
    return format(dec, "f")


def prices(raw, durations):
    value = json_value(raw)
    if type(value) is not dict:
        fail()
    out = {}
    for currency, by_day in value.items():
        if currency not in ("RUB", "USD", "XTR") or type(by_day) is not dict:
            fail()
        out[currency] = {}
        for day, number in by_day.items():
            if day not in durations or type(number) not in (int, Decimal):
                fail()
            decimal = Decimal(number)
            rendered = format(decimal, "f")
            scale = 0 if currency == "XTR" else 2
            if decimal < 0 or not PRICE.fullmatch(rendered) or -decimal.as_tuple().exponent > scale or decimal * (10 ** scale) > INT64:
                fail()
            out[currency][day] = rendered
    return out


def enum(value, allowed):
    if type(value) is not str or value not in allowed:
        fail()
    return value


def https_url(value, *, limit=512, nullable=False):
    value = string(value, limit=limit, nullable=nullable, nonempty=True)
    if value is None:
        return None
    parts = urlsplit(value)
    if (value != value.strip() or parts.scheme != "https" or not parts.hostname or parts.username is not None or
            parts.password is not None or parts.query or parts.fragment or "#" in value or any(c in value for c in "\\\r\n\t") or
            any(part in (".", "..") for part in parts.path.split("/"))):
        fail()
    if parts.port is not None and not 1 <= parts.port <= 65535:
        fail()
    return value


def private_source(path):
    p = Path(path)
    if not p.is_absolute() or p.resolve(strict=True) != p:
        fail()
    # Reject a symlink in any path component, including the last component.
    for component in (p, *p.parents):
        if component.is_symlink():
            fail()
    mode = p.stat()
    if not stat.S_ISREG(mode.st_mode) or mode.st_uid != os.getuid() or stat.S_IMODE(mode.st_mode) != 0o600:
        fail()
    return p


def read_tables(db):
    objects = [(name, kind) for name, kind in db.execute("SELECT name,type FROM sqlite_master WHERE type IN ('table','view')")
               if not name.startswith("sqlite_")]
    if any(kind != "table" for _, kind in objects):
        fail()
    names = {name for name, _ in objects}
    if names - {"alembic_version"} != TABLES.keys():
        fail()
    if "alembic_version" in names and [r[1] for r in db.execute("PRAGMA table_xinfo(alembic_version)")] != ["version_num"]:
        fail()
    for name, columns in TABLES.items():
        if set(row[1] for row in db.execute(f"PRAGMA table_xinfo({name})")) != set(columns):
            fail()
    if db.execute("PRAGMA quick_check").fetchone()[0] != "ok" or db.execute("PRAGMA integrity_check").fetchone()[0] != "ok" or db.execute("PRAGMA foreign_key_check").fetchone():
        fail()
    return {name: [dict(zip(columns, row)) for row in db.execute(f"SELECT {','.join(columns)} FROM {name} ORDER BY id")]
            for name, columns in TABLES.items()}


def build(rows, source, bot_id, group_id):
    for table_rows in rows.values():
        ids = [integer(row["id"], minimum=1) for row in table_rows]
        if len(ids) != len(set(ids)):
            fail()
    servers = [{"source_id": integer(r["id"], minimum=1), "name": string(r["name"], limit=255, nonempty=True),
                "host": https_url(r["host"], limit=255), "max_clients": integer(r["max_clients"]),
                "location": string(r["location"], limit=32, nullable=True), "online": flag(r["online"]),
                "subscription_url": https_url(r["subscription_url"], nullable=True)} for r in rows["servers"]]
    server_ids = {r["source_id"] for r in servers}
    if len({r["name"] for r in servers}) != len(servers) or any(r["name"] != r["name"].strip() for r in servers):
        fail()
    users = []
    approvals = []
    payments_users = []
    stars = []
    campaigns_users = []
    for r in rows["users"]:
        uid, tg = integer(r["id"], minimum=1), integer(r["tg_id"], minimum=1)
        server_id = integer(r["server_id"], minimum=1, nullable=True)
        if server_id is not None and server_id not in server_ids:
            fail()
        vpn, sub = string(r["vpn_id"], limit=36, nonempty=True), string(r["sub_id"], limit=36, nonempty=True)
        if UUID(vpn).int == 0 or str(UUID(vpn)) != vpn or not re.fullmatch(r"[0-9a-z]{16}", sub) and (len(sub) != 36 or str(UUID(sub)) != sub):
            fail()
        trial = flag(r["is_trial_used"], nullable=True)
        invite = string(r["source_invite_name"], limit=100, nullable=True, nonempty=True)
        if invite is not None and not invite.strip():
            fail()
        inbound = groups(r["inbound_groups"], nullable=True, allow_empty=True)
        if inbound and (not set(inbound) <= {"regular", "euru", "unlimited", "banned"} or
                        inbound != ["banned"] and len(set(inbound) - {"banned"}) != 1):
            fail()
        users.append({"source_id": uid, "tg_id": tg, "vpn_id": vpn, "sub_id": sub, "server_id": server_id,
                      "first_name": string(r["first_name"], limit=32, nonempty=True), "last_name": string(r["last_name"], limit=64, nullable=True),
                      "username": string(r["username"], limit=32, nullable=True), "language_code": string(r["language_code"], limit=5, nonempty=True),
                      "created_at": stamp(r["created_at"]), "is_trial_used": trial,
                      "inbound_groups": inbound, "source_invite_name": invite})
        approvals.append({"source_legacy_user_id": uid, "source_tg_id": tg, "status": enum(r["approval_status"], {"pending", "approved", "rejected"}),
                          "requested_at": stamp(r["approval_requested_at"], nullable=True), "decided_at": stamp(r["approval_decided_at"], nullable=True),
                          "decided_by": integer(r["approval_decided_by"], minimum=1, nullable=True)})
        payments_users.append({"source_legacy_user_id": uid, "source_tg_id": tg})
        stars.append({"source_legacy_user_id": uid, "source_tg_id": tg, "stars_charge_id": string(r["stars_charge_id"], limit=64, nullable=True),
                      "is_stars_auto_renew": flag(r["is_stars_auto_renew"], nullable=True), "stars_expires_at": integer(r["stars_expires_at"], minimum=0, nullable=True)})
        if invite is not None:
            campaigns_users.append({"source_legacy_user_id": uid, "source_tg_id": tg, "source_invite_name": invite, "is_trial_used": trial})
    tg_ids = {u["tg_id"] for u in users}
    if (len(tg_ids) != len(users) or len({u["source_id"] for u in users}) != len(users) or
            len({u["vpn_id"] for u in users}) != len(users) or len({u["sub_id"] for u in users}) != len(users)):
        fail()
    durations = [{"source_id": integer(r["id"], minimum=1), "days": integer(r["days"], minimum=1)} for r in rows["plan_durations"]]
    days = {str(r["days"]) for r in durations}
    if len(days) != len(durations) or len(days) > 100 or any(r["days"] > 106751 for r in durations):
        fail()
    plans = []
    source_plans = []
    for r in rows["plans"]:
        gid = groups(r["inbound_groups"])
        hidden = flag(r["hidden"])
        profile = {("regular",): "regular", ("euru",): "euru", ("unlimited",): "unlimited"}.get(tuple(gid))
        if profile is None or profile == "unlimited" and not hidden:
            fail()
        p = prices(r["prices"], days)
        pid = integer(r["id"], minimum=1)
        if (not hidden and (not days or set(p) != {"RUB", "USD", "XTR"} or any(set(v) != days for v in p.values())) or
                hidden and p and (set(p) != {"RUB", "USD", "XTR"} or any(set(v) != days for v in p.values()))):
            fail()
        devices, traffic = integer(r["devices"], minimum=1), integer(r["traffic_gb"])
        if devices > 10000 or traffic > 100000:
            fail()
        plans.append({"legacy_plan_id": pid, "devices": devices, "traffic_gb": traffic,
                      "profile": profile, "hidden": hidden, "prices": p})
        source_plans.append({"source_id": pid, "inbound_groups": gid, "prices_json": string(r["prices"])})
    if len({p["devices"] for p in plans}) != len(plans):
        fail()
    transactions = []
    for r in rows["transactions"]:
        tg = integer(r["tg_id"], minimum=1)
        if tg not in tg_ids:
            fail()
        transactions.append({"source_id": integer(r["id"], minimum=1), "source_tg_id": tg,
                             "payment_id": string(r["payment_id"], limit=64, nonempty=True),
                             "subscription": string(r["subscription"], limit=255, nonempty=True),
                             "status": enum(r["status"], {"pending", "completed", "canceled", "refunded"}),
                             "created_at": stamp(r["created_at"]), "updated_at": stamp(r["updated_at"])})
    if len({r["payment_id"] for r in transactions}) != len(transactions):
        fail()
    promos = []
    for r in rows["promocodes"]:
        actor = integer(r["activated_by"], minimum=1, nullable=True)
        activated = flag(r["is_activated"])
        if actor is not None and (actor not in tg_ids or not activated):
            fail()
        duration = integer(r["duration"], minimum=1)
        if duration > 2147483647:
            fail()
        promos.append({"source_id": integer(r["id"], minimum=1), "code": string(r["code"], limit=32, nonempty=True),
                       "duration": duration, "is_activated": activated,
                       "activated_by": actor, "created_at": stamp(r["created_at"])})
    if len({p["code"] for p in promos}) != len(promos):
        fail()
    refs = []
    for r in rows["referrals"]:
        referred, referrer = integer(r["referred_tg_id"], minimum=1), integer(r["referrer_tg_id"], minimum=1)
        if referred not in tg_ids or referrer not in tg_ids or referred == referrer:
            fail()
        bonus_days = integer(r["referred_bonus_days"], nullable=True)
        if bonus_days is not None and bonus_days > 2147483647:
            fail()
        refs.append({"source_id": integer(r["id"], minimum=1), "referred_tg_id": referred, "referrer_tg_id": referrer,
                     "created_at": stamp(r["created_at"]), "referred_rewarded_at": stamp(r["referred_rewarded_at"], nullable=True),
                     "referred_bonus_days": bonus_days})
    parents = {r["referred_tg_id"]: r["referrer_tg_id"] for r in refs}
    if len(parents) != len(refs):
        fail()
    for child in parents:
        seen = set()
        while child in parents:
            if child in seen:
                fail()
            seen.add(child)
            child = parents[child]
    rewards = []
    for r in rows["referrer_rewards"]:
        tg = integer(r["user_tg_id"], minimum=1)
        if tg not in tg_ids:
            fail()
        kind = enum(r["reward_type"], {"DAYS", "MONEY"})  # SQLAlchemy stores Enum member names.
        level = r["reward_level"]
        if level is not None:
            level = {"FIRST_LEVEL": 1, "SECOND_LEVEL": 2}.get(level)
            if level is None:
                fail()
        rewards.append({"source_id": integer(r["id"], minimum=1), "user_tg_id": tg, "reward_type": kind,
                        "reward_level": level, "amount": amount(r["amount"], kind), "payment_id": string(r["payment_id"], limit=64, nonempty=True),
                        "created_at": stamp(r["created_at"]), "rewarded_at": stamp(r["rewarded_at"], nullable=True)})
    if len({(r["user_tg_id"], r["payment_id"]) for r in rewards}) != len(rewards):
        fail()
    campaigns = []
    for r in rows["invites"]:
        code = string(r["hash_code"], limit=64, nonempty=True)
        if not re.fullmatch(r"[A-Za-z0-9_-]+", code) or code.isdecimal():
            fail()
        campaigns.append({"source_id": integer(r["id"], minimum=1), "name": string(r["name"], limit=100, nonempty=True),
                          "hash_code": code, "clicks": integer(r["clicks"]),
                          "is_active": flag(r["is_active"]), "created_at": stamp(r["created_at"], nullable=True)})
    if len({c["name"] for c in campaigns}) != len(campaigns) or len({c["hash_code"] for c in campaigns}) != len(campaigns):
        fail()
    if any(not c["name"].strip() for c in campaigns):
        fail()
    tickets = [{"source_id": integer(r["id"], minimum=1), "tg_id": integer(r["tg_id"], minimum=1),
                "thread_id": integer(r["thread_id"], minimum=1, nullable=True), "status": enum(r["status"], {"open", "closed", "banned"}),
                "created_at": stamp(r["created_at"]), "updated_at": stamp(r["updated_at"])} for r in rows["support_tickets"]]
    threads = [t["thread_id"] for t in tickets if t["thread_id"] is not None]
    if (len({t["tg_id"] for t in tickets}) != len(tickets) or len(set(threads)) != len(threads) or
            any(t["updated_at"] < t["created_at"] for t in tickets)):
        fail()
    events = []
    approval_events = []
    for r in rows["audit_log"]:
        event = {"source_id": integer(r["id"], minimum=1), "created_at": stamp(r["created_at"]),
                 "action": string(r["action"], limit=128, nonempty=True), "target_tg_id": integer(r["target_id"], minimum=1, nullable=True),
                 "actor_type": enum(r["actor_type"], {"admin", "support", "system", "user"}), "actor_id": integer(r["actor_id"], minimum=1, nullable=True),
                 "actor_name": string(r["actor_name"], limit=256, nullable=True), "source": enum(r["source"], {"main_bot", "support_bot", "job"}),
                 "payload_json": string(r["payload"], nullable=True)}
        payload = json_value(r["payload"], nullable=True)
        if (r["payload"] is not None and (type(payload) is not dict or len(r["payload"].encode("utf-8")) > 64 << 10)):
            fail()
        events.append(event)
        if event["action"] in ("approval.approve", "approval.reject"):
            if event["target_tg_id"] not in tg_ids:
                fail()
            approval_events.append({key: value for key, value in event.items() if key != "payload_json"})
    return {"version": 1, "source": source, "users": users, "servers": servers,
            "catalogue": {"version": 1, "durations": [d["days"] for d in durations], "plans": plans},
            "approvals": {"version": 1, "users": approvals, "approval_events": approval_events},
            "payments": {"version": 1, "users": payments_users, "transactions": transactions},
            "stars": stars, "bonuses": {"version": 1, "source": source, "promocodes": promos, "referrals": refs, "rewards": rewards},
            "campaigns": {"version": 1, "source": source, "campaigns": campaigns, "users": campaigns_users},
            "support": {"version": 1, "bot_id": bot_id, "group_id": group_id, "tickets": tickets},
            "audit": {"version": 1, "events": events},
            "catalogue_source": {"durations": durations, "plans": source_plans}}


def export(path, source, bot_id, group_id):
    private_source(path)
    if not SLUG.fullmatch(source) or integer(bot_id, minimum=1) != bot_id or type(group_id) is not int or not -(1 << 63) <= group_id < 0:
        fail()
    uri = Path(path).as_uri() + "?mode=ro"
    with closing(sqlite3.connect(uri, uri=True)) as db:
        db.execute("PRAGMA query_only=ON")
        db.execute("BEGIN")
        packet = build(read_tables(db), source, bot_id, group_id)
        db.rollback()
    raw = json.dumps(packet, ensure_ascii=False, separators=(",", ":"), allow_nan=False).encode("utf-8") + b"\n"
    if len(raw) > MAX_PACKET:
        fail()
    return raw


class QuietParser(argparse.ArgumentParser):
    def error(self, message):
        fail()


def main():
    parser = QuietParser(add_help=False)
    parser.add_argument("snapshot")
    parser.add_argument("--source", required=True)
    parser.add_argument("--support-bot-id", required=True, type=int)
    parser.add_argument("--support-group-id", required=True, type=int)
    try:
        args = parser.parse_args()
        raw = export(args.snapshot, args.source, args.support_bot_id, args.support_group_id)
    except Exception:  # Source-derived failures must never expose paths or row values.
        sys.stderr.write("EXPORT_FAILED\n")
        return 1
    sys.stdout.buffer.write(raw)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
