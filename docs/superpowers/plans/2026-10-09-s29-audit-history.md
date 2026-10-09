# С29 — Operator Audit History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Chosen method: **Native**, preserving the user's autonomous document/implementation mandate; one fresh whole-branch review at the end.

**Goal:** Дать оператору полный просмотр подтверждённых audit-метаданных и завершить локальный legacy import/retention/optional General mirror.

**Architecture:** Существующий `audit_reports` владеет журналом и своими SQL; accounts предоставляет caller-Tx права и неизменяемые legacy identity proofs. Новый HTTP consumer и один React-admin компонент сохраняют прежние DTO; scheduler и Telegram transport работают в одном Go-процессе.

**Tech Stack:** Go/Echo/pgx/sqlc/River, PostgreSQL/Redis, React/React-admin/TypeScript/Playwright; без новых зависимостей.

**Spec:** [2026-10-09-s29-audit-history-design.md](../specs/2026-10-09-s29-audit-history-design.md).

## Global Constraints

- Владелец #39/`audit_reports`, версия `2026-10-09-s29-audit-history-v1`; monolith `2026-10-05-modular-monolith-v1`.
- База `origin/v2`, PR → `v2`, без `codex/`; Conventional Commits, `Refs #39`, `Co-Authored-By: Codex <noreply@openai.com>`.
- Owned local fixtures, TLS SMTP, 3X-UI **3.7.0**; no production, real wallet/bot/SMTP, live Happ/trust changes.
- Старые DTO/NULL/IDs/hashes/финансы/access сохраняются; TG/source ID — точный decimal signed int64, UUID не выводится из текущей TG-привязки.
- Page 50 + 1, descending tuple cursor; `audit-history-v1`, kind `native|legacy|system`; web session/Origin/CSRF/current operator.
- CLI version 1/stdin ≤32 MiB, payload JSON object ≤64 KiB; атомарный apply, read-only dry-run, digest ledger переживает prune.
- Retention default365,1..3650; timezone `AUDIT_RETENTION_TIMEZONE`, fallback `BOT_TIMEZONE`, затем UTC; после03:30, DB clock, одна транзакция/receipt за локальный день.
- `AUDIT_MIRROR_ENABLED=false`, `SUPPORT_BOT_TOKEN_FILE`, negative `SUPPORT_GROUP_ID`; metadata only, mark-before-wire, old pending muted at startup, one attempt/sec/no retry.
- Native execution: per-task RED→GREEN, one Astra/high final reviewer, one Critical/Important author correction pass, minors deferred; exact-source CI/manual merge/prerelease/OCI gates.

## Review Focus

1. Legacy history после unlink/rebind не должна переходить к новому владельцу TG ID — Task1 `TestAuditHistoryLegacyIdentity`.
2. Replay пакета после retention не должен воскрешать private payload — Task1 `TestAuditRetentionReplay`.
3. Отзыв роли при чтении и MaxConns=1 должны отказать без зависания/второго borrow — Task1 `TestAuditHistoryRightsAndSingleConnection`.
4. Late response старого account/filter/lang не должна подменять текущий журнал или раскрывать payload — Task2 `audit-history.spec.ts`.
5. Потерянный ACK/рестарт/429 не должны повторять General mirror или отправлять private reason/body — Task3 `TestNativeTrialAudit`.

---

### Task 1: Owned journal, import, retention and Telegram mirror

**Files:**
- Create `backend/db/migrations/00033_audit_history.sql`; own history/ledger/system tables, mirror marker, indexes, guarded downgrade.
- Modify `backend/internal/modules/audit_reports/audit.go`; create `history.go`, `legacy.go`, `retention.go`, `internal/queries/history.sql`.
- Create `backend/internal/modules/accounts/audit_history.go`; modify own `internal/queries/restrictions.sql` for immutable legacy batch proof.
- Create `backend/internal/httpapi/audit_history.go`; modify `api.go`, owning `docs/api/openapi.yaml`; regenerate existing Go wire/sqlc/web schema.
- Create `backend/internal/modules/telegram/audit_mirror.go`; modify own `internal/botapi/client.go` for separate General send, never relax private-chat guards.
- Modify `backend/internal/app/config.go`, `modules.go`, `backend/cmd/server/main.go`, `import.go` for config/CLI/same-process scheduler; create `deploy/audit/README.md`.
- Tests: `backend/internal/httpapi/audit_history_test.go`, `backend/internal/modules/audit_reports/retention_test.go`, `backend/cmd/server/import_audit_test.go`, `backend/db/audit_history_migration_test.go`; extend existing Bot API/config/boundaries tests.

**Interfaces:**
- Consumes existing `accounts.LookupTx`, `LegacyApproval`, `LockNoticeOperatorTx`, `RecordTx`, `StatisticsPorts`, strict `decodeLegacyJSON[T]`, `operatorAuth`/`operatorAudit`, private `botapi.Client.call`.
- Accounts produces `LegacyAuditTargetTx(ctx, tx, account UUID) (*int64,error)` and `LegacyAuditLinksTx(ctx, tx, targets []int64) (map[int64]uuid.UUID,error)`, using only immutable source snapshots.
- Audit produces `HistoryInput{Kind,AccountID,LegacyTargetTgID,BeforeCreatedAt,BeforeID,BeforeSourceID}` and `History(ctx, actor UUID, in HistoryInput) (HistoryPage,error)`; page has version/kind/filter, `Native []Event`, `Legacy []LegacyEvent`, `System []SystemEvent`, `HasMore`.
- Audit consumes `HistoryPorts{LockOperatorTx,AccountExistsTx,LegacyTargetTx,LegacyLinksTx}`. `New(pool, statistics, history, Config{RetentionDays,Timezone})` assembled at the sole `app.NewModules` call.
- Audit produces `LegacyAuditPackage{Version,Events}`, `ImportLegacy(ctx, package, dryRun) (LegacyAuditImportResult,error)`, `Prune(ctx) (SystemEvent,bool,error)` and `RunScheduler(ctx, mirror func(context.Context,string)error) error`.
- Telegram produces `LoadAuditMirrorConfig() (AuditMirrorConfig,error)` and `NewAuditMirror(cfg, *http.Client) (func(context.Context,string)error,error)`; disabled returns nil without reading a secret.

- [ ] **Step 1: Write focused failing tests** for HTTP reading/actor-target/references/same-time 50+1/rights/invalid cursors/private payload, immutable legacy mapping, CLI replay/conflict/rollback and retention boundaries/foreign-data preservation. Initial HTTP assertion: authorized `POST /api/v1/operator/audit/history {kind:native}` returns200/version and exact current Event fields; source with no actor remains unknown.
- [ ] **Step 2: Run RED** `go test ./internal/httpapi -run '^TestAuditHistory' -count=1` in backend with owned test URL files. Expected: missing route returns404 for the required200, without broken environment. Add migration/import/scheduler tests before their corresponding implementations; observe their concrete RED separately.
- [ ] **Step 3: Implement the additive contract and owners** before consumers; reuse old metadata mapping and existing auth/decoder/client helpers. Preserve old OpenAPI definitions text/semantics. Own normal caller Tx performs actor guard and reads; no private module SQL outside its owner. Safe validation/errors, import digest ledger, atomic DB-clock prune receipt, mark-before-wire mirror and one-process lifecycle as spec. Run the repository generators; inspect their exact diff.
- [ ] **Step 4: Run focused GREEN** `go test -json -race ./internal/httpapi ./internal/modules/audit_reports ./internal/modules/telegram ./internal/modules/telegram/internal/botapi ./internal/app ./cmd/server ./db -run 'TestAudit|TestLegacyAudit|TestDecodeLegacyAudit|TestAuditMirror|TestAuditReports|TestModule|Test.*Boundary|TestNativeLifecycle' -count=1 -timeout=5m`. Expected: all named owned tests pass,0 failures/test skips; existing audit persisted compatibility and boundaries remain green. Verify generation/vet plus protected empty/nonempty migration behavior; preserve complete private logs and exact frozen input hashes.
- [ ] **Step 5: Commit** only owned source/tests/generated/config/docs with `feat(audit): add operator journal and retention`. Run task-done with an owned proof verifier that compares current frozen bytes, exact successful focused logs and generation/static results, avoiding an unchanged costly rerun. Expected: task completion only, final review/delivery still pending.

### Task 2: Common operator journal UI

**Files:** create `web/src/AdminAudit.tsx`, `web/tests/audit-history.spec.ts`; modify `web/src/Admin.tsx`, `web/src/api/client.ts`, `web/src/i18n.ts`, `web/playwright.config.ts`; generated `web/src/api/schema.gen.ts` stays generator-owned.

**Interfaces:**
- Consumes Task1 `readOperatorAuditHistory` and generated DTOs, existing `request`, RU/EN/error handling, operator session/forbidden mechanism.
- Produces `getOperatorAuditHistory(input: AuditHistoryInput, signal?: AbortSignal): Promise<AuditHistory>` and `AdminAudit({lang,accountId?,onForbidden})` for global Resource and keyed client card.

- [ ] **Step 1: Write rendered tests** for all three streams/target filter, UUID client link, original int64 IDs, unknown actor/missing reason, references, previous/next cursor, keyboard/labels/RU/EN, empty/loading/error/retry and forbidden clearing. Delay old account/filter/lang response and verify it never replaces current view; inject private payload/HTML into fixture and assert no body/DOM execution/network.
- [ ] **Step 2: Run RED** `npx playwright test tests/audit-history.spec.ts --reporter=line` in web. Expected: missing journal UI/component, meaningful failed assertions; register the file in existing explicit `testMatch` before running.
- [ ] **Step 3: Implement** one `AdminAudit` with current request abort/scoped result/cursor stack, semantic text and strict response version/filter checks. Mount global journal in React-admin and same keyed component in client card; use existing payment/access/support screens for details. Keep previous client history and endpoint consumers unchanged.
- [ ] **Step 4: Run GREEN** the same Playwright command plus `npm run typecheck`, `npm run build`, `node scripts/runtime-config.test.mjs`. Expected: all owned cases pass,0fail; production build/runtime config works. Freeze actual web inputs/results and retain failed RED evidence.
- [ ] **Step 5: Commit** `feat(web): add operator audit journal`; task-done verifies the exact frozen proof. Expected: independently usable HTTP/UI journal; review/delivery pending.

### Task 3: Compiled local acceptance, whole branch and v2 delivery

**Files:** create `backend/tests/native_audit_test.go`, `docs/superpowers/evidence/2026-10-09-s29-audit-history.md`; extend owned existing `backend/tests/native_notices_test.go` binary helper only for bounded child settings reuse. Existing native selector automatically includes `TestNativeTrialAudit`; change runner only if an observed requirement needs it.

**Interfaces:**
- Consumes Task1/2, `nativeNoticeBinary`, current isolated fixture, strict loopback Bot API TLS/CONNECT and TLS SMTP/3X-UI3.7.0 Docker proof.
- Produces `TestNativeTrialAudit`, exact current whole/native/compatibility evidence and source/merge/release/OCI readback; no external financial/provider action.

- [ ] **Step 1: Add compiled acceptance**: fresh cmd/server serves real HTTP/operator UI and audit scheduler; strict fake support Bot API verifies negative group/no thread/plain metadata, response loss/429/one attempt, post-commit/rollback and restart. Compiled import CLI dry-run/apply/replay/conflict; seed retention-owned old private history, observe daily receipt and repeat restart; financial/access/support/identity snapshots unchanged. Test late UI responses and privacy through actual browser consumer. Expected: meaningful native assertions, no real Telegram/panel writes or exported private body.
- [ ] **Step 2: Run native and focused acceptance**, retain actual binary hash/logs/counts and observed fail/changed-input fixes; `LOCAL_PROFILE=native python3 deploy/acceptance/local.py up` then `LOCAL_PROFILE=native python3 deploy/acceptance/local.py check` with own native state. Expected: current full native selection includes browser cases,0testskip, actual trial/reminder/audit restarts. Do not infer a hardcoded native count.
- [ ] **Step 3: Run current whole checks once** after final product changes: generators/vet, `RUN_BROWSER_TESTS=1 go test -json -race ./... -count=1 -timeout=20m`, Python whole consumer suite, web whole Playwright/types/build/runtime, three local images/smokes/native Docker. Expected: all0fail/testskip; exact manifest and complete private logs match current source. Reuse still-identical earlier proofs only with byte equality; record a changed input before expensive repeats.
- [ ] **Step 4: Record local evidence and commit** `test(audit): verify native journal import and retention`; task-done verifies exact proof rather than rerunning unchanged suites. Then one fresh Astra/high whole-branch reviewer reads base..HEAD/plan/spec/five Review Focus/rulings; root grades and performs ONE Critical/Important RED→GREEN correction pass plus full current checks. Expected: no unresolved Critical/Important; minors ledgered; no second review.
- [ ] **Step 5: Deliver** guarded SSH push and PR→v2, attach PR/milestone1; exact-source CI and fresh source/target/dependency/parent/mandate checks; manual merge without auto-merge. Verify actual parents/tree, annotated v2 prerelease and three OCI indices/6platform labels, close #39/Project Done with current checkpoint. Archive/delete only own SDD after final fix commit; exhaustive rulings retained for final report. Then fresh origin/v2 for С37/#40. Expected: local/implementation/delivery complete, production and С13 external acceptance explicitly unproved.
