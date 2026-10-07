# С17 — смена тарифа Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Native выбран владельцем; координатор реализует три задачи, затем один fresh Astra/high reviewer проверяет всю ветку. Документы и реализация автономны; повторное согласование плана не требуется.

**Goal:** Оплаченная смена конечного тарифа с нового момента, постоянными VPN IDs и однократным reset; все существующие внешние методы для change_plan/renew.

**Architecture:** Существующие payments order/receipt/River/VPN writes сохраняются. subscriptions проверяет текущий конечный источник, VPN читает его применённую операцию; source_access_operation_id фиксируется в неизменяемом quote. Новый target — от now; renew сохраняет max(expiry,now).

**Tech Stack:** Существующие Go/Echo/pgx/PostgreSQL/Redis/River, React/React-admin/Playwright; без новых зависимостей.

**Spec:** [2026-10-07-s17-plan-change-design.md](../specs/2026-10-07-s17-plan-change-design.md), контракт 2026-10-07-s17-plan-change-v1, owner#24.

## Global Constraints

- Один Go-процесс и один активный владелец операций; допущено окно обслуживания.
- Новые ветки от origin/v2, без codex/, PR только v2.
- UUID, sub_id, panel_key и назначенный сервер сохраняются; трафик сбрасывается один раз.
- Миграции15–20 не редактируются; старые purchase/renew bytes/hashes/proofs/job kinds сохраняются.
- SourceAccessOperationId *uuid.UUID опускается в старых Input/Quote; обязателен только action=change_plan.
- Trial-only — С10; unknown Telegram/legacy billing закрыт до С35. Ban/restriction/unlimited не снимаются оплатой.
- YooMoney AC/PC/manual/YooKassa — RUB; Cryptomus/Heleket — USD; действующие enabled flags сохраняются.
- Только собственный localhost cabinet-c17/native3X-UI3.7.0/TLS Mailpit и заглушки; без реальных денег/production/live Happ/VPN/Mac trust/внешних SMTP/Telegram.
- RED→GREEN и проверка реальных завершённых logs/source digests; original task evidence сохраняется при дальнейших изменениях.

## Review Focus

1. Назначен тот же plan UUID после checkout: source operation UUID меняется, деньги сохраняются needs_review; Task1 late-source test.
2. Исходный own hidden/archived план: переход на видимую цель допустим, hidden чужие планы недоступны; Task1/2 eligibility cases.
3. Locale после потерянного POST: source/key/body/показанные условия неизменны; Task2 frozen retry.
4. Renew через USD provider: точный USD principal/unknown net сохраняется, старые RUB proofs не переинтерпретируются; Task1 all-method matrix.
5. Restore prepared change: новый now не выбирается, reset/UUID/server не повторяются; Task3 native READ ONLY restore/restart.

---

### Task 1: Общая оплаченная смена и все внешние методы продления

**Files:** Create `backend/db/migrations/00021_purchase_plan_change.sql`,
`backend/internal/httpapi/plan_change_test.go`. Modify
`backend/internal/modules/vpn/{contracts,data}.go`,
`backend/internal/modules/vpn/internal/queries/access_operations.sql`,
`backend/internal/modules/subscriptions/renewal.go`,
`backend/internal/modules/payments/{contracts,renewal,purchase,purchase_worker,manual_payment,yoomoney,yookassa,crypto_payment}.go`,
`backend/internal/httpapi/{api,purchase,payments_mapping,renewal_test,cryptomus_test,yookassa_test}.go`,
`docs/api/openapi.yaml`, generated Go/sqlc/TypeScript outputs.
Private reports/check verifier: `.superpowers/acceptance/c17-plan-change/`.

**Interfaces:** Preserve `vpn.CurrentPlanIDTx` and `subscriptions.RenewalPlanIDTx`.
Produce `vpn.PlanAssignment{OperationID uuid.UUID; PlanID *uuid.UUID}` and
`vpn.Service.CurrentPlanSourceTx(ctx context.Context, tx pgx.Tx, account uuid.UUID)
(*vpn.PlanAssignment,error)` using the existing applied provenance query;
`subscriptions.Service.CurrentPlanSourceTx(ctx,tx,account) (vpn.PlanAssignment,error)`
owns the common eligibility and preserves old renewal errors.
`payments.PlanChangeContext{CurrentPlanId uuid.UUID; SourceAccessOperationId uuid.UUID}`
and `payments.Service.PlanChangeContext(ctx,account) (payments.PlanChangeContext,error)`
feed authenticated GET `/api/v1/subscription/plan-change`.
Input/Quote append optional SourceAccessOperationId with json source_access_operation_id/omitempty;
order action gains change_plan. Existing CreatePurchaseOrder/fulfillment signatures remain.

- [ ] **Step 1:** Write connected `TestPlanChangeHTTPFlow`: create/apply original funded purchase with existing fixture, authenticate actual session, GET plan-change context, choose another plan via POST orders with raw JSON source UUID, callback/prepare/apply/replay twice. Assert new expiry=e.Clock()+chosen days (old future expiry discarded), new devices/traffic/profile, same native identity/server, one source/quote/receipt/job/target/reset. No generated new type needed for the initial GET assertion.
- [ ] **Step 2:** Run bounded `go test ./internal/httpapi -run '^TestPlanChangeHTTPFlow$' -count=1 -v` with protected absolute test DB/Redis URL files. Expected: actual behavioral RED at missing context/plan-change route; fixture setup runs, no prerequisite/import failure.
- [ ] **Step 3:** Author migration21/action/optional source/PlanChangeContext/OpenAPI GET; run `make generate` in backend and `npm run api:generate` in web. Implement public provenance/eligibility and shared live purpose/source guards at preflight/final create, public checkout, existing five funding paths, preparation/reconcile and CheckPurchaseAccess/final Tx. Reuse current history/owner/native guards; allow proved exhausted/expired managing actions. Change only target base for change_plan, retain frozen target/current quote/reset. Reuse queueFundedPurchaseTx across the four funding callers so valid receipts stay valid and late access conflicts become paid review before queueing. Remove the first-iteration renew-method restriction; offer positive RUB or USD own finite prices. Expected: Step1 GREEN, old purchase/renew replay bytes retained.
- [ ] **Step 4:** Add `TestPlanChangeAllExternalMethods` for change_plan/renew × all five delivered methods, using existing kassa/crypto/manual/YooMoney fixtures and real receipt funding; assert exact currency/proof/source/IDs/reset/period and repeated jobs. Add `TestPlanChangeEligibility`, `TestPlanChangeLateSource`, `TestPlanChangeLateGuards`, `TestPlanChangeFrozenQuote`, `TestPlanChangeHTTPBoundary`, `TestPlanChangeActionMigration`, `TestPlanChangeConcurrentCreate` and `TestPlanChangeFundingGuards`: same visible target, own hidden/archive source, invalid hidden/archive/zero/stale/unlimited target; cleared/foreign/nil source, same-plan reassignment, compensation/reset preserving source, unknown billing/native disable; late account/source/native changes preserve paid review and safe reconcile; quote/action immutability, guarded Down and unchanged old default. Adapt old phase-specific renew-method assertions and migration20 test to explicit DownTo19 without modifying historical migrations/evidence. Expected: all named connected tests PASS; no wrong-money or unapproved write.
- [ ] **Step 5:** Run bounded `go test ./internal/httpapi -run '^(TestPlanChange|TestRenewal|TestPayments|TestRegressionPurchase|TestRegressionYooMoney|TestYooKassa|TestCryptomus|TestHeleket|TestManual)' -count=1 -race -v`, `go test ./internal/app -count=1 -race`, `go vet ./...`, generation and web typecheck. Expected: PASS/no skip/race; inspect actual results. Seal task1 records/log hashes and current backend/API digests; private `verify-checks.py task1` rejects incomplete/stale proof and confirms named test roots ran.
- [ ] **Step 6:** Inspect diff, commit/push `feat(subscriptions): support funded plan changes` (Refs#24, Co-Authored), verify SSH/upstream/exact remote source. Run task-done with `python3 .superpowers/acceptance/c17-plan-change/verify-checks.py task1`. Expected: PASS and task1 ledger completion.

### Task 2: Кабинет смены и продление всеми методами

**Files:** Modify `web/src/{Catalogue,Cabinet,PurchaseOrder,OperatorPurchase,main}.tsx`,
`web/src/i18n.ts`, `web/src/api/client.ts`, `web/tests/renewal.spec.ts`,
`web/playwright.config.ts`; Create `web/tests/plan-change.spec.ts`.

**Interfaces:** Consume Task1 PlanChangeContext/optional source/action and existing
catalogue/method/current order/fresh GET. Produce
`Catalogue({lang,action='purchase'}:{lang:Lang;action?:PurchaseOrderInput['action']})`,
`api.getPlanChangeContext(signal?:AbortSignal):Promise<PlanChangeContext>` and
`/cabinet/change-plan`; existing /catalogue and /cabinet/renew behavior is preserved/extended.

- [ ] **Step 1:** Write rendered `plan change warns and freezes source through a lost response`: real route/375px, visible target and old applied history, new context UUID, warning before confirmation, first POST aborted, locale switched, same body/key/source retried. Assert change_plan purpose and exact amount/period. Add file to explicit existing Playwright testMatch. Expected before controls exist: behavioral RED at missing route/heading.
- [ ] **Step 2:** Run bounded `npm run test:e2e -- tests/plan-change.spec.ts -g 'plan change warns'`. Expected: one actual RED; no external provider request.
- [ ] **Step 3:** Replace optional renewal boolean with one action mode, reuse the existing catalogue/payment/order components. Read context+catalogue, preserve frozen attempt for managing actions, include source only on change_plan. Add RU/EN warning/title/purpose/eligibility, cabinet link and enabled RUB/USD methods for renew. Keep default/current purchase and fresh-checkout safe behavior. Expected: core test GREEN.
- [ ] **Step 4:** Add `plan-change.spec.ts` rendered RU/EN375px/keyboard/focus/labels, all five methods/currencies, same target allowed, empty/hidden filtering, context denied/503/retry, disabled methods/zero price, current pending/review and stale source/revision/reload. Add fresh GET 403/404/foreign/purpose/source mismatch scenarios before any provider request and operator purpose/link. Replace old first-iteration renewal-only-method assertions with current all-method cases while retaining frozen old YooMoney cases. Expected: PASS, no real payment navigation.
- [ ] **Step 5:** Run typecheck/build and `npm run test:e2e -- tests/plan-change.spec.ts tests/renewal.spec.ts tests/purchase.spec.ts tests/manual-payment.spec.ts tests/yookassa.spec.ts tests/cryptomus.spec.ts tests/heleket.spec.ts` with the existing local runner. Expected: PASS; seal actual completed logs/current web source and verify task1 product bytes unchanged.
- [ ] **Step 6:** Inspect diff, commit/push `feat(web): expose funded plan changes` (Refs#24, Co-Authored), verify exact remote source; task-done `python3 .superpowers/acceptance/c17-plan-change/verify-checks.py task2`. Expected: PASS.

### Task 3: Native приёмка, восстановление и доставка

**Files:** Create `deploy/purchase/plan-change-local.py`,
`docs/evidence/s17-plan-change-acceptance.md`; modify `deploy/purchase/README.md`,
`deploy/purchase/{local,renewal-local}.py` only if sharing the existing VM-local
counter writer removes duplication. Reuse common acceptance/YooMoney/native3.7.0 overlays.
Private runtime/dumps/reports only `.superpowers/acceptance/c17-plan-change/native`.

**Interfaces:** Consume Tasks1/2 HTTP/workers and existing local fixture driver;
produce prepare/up/check/restore/stop for own cabinet-c17/cabinet_c17,
subnet10.253.17.0/28, free loopback58443/59444–59447 and pinned native3.7.0.
Source image labels and one backend process are verified before any claim.

- [ ] **Step 1:** Check actual local Docker, protected test files and own port/network/project ownership. Implement bounded native checker using existing registration/login/order/YooMoney HMAC/River/panel helpers; synthetic counter write inside Docker VM with stopped own panel, pinned existing Python base/network disabled/own DB-only mount. Expected: preflight PASS before costly native checks; no live VPN/trust/real provider.
- [ ] **Step 2:** Run own active/expired/exhausted changes, regular→euru→regular, replay and late ban/same-plan reassignment/identity guards. Assert now-based saved target, one reset and IDs/server; exercise an all-method renewal in connected tests rather than claiming five native gateways. Expected: native PASS; no unconfirmed reset inferred.
- [ ] **Step 3:** Pause own access job after funded preparation, dump PostgreSQL, stop original backend, restore isolated DB READ ONLY without restored workers; compare action/source/money/IDs/target digests, run idempotent post_restore_auth, restart original backend and apply same target once. Expected: PASS with original saved expiry and receipt, one writer.
- [ ] **Step 4:** Run current full `RUN_BROWSER_TESTS=1 go test ./... -count=1 -race -v` in backend with actual protected DB/Redis URL files, `poetry run python -m unittest discover -s tests -v` in root with the same files, full `npm run test:e2e` in web, `make -C backend generate`, `npm --prefix web run api:generate`, `python3 deploy/acceptance/check_names.py`, `npm --prefix web run test:e2e -- tests/runtime-config.spec.ts` and `git diff --check`. Expected: all PASS/no skip/race; save actual durations/output hashes and exact source manifests. Public evidence records limits, all ledger rulings/costs and prior checkpoints; private verifier validates every real completed record against its original revision and final current source. Do not repeat unchanged passing expensive checks merely to complete task-done.
- [ ] **Step 5:** Inspect diff, commit/push `test(subscriptions): verify native plan change recovery` (Refs#24, Co-Authored); task-done `python3 .superpowers/acceptance/c17-plan-change/verify-checks.py final`. Expected: PASS. Dispatch ONE fresh Astra/high read-only whole-branch reviewer with exact base/source/spec/plan/Review Focus/ledger/evidence; re-grade findings, Critical/Important ONE RED→GREEN fix pass/current suites, Minor deferred, no re-review. Record every declined judgment/ruling publicly.
- [ ] **Step 6:** Create/attach PR→v2, wait required current-source CI, verify target/rules/authority/no auto-merge, manual SHA-guarded merge. Verify actual parents/tree, push preview, annotated tag/nondraft prerelease, three OCI indexes/amd64+arm64/six source/version labels. C09 then close#24/Project Done; archive only own SDD/logs and stop only own fixture, retain native data. Expected: implementation/local acceptance/v2 delivery separated and verified; next dependency-ready scenario begins from fresh origin/v2.
