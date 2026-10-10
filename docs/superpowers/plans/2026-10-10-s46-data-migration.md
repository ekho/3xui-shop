# С46 Data Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Полностью и атомарно перенести поддерживаемый synthetic SQLite snapshot и доказать сохранность итоговой PostgreSQL схемы.

**Architecture:** operations координирует одну транзакцию публичными owner ports. Существующие CLI импорты сохраняют wrappers; исходные facts отделены от native funding/выдачи.

**Tech Stack:** Go/pgx, PostgreSQL 17, standard-library Python sqlite3/decimal, существующие tests/backup fixtures.

**Spec:** `docs/superpowers/specs/2026-10-10-s46-data-migration-design.md`

## Global Constraints

- `2026-10-05-modular-monolith-v1`: один Go процесс/go.mod; SQL только у владельца.
- `feature/s46-data-migration` от `6236d8c0e3100ef816d036fe0b8fa11163f0f44a`, PR/merge только v2.
- DDL только 00043; старые migration/Down guards не меняются; Python остаётся до #54.
- Собственные synthetic fixtures; без production, пользовательских DB/backup/secret files, реальных панелей/платежей/Telegram.
- Повтор не перезаписывает текущие native данные; unknown не разрешает доступ/награду/рекуррент.
- Conventional Commits и Co-Authored-By; exact-source CI; manual merge, Closed/Done/cleanup.

## Review Focus

- Частичная либо новая неожиданная SQLite схема: ошибка до package/DB side effects.
- Нормализация UUID, decimal, NULL либо timestamp: потеря запрещена, явный отказ.
- Перемещённая Telegram identity и изменённый source: original mapping retained, никакого нового владельца истории.
- Поздняя ошибка после новых account/server/referral inserts: rollback всего import.
- Существующие funded/pending/grants/reward facts и новые alias/edits: repeat/backup/restore не меняет их.

### Task 1: Owner transaction ports and missing data

**Files:** Existing six import files; new `accounts/legacy_users.go`, `vpn/legacy_servers.go`, `payments/legacy_stars.go`, `bonuses/legacy.go`; `subscriptions/trial.go`; `backend/db/migrations/00043_legacy_migration.sql`.

**Interfaces:** Existing public imports gain `...Tx(ctx, pgx.Tx, package, mode)` without commit. New `accounts.ImportLegacyUsersTx(ctx, tx, []LegacyUser)`, `vpn.ImportLegacyServersTx(ctx, tx, []LegacyServer)`, `payments.ImportLegacyStarsTx(ctx, tx, []LegacyStarsUser)`, `bonuses.ImportLegacyTx(ctx, tx, LegacyPackage)`; each returns inserted count/error. `accounts.LegacyTrialStateTx(ctx, tx, account)` returns used/unknown for historical accounts.

- [x] Write focused tests for exact identity/raw snapshot, owner Tx rollback, missing/conflicting refs, immutable inviter, numeric units and unknown guards.
- [x] Run each negative check before implementation; add additive owner Tx methods without weakening old wrappers.
- [x] Add source provenance/recurring/operation ledger in real migration 00043 with retained-history DownTo guard; verify old migrations remain untouched.
- [x] Run affected connected owner tests and module boundary checks.

### Task 2: Complete packet, export and operations CLI

**Files:** `backend/internal/modules/operations/legacy.go`, app composition, cmd/server import routing/validation; `deploy/data-migration/export_legacy.py`; exporter/CLI tests.

**Interfaces:** `operations.LegacyImporter.Import(ctx, actor UUID, LegacyPackage, dryRun bool) (LegacyReport,error)`; full source package section names/types fixed in the spec. CLI reads private operator/DB files and stdin; source JSON stays private.

- [x] Implement strict source-schema/field/type/FK/time/enum validation in private read-only exporter and tests; no default input fallback.
- [x] Write CLI tests for malformed/partial/unlawful input, authorization and later rollback.
- [x] Wire one READ COMMITTED transaction, owner ports, canonical source digest and immutable replay ledger; report explicit unknowns, counts and exact reward/currency totals.
- [x] Run exporter, real CLI and boundary checks; old single-owner CLI tests remain passing.

### Task 3: Populated final rehearsal and delivery

**Files:** `backend/tests/legacy_migration_test.go`, synthetic source fixture utility, runbook and redacted acceptance evidence; CI only as needed to include new check.

- [x] Execute exporter→CLI→PG on nonempty synthetic fixtures; verify IDs inside asserts, sums/status/links/report, repeat/restart/full rollback and role/input errors.
- [x] Add actual current native #51/#52/funded/pending facts through owner operations; prove repeat and backup/restore #45 preserve the complete populated schema.
- [ ] Run independent read-only review, repair concrete findings, then exact-source full Platform and bot/backend/web Image checks once.
- [ ] Verify exact HEAD/base/dependencies/gates/effects, manual merge to v2; close #53/Project Done, remove only owned fixtures and hand off evidence.
