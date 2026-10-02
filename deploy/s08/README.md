# С08/С41 local acceptance drivers

The one-shot S08 acceptance ran against owned `cabinet-s01-local` on product
revision `1b90f9f5c7cfe3f1bfcdbcaf9bcaa3ed19a75b1f`, backend image
`sha256:f676404a90c39349ee6539133d324fa0a01f64b1018c107591ac6fb853fc2975`,
gateway image `sha256:d70e6e26598449623146affebac698fdf2d33b039bf3c09870b661561acc4ccb`,
and pinned 3X-UI 3.7.0. Root verified live image IDs separately in
[runtime-s08.json](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/runtime-s08.json).
The driver checks owned transport and primary VPN config digest against that
manifest; it does not inspect live image IDs. No gate, probe, Compose, native
inbound, installed VPN, browser trust or clipboard operation is performed by
these drivers.
Root's independent [postflight](../../.superpowers/sdd/2026-10-02-s48-s09-s07-s08/postflight-s08.json)
confirmed unchanged images and primary VPN digest, health, stopped
probe/gate/bot/reconcile, UTC, and zero unresolved access operations.

The executed sequence was `readiness` (3 PASS), `setup` (2), `finite` (3),
`unlimited` (3), `ban` (4), `intent-trial` (3), `intent-bonus` (4), `guards`
(3), `ui` (3), then `restore` (4): **32/32 actual local evidence rows PASS**.
Each stage wrote one private JSONL under
`.superpowers/sdd/2026-10-02-s08-access-profiles/e2e/`; the credential
checkpoint is mode 0600 and its contents must not be printed. Stages are
one-shot and should not be repeated. Exact commands, bounded wrapper logs,
AC1–8 mapping and limitations are in
[the acceptance evidence](../../docs/evidence/s08-s41-acceptance.md).

`readiness` reused the existing eligible S07 finite account and operator,
confirmed a real nonzero native counter, exact inbound IDs 1 regular / 2
unmanaged / 3 EURU / 4 unlimited and exactly one current hidden unlimited
seed. It checkpointed two fresh `s08-UUID@example.test` credentials before
`setup` registered precisely those two no-client accounts. Root attached
only the already existing unmanaged id2 to the owned finite client and ran
the separate local VPN probe; the driver never configures those surfaces.
Root's probe yielded 224 bytes before `finite` and 1344 stable bytes after
`unlimited` revoked to EURU, both with the primary VPN preserved.

`browser.mjs` uses actual cabinet/operator HTTPS requests and Chromium at
375px, and `local.py` reads native 3X-UI and owned PostgreSQL rows without
printing credentials, links, raw panel replies or personal data. `restore`
copies the live DB through a pipe into a fresh disposable DB, seeds one
synthetic `2099-01` account-period **there only**, checks uniqueness and
repeated post-restore auth cleanup, then backs that disposable DB into a
second fresh disposable DB and verifies the period and all digests again.
The active DB receives no synthetic monthly period.

The live stages prove active finite profile/ban transitions, no-client
intent→first trial and bonus, strict HTTP guards, current-state no-op,
real RU/EN keyboard/confirmation UI and disposable restore. S41 clock,
timezone, busy period, elapsed period, eligibility and lost-reset cases
are executed Go source tests with a controlled panel/DB fixture, **not** a
forced live scheduler tick or macOS clock change. Expired/exhausted unban,
expired unlimited activation and unready unlimited-plan/inbound cases are
source-only boundaries. Pending/error/retry UI uses route-double component
tests; the live UI stage does not inject those faults. Prior real S07
lost-reply/restart/partial-effect evidence covers the shared executor,
while new S08-specific races and reconciliation guards use Go tests.
