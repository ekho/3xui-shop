# С19 Fulfillment Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking. Native выбран владельцем: координатор выполняет три задачи, одна fresh Astra/high whole-branch review, один Critical/Important fix pass; Minor deferred, без re-review. Документы/реализация/merge разрешены автономно.

**Goal:** Оператор безопасно восстанавливает сохранённую оплаченную выдачу, а её данные и однократный эффект переживают перезапуск Go-процесса.

**Architecture:** Существующие payments/subscriptions/VPN operations, owner lock и River отвечают за durable money/access. Два React-интерфейса получают узкие fixes запросов/consent. Локальный acceptance helper расширяется подписанным YooMoney fixture и остановкой того же native backend; второй финансовый writer не появляется.

**Tech Stack:** Go/pgx/River/PostgreSQL, React/TypeScript/Playwright, Python stdlib и Docker Compose. Новых dependencies нет.

**Spec:** [2026-10-07-s19-fulfillment-recovery-design.md](../specs/2026-10-07-s19-fulfillment-recovery-design.md); canonical `2026-10-07-s19-fulfillment-recovery-v1`, owner [#26](https://github.com/ekho/3xui-shop/issues/26).

## Global Constraints

- Один Go-процесс HTTP/River/Telegram, один go.mod; public module operations, без чужих SQL/private пакетов.
- База `5246923c6a7fe92a1f29693d401fde292368d63d` от origin/v2; feature/s19-fulfillment-recovery, без codex/, PR в v2.
- Существующие quote, financial proof, order/funding/access IDs, абсолютный target, API/DTO/hash и migrations 15–22 сохраняются.
- Checkout действует 30 минут. Просроченный pending не означает canceled; время фактической оплаты отличается от времени доставки уведомления.
- Причина операторского действия: 1–1000 Unicode code points после trim, без NUL. Актуальные права, Origin/CSRF, UUID Idempotency-Key, audit сохраняются.
- Название/домен только в конфигурации; ru/en, keyboard/screen reader и 375px.
- Собственные localhost данные/заглушки. Реальные деньги, публичный callback, production, живой Happ/VPN, Mac trust, внешние SMTP/Telegram не входят.

## Review Focus

- Cancel/reopen или новое operation state не наследуют разрешение уничтожить новый трафик; Task1 rendered test.
- Ответ старого preparation request после другой карточки не публикует чужой access ID; Task1 held response test.
- Изменённый order/operation/reason/consent не переиспользует key, неизменённый lost request использует тот же; Task1 tests.
- Lost create reply оплаченного клиента проверяется readback того же target без второго Add; Task2 regression test.
- Process stop между receipt/job и target/job не теряет funding и не добавляет повторный период; Task2 actual compiled Docker checks.

---

## Task 1: Безопасное восстановление в операторской карточке

**Files:** Modify web/src/AccessOperations.tsx, OperatorPurchase.tsx, i18n.ts; test web/tests/subscription-operations.spec.ts и purchase.spec.ts. Действующие wrappers в web/src/api/client.ts уже принимают AbortSignal; не менять API/schema.

**Interfaces:** `AccessReconcileInput{reason:string,acknowledge_reset_cost:boolean}`; `reconcileAccessOperation(clientId,operationId,input,key,signal?)`; `reconcilePurchaseOrder(clientId,orderId,{reason},key,signal?)`. Оба компонента передают parent callbacks только для живого current request. Produces safe false/default и явный fresh true consent.

- [x] **1. RED:** Rendered ambiguous-reset safe path: after reason/confirm, unchecked checkbox и enabled confirm; payload `{reason:'Readback checked',acknowledge_reset_cost:false}`. Fresh-consent test: check→cancel→reopen unchecked; изменённое состояние требует нового согласия. Held preparation reply after client switch не вызывает старый access GET/current UI; тот же order+reason lost reply retains key, другой order/input gets another key. Run `npm run test:e2e -- tests/subscription-operations.spec.ts tests/purchase.spec.ts --grep 'safe verification|fresh reset consent|late preparation|preparation key'`. Expected: новые behavior assertions FAIL на текущем коде; сохранить actual RED.
- [x] **2. Implement:** Разрешить false verify без checkbox. RU/EN copy объясняет проверку и отдельный destructive repeat. Reset consent on fresh confirmation/cancel/operation state; attempt payload включает operation ID и input. Preparation retry использует существующий AbortController/request ref pattern и order+reason scoped attempt; abort old reads перед принятым write, поздние responses ignored. Сохранять busy/reason/denial/stable key semantics; не добавлять общий store/helper/framework.
- [x] **3. GREEN:** `npm run typecheck`; `npm run test:e2e -- tests/subscription-operations.spec.ts tests/purchase.spec.ts`. Expected: все новые и прежние assertions PASS, RU/EN/375px/native keyboard controls, true путь и false путь проверены. Полная web suite выполняется в Task3 на текущих inputs.
- [x] **4. Commit:** `fix(web): make fulfillment recovery safe and scoped`, Refs #26, Co-Authored-By: Codex <noreply@openai.com>. Source-bound Task1 check record, actual shared #26 checkpoint; delivery pending. Task-done запускает проверку текущего исходника и завершённых named checks, не повторяет неизменённую дорогую suite.

## Task 2: Оплаченная выдача переживает потерянный ответ и перезапуск

**Files:** Add paid lost-create case in backend/internal/httpapi/regression_purchase_recovery_test.go using current panelFixture. Modify deploy/acceptance/local.py и compose.native.yml; create tests/test_paid_recovery_acceptance.py. Test-only files/secrets в собственном private acceptance state; production module writers не менять, если тест не выявил gap.

**Interfaces:** Existing paidPurchase/panelFixture/fulfillPurchase/applyAccess regression helpers; existing `local.prepare()`, `signup()`, `sql(query,db=None,operation=None)`, `panel_readback(target)`, `compose`, `api`, `session`. New local `native_paid_restart()` called by explicit `paid-recovery` action; local signer consumes unique string fields/private secret, produces YooMoney HMAC-SHA256 sign using existing canonical field ordering/RFC3986 contract. Both stopped stages queue the same immutable IDs, no runtime-only Go constructor is substituted for a process restart.

- [x] **1. Pin existing behavior:** Add `TestRegressionPurchaseLostCreateReply` with paidPurchase/panelFixture loseAdd: fulfill one order, save target/funding/IDs; apply twice; assert one Add, one access target, applied result and exact readback/unchanged money identity. Run `go test -race ./internal/httpapi -run 'Regression(Purchase|YooMoney|AccessConcurrent|AccessReconcile)' -count=1 -timeout=5m`. Expected: PASS correct existing guards; if new test is already GREEN, bounded remove-readback mutation must make it RED then restore exact source bytes. Не переписывать working Go product ради RED.
- [x] **2. RED fixture helper:** Python stdlib test prepare existing/new state creates 0600 secret before ENV early-return and native fixture env/mount; signer matches existing official vector, only owned localhost and UUID parameters. Run `python3 -m unittest tests.test_paid_recovery_acceptance -v`. Expected: FAIL missing fixture/helper before implementation, без реальных provider calls.
- [x] **3. Implement local proof:** Native overlay enables only local YooMoney stub wallet/FILE secret, migrate startup receives valid config if necessary. prepare creates secret even on existing state. Add explicit paid-recovery command: own verified client/operator/test catalogue; signed duplicate notification; own trigger holds only fixture purchase/access jobs. Stop compiled backend after receipt/job commit, release one job, start same image no rebuild; hold saved target/job, stop again, release/start, wait applied. Compare receipt/funding/access counts, frozen target/identity and actual 3X-UI3.7.0 readback. Repeat notification after applied, no extra period. finally drop own triggers/clear own role file/start only own backend; secret/IDs/URLs never printed. Existing trial/native checks preserved.
- [x] **4. GREEN:** `python3 -m unittest tests.test_paid_recovery_acceptance -v`; current focused Go recovery command; own `LOCAL_STATE_DIR=<private owned state> python3 deploy/acceptance/local.py up`, then `paid-recovery`. Expected: PASS actual two compiled-process restart stages/real native panel readback/stable operation and receipt identities, Telegram disabled. Record actual images/project/version/checks/limitations, stop only own stack after proof.
- [x] **5. Commit:** `test(payments): prove durable fulfillment recovery`, Refs #26/Co-Authored; task-done current source/check verifier, shared checkpoint. Existing paid role-revocation behavior must remain unchanged.

## Task 3: Текущая приёмка, evidence и доставка

**Files:** Create docs/superpowers/evidence/2026-10-07-s19-fulfillment-recovery.md; own plan checkbox updates/private task records. Не менять чужие SDD directories или primary dirty checkout.

- [x] **1. Prerequisites:** Read shared #26/actual dependencies/canonical refs; C04 earliest Task1 current public API slice, C05 roles/rights/consent/races, C10 own PG/Redis/Docker3.7/TLS fixture readiness. Expected: met for local-only criterion; C13 external remains open, no ownerless fake prerequisite.
- [x] **2. Current full verification:** `make generate`, `npm run api:generate` and no drift; `make vet`; `RUN_BROWSER_TESTS=1 make test-integration` with FILE-backed own local DB/Redis; `npm run build`, complete `npm run test:e2e`; complete existing Python suite. Static/runtime/naming checks and `git diff --check`. Expected: all PASS; preserve counts/source/input hashes. Repeats only for changed inputs/new supported hypotheses, not because CI is pending.
- [x] **3. Evidence/commit:** Full scenario matrix, RED→GREEN/mutation/real restart proof, commands/counts/source hashes, current rights/safe consent/late responses and all limitations. Record all Native rulings and cost. `docs: record fulfillment recovery acceptance`, Refs #26/Co-Authored. Task-done verifier checks current inputs and completed checks; delivery pending.

## После трёх задач

Одна свежая Astra/high read-only whole-branch review по spec/plan/base/head/current evidence. Каждый finding — severity по эффекту/ruling/cost; один авторский Critical/Important TDD fix pass, Minor deferred, без re-review. Архивировать только свой SDD перед удалением после commit fixes. Push exact SSH/current source, PR в v2 без auto-merge; проверить required actual source gates и немедленно exact SHA/target/parents/tree, merge вручную. Проверить actual v2 prerelease/tag commit и три OCI indexes/source-version labels обеих платформ. Только actual local acceptance+delivery закрывает #26 и Project Done. Затем следующая готовая задача С20/#27, по очереди.
