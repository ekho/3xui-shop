# С13 — YooMoney: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Native выбран владельцем; один fresh whole-branch reviewer после задачи.

**Goal:** Завершить локальную приёмку готового YooMoney на заглушках и доставить доказательства в v2.

**Architecture:** Сохранить existing payments/HTTP/web/River/VPN. Добавить только недостающее connected HTTP acceptance coverage и документы; source proofs неизменённого native пути переиспользовать.

**Tech Stack:** существующие Go/Echo/pgx/River/PostgreSQL/Redis, Playwright, Docker3X-UI3.7.0.

**Spec:** [2026-10-06-s13-yoomoney-local-design.md](../specs/2026-10-06-s13-yoomoney-local-design.md).

## Global Constraints

- Owner #18, контракт2026-10-06-c13-local-stubs-v2; base5144fc26b68d9660914e09459849b00303914dbe, feature/c13-yoomoney-local от свежего origin/v2 после С11, PR→v2 без codex. Conventional Commit/Co-Authored.
- Только synthetic local money, никакого provider/wallet/production/Telegram/Happ/VPN/trust изменения. Не спрашивать повторно ресурсы для С13.
- Product/API/migrations/dependencies сохраняются; существующий адаптер не переписывать. Native доказательства reuse допустимы только при byte-equivalence продукта и доступных исходных успешных logs.
- Роли/quote/gross-net/funding/replay/queue/access/30min/FILE validation не упрощаются. True protected/held cases defensive, fees/minimum не утверждать.
- Python удаляется только С47; unknown legacy label200 не означает legacy migration. C19/C20 и С45–С47 остаются отдельными сценариями.

## Review Focus

- SHA1-only/test bypass: unsigned test не получает200 и не создаёт денег — новый HTTP boundary test.
- Malformed gross/unknown label: нет привязки или побочного funding; не назвать это сверкой legacy — тот же test + явная граница evidence.
- Wrong currency/type/net/held: подписанный факт retained для разбора, ни job, ни доступ — тот же test.
- Conflicting operation ID: первый receipt неизменен, подготовка блокирована, повтор не выдаёт второй доступ — новый conflict test.
- Reuse прошлых native proofs: exact product equivalence, source logs, UI mocked/native real и limits разделены — собственный verifier/evidence, без повторения неизменённой26-stage матрицы.

### Task 1: HTTP stub coverage и собственная приёмка

**Files:** Modify backend/internal/httpapi/purchase_test.go. Create docs/evidence/s13-acceptance.md. Reconcile delivery facts в docs/evidence/s11-acceptance.md, docs/superpowers/plans/2026-10-06-s11-manual-payment.md, docs/roadmaps/2026-10-01-platform-roadmap.md. Два текущих spec/plan документа сохраняют принятый scope.

**Interfaces:** Consumes existing purchaseFixture/purchaseNotice/New(app.NewModules)/supportRequest и public payments operations, прежние logs26-stage product d407ad3. Produces two connected HTTP acceptance tests and criterion/equivalence report; новых public signatures нет.

- [x] **Step 1:** На actual handler/изолированной PG/Redis через existing fixtures добавить TestYooMoneyHTTPReceiptBoundary и TestYooMoneyHTTPReceiptConflict, точные assertions AC02–AC04. Поведение уже реализовано: сначала проверяется существующий код, искусственный RED не создаётся; любой выявленный дефект требует отдельного RED→GREEN fix.
- [x] **Step 2:** `go -C backend test -race ./internal/httpapi ./internal/modules/payments -run 'TestYooMoney|TestRegression(Purchase|YooMoney)|TestManualPayment.*CrossMethod' -count=1`. TEST_DATABASE_URL_FILE/TEST_REDIS_URL_FILE только собственные protected files. Expected all selected tests PASS, no skip; сохранить full log.
- [x] **Step 3:** Проверить equivalence native product/API/migrations/deps к product d407ad3 исключая docs и новые tests; проверить26 успешных source records/logs и Native export SHA256 manifest. Зафиксировать AC01/AC05 из прежних доказательств и actual С11 delivery, сохранив точные уровни/ограничения. Expected production diff empty, доступные логи, шесть AC mapped.
- [x] **Step 4:** Проверить `git diff --check`, semantic names и spec/plan self-review; commit tests/evidence/docs. `task-done` запускает final evidence verifier, а не повтор неизменённых suite/build/native. Expected proof/text/source consistency PASS.

## Finish

- [x] Один fresh Astra/high read-only reviewer всего диапазона + Review Focus + ledger. Critical/Important — один fix pass с RED→GREEN и green affected suite; без re-review. Каждый Declined-to-judge → Final ruling/cost, minors deferred.
- [x] Опубликовать все rulings/minors в evidence, сохранить проверенный export и удалить только собственный SDD scratch.
- [x] Exact-source CI и ручной v2 merge под source guard; actual parents/source-equal tree + действительный preview/tag/3indexes/6labels проверить. С11 source-equivalent delivery отражена отдельно от С13.
- [x] Только после собственных AC/source/delivery #18 CLOSED/Project Done; затем следующая готовая задача. Реальную доставку/legacy/production не считать проверенными.

Self-review: один cohesive task; AC01–06/Review Focus mapped, signatures existing,
неизменённые expensive checks не повторяются. Единственная новая executable
граница — actual HTTP signed/unsigned receipt tests, выполняемые со связанными
purchase/cross-method regressions. План принят по автономному mandate.

Delivery checkpoint: PR73/source d6087cf/merge d7b69eb/dev.35/#18 CLOSED/Project Done; proof и ограничения в docs/evidence/s13-acceptance.md.
