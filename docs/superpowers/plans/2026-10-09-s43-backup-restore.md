# С43 backup/restore Implementation Plan

> **For agentic workers:** Execute in this chat with one bounded backend specialist and one fresh whole-branch reviewer; no second coordinator pipeline. Steps use checkbox syntax.

**Goal:** Защищённая согласованная копия PostgreSQL со всеми сохранёнными вложениями и доказанная безопасная restore rehearsal.

**Architecture:** CLI в существующем server, public operations owner; accounts/support/audit_reports public ports. Exported PG snapshot для dump и inventories. Rehearse только в новой изолированной БД, без worker startup.

**Tech Stack:** Go 1.27.1, pgx, PostgreSQL pg_dump/pg_restore, stdlib JSON/SHA-256. Новых зависимостей нет.

**Spec:** `docs/superpowers/specs/2026-10-09-s43-backup-restore-design.md`

## Global Constraints

- Один Go monolith, один go.mod; module private SQL вызывается только owner.
- Без миграции/OpenAPI/UI и без реализации #44/#46.
- Только собственные synthetic данные/отдельные DB/Compose/сеть/loopback ports.
- Backup 0700/0600, same OS owner, trusted dump; DSN/bytes не попадают в logs.
- Merge/closure/архив выполняет головной чат; PR base v2, без auto-merge.

## Review Focus

- Snapshot lifetime/race: concurrent committed attachment write не смешивается с DB snapshot.
- Package tampering: bad hash/size, unknown fields и symlink не достигают pg_restore.
- Existing destination: retries не перезаписывают DB/каталог даже после failed restore.
- Compatibility: другая схема/PG major и отсутствующие attachment bytes fail closed.
- Authority/effects: роль перепроверяется, а restore не запускает живые workers.

### Task 1: Operations + CLI + focused tests

**Files:** create `backend/internal/modules/operations/{backup,manifest,inventory}.go`
and focused tests; create `backend/cmd/server/backup.go` and CLI tests; small
dispatch addition in `backend/cmd/server/main.go`; public snapshot inventory in
`backend/internal/modules/support/backup.go`; schema fingerprint helper in `backend/db`.
Utility packaging in `backend/Dockerfile` only if needed to run the command with
PostgreSQL client tools; default server remains the existing scratch image.

**Interfaces:** CLI consumes DATABASE_URL_FILE, RESTORE_DATABASE_URL_FILE and
private operator UUID file. Public `operations.Create(ctx, actor, directory)` and
`operations.Rehearse(ctx, actor, directory, targetDatabase)` expose the two paths.
Support inventory consumes caller `pgx.Tx`; db schema helper owns migrations.

- [x] Add tests for AC1–AC5 and observe RED for the missing capability.
- [x] Implement minimal snapshot, private package, inventories, authorization/audit and restore path.
- [x] Run `go -C backend test ./internal/modules/operations ./cmd/server ./internal/modules/support ./db -count=1`, then `go -C backend vet ./...` and generation check. Expected PASS with own fixture configured.
- [ ] Coordinator inspects scope/diff and commits verified result with Conventional Commits + Co-Authored-By; push SSH feature/s43-backup-restore.

### Task 2: Local operational acceptance and delivery

**Files:** create `deploy/acceptance/backup_restore.py` with isolated disposable
fixture; `docs/runbooks/s43-backup-restore.md`; `docs/evidence/s43-backup-restore-acceptance.md`.
No source overlap with Task 1.

**Interfaces:** invokes built server backup commands from Task 1. Uses private
synthetic fixture files, PostgreSQL/Redis test secrets, and unique Compose project.

- [x] Run real backup/rehearsal and assert linked data, all bytes/hash/manifest and audit; probe corrupt/incomplete packages, foreign/revoked roles, repeats, missing remote media and schema mismatch.
- [ ] Run full local scopes feasible here: Go/race/vet, generated drift, web/typecheck/build/e2e, Python and isolated native checks; separate any unrun scope from PASS.
- [x] Record exact commands, counts, isolation/cleanup and external limits in runbook/evidence.
- [x] Fresh whole-branch reviewer Astra/high against spec/plan/diff/evidence; fix material findings and rerun affected checks.
- [ ] Commit/push final changes; create/attach PR base v2; wait all current HEAD CI, resolve bounded failures, mark ready.
- [ ] Return PR URL, exact SHA, all checks and limitations. Do not merge or close/archive.
