# С47 Cutover/Rollback Implementation Plan

> **For agentic workers:** follow this bounded plan in this session; ordinary independent workers own the named files. The coordinator owns integration, delivery and final acceptance.

**Goal:** retire the Python product after migrating its consumers and prove a single Go executor and lossless current-data rollback.

**Architecture:** `2026-10-05-modular-monolith-v1`: one Go HTTP/River/Telegram process and one go.mod; web/Caddy separate. Reuse public module operations, current PostgreSQL and the existing С43/С46 mechanisms.

**Tech Stack:** Go 1.27.1, PostgreSQL, River, Redis, React/Caddy; already pinned SQLite driver for offline export.

**Spec:** `docs/superpowers/specs/2026-10-10-s47-cutover-rollback-design.md`.

## Global Constraints

- Base `e53746c`, branch `feature/s47-cutover-rollback`, PR/manual merge only `v2`.
- No production, user DB/backup/panel/Telegram/PSP; own synthetic fixtures only.
- Migration 00044 only for legacy money retention; no renumbering or empty DDL.
- Keep signatures/legacy decoders/IDs/keys, strict input, authority, audit and all current real CI scopes.
- No shared Docker changes, other worktrees, auto-merge or GitLab.

## Review Focus

- Old signed UUID labels not present in native orders must retain money.
- Old recurring Stars payload/charge/refund must not disappear or mint a new entitlement.
- Monitored owner loss and warm-up cover the bounded local case; require confirmed old-process/supervisor stop before takeover, including paused/partitioned cases.
- Recovery snapshot must include late facts, not replay an old SQLite over them.
- Removed adapter/bot consumers must not leave executable CI/runbook references behind.

### Task 1: Go offline SQLite exporter

**Files:** `backend/internal/modules/operations` exporter source/tests, `backend/cmd/server` exporter command/tests, `backend/go.mod/go.sum`, existing migration tests and fixture/exporter consumers.
**Interface:** `server export-legacy <private SQLite> --source <slug> --support-bot-id <id> --support-group-id <id>` emits the existing `operations.LegacyPackage`; never starts runtime.

- [x] Port the current validated aggregate exporter using the installed SQLite driver; preserve schema, nullable values, exact decimal/time/Unicode and private-path checks.
- [x] Prove strict rejection, no SQLite mutation and compatible complete packet with small Go tests before deleting the old producer.
- [x] Migrate CLI/native fixture callers and focused exporter consumers; preserve source/dry/apply/replay assertions.
- [x] Verify `go -C backend test ./cmd/server ./internal/modules/operations` with own private test URLs.

### Task 2: Retain legacy late money

**Files:** `backend/internal/modules/payments`, migration `00044_legacy_payment_receipts.sql`, focused HTTP/Telegram tests and an operator CLI report if needed.
**Interface:** existing `Receive*`/`RecordStars*` public ports retain verified legacy events; opaque receipt report requires current operator authority.

- [x] Pin old paths/label/payload behavior from old gateways and write late/duplicate/conflict/refund/recurring tests.
- [x] Store verified money proof/source identifiers in an idempotent review journal without changing immutable imported history or issuing new access.
- [x] Keep provider signatures/API verification; reject malformed/unauthenticated data and avoid sensitive audit/log values.
- [x] Verify payments/module/HTTP/native Telegram tests, migration downgrade guard and current-schema backup compatibility.

### Task 3: One runtime owner

**Files:** `operations` owner source/tests, `backend/cmd/server/main.go`, focused real process tests.
**Interface:** session advisory ownership acquired before `serve` or `reconcile` effects, fatal loss, retained until all effects stop; no DDL.

- [x] Write contention/loss/restart tests on owned PostgreSQL and actual process startup.
- [x] Reject competing `serve`/`reconcile`, cancel on lock-session loss, preserve existing safe shutdown ordering.
- [x] Verify cmd/server/operations race tests and no duplicate HTTP/worker/TG/provider effects.

### Task 4: Retire transition and Python deployment

**Files:** `app`, root runtime/deps/scripts/image, old product tests, transition API/OpenAPI/config and consumers; Compose/Caddy, `.github/workflows`, README/DEPLOYMENT/current runbooks.
**Interface:** native embedded Telegram replaces `/internal/v1/*`; backend/web are the remaining product images. Existing public money paths stay compatible.

- [x] Remove transport routes/types/config only after migrating live test consumers to public operations/native Telegram fixtures; regenerate contracts.
- [x] Delete old product runtime/dependencies/images/CI and update deployment commands; leave stdlib orchestration/provider fixtures independent.
- [x] Keep every real remaining Go/web/security/native/browser/backup scope and add cutover coverage to principal CI.
- [x] Verify generated diff, Go vet, web typecheck/build/browser, Compose validation and native process/backup fixtures.

### Task 5: Integrated acceptance and delivery

**Files:** cutover native tests, `deploy/cutover/README.md`, final evidence document.

- [ ] Bind rollback to a compatible checkpoint commit and read-only artifact preflight; reject base e53746c before stopping current PID; switch distinct binaries and replay old/new callbacks after rollback.
- [ ] Prove maintenance, current-data rollback, late money, restart, full public digest after real backup/restore and source replay together.
- [ ] Run substantive native local scopes on final source; request independent whole-branch review and resolve findings.
- [ ] Conventional Commit with Co-Authored-By, SSH source/upstream/remote preflight, push; create/attach PR targeting `v2`.
- [ ] One full principal exact-source CI and all remaining images; retries only for changed input/evidenced transient with a budget.
- [ ] Recheck exact HEAD/target/dependencies/gates/effects; manual merge, cleanup owned fixtures, close issue, Project Done and parent handoff.
