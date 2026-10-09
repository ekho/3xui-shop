# С39 — Server Pool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Native уже выбран пользователем; один fresh whole-branch review после всех задач.

**Goal:** Распределять первые выдачи и обслуживать все существующие операции на назначенном сервере.

**Architecture:** `vpn` владеет PostgreSQL реестром, резервациями и конкретными `PanelClient`. Первые targets выбираются под короткой lock пула; provider calls остаются вне неё. Назначение и ключи сохраняются при outage, повторе и рестарте.

**Tech Stack:** существующие Go/pgx/sqlc/River, TLS PanelClient и PostgreSQL; Docker 3X-UI3.7.0, Mailpit и контролируемые Bot API fixtures. Новых зависимостей нет.

**Spec:** `docs/superpowers/specs/2026-10-09-s39-server-pool-design.md` (`2026-10-09-s39-server-pool-v1`, owner#41).

## Global Constraints

- Один Go-процесс HTTP/River/Telegram, один go.mod; Python удаляется только с С47.
- 3X-UI **3.7.0**; HTTPS/CA проверяются; credentials остаются file-only и общими для пула.
- SQL/private imports только своего владельца; `accounts` даёт публичный caller-Tx read загрузки.
- Assigned/target ID не заменяется при outage; UUID/panel_key/sub_id и денежные/source/actor guards сохраняются.
- Soft cap: сначала минимум ниже лимита, иначе минимум всего доступного пула; стабильный ID разрешает равенство.
- NULL capacity только для прежнего первого deployment; явный legacy-compatible ноль — полный сервер.
- Пустой panel_id разрешён только новой trial operation до первого выбора; старые непустые targets не переписываются.
- Резервация/операция атомарны; применение назначения/освобождение резервации атомарны; нет SQL lock пула через сеть.
- Blind create retry без подтверждённого duplicate guard запрещён.
- Owned local fixtures only; production/реальные платежи/живой Telegram/внешний SMTP/живой Happ/macOS trust исключены.
- Ветки от актуальной origin/v2 без codex prefix; PR→v2; Conventional Commits, `Refs #41`, Co-Authored-By.
- Один fresh Astra/high reviewer, один авторский проход; новые подтверждения документов не запрашиваются по принятой автономии.

## Review Focus

1. Server ID/host reuse после tombstone или изменения startup config: старый target не попадает в другую панель — Task1 stale/reuse tests.
2. Одинаковый load при параллельных выдачах и refund до первой записи: реальные резервации учитываются и безопасно освобождаются — Tasks2/3 concurrency/refund tests.
3. Target старого single-panel deployment и сбой назначенного вторичного сервера: данные/keys не переносятся — Tasks2/3 legacy/outage tests.
4. Запоздалый successful probe, disabled subscription и IPv6 URI: online/base не становятся ложными — Task1 discovery tests.
5. Частично недоступный multi-panel report/reminder и родственная чужая identity: только свои подтверждённые факты — Task4 partition/identity tests.

## Verification contract

Команды Go выполняются с существующими `TEST_DATABASE_URL_FILE` и `TEST_REDIS_URL_FILE`,
без печати их содержимого. Шумные/долгие команды запускаются через
`node /Users/ekho/.codex/plugins/cache/tradeos/tradeos-ai-engineering-kit/2.0.0/scripts/run-check.mjs --timeout-seconds <budget> --cwd <repo> -- <argv>`.
Полные результаты и logs читаются и сохраняются в собственной private acceptance.
RED имеет ожидаемую причину отсутствующего поведения; wrapper timeout не является RED.
После каждого завершённого этапа — scoped commit/push и ledger; перед финальным review
нужны все suites на текущих runtime/test inputs. Не повторять дорогой успешный запуск
при изменении только evidence prose; exact input manifests позволяют сохранить доказательство.

### Task 1: Реестр, синхронизация и базовые ссылки

**Files:**
- Create: `backend/db/migrations/00035_server_pool.sql`, `backend/internal/modules/vpn/server_pool.go`, `backend/internal/modules/vpn/panel_subscription.go`, `backend/internal/modules/vpn/internal/queries/servers.sql`, `backend/internal/modules/vpn/server_pool_test.go`.
- Modify: `backend/internal/modules/vpn/service.go`, `backend/internal/modules/accounts/data.go`, `backend/internal/modules/accounts/internal/queries/shared.sql`, `backend/internal/app/modules.go`; sqlc output.

**Interfaces:**
- Consumes: accounts public Snapshot/Lock and VPN Settings/Config/PanelClient; owner SQL stores.
- Produces: `Server` (ID/Name/Host/SubscriptionBaseURL, MaxClients *int64, Online/Retired, Revision, ObservedAt *time.Time, AssignedClients/ReservedClients int64), `ServerInput` (ID/Name/Host string, MaxClients int64).
- Produces: `RegisterServerTx(context.Context, pgx.Tx, ServerInput) (Server,error)`, `ServersTx(context.Context,pgx.Tx) ([]Server,error)`, `ServerTx(context.Context,pgx.Tx,string) (Server,error)`, `SyncServers(context.Context) error`, `PanelFor(context.Context,string) (*PanelClient,error)`, `SubscriptionBase(context.Context,string) (string,error)`, `AvailableServer(context.Context) (Server,error)`.
- Produces: accounts `PanelLoadsTx(context.Context,pgx.Tx) (map[string]int64,error)` and VPN Settings `SubscriptionBaseURL string`, wired from existing subscriptions config.
- Trusted registry ports do not expose a new HTTP endpoint; C38 owns infrastructure authorization/audit. Query-only consumers must not seed or mutate registry.

- [ ] **Step 1: Write failing `TestServerPoolRegistry`, `TestServerPoolSelection`, `TestPanelSubscriptionBase` and `TestServerPoolSyncRevision`.**
  Real PostgreSQL + two TLS httptest panels. Assert registry validation/name/host/ID uniqueness/tombstone, assigned and reserved literal loads, NULL/zero/soft-full selection, stable ties, all offline with no target/write, revision/observation ordering. URI assertions use literal HTTPS outputs for subURI/fallback/IPv6; malformed/disabled/plaintext never fills blank base; existing explicit base unchanged. Down succeeds without retained facts and rejects retained facts.
- [ ] **Step 2: Run `go -C backend test -race ./internal/modules/vpn -run 'TestServerPool|TestPanelSubscriptionBase' -count=1`.**
  Expected: RED for missing registry/selection/discovery behavior; retain exact output. Do not accept a missing database as RED.
- [ ] **Step 3: Implement owner tables/queries/ports and discovery.**
  Registry IDs/hosts immutable, tombstones retained, short xact lock `vpn-server-pool`; online updates compare revision and observation start. Sync lazily seeds the configured primary without overwriting facts; query-only fallback represents pre-registry single-panel config. Use existing bounded authentication/inbound read and settings POST. Expose no credentials/raw responses. `AvailableServer` does not reserve an unpaid checkout.
- [ ] **Step 4: Run Step2 GREEN, `make -C backend generate`, `go -C backend vet ./...`, `git diff --check`.**
  Expected: all selected tests PASS, no test skips, generated/static clean. Read affected generated diff and all meaningful logs.
- [ ] **Step 5: Commit/push `feat(vpn): persist server pool and subscription discovery`, record Rulings and task completion.**

### Task 2: Ранний вертикальный trial slice и устойчивый выбор

**Files:**
- Modify: `backend/internal/modules/subscriptions/trial.go`, `backend/internal/modules/vpn/provision.go`, `backend/internal/modules/vpn/data.go`, `backend/internal/modules/vpn/profile.go`, `backend/internal/modules/subscriptions/subscription.go`, `backend/internal/modules/vpn/internal/queries/provisioning.sql`; sqlc output.
- Create: `backend/internal/httpapi/server_pool_trial_test.go`; extend existing `regression_provision_test.go` fixtures only when required for truthful provider responses.

**Interfaces:**
- Consumes: Task1 registry, selection, concrete panel/base and accounts loads.
- Produces: durable trial binding under the existing account-access owner before lease, `ReserveInitialAccessTx(context.Context,pgx.Tx,uuid.UUID,uuid.UUID,string) error` for later access creators, `ReleaseServerReservationTx(context.Context,pgx.Tx,uuid.UUID,string) error` for confirmed application; mismatch returns safe `ErrBusy`, no candidate `ErrPanel`.
- Trial binding owns its short transaction and ties a reservation to its existing trial operation. Access reservation is inserted with its access operation in caller Tx; the selected server is rechecked against current load before commit.

- [ ] **Step 1: Write failing `TestServerPoolTrialIssuance`, `TestServerPoolConcurrentTrials`, `TestServerPoolUnavailableTrial` and `TestServerPoolTrialLegacyAndRestart`.**
  Real approval/worker/HTTP paths, two TLS panels, literal assignments and matching key base. Prove concurrent reservations change next choice, no cross-panel writes, same operation/grant/keys after rebuilding composition, old bound operation keeps its ID. All offline preserves approval/grant reservation, blank panel/first_started_at and zero panel write attempts; later availability issues once. Unknown/retired assigned server fails closed.
- [ ] **Step 2: Run `go -C backend test -race ./internal/httpapi -run 'TestServerPool.*Trial|TestServerPoolConcurrentTrials' -count=1`.**
  Expected: RED because approval/provision/key still use the configured single panel.
- [ ] **Step 3: Implement late trial binding, specific panel reads/write and key base.**
  New approvals pass empty panel ID; missing primary configuration validation stays. No candidate snoozes before lease/period start. Existing nonempty target never reselects. Apply assignment and release reservation in the same Tx; preserve ambiguous target/duplicate guard behavior. Source/identity changes cannot cause writes or remapping.
- [ ] **Step 4: Run Step2 plus `TestRegressionProvision|TestTelegramTrial` GREEN.**
  Expected: early complete approval→provision→HTTP subscription/key slice PASS and original single-panel guards PASS, no skips. Publish that exact slice checkpoint before Task3.
- [ ] **Step 5: Commit/push `feat(vpn): allocate trials durably across servers`, complete task ledger.**

### Task 3: Все операции используют назначение

**Files:**
- Modify: `backend/internal/modules/payments/purchase.go`, `purchase_worker.go`; `backend/internal/modules/subscriptions/access_operations.go`, `renewal.go`, `access_subscription.go`, `service.go`, `operator.go`; `backend/internal/modules/vpn/access_worker.go`, `access_read.go`, `access_profile.go`, `monthly_reset.go`, `owner.go`; `backend/internal/httpapi/operator_mapping.go`.
- Create: `backend/internal/httpapi/server_pool_access_test.go` and `server_pool_purchase_test.go`; reuse existing refund/renewal/profile/monthly regression fixtures.

**Interfaces:**
- Consumes: Task1 concrete panel/base and Task2 reservation/release ports.
- Produces: unchanged public API DTOs/idempotency with actual assigned server; all new access targets bind the validated server, payment/renewal provenance stays with existing owners.

- [ ] **Step 1: Write failing `TestServerPoolPaidFulfillment`, `TestServerPoolRenewalAndPlanChange`, `TestServerPoolOperatorAccess`, `TestServerPoolMonthlyReset`, `TestServerPoolAssignedOutage` and `TestServerPoolRefundReservation`.**
  Assert second-panel purchase/renew/change/profile/ban/compensation/reset/monthly outcomes and unchanged identities/money. Revoke source/role/funding after preparation: no write. Concurrent provisional selection mismatch never queues on the wrong server. No-client metadata and validation errors do not hold capacity. Refund before a panel write retires queued intent and frees reserve; partial write preserves recovery evidence. Card/key show the actual second server.
- [ ] **Step 2: Run `go -C backend test -race ./internal/httpapi -run 'TestServerPool.*(Paid|Purchase|Renewal|Operator|Monthly|Assigned|Refund)' -count=1`.**
  Expected: RED for global panel factory/ID/base and missing reservation lifecycle.
- [ ] **Step 3: Route every listed consumer through assigned/frozen ID.**
  New checkout chooses only for its read, without reserve. Paid/new operator issuance confirms provisional choice and inserts reservation with final target in the short Tx; changed selection snoozes paid work or returns existing operator conflict. Keep all second-Tx role/source/funding checks and immutable target/readback. Assigned offline never calls another panel. Free reservation only on confirmed application or positively unstarted retired purchase intent.
- [ ] **Step 4: Run Step2 and existing renewal/plan-change/profile/access/monthly/refund integration tests GREEN; generate/static/diff checks.**
  Expected: all relevant cases PASS, unchanged public aliases/JSON and cached responses; no skips or secret-bearing output.
- [ ] **Step 5: Commit/push `feat(vpn): route access and purchases to assigned servers`, complete task ledger.**

### Task 4: Отчёты, reminders и native Docker приёмка

**Files:**
- Modify: `backend/internal/modules/vpn/statistics.go`, `reminders.go`; `deploy/acceptance/compose.native.yml`, `Caddyfile.local`, `local.py`; `.github/workflows/platform-checks.yml`.
- Create: `backend/internal/httpapi/server_pool_reports_test.go`, `backend/tests/native_server_pool_test.go`, `docs/superpowers/evidence/2026-10-09-s39-server-pool.md`.

**Interfaces:**
- Consumes: readonly Task1 server list, same owner report/reminder contracts and Task2/3 assignments.
- Produces: server-partitioned unchanged report/reminder DTOs, actual two-panel native evidence and exact source readiness.

- [ ] **Step 1: Write failing `TestServerPoolStatisticsAndReminders` and `TestNativeTrialServerPool`.**
  Assert two server snapshots with separate own identities, unknown outage and surviving healthy reminder facts; GET/read reports do not change registry/grants/keys/money. Native compiled one-process uses two actual pinned 3.7.0 panels, preserves assignment/keys on restart and fails closed when assigned secondary stops. Fake Bot API and TLS SMTP remain explicit fixtures. No VPN switch.
- [ ] **Step 2: Run report test RED; validate new native test's missing pool path RED against owned native fixtures.**
  Run: `go -C backend test -race ./internal/httpapi -run TestServerPoolStatisticsAndReminders -count=1`.
  Expected: RED for single-server snapshot; native RED has the new behavior as its cause, never absent resources.
- [ ] **Step 3: Implement partitioned reads and declarative second-panel fixture.**
  Collect each actual server independently, use assigned ID for activity/period/traffic, preserve unknown on unsupported 3.7.0 bulk endpoint. Isolate panel failures from other servers and from process lifecycle. Add secondary panel behind the existing loopback TLS gateway and existing test CA, private separate volume and idempotent initialization. Raise Platform job budget 35→60 minutes, preserve every check.
- [ ] **Step 4: Run complete validation once on frozen current inputs.**
  `make -C backend generate`; `npm --prefix web run api:generate`; `go -C backend vet ./...`; `npm --prefix web run typecheck`; `npm --prefix web run build -- --mode test`; runtime-config checks; `RUN_BROWSER_TESTS=1 make -C backend test-integration TEST_TIMEOUT=60m`; `poetry run python -m unittest discover -s tests -v`; `npm --prefix web run test:e2e`; owned image build/config/smoke; `python deploy/acceptance/local.py up --reuse-images`; `python deploy/acceptance/local.py check`; `python deploy/acceptance/check_names.py`; `git diff --check`.
  Expected: no failures/skips, generated results identical after regeneration, all prior/current cases covered; native real panel/TLS/restart proofs. Read complete logs, record actual counts and input/image/log hashes; retain failures honestly.
- [ ] **Step 5: Commit/push acceptance, complete ledger, one final review and delivery.**
  One fresh `gpt-6-astra/high` whole-branch reviewer receives full spec/plan, exact base/head, ledger and Review Focus. Re-grade by user impact, one author fix pass with RED→GREEN and green current suites; no second review. Publish every Ruling/deferred Minor before archiving only this plan's SDD. Create/attach PR→v2, require exact-source full PR CI/images, manual SHA-guarded merge, actual v2 prerelease/annotated tag/three amd64+arm64 images. Close #41/Project Done only after delivery, then start #42 from fresh origin/v2.

## Self-review and execution

Каждая секция спецификации покрыта Tasks1–4; реестр/резервации/потребители образуют
одну связанную границу. Имена портов, Tx ownership и empty/legacy panel semantics
совпадают между задачами. C38 UI и C40 reconciliation не объявляются реализованными.
Все решения записываются в ledger с причиной и ценой ошибки. Действует ранее выбранный
Native; документы и реализация продолжаются автономно в пределах #41.
