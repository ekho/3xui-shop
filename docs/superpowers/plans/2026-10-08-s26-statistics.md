# С26 — статистика: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Native: root реализует все задачи; один свежий gpt-6-astra/high reviewer всей ветки, один авторский проход исправлений, без повторного review.

**Goal:** Дать оператору общий отчёт и такой же отчёт неизменяемой когорты кампании.
**Architecture:** audit_reports объединяет публичные typed ports владельцев в REPEATABLE READ/read-only. VPN читает 3X-UI3.7.0 bulk API; общий React компонент показывает денежные доказательства и неполноту активности.
**Tech Stack:** существующие Go/Echo/pgx/PostgreSQL/River, React/React-admin/TypeScript/Playwright; новых зависимостей нет.
**Spec:** docs/superpowers/specs/2026-10-08-s26-statistics-design.md

## Global Constraints
- Один Go-процесс HTTP/River/Telegram, публичные owner ports, отсутствие foreign SQL/private imports/app domain logic.
- Go checks используют TEST_DATABASE_URL_FILE/TEST_REDIS_URL_FILE из .superpowers/acceptance/c26-statistics; runner передаёт только file paths, без secret values.
- 3X-UI3.7.0; только owned local fixtures/loopback TLS SMTP/Bot API; production/real money/live Happ/Mac VPN/trust исключены.
- branch от origin/v2, PR в v2, Conventional Commits/Co-Authored, manual source-SHA merge, advance mandate #55.
- Старые API/DTO/payments/subscriptions/source IDs/hashes/quotas/persisted intents сохраняются.
- Empty cohort не global; exact money strings и decimal percents; unknown не zero.
- Generated output только make -C backend generate и npm --prefix web run api:generate.
- Native ledger/brief/RED→GREEN/task-done, один final reviewer; Minor defer.

## Review Focus
1. Paid funding после review/refund, extra receipts, currencies/netunknown/legacy и 1/3/zero ratios сохраняют разные смыслы.
2. Malformed/duplicate/oversize bulk panel, missing/negative/overflow traffic и identity/membership drift дают unknown, read ничего не меняет.
3. Empty/nil cohort, missing/paused/deleted campaign и роль отозванная во время panel read не раскрывают чужие показатели.
4. Pending/review/applied NoClientIntent, expired/exhausted/disabled/ban/zero limits и multi-inbound dedup не превращаются в ложную активность.
5. Late response/abort/campaign/language/role change, 375px keyboard и money>2^53 отображаются без stale data и утечки private keys.

### Task 1: Полный защищённый backend отчёт
**Files:** Create backend/internal/modules/audit_reports/statistics.go, backend/internal/modules/accounts/statistics.go, backend/internal/modules/catalogue/statistics.go, backend/internal/modules/vpn/statistics.go, backend/internal/modules/vpn/statistics_panel.go, backend/internal/httpapi/statistics.go, backend/internal/httpapi/statistics_test.go, backend/internal/modules/vpn/statistics_test.go.
Modify backend/internal/modules/audit_reports/audit.go, backend/internal/modules/payments/statistics.go, backend/internal/modules/subscriptions/statistics.go, backend/internal/modules/campaigns/statistics.go (только scoped read/existence), backend/internal/modules/accounts/data.go и backend/internal/modules/accounts/internal/queries/shared.sql, backend/internal/app/modules.go, backend/internal/httpapi/api.go, docs/api/openapi.yaml; owning generated Go/TS/sqlc outputs.
Test: existing backend/internal/httpapi/campaign_statistics_test.go и module boundaries.

**Interfaces:**
Produces auditreports.Service.Statistics(ctx context.Context, actor uuid.UUID, campaign *uuid.UUID) (StatisticsReport,error).
Produces public report DTOs PaymentStatistics/TrialStatistics (старые names — type aliases), StatisticsAccount{ID,AccessProfile,VpnBanned}, VPNStatistics{Activity,Servers,InboundReferences}.
Ports: AccountsTx(ctx,tx) ([]StatisticsAccount,error), ReportCohortTx(ctx,tx,id) ([]uuid.UUID,bool,error), PaymentsTx/TrialsTx(ctx,tx,ids), PlansTx(ctx,tx) (map[string]int64,error), VPNTx(ctx,tx,ids) (VPNStatistics,error), RequireOperator(ctx,id) error.
Produces POST ReadOperatorStatistics, StatisticsReport schema и generated API types; ReportCohortTx возвращает found=false для отсутствующей кампании, не меняет старый CohortTx.
Consumes owner facts/accounts.LookupManyTx(ctx,tx,ids) ([]Snapshot,error), existing PanelClient HTTPS/auth/call/limits, previous StatisticsTx methods.

- [ ] **Step 1:** Написать TestOperatorStatisticsScopeAndAuthority: global count includes operator, empty cohort users0/payments0, missing404, paused/deleted retained, client403/cookie absent401, wrong CSRF/Origin403, malformed UUID/unknown JSON/query400. Расширить TestCampaignStatisticsProofs: same cohort payments/trials/users equal old CampaignDetail, unchanged DB snapshot, exact 18446744073709551614 RUB, USD netunknown1, XTR gross300, USDT refund unchanged.
- [ ] **Step 2:** Запустить go -C backend test ./internal/httpapi -run 'TestOperatorStatistics|TestCampaignStatisticsProofs' -count=1. Expected RED: новый endpoint404 вместо200; старый campaign path по-прежнему работает.
- [ ] **Step 3:** Добавить coherent OpenAPI contract до consumer кода; typed owner callbacks/aliases, read-only snapshot и свежий role guard после snapshot/provider перед ответом. Добавить TestPanelStatisticsSnapshot/TestOperatorStatisticsPanel/RoleRevoked: two bulk GETs, expiry0/quota0/1-of3, pending/review/NoClientIntent, malformed/negative/overflow/duplicate/identity drift/missing traffic/disabled memberships, deadline/oversize/outage, role revoked on held response =>403, no provider/DB writes. RED отдельно на новых public methods/branch behaviors.
- [ ] **Step 4:** Минимальная реализация exact spec; generate/typecheck/vet/boundaries и focused Go tests. Expected GREEN: все перечисленные классы, JSON schema и old campaign contract.
- [ ] **Step 5:** Scoped diff, commit feat(reports): add shared operator statistics, exact SSH branch/upstream guard/push; task-done с тем же focused command. Expected exit0 и записанный ledger.

### Task 2: Общий React-admin экран и карточка кампании
**Files:** Create web/src/AdminStatistics.tsx, web/tests/statistics.spec.ts.
Modify web/src/Admin.tsx, web/src/AdminCampaigns.tsx, web/src/api/client.ts, web/src/i18n.ts, web/tests/campaigns.spec.ts; style только если существующего stats-grid недостаточно.
**Interfaces:** Consumes Task1 StatisticsReport/ReadOperatorStatistics. Produces AdminStatistics({lang,campaignId?,onForbidden}), getOperatorStatistics(campaignId?:string,signal?:AbortSignal):Promise<StatisticsReport>.
Uses existing displayPrice, errorText, semantic dl/buttons/focus, AbortController.

- [ ] **Step 1:** Добавить rendered tests: RU/EN global and campaign same reports, money18446744073709551614 exact; unknown/partial vs0, empty denominator=>unknown, panel safe errors/observation time/groups/archived plan references. Campaign fixture responds new report endpoint; old metadata/events/replay paths сохраняются.
- [ ] **Step 2:** npm --prefix web run test:e2e -- tests/statistics.spec.ts. Expected RED: resource/labels absent.
- [ ] **Step 3:** Создать один report component и /admin/statistics resource; использовать его в campaign card без копии финансовых формул. Manual refresh/retry, abort+scope identity, 401/403 parent denial; source-specific metadata карточки С25 сохранены.
- [ ] **Step 4:** Добавить blocked delayed responses/lang/roleloss/error/retry/375px keyboard checks. Run typecheck + tests/statistics.spec.ts tests/campaigns.spec.ts. Expected GREEN, no horizontal overflow/private key fields.
- [ ] **Step 5:** Scoped commit feat(admin): show shared statistics reports, guarded push, task-done focused rendered command. Expected exit0 и ledger.

### Task 3: Нативная приёмка, финальное review и доставка
**Files:** Modify backend/tests/native_trial_integration_test.go; Create docs/superpowers/evidence/2026-10-08-s26-statistics.md; update spec/plan чекбоксы/синхронизация предыдущей доставки.
**Interfaces:** Consumes production modules from Tasks1/2 and existing native TLS3X-UI3.7.0/SMTP fixture. Produces exact-source local/CI/delivery proof.

- [ ] **Step 1:** Добавить TestNativeTrialReports: owned operator/login/trial+campaign через существующий fixture, bulk report against actual3.7.0 same UUID/grant, active1/typed counters/emptycohort, snapshots unchanged before/after. Expected RED до actual bulk compatibility подтверждения; если current fixture уже проходит, честно зафиксировать characterization GREEN и не выдумывать RED.
- [ ] **Step 2:** Проверить owned containers/config files first; NATIVE_DOCKER_STATE=owned TEST_DATABASE_URL_FILE=owned TEST_REDIS_URL_FILE=owned go -C backend test -race ./tests -run TestNativeTrialReports -count=1. Expected PASS на actual pinned3.7.0; external acceptance не выводится из него.
- [ ] **Step 3:** generate/schema-additive diff/static/typecheck/build/runtime-config; RUN_BROWSER_TESTS=1 make -C backend test-integration; poetry run python -m unittest discover -s tests -v; npm --prefix web run test:e2e. Full logs private; прочитать все итоги. Expected все current-source checks exit0, no failed/skipped required checks.
- [ ] **Step 4:** Evidence/spec/plan/ledger полный scoped commit+push, task-done source proof; one fresh gpt-6-astra/high whole-branch review with Review Focus verbatim/all Rulings. Critical/Important — one author RED→GREEN+whole current suite; Minor defer; no rereview.
- [ ] **Step 5:** Архивировать proof, удалить только свой SDD; attach PR to v2/milestone; current-source PR Platform+images success; fresh role/deps/authority/effects/exact-SHA manual merge. Проверить merge parents/tree, v2 prerelease/annotated tag/3OCIindices6labels. Close #36/ProjectDone после local+review+CI+publication; продолжить #37.
