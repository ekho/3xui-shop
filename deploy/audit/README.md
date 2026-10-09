# Audit history and local legacy import

The `audit_reports` module owns native, legacy and system history. Operators use
the cabinet journal or a client's journal tab. Only metadata leaves this owner;
legacy JSON bodies are private and expire with their audit row.

Deploy-time settings (no build-time names or domains):

| Setting | Default | Meaning |
| --- | --- | --- |
| `AUDIT_RETENTION_DAYS` | `365` | Integer `1..3650`; database UTC cutoff |
| `AUDIT_RETENTION_TIMEZONE` | `BOT_TIMEZONE`, then `UTC` | IANA daily schedule after 03:30 |
| `AUDIT_MIRROR_ENABLED` | `false` | Optional metadata mirror to support General |
| `SUPPORT_BOT_TOKEN_FILE` | unset | Separate bot secret file, required when enabled |
| `SUPPORT_GROUP_ID` | unset | Negative supergroup ID within Telegram's 52-bit limit |

The audit scheduler runs inside `server serve`. The support mirror can run while
`TELEGRAM_ENABLED=false`; it does not receive support updates. A failed wire call
does not stop the cabinet or change the recorded action. It is one best-effort
attempt after commit, without retries; startup skips earlier pending mirrors.
No reasons, names, email addresses, messages, attachments or keys are mirrored.

Use a copy of the old `audit_log` and the owned database URL file:

```sh
server import-legacy-audit --dry-run < audit-package.json
server import-legacy-audit --apply < audit-package.json
```

Set `DATABASE_URL_FILE` for the target. Package shape:

```json
{"version":1,"events":[{"source_id":7,"created_at":"2026-10-01T12:00:00.123456Z","action":"support.message","target_tg_id":701,"actor_type":"operator","actor_id":702,"actor_name":"Recorded name","source":"support_bot","payload_json":"{\"original\": \"private source body\"}"}]}
```

All IDs are exact signed 64-bit integers; source and target IDs must be positive.
Optional metadata/payload fields accept null. An empty `events` array is valid.
The CLI rejects unknown fields, invalid Unicode, sub-microsecond timestamps,
duplicate source IDs and payloads outside JSON objects/64 KiB. The entire package
is limited to 32 MiB. It prints inserted/replayed counts or a safe error code.

Apply is atomic. Repeating unchanged sources creates no new fact. A changed source
fails with `IMPORT_SOURCE_CONFLICT`; dry-run writes nothing. The minimal source
digest ledger survives retention, so repeating a pruned package cannot restore
its private body. Mapping uses immutable account import proofs; current Telegram
bindings never move legacy history. Full migration/export is covered by С46.

Daily prune deletes only this module's audit rows and records actual counts in
one transaction. It preserves identity, payment, access, support and notification
facts. A receipt prevents a second prune on the same local day, including restart.
Downgrading migration 33 is blocked while legacy digests/history or system receipts
exist. Preserve the database backup; do not clear the ledger to force a downgrade.

Acceptance uses local fixtures and fake Telegram. This guide does not authorize
production import, real support-bot sends, wallet transfers or Happ/VPN changes.
