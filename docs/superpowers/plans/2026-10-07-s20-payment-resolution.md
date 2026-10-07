# С20 — Payment resolution implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Native выбран пользователем; координатор реализует, затем один свежий whole-branch reviewer.

**Goal:** Оператор разбирает любой заказ и фиксирует полный внешне подтверждённый возврат без потери денежных фактов и автоматического изменения доступа.

**Architecture:** Payments владеет журналом возвратов и общим funding guard. VPN прекращает только дальнейшие попытки конкретной purchase-операции через публичный метод под существующим account-access lock. HTTP и React используют эти операции.

**Tech Stack:** Go, Echo, pgx/PostgreSQL, River, существующий React/React-admin/Playwright; новых зависимостей нет.

**Spec:** `docs/superpowers/specs/2026-10-07-s20-payment-resolution-design.md`, версия `2026-10-07-s20-v1`, владелец #27.

## Global Constraints

- Один Go-процесс HTTP/River/Telegram; публичные операции модулей, без чужого SQL/private.
- Ветка от `76bfe63fbebd8fa3b2b49a5280dfe7ac83c41894` (`origin/v2`); PR в `v2`, без codex prefix.
- Настоящая локальная 3X-UI **3.7.0**; собственные fixture, без внешних переводов/Telegram/SMTP/live Happ/production.
- Источник возврата всегда `operator`; полный возврат, исходные валюта/суммы, никаких float/автовычитаний дней/провайдерских выплат.
- Один fresh Astra/high final review; Critical/Important — один author RED→GREEN fix pass; Minor — deferred; без re-review.
- RU/EN, строгие входы, права/Origin/CSRF/audit/idempotency обязательны.

## Review Focus

- Подтверждение funding и extra receipt: отменяется только соответствующая незавершённая выдача; неизданный полностью возвращённый заказ не блокирует новый.
- Callback после возврата и новый отдельный late receipt: первый сохраняет одну историю, второй не считается уже возвращённым.
- Сумма больше безопасного JS integer, неизвестный provider net, криптокомиссия: исходные факты сохраняются, conversion не появляется.
- Смена клиента/выбранного поступления во время ответа и timeout: данные/согласие не переносятся, неизменный повтор использует прежний key.
- Активный account-access executor, отозванная роль и прямой UPDATE/DELETE журнала: защищённая запись отказывается без частичного commit.

---

### Task 1: API, журнал и безопасное прекращение выдачи

**Files:**
- Modify: `docs/api/openapi.yaml`, generated `backend/internal/wire/models.gen.go`, `web/src/api/schema.gen.ts`.
- Create: `backend/db/migrations/00023_purchase_refunds.sql`, `backend/internal/modules/payments/refunds.go`, `backend/internal/httpapi/payment_resolution.go`, `backend/internal/httpapi/payment_resolution_test.go`.
- Modify: payments `contracts.go`, `purchase.go`, `renewal.go`, `history.go`; VPN `owner.go`; HTTP `api.go`, `payment_history.go`, `payments_mapping.go`; `backend/internal/app/boundaries_test.go`.

**Interfaces:**
- Produces: `payments.OperatorPaymentCase(ctx, actor, target, order uuid.UUID, receipt *string) (PaymentCase,error)`, `ConfirmPurchaseRefund(ctx, actor,target,order,key uuid.UUID,in PurchaseRefundInput) (PaymentRefund,error)`.
- Produces: `vpn.RetirePurchaseAccessTx(ctx,tx pgx.Tx,owner *AccessOwner,account,order,operation uuid.UUID) error`; требует фактический owner lock и ту же connection/Tx.
- Consumes: existing `LockOperatorPair`, `replay/saveIdempotency`, `purchaseFundingCheck`, `HistoryReceipt`, audit `RecordTx`.

- [ ] Step 1: Record canonical #27 choice + dependent references and C02/C03 compatibility. Edit owning OpenAPI first; generate through repository commands.
  Run: `make generate` (backend), `npm run api:generate` (web). Expected: valid additive generated types; no unrelated churn.
- [ ] Step 2: Add `TestPaymentResolutionHTTP` using actual sessions/role/order/receipt: case=200, refund=201, money unchanged, funding future guard denied, one replay/audit, conflict/403/404/invalid crypto.
  Run: `go test ./internal/httpapi -run TestPaymentResolution -count=1`. Expected RED: new HTTP path missing (404 rather than 200), no fixture failure.
- [ ] Step 3: Implement immutable ledger, read case and write confirmation; public VPN retirement and common funding guard. Extend history and current-purchase policy for financially closed records. No new payout worker or provider network call.
- [ ] Step 4: Extend same focused suite for all five methods, extra/late receipt, active owner, two keys, append-only DB/Down guards, current purchase eligibility; existing manual/recovery/history regressions.
  Run: `go test ./internal/httpapi ./internal/modules/payments ./internal/app -run 'TestPaymentResolution|TestPaymentHistory|TestPurchase|Test.*SQLBoundary' -count=1`. Expected GREEN, 0 skip.
- [ ] Step 5: Commit `feat(payments): record confirmed refunds and resolve payment cases` with Co-Authored-By; task-done runs the focused command above.

### Task 2: Карточка оператора и история возвратов

**Files:**
- Create: `web/src/PaymentCase.tsx`.
- Modify: `web/src/PaymentHistory.tsx`, `web/src/OperatorPurchase.tsx`, `web/src/Admin.tsx`, `web/src/PurchaseOrder.tsx`, `web/src/Catalogue.tsx`, `web/src/api/client.ts`, `web/tests/payment-history.spec.ts`, `web/tests/purchase.spec.ts`.

**Interfaces:**
- Consumes: generated `PaymentCase`, `PaymentRefund`, `PurchaseRefundInput` and Task 1 endpoints.
- Produces: `PaymentCase({clientId,orderId,receiptId?,lang,onDenied,onChanged})`; optional explicit `orderId` input for existing `OperatorPurchase` rather than a second money-decision implementation.

- [ ] Step 1: Add browser scenario opening an old receipt, confirming refund, reading client history; assertions cover RU/EN, explicit consent, client switch/late response, denied role and timeout idempotency.
  Run: `npm run test:e2e -- --grep 'payment resolution'`. Expected RED: selected receipt action missing.
- [ ] Step 2: Add typed API wrappers, selected case and native labelled form; reuse manual decisions/recovery, abort ownership-stale calls and refresh history after success. Add `refunds` tab and fully_refunded copy to client purchase.
- [ ] Step 3: Run browser-focused tests and `npm run typecheck`/`npm run build`. Expected GREEN and no mobile/keyboard ownership failures.
- [ ] Step 4: Commit `feat(web): expose payment cases and confirmed refunds`; task-done runs focused browser command.

### Task 3: Локальная приёмка и доставка

**Files:**
- Modify: owning native purchase fixture/script only if needed for a runnable refund/restart scenario; create `docs/superpowers/evidence/2026-10-07-s20-payment-resolution.md`.

**Interfaces:**
- Consumes: Task 1 financial guards + Task 2 operator flow, existing own Docker native acceptance with 3X-UI 3.7.0.
- Produces: redacted proof of preserved panel identity/expiry/limits/traffic, stopped subsequent writes across two actual restarts and exact source checks.

- [ ] Step 1: Add failing own native scenario: refund a funded unresolved order, restart twice, assert no later panel write/financial loss; applied access remains unchanged.
  Run: owning native Docker acceptance. Expected RED if guards absent, GREEN against final implementation; retain actual result and mark external YooMoney untested.
- [ ] Step 2: Run generated drift, Go race + browser suite, web typecheck/build, Python regression, architecture/contract/static and required container/native checks against exact inputs. Expected GREEN without secret leaks or skips for required scenarios.
- [ ] Step 3: Commit redacted evidence, complete Native ledger, request one fresh Astra/high final review. Important fixes receive one author RED→GREEN pass and a green whole suite; Minor deferred in evidence. Expected no unaddressed Critical/Important.
- [ ] Step 4: Explicitly push own branch, create/attach PR to v2, verify actual checks/source/target immediately before authorized merge. Verify merged tree, v2 CI, dev release and both OCI architectures; close #27/Project Done only after all local DoD. Continue next ready roadmap item.
