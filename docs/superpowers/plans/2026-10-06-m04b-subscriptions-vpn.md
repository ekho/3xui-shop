# М04б: Subscriptions and VPN Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Завершить владельцев subscriptions/vpn и выполнить панельные чтения вне SQL Tx без потери прежних правил.

**Architecture:** Перенести существующие функции и приватные sqlc repositories. App связывает concrete owners и caller-Tx outcome callback; platform остаётся фасадом. Подготовка command использует session ownership между двумя короткими Tx.

**Tech Stack:** Существующие Go, pgx/sqlc, PostgreSQL, Redis, River, HTTPS; React consumers сохраняются.

**Spec:** `docs/superpowers/specs/2026-10-06-m04b-subscriptions-vpn-design.md`.

## Global Constraints

- Fresh origin/v2 `65560b061438b5ceabb18d10c36502cff24954ff`, branch `feature/m04-subscriptions`, PR в v2, без codex prefix.
- Контракт `2026-10-06-m04b-subscriptions-vpn-v1`, owner #58; архитектура `2026-10-05-modular-monolith-v1`.
- Native: координатор, один свежий Astra/high whole-branch reviewer; документы/реализация/merge автономны.
- Схема/API/миграции/dependencies/job kinds/UUID/assignment/keys/hash/result/target JSON сохраняются; поля/int64/nil/empty не теряются.
- Own Docker **3X-UI 3.7.0**, TLS Mailpit, PG/Redis и simulated Telegram; production/Happ/system trust/real payments исключены.
- Conventional Commits, `Co-Authored-By: Codex <noreply@openai.com>`; PR5 afaeacf сохраняется до полного М04, затем С10 → М05 → М06.

## Review Focus

1. Legacy idempotency/callback field-order, omission и nested result drift — Task1 frozen owner fixtures и все replay regressions.
2. VPN outcome callback отсутствует/падает после readback — Task1 DB rollback test: нет applied/granted/assignment без успешного caller-Tx callback.
3. Operator revoked/account identity или trial queue изменились во время panel read — Task2 pause/read race: нет новой операции/job; финальные права и unresolved guards повторяются.
4. Original physical owner session умерла — Task1 owner-loss check и Task2 command check запрещают final enqueue с replacement connection.
5. Monthly период/zone/ban изменились либо reset reply потерян — Task2 repeated period guards плюс прежние elapsed/reset acknowledgement/ban-reapply tests.

---

### Task 1: Передать данные и исполнение владельцам

**Files:**
- Create: `backend/internal/modules/subscriptions/{service,contracts,trial,operator,access_operations,subscription,access_subscription,shared}.go`, private `internal/{queries,store}`.
- Create/modify: `backend/internal/modules/vpn/{service,data,provision,access_worker,monthly_reset,access,owner}.go`, private `internal/{queries,store}`; прежний panel.go.
- Modify: `backend/internal/modules/accounts/contracts.go`, `backend/sqlc.yaml`, `backend/db/queries/{trials,provisioning,access_operations,operators}.sql`.
- Modify: `backend/internal/platform/{service,trial,provision,access_operations,access_worker,monthly_reset,subscription,operator,telegram,accounts}.go`, `backend/internal/app/{accounts,trial_bridge}.go`, `backend/cmd/server/main.go`.
- Tests: `backend/internal/app/boundaries_test.go`, `backend/internal/modules/subscriptions/contracts_test.go`, `backend/internal/modules/vpn/data_test.go`; прежние platform/app/httpapi/tests.

**Interfaces:**
- Consumes: accounts public Snapshot/Lookup/Lock/LockOperatorPair/RequireSupportOperator/AssignPanel/SetAccessMetadata; catalogue CurrentPlanTx/UnlimitedPlansTx; vpn PanelClient/targets М04а.
- Produces: `vpn.New(pool *pgxpool.Pool, authority *accounts.Service, queue func() *river.Client[pgx.Tx], config func() Settings, now func() time.Time, outcome func(context.Context,pgx.Tx,uuid.UUID,uuid.UUID,string) error) *Service`.
- Produces: `subscriptions.New(pool *pgxpool.Pool, authority *accounts.Service, catalogue *catalogue.Service, vpn *vpn.Service, config func() Config, now func() time.Time) *Service`.
- Produces: `vpn.ReserveTrialTx(ctx,tx,TrialReservation) error`, `TrialStateTx(ctx,tx,id) (TrialState,error)`, `AccountTrialState(ctx,account) (TrialState,error)`, `UnresolvedTx(ctx,tx,account) (bool,error)`, `RequeueTrialTx(ctx,tx,id) error`, `QueueAccessTx(ctx,tx,AccessWrite) (AccessState,error)`, `LatestAccessState/LatestAppliedAccessState`, `RequeueAccessTx` and batch metadata. Public snapshots omit lease fields.
- Produces: `subscriptions.RecordTrialOutcomeTx(ctx,tx,request,operation,status) error`, trial/access/profile/history/CardTx operations with neutral DTOs. Existing signatures/names retained at platform facade.
- Produces: `vpn.OpenAccessOwner(ctx,account) (*AccessOwner,error)`, `(*AccessOwner).Begin(ctx) (pgx.Tx,error)`, `TryLock(ctx) error`, `Release()`; same physical session, ErrBusy, no replacement. VPN read-profile/observation methods own panel/SQL.

- [ ] **Step 1: Write boundary and frozen owner tests.** New subscriptions/vpn ownership checks scan real SQL/AST using existing boundary helper; fixtures prove input/result JSON and int64 `9007199254740993`, null/omitempty/nil/empty and old callback/idempotency replay. Owner-loss test terminates its own PG session and forbids a successful second Begin; outcome failure proves DB rollback.
- [ ] **Step 2: RED.** Run focused new boundary/owner tests through run-check. Expected: FAIL because table queries are outside owners and new public owner types/methods do not exist.
- [ ] **Step 3: Move existing implementations and wire concrete modules.** Private sqlc repositories split by owned tables, no schema migration. Preserve SQL guard ordering and actor/account lock order; reserve+grant+decision+job and final outcome remain caller-Tx atomic. Keep audit/delivery transition explicit. Public neutral DTO/error mapping preserves persisted JSON. Command network ordering still matches baseline until Task2.
- [ ] **Step 4: GREEN and early vertical regression.** Run generation, module/boundary checks, focused trial/decision/provision/access/profile/monthly/app regressions with race. Expected: PASS; one grant/job, old targets/keys and errors remain; generated files match.
- [ ] **Step 5: Commit and task-done.** Conventional commit plus CoAuthor. Task-done runs `go -C backend test -race ./... -count=1` against own PG/Redis file-secret environment. Expected: PASS every package; record exact revision, commands/results and all rulings.

### Task 2: Закрыть SQL Tx до панельного чтения и выполнить полную приёмку

**Files:**
- Modify: `backend/internal/modules/subscriptions/access_operations.go`, `backend/internal/modules/vpn/{owner,monthly_reset,access}.go`.
- Tests: `backend/internal/platform/access_preparation_test.go`; existing monthly/access/ownership tests.
- Evidence: `docs/evidence/m04b-acceptance.md`, `docs/evidence/m04a-acceptance.md` delivery section.

**Interfaces:**
- Consumes: Task1 owners, neutral state/targets, caller-Tx write methods and physical AccessOwner.
- Produces: `vpn.AccessBaselineTx(ctx,tx,account) (AccessBaseline,error)` с прежними trial/latest-applied metadata; existing catalogue Tx ports unchanged. CreateAccessOperation and ApplyMonthlyReset preserve public behavior with operation/catalogue snapshots in first Tx, short final Tx and network phase between them.

- [ ] **Step 1: Write TLS/PG preparation regressions before transaction changes.** During access/monthly HTTP reads, query the isolated DB: `pg_stat_activity` must have zero other sessions with `state='idle in transaction'` and nonnull xact_start. Pause HTTP while own connection revokes actor/changes identity/queues competing trial; final operation/job must not appear. Terminate the owner session during read and assert no replacement enqueue. Saved replay must succeed with panel unavailable and zero panel calls.
- [ ] **Step 2: RED.** Run `go -C backend test -race ./internal/platform -run 'TestAccessPreparation|TestMonthlyPreparation' -count=1`. Expected: FAIL on held Tx and blocked concurrency; use bounded handler deadlines so failure cannot hang the suite.
- [ ] **Step 3: Implement two short Tx phases on retained session ownership.** Open one dedicated session; initial auth/idem/replay/unresolved guards, TryLock and operation/catalogue snapshots use its first Tx. Close Tx and prepare using snapshots; final same-session Tx repeats actor/account fingerprint, idempotency, unresolved trial/access, used catalogue revision/archive and monthly period/eligibility. Persist operation+job+audit+idem atomically. Reuse old network/parser methods; no new retry/reset behavior or second pool connection during preparation.
- [ ] **Step 4: GREEN.** Same focused preparation command and affected access/monthly/ownership race regressions. Expected: PASS; panel sees no held SQL Tx, original session loss and concurrent state changes do not enqueue/write.
- [ ] **Step 5: Commit and task-done full matrix once.** Run names; backend/web generation and no drift; vet; web typecheck/build/runtime config; `RUN_BROWSER_TESTS=1 go -C backend test -race ./... -count=1`; 105 Python tests; full Playwright; Compose config/build backend/gateway/bot; HTTPS smoke; local.py up/check/down with 3X-UI3.7.0/TLS Mailpit/simulated Telegram and physical stop/start. Expected: all exit0; preserve logs privately and sanitized results in evidence.

Final: one fresh Astra/high review of full base..HEAD with ledger/Review Focus; resolve every declined point, one RED→GREEN fix pass if needed, export rulings/evidence before own scratch cleanup. Create/attach PR, exact-source CI, authorized merge v2 and verify actual parents/tree + multiarch images/preliminary Release. Then close #58/Project Done and adapt preserved PR5, with its own checks/review.
