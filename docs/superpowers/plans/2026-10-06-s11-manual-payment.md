# С11 — ручная оплата: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Native выбран владельцем; один fresh whole-branch reviewer после задач.

**Goal:** Клиент подаёт одну заявку о ручном переводе, оператор подтверждает проверенную сумму или отказывает; общий заказ сохраняет финансовый факт и одну выдачу.

**Architecture:** payments расширяет существующий заказ и funding proof, используя accounts/audit и прежний River/VPN worker. HTTP и React-admin — адаптеры публичных операций. Заявка хранится в purchase_orders, отдельный платёжный framework не добавляется.

**Tech Stack:** существующие Go/Echo/pgx/River/PostgreSQL/Redis, React/React-admin/Playwright, Docker3X-UI3.7.0 и TLS SMTP.

**Spec:** [2026-10-06-s11-manual-payment-design.md](../specs/2026-10-06-s11-manual-payment-design.md).

## Global Constraints

- Контракт 2026-10-06-s11-manual-payment-v1, owner #19; fresh origin/v2 c9c075e40f822fbe4f7d058d292f2a14ad07cc3f, feature/c11-manual-payment; PR→v2, без codex prefix, Conventional Commit/Co-Authored.
- SHOP_PAYMENT_MANUAL_ENABLED default false; MANUAL_CARD_DETAILS_FILE защищённый, snapshot/plain text, никакого изменения действующей конфигурации ради проверки.
- Цена серверная/целая; report не деньги; approve требует exact confirmed_amount_minor + reason1–1000 и operator authority. Одна active заявка, терминальное решение, Tx audit+receipt+queue+idem.
- Pending report не истекает и не отменяется клиентом; решение после 30min разрешено для своевременного report. Down не удаляет manual history.
- Существующие15 миграций/API/IDs/hash/job/replay/roles/ban сохраняются; только additive migration16/API. Новых deps/SQL вне owner/private peer imports/общего facade нет.
- Ru/en/375px/keyboard/role=status/error/busy. Outbound non-TG channels С27/28, thin bot С32, Python removal С47; production/реальные деньги/Telegram/Happ/VPN/trust исключены.

## Review Focus

- Оператор решает после окна/отключения метода: уже сообщённые деньги не теряются; ограничения выдачи не обходятся — Task1 late/disabled/restricted tests.
- Потеря ответа и конкурентное противоположное решение: один receipt/job; роль проверяется даже при replay — Task1 races/idempotency/role tests.
- Корректно подписанный YooMoney callback с manual label: не заменяет operator authority, факт сохраняется спорным — Task1 cross-method/funding tests.
- Browser хранит старую форму/дедлайн/ключ при изменении состояния: не отправляет второй платёж или противоположное решение — Task2 stale/reload/error tests.
- Собственная очередь скрывает заявки при переходе на карточку/после обновления: pagination/empty/revoked role и безопасные ссылки — Task1 inbox + Task2 browser.

### Task 1: Manual money, migration and HTTP contract

**Files:** Create backend/db/migrations/00016_manual_payment.sql, backend/internal/modules/payments/manual_payment.go, backend/internal/httpapi/manual_payment.go, backend/internal/httpapi/manual_payment_test.go. Modify payments/{contracts,service,purchase,yoomoney}.go, app/config.go, httpapi/{api,payments_mapping}.go, docs/api/openapi.yaml; generated Go/TS/sqlc через make generate и npm api:generate. Не изменять исходные миграции1–15.

**Interfaces:** Consumes accounts.LockOperatorPair/Lookup/Lock, audit_reports.RecordTx, существующие purchase row/funding/idem и River InsertTx(PurchaseArgs). Produces ReportManualPayment, DecideManualPayment, ManualPaymentRequests и optional ManualPayment DTO точно по spec. HTTP и будущий Telegram используют те же public methods.

- [x] **Step 1: Contract first, then failing behavior tests.** Additive enums/manual DTO и три операции в авторском OpenAPI; штатная generation. Добавить TestManualPaymentReportDecisionAndSnapshot, TestManualPaymentTerminalRacesAndRollback, TestManualPaymentBoundariesAndInbox и TestManualPaymentCrossMethodFunding на actual app composition/PG/Redis; assertions AC01–AC05, включая paid/receipt/job counts, exact big integer, role/target/CSRF и forged funding. RED до implementation, полный лог сохранить.
- [x] **Step 2: Minimal schema and owner behavior.** Миграция16 добавляет поля заказа/constraints/immutable proof, blocked downgrade. Сохранить старую ветку fund SQL; добавить manual proof с actor/decision/report time/receipt. Input hashes прежних YooMoney input не меняются. Report/decision/queue/audit/idem в одной caller Tx, account→order locks, no provider/panel call внутри этой Tx.
- [x] **Step 3: Actual HTTP and runtime config.** manual opt-in/file validation, snapshot и JSON projections. Реальные маршруты/contract validation/роль/CSRF/Origin/idempotency; oldest first inbox50+cursor. Report-only/new pending после report не дублирует событие. Чужой provider fact — retained needs_review. Expected: все четыре focused tests PASS, старые purchase/YooMoney tests PASS; migration downgrade отказ без удаления строк; generate no drift/go vet.
- [x] **Step 4: Commit.** feat(payments): add operator-confirmed manual payments, Co-Authored; task-done выполняет focused Go command на committed revision.

Task command: `go -C backend test -race ./internal/httpapi ./internal/app ./internal/modules/payments -run 'TestManualPayment|TestRegressionPurchase|TestRegressionYooMoney|TestYooMoney|TestPurchaseHTTP' -count=1`. TEST_DATABASE_URL_FILE/TEST_REDIS_URL_FILE — own private loopback fixtures. Expected: все3 пакета PASS, без skipped DB tests.

### Task 2: Client and React-admin surfaces

**Files:** Modify web/playwright.config.ts (existing whitelist), web/src/Catalogue.tsx, web/src/PurchaseOrder.tsx, web/src/OperatorPurchase.tsx, web/src/Admin.tsx, web/src/i18n.ts, web/src/style.css, web/src/api/client.ts; create web/src/ManualPaymentInbox.tsx and web/tests/manual-payment.spec.ts. Использовать имеющийся data flow/компоненты; generated schema не редактировать.

**Interfaces:** Consumes Task1 generated types и новые HTTP operations. Produces выбор manual, plain-text snapshot/report, persistent decision role=status и operator inbox→client card→decision. Existing YooMoney form/post/price validation остаются.

- [x] **Step 1: Browser RED.** Test client manual→report/reload→late waiting→approved/rejected, lost-response replay/key, disabled/YooMoney coexistence, stale state and error. Test operator inbox empty/50+more→card, approve amount/reason/check acknowledgement, reject, forbidden/revoked role. Ru/en/375px/keyboard/escaped instruction text; mock существующего HTTP contract, внешний перевод не отправлять.
- [x] **Step 2: Minimal UI/API methods.** Добавить методы reportManualPayment/decideManualPayment/getManualPaymentRequests к существующему api client. Общий каталог выбирает method и корректный PaymentType; на order не применять старый expired message к reported waiting. Отдельная очередь Resource в React-admin и действия OperatorPurchase с явной проверкой денег. Повторная команда хранит тот же key/body; при смене input новый key.
- [x] **Step 3: Verify and commit.** Expected: новый focused browser suite PASS, существующий purchase suite PASS; npm typecheck/build/runtime-config PASS. feat(web): add manual payment requests, Co-Authored. task-done выполняет focused browser command.

Task command: `npm --prefix web run test:e2e -- manual-payment.spec.ts purchase.spec.ts`. Expected: все cases PASS. Если имя существующей purchase suite отличается, записать проверенное имя и ruling, не запускать отсутствующий файл.

### Task 3: Owned native acceptance and delivery evidence

**Files:** Create deploy/purchase/compose.manual.yml; extend deploy/purchase/local.py с `prepare-manual`/`check-manual`/`restore-manual`, reuse owned account/SQL/SMTP/panel/restore helpers; modify deploy/purchase/README.md; create docs/evidence/s11-acceptance.md. Reconcile delivered М06 facts в roadmap, M06d evidence/plan и monolith spec; никаких изменений их product.

**Interfaces:** Consumes actual committed Task1/2 plus own native local fixture. Produces blinded AC01–07 records/real HTTP+3X-UI state; unreported/report/approve/reject/repeated decision, panel outage/restart и backup/restore paid/manual actor proof. Не настоящий банковский перевод; подтверждение оператором синтетического fixture обозначить явно.

- [x] **Step 1: Owned opt-in fixture.** Synthetic file details, LOCAL_STATE_DIR identity guard, no provider calls/реальные card details/чужие accounts. Current test SMTP/3X-UI3.7.0; тестовый operator role только собственного аккаунта.
- [x] **Step 2: Thin actual path.** Реальный HTTP session/client report/operator decision, одна выдача и сохранённые trial IDs; отказ/конфликты/запрет чужого target; native outage/restart, backup/restore незавершённого paid с immutable claim/actor/receipt. Expected: exact assertions PASS, собственный stack stopped; детали/credentials/dump private0700/0600.
- [x] **Step 3: Final suite once on committed product.** Штатные generation+no drift, names/import/SQL/API compatibility, go vet/race connected, npm typecheck/build/runtime config, Python/Playwright, Compose/smoke/native HTTPS/TLS, existing YooMoney purchase repeat/restore и новый manual path. Expected: все stages PASS; old15/dependency hashes unchanged, migration16 present. Полные логи через run-check с timeout, без повторного полного прогона без новых inputs.
- [x] **Step 4: Evidence and commit.** All AC results, Native rulings/costs и реальные ограничения; inherited SMTP/performance не закрывать; С13 проверяется отдельно с заглушками по уточнению владельца 2026-10-06. docs(payments): record manual payment acceptance, Co-Authored; task-done проверяет evidence + committed-product suite record.

## Finish

- [x] Один fresh Astra/high whole-branch reviewer от base c9c075e до окончательного head; все findings re-grade, каждое Declined to judge — Final ruling/cost. Critical/Important один RED→GREEN fix pass и green suite без re-review; Minor deferred.
- [x] Экспортировать собственные briefs/ledger/review/full logs/rulings, проверить копию, удалить только own scratch.
- [ ] Exact-source required CI и PR images, manual SHA-guarded merge→v2; actual parents/source-equal tree, actual preview tag/3public multiarch indexes/6config labels.
- [ ] Только после собственной AC/доставки #19 CLOSED/Project Done и fresh v2 next task. С13/#18 остаётся отдельной задачей локальной приёмки с заглушками по [новому решению](https://github.com/ekho/3xui-shop/issues/18#issuecomment-6014628265); настоящая доставка провайдера/legacy cutover не заявляются проверенными.
