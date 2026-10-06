# С10 — первая покупка: план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Placement follows the enabled repository policy: owned backend/frontend boundaries and one fresh final reviewer.

**Goal:** Первая покупка через YooMoney из кабинета выдаёт доступ без Telegram, а переход с триала сохраняет идентификаторы и оставшийся срок.

**Architecture:** PostgreSQL хранит неизменяемый заказ и отдельные денежные факты. Подписанное уведомление атомарно сохраняет деньги и River-задание подготовки. Подготовка сериализует аккаунт и сохраняет абсолютную операцию purchase; существующий AccessWorker выполняет 3X-UI и readback.

**Tech Stack:** Go 1.27.1, Echo 5, pgx/sqlc, River, PostgreSQL, Redis, React/TypeScript, Playwright, Docker 3X-UI 3.7.0; без новых зависимостей.

**Spec:** [2026-10-03-s10-first-purchase-design.md](../specs/2026-10-03-s10-first-purchase-design.md)

## Global Constraints

- Ветка feature/s10-first-purchase от origin/v2 157fb918; PR в v2, без codex prefix.
- Семантические имена в приложении; название бренда и номера сценариев туда не добавляются.
- Секрет уведомлений только YOOMONEY_NOTIFICATION_SECRET_FILE; настройки при деплое.
- Ни настоящих переводов, ни production, ни изменения URL уведомлений кошелька.
- Живой Happ, VPN и доверие сертификатам macOS не меняются.
- Деньги только целые minor units, абсолютный target до panel write; без SQL-транзакции вокруг сети.
- Прежние trial/operator guards и destructive-reset acknowledgement сохраняются.
- Координатор владеет API-контрактом, интеграцией, локальным стендом и публикацией; специалисты не коммитят общий diff и не публикуют его.

## Review Focus

- Второй настоящий перевод на уже применённый заказ: сохранить оба факта, один доступ; тест Task 2.
- Cancel/expiry против позднего callback: signed время и cancellation не должны потерять деньги; тест Task 2.
- Бан или операторский write между checkout и исполнением: paid остаётся, права и target не подменяются; тест Task 3.
- Перезапуск после неопределённого reset: не повторять destructive effect без явного подтверждения; тест Task 3.
- Изменённая форма/redirect/сессия в нескольких вкладках: доверять серверной цене и подписанному callback, состояние восстановить; тест Task 4.

---

### Task 1: Стабильный контракт API

**Owner:** coordinator.
**Files:** docs/api/openapi.yaml; generated backend/internal/wire/models.gen.go and web/src/api/schema.gen.ts.
**Interfaces:** PaymentMethods.methods; PurchaseOrderInput {action,plan_id,revision,period_days,payment_method,payment_type}; PurchaseOrder {order_id,quote,payment_status,fulfillment_status,checkout,can_pay,can_cancel,expired,review_required,access_operation_id,timestamps}; CurrentPurchaseOrder.order nullable. Public access-operation output kind adds purchase, input does not.

- [x] Generate with `make -C backend generate` and `npm --prefix web run api:generate`.
  Expected: success, only additive generated changes.
- [x] Verify JSON references and unique operationId; preserve all previous paths, inputs and security. New customer writes require session/Origin/CSRF/key; callback is /webhooks/yoomoney.
  Expected: old API compatible, generated wire and web consumers agree.

### Task 2: Заказ и подписанные денежные факты

**Owner:** native backend, same owner as Task 3.
**Files:** backend/internal/platform/purchase.go, yoomoney.go, config.go and focused *_test.go; backend/internal/store source schema/queries + generated files; migration 15; backend/internal/httpapi/purchase.go, api.go and tests.
**Interfaces:** Service.PaymentMethods(ctx,accountID); CreatePurchaseOrder(ctx,accountID,key,wire.PurchaseOrderInput); PurchaseOrder(ctx,accountID,orderID); CurrentPurchaseOrder(ctx,accountID); CancelPurchaseOrder(ctx,accountID,orderID,key); OperatorPurchaseOrder(ctx,actor,target); ReconcilePurchaseOrder(ctx,actor,target,orderID,key,wire.PurchaseReconcileInput). Names may be adjusted within owner while HTTP contract remains fixed. ReceiveYooMoney(ctx,url.Values) is called only after strict form decoding.

- [x] Write `TestPurchaseOrderQuoteAndReplay` against actual test PostgreSQL: wrong price/owner fields rejected by HTTP, stale/hidden/archive/zero declined; 9007199254740993 minor units stay exact, lost response replays same UUID, concurrent keys have one open order, catalog edit preserves snapshot.
- [x] Write `TestYooMoneySignatureAndReceipt`: official public secret123 vector expects a452af731650e2c5b39abcdc7c28dd27db7b3b654c2230ad2c386e64afb98605; altered gross/extra signed field/duplicate key/SHA1-only/test notification cannot issue. Gross 100.00/net 98.00 accepts a 10000 quote. Receipt ID repeats once, second ID is retained without second access. Late in-window paid datetime accepted; after-window/canceled amount persists for review; cancellation/callback race loses neither.
- [x] Run focused tests before behavior exists and record actual RED cause. Implement exact parsing, session/ownership, immutable schema/constraints, file configuration, POST checkout and HMAC with stdlib; no provider API call during creation. Safe error codes, no raw/signature/person/payment-recipient logging.
- [x] Run `go test ./internal/platform ./internal/httpapi -run 'Test(Purchase|YooMoney)' -count=1` with TEST_DATABASE_URL_FILE/TEST_REDIS_URL_FILE from existing private test files.
  Expected: PASS, no skipped required integration tests. Money fact plus PurchaseArgs{OrderID} inserted in one transaction.

### Task 3: Подготовка и выдача оплаченного доступа

**Owner:** same native backend.
**Files:** backend/internal/platform/purchase_worker.go, access_worker.go, access_operations.go as needed, queue registration/bootstrap; focused integration tests; source query generation.
**Interfaces:** PurchaseArgs{OrderID uuid.UUID}, Kind purchase_fulfillment; PurchaseWorker calls Service.FulfillPurchase. Purchase operation references immutable order; AccessWorker checks paid provenance instead of an operator role for this kind only. Reconciliation before target retries preparation for the same order; after target uses existing access reconciliation and acknowledgement.

- [x] Write `TestPurchaseFulfillmentPreservesTrial`: new account creates one client; current finite trial expiry +30 days, expired trial now+30; UUID/sub_id/panel_key/assigned_panel/grant remain, new devices/profile/traffic apply and reset. Literal period/amount expectations independent of implementation.
- [x] Write `TestPurchaseFulfillmentCrashAndBan`: unavailable panel retains paid and retries; ban/current ownership/change or partial write becomes review; saved expiry survives worker restart; ambiguous reset is never repeated blindly. Purchase still succeeds without operator role, while old operator operations retain role guard.
- [x] Run RED, implement reuse of account-access ownership, immutable absolute target and existing AccessWorker. Payment never creates a fake trial grant or a customer-initiated operator action.
- [x] Run `go test ./internal/platform -run 'Test(Purchase|Access|Monthly)' -count=1`.
  Expected: PASS, one paid order -> one access operation; subscription/key reads applied target; no panel call while SQL tx held.

### Task 4: Покупка в кабинете и минимальный разбор подготовки

**Owner:** native frontend after Task 1; backend task independent behind stable API.
**Files:** web/src/Catalogue.tsx, new PurchaseOrder.tsx, api/client.ts, main.tsx, Cabinet.tsx, Admin.tsx/OperatorPurchase.tsx, AccessOperations.tsx, i18n.ts, style.css; web/tests/purchase.spec.ts and affected fixtures.
**Interfaces:** generated Task 1 schemas and exact new API paths; POST form action only https://yoomoney.ru/quickpay/confirm, hidden fields from checkout, real submit button; /orders/<uuid> returns to same order without treating return as money.

- [x] Write browser RED for confirmed quote, AC/PC choice, POST fields, exact amount, preserved key on lost response, foreign/expired/no-method/stale selection, reload/polling with paid-before-applied, cancel, RU/EN, keyboard and 375px. Route/intercept external form before any network transfer.
- [x] Implement typed wrappers, checkout confirmation, order page with bounded polling/cleanup and support; current order reachable after reload. Wrong external action never submits. Existing multi-currency catalog display works when checkout disabled; zero remains display-only.
- [x] Add operator read/preparation retry with required reason and fixed quote, and link to existing access operation reconciliation after target. Do not expose money override or refund controls.
- [x] Run `npm --prefix web run typecheck`, `npm --prefix web run test:e2e -- purchase.spec.ts` then affected existing suites.
  Expected: PASS without real provider requests, no browser errors or stale updates, disabled busy controls and visible focus.

### Task 5: Интеграция, native приёмка и доставка

**Owner:** coordinator; optional bounded native e2e specialist for evidence only.
**Files:** deploy/acceptance/Caddyfile and compose sources, deploy/purchase/local.py + README, sanitized verification docs, roadmap.

- [x] Route /webhooks/yoomoney publicly to backend and allow exact YooMoney form destination in CSP. Mount generated local secret file through existing declaration; disabled defaults for ordinary acceptance. Add minimal declared config to v2 deployment example if it exists.
- [x] Test helper behavior with controlled data; run owned HTTPS browser -> order -> signed local callback -> real Docker 3X-UI 3.7.0 -> subscription/key. Include both new and trial-conversion accounts, repeat callback, paid queue restart, current/canceled/test/tampered facts. Never send actual payment.
- [x] Before schema migration on owned acceptance stack, stop writers, backup, preserve old identities; apply migration then restore into separate owned database and inspect pending paid order/old access data. Expected: native local checks and restore PASS.
- [x] Generate again without diff. Run `go test -race ./... -count=1`, `go vet ./...`, web typecheck/build/full Playwright incl. connected API, and Python regression via bounded run-check wrapper.
  Expected: PASS, required database tests not skipped.
- [x] Fresh read-only gpt-6-astra/high whole-branch reviewer reads spec/plan/diff/evidence; fix material findings via focused RED->GREEN and full affected checks. Update roadmap with exact local boundaries; actual YooMoney transfer remains external verification.
- [ ] Inspect scoped diff, Conventional Commit with Co-Authored, verify SSH origin/source/base, push feature branch and create PR into v2; attach PR; wait required CI on exact source SHA. Expected: ready PR, no production or wallet setting changes and no auto-merge.
