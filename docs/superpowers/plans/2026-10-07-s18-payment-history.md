# С18 Payment History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking. Native выбран владельцем: координатор реализует три задачи, затем один fresh Astra/high reviewer проверяет всю ветку; один Critical/Important fix pass, Minor deferred, без re-review.

**Goal:** Клиент и оператор читают заказы, денежные подтверждения и сохранённый legacy-архив без финансовых или VPN writes.

**Architecture:** payments владеет history/archive и вызывает публичные accounts проверки. Два read-only POST используют действующие cookie/Origin/CSRF и fixed keyset pages. Один React-компонент обслуживает кабинет и карточку React-admin; текущие purchase workers/DTO/hash не меняются.

**Tech Stack:** Go/pgx/Echo/oapi-codegen/sqlc, PostgreSQL, React/TypeScript/Playwright, Python stdlib SQLite exporter. Новых dependencies нет.

**Spec:** [2026-10-07-s18-payment-history-design.md](../specs/2026-10-07-s18-payment-history-design.md), [#25 canonical v1](https://github.com/ekho/3xui-shop/issues/25#issuecomment-6029754500).

## Global Constraints

- Один Go-процесс HTTP/River/Telegram, один go.mod; public module operations, без чужих SQL/private пакетов.
- База c827f1944d543624309c93da6e55ad92871efaa7 от origin/v2; feature/s18-payment-history, без codex/, PR в v2.
- Существующие operations/DTO/quote hash и migrations 15–21 не меняются.
- Fixed limit 50; cursor created_at + ID, timestamp до µs; minor units/source IDs — строки.
- Import UTF-8 stdin до 32 MiB, raw payment ID/subscription до 16 KiB, context 2 min; whole Tx, dry-run read-only, exact replay без writes/audit.
- Legacy pending/completed/canceled/refunded сохраняются; fulfillment=unknown. Quote не является receipt, completed не доказывает выдачу.
- Название/домен только в конфигурации; ru/en, keyboard/screen reader и 375px.
- Собственные localhost данные/заглушки. Реальные деньги, публичный callback, production, живой Happ/VPN, Mac trust, внешние SMTP/Telegram не входят.

## Review Focus

- Tied timestamps и новая запись между страницами: ни повторов, ни пропусков старых строк; Task1 HTTP pagination test.
- Restricted/Telegram-only target и поздняя потеря роли: оператор читает только при актуальных правах, клиентский экран очищает данные; Tasks1/2 denied tests.
- Review/protected receipt, неизвестный net/currency и crypto decimal: UI не изображает их гарантированным зачислением; Tasks1/2 facts tests.
- Long Stars charge ID/unknown payload и источник с перемещённой identity: raw сохранён, mapping conflict атомарно откатывается; Task1 import test.
- Переключение kind/target во время запроса и failed load-more: старый ответ не смешивается с новой историей, retry сохраняет cursor; Task2 rendered race test.

## Task 1: Публичная история и контролируемый архив

**Files:** author docs/api/openapi.yaml; generate backend/internal/wire/models.gen.go и web/src/api/schema.gen.ts через существующие команды. Create backend/internal/modules/payments/history.go, legacy_history.go и legacy_history_test.go; migration backend/db/migrations/00022_legacy_payment_history.sql; HTTP backend/internal/httpapi/payment_history.go и payment_history_test.go. Modify backend/internal/httpapi/api.go для регистрации двух routes и backend/cmd/server/{import.go,import_test.go,main.go}. Create deploy/payment-history/export_legacy_payments.py и tests/test_legacy_payment_export.py.

**Interfaces:**

- payments.PaymentHistoryInput{Kind string; BeforeCreatedAt *time.Time; BeforeId *string}.
- payments.PaymentHistoryPage{Kind string; Orders []HistoryOrder; Receipts []HistoryReceipt; LegacyTransactions []LegacyPayment; HasMore bool}.
- Service.PaymentHistory(ctx context.Context,account uuid.UUID,in PaymentHistoryInput) (PaymentHistoryPage,error).
- Service.OperatorPaymentHistory(ctx context.Context,actor,target uuid.UUID,in PaymentHistoryInput) (PaymentHistoryPage,error).
- HistoryOrder: OrderId, Action, PaymentMethod, PaymentType, Quote PurchaseQuote, PaymentStatus, FulfillmentStatus, ReviewRequired, ReviewReason, AccessOperationId, CreatedAt, ExpiresAt.
- HistoryReceipt: OperationId, OrderId, PaymentMethod, CreatedAt, OccurredAt, GrossMinor, NetMinor, Currency, RawCurrency, Source, FundsOrder, ReviewRequired, ReviewReason, Codepro, Unaccepted; optional CryptoAmounts{PaymentAmount,PayerAmount,MerchantAmount,PayerCurrency}.
- LegacyPayment: SourceId, CreatedAt, UpdatedAt, PaymentStatus, FulfillmentStatus="unknown", nullable PaymentMethod и Quote LegacyQuote{Action,AmountMinor,Currency,Devices,PeriodDays,TrafficGb}.
- LegacyPaymentPackage{Version int; Users []LegacyPaymentUser; Transactions []LegacyPaymentTransaction}; user SourceLegacyUserID/SourceTgID, transaction SourceID/SourceTgID/PaymentID/Subscription/Status/CreatedAt/UpdatedAt. JSON names snake_case; CLI strict decoder and exporter emit this shape.
- Service.ImportLegacyPayments(ctx context.Context,p LegacyPaymentPackage,dryRun bool) (LegacyPaymentImportResult,error), result Users/Transactions/Inserted ints. Публичные accounts.LookupTelegram/LookupTx/Lock и auditreports.RecordTx; read-only Tx dry-run, sorted account locks apply.

- [ ] **1. RED:** Add runnable TestPaymentHistoryHTTP using existing httpFixture/supportLogin/supportRequest. Request three kinds before registration exists → actual 404, expected200. Assertions: arrays present, exact quotes/receipts/statuses, disabled provider settings do not affect read; 51 tied rows → 50 then1, new newer row never duplicates. Self/nonoperator/foreign target/restricted/revoked/Origin/CSRF/query/unknown fields/cursor boundaries.
- [ ] **2. Verify RED:** existing FILE-backed local DB/Redis, go test ./internal/httpapi -run '^TestPaymentHistoryHTTP$' -count=1 -race -timeout=5m. Save actual failing assertion and source revision privately.
- [ ] **3. Author/generate contract:** New input/page/item schemas and two stable operations/security/errors before handler implementation. Keep old schemas byte-semantics; make generate; npm run api:generate. Implement minimal owner query + narrow DTO projection/handler, parameterized cursor SQL, no publicPurchase/provider/native calls.
- [ ] **4. RED archive:** TestLegacyPaymentPayload exact known9-part RUB/USD/XTR + malformed/overprecision/unknown facts; TestLegacyPaymentImport connected DB assertions dry-run0, apply exact raw/IDs/UTCµs, replay0/noaudit, changed source/payment/identity whole rollback, no orders/receipts/jobs. Decoder TestDecodeLegacyPaymentPackage rejects invalid UTF-8/oversize/unknown/trailing; stdlib unittest export preserves all statuses and 100+char charge ID, explicit timezone, byte-identical SQLite before/after. Run new tests and record RED before archive/CLI/export implementation.
- [ ] **5. Implement archive:** new guarded migration22; same snapshot raw comparison and safe fail-closed errors. Existing minorUnits for legacy quote, no float or unsupported payload guesses; preserve unknown data. Controlled CLI flag dispatch and bounded decoder, no runtime startup. Stdlib read-only exporter uses source users/transactions, full rows with UTC dates, safe EXPORT_FAILED.
- [ ] **6. GREEN:** go test ./cmd/server ./internal/modules/payments ./internal/httpapi -run 'PaymentHistory|LegacyPayment' -count=1 -race -timeout=5m; python3 -m unittest tests.test_legacy_payment_export -v. Verify all typed arrays/crypto narrow fields/legacy unknown, exact import replay/rollback counts and no foreign ownership imports; git diff --check.
- [ ] **7. Commit:** feat(payments): add account history and legacy archive, Refs #25, Co-Authored-By: Codex <noreply@openai.com>. Link completed early HTTP/DB slice and bound source evidence in #25; delivery remains pending.

## Task 2: Один экран в кабинете и React-admin

**Files:** Create web/src/PaymentHistory.tsx и web/tests/payment-history.spec.ts. Modify web/src/{Admin.tsx,Cabinet.tsx,main.tsx,api/client.ts,i18n.ts,styles.css} only where needed; web/tests/operator-cabinet.spec.ts old payments placeholder assertion/mocks. Не создавать новый UI/store/framework.

**Interfaces:** Task1 generated schemas PaymentHistoryInput/Page. PaymentHistory({clientId?:string,onDenied?:()=>void}) chooses self/operator wrapper; wrappers getPaymentHistory(input) / getOperatorPaymentHistory(clientId,input) use existing request(sessionWrite=true)/cached CSRF. Load current account/session before self read; operator parent already obtains session. Existing displayPrice/BigInt formats known currency, unknown raw shown explicitly.

- [ ] **1. RED:** rendered self/operator history test before route/component exists. Assertions RU/EN at375px: separate payment/fulfillment, exact amount9007199254740993, nullable net/unknown currency/legacy quote, manual operator source, no raw payload/charge/checkout. Empty/error/retry; load-more cursor matches last row, failed append retains old page; focus/label/select/buttons keyboard.
- [ ] **2. Run RED:** npm run test:e2e -- tests/payment-history.spec.ts. Use existing test server/fakes; no real provider/browser payment.
- [ ] **3. Implement consumer:** /cabinet/history navigation/title, common native-select/list/dl/time component in cabinet/card. A query generation flag/AbortController clears and discards late target/kind/locale responses; 401/403 clears history and parent denial/login; bounded pagination append. Only self modern orders link existing order detail; archive no payment controls. Existing operator current purchase and promocodes placeholder remain.
- [ ] **4. GREEN:** npm run typecheck; npm run test:e2e -- tests/payment-history.spec.ts tests/operator-cabinet.spec.ts. Pin race where held old client response resolves after target switch/denied response; pin load-more failure/retry and clear-on-403. Only change legitimate old placeholder expectations.
- [ ] **5. Commit:** feat(web): show client and operator payment history, Refs #25 and Co-Authored; record actual rendered checks/current source in #25.

## Task 3: Локальная приёмка и evidence

**Files:** Create docs/superpowers/evidence/2026-10-07-s18-payment-history.md; update own plan checkboxes. Private own .superpowers/acceptance/c18-payment-history and SDD task records only. Product tests from Tasks1/2 cover reader/import; no speculative native VPN mutation matrix.

- [ ] **1. Acceptance prerequisites/event:** actual own local PG/Redis healthy, fake provider unavailable, controlled SQLite/identity mappings, no real wallet/prod/Happ. C04 early Task1 slice and dependencies verified; C05 roles/data/rights/denied/mobile transitions. Read shared #25 decisions before dependent continuation.
- [ ] **2. Owned restore/Down proof:** controlled own database with imported unknown/long raw data; pg_dump and separate read-only restored DB compare raw rows/IDs/dates/status/quote and order/receipt/job/audit counts. No writer starts against restored DB. UPDATE/DELETE guard and migration Down nonempty reject without data loss; empty archive Down may remove table. Record owned runtime/state and stop it after proof.
- [ ] **3. Current verification:** regenerate backend/web and prove no unstaged generator drift; make vet; make test-integration (-race -timeout=20m); npm run build and complete npm run test:e2e; existing Poetry Python test suite includes new stdlib exporter check. Run each once on current inputs; only failures/changed input justify repeat. Existing broader proofs remain historical on their revisions.
- [ ] **4. Evidence/commit:** actual commands, pass counts/times/source, RED→GREEN, rights/read-only/import/restore/cursor/UI checks, shared consumer links and limitations. docs: record payment history acceptance, Refs #25/Co-Authored. Implementation/local acceptance and delivery tracked separately.

## После трёх задач

Одна свежая Astra/high whole-branch read-only review (Task1–3 spec/plan/exact base/head/evidence), затем один Critical/Important fix pass. Каждое замечание — public ruling/причина/cost; Minor deferred, no re-review. Нужные changed-source проверки ограничены затронутым кодом. Push exact SSH/current source, PR в v2 без auto-merge; actual source CI gates, immediate SHA/base/parents/tree check, manual merge. Проверить actual v2 prerelease/tag commit, три OCI indexes и source/version labels обеих платформ. Только после actual delivery закрыть #25/Project Done; окончательная external/production приёмка остаётся С45–С47. Затем следующая готовая задача С19/#26, не запускать её параллельно.
