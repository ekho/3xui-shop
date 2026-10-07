# С33.Р4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans (Native).

**Goal:** обычный Telegram-триал активируется самостоятельно и выдаётся один раз.
**Architecture:** subscriptions резервирует существующий grant/River operation;
HTTP и общий Cabinet используют публичный контракт, Telegram открывает Mini App.
**Tech Stack:** текущие Go/pgx/PostgreSQL/River/Redis, React/TypeScript/Playwright.
**Spec:** docs/superpowers/specs/2026-10-08-s33-telegram-trial-design.md

## Global Constraints

- Native inline, один fresh Astra/high whole-branch review, один авторский
  Critical/Important RED→GREEN pass; Minor отложить, повторного review нет.
- Свежая origin/v2 → feature/s33-telegram-trial → PR/v2; Conventional + Co-Authored.
- Owned local fixtures, TLS 3X-UI 3.7.0; без production/денег/public callback/Happ.
- SourceKind определяет политику; grant/account lock сохраняют единственность.
- Существующий manual endpoint/DTO/operator hashes сохраняются.
- Неизвестный legacy trial-used не даёт автоматический триал; Р7 остаётся отдельно.
- Никаких новых зависимостей, worker/identity registry или чужого SQL.

## Review Focus

- Web+Telegram и TG+email выбирают первоначальную политику под актуальным lock.
- Replay не обходит актуальную auth/quarantine; другой ключ не резервирует заново.
- SQL constraint отличает настоящий manual actor от automatic provenance.
- При queue/outbox rollback и неизвестном panel результате сохраняется один grant.
- UI retry не переключает endpoint/ключ; bearer/CSRF сохраняются, ключ скрыт.

### Task 1: Eligibility и атомарная активация

**Files:** subscriptions/trial.go, service.go, queries/trials.sql и generated store;
db/migrations/00027_telegram_trial.sql; httpapi/telegram_trial_test.go (новый).
**Interfaces:** consumes accounts.Snapshot/SourceKind + vpn.ReserveTrialTx;
produces ActivateTelegramTrial(ctx,account,key)(TrialRequest,bool,error), TrialMode(snapshot) string.

- [x] RED: реальные PG тесты auto без оператора/replay/concurrent/rollback,
  исходный web deny, TG+credentials allow, legacy/restricted/disabled/used deny,
  pending/rejected manual не обходятся, SQL actor/Down факты сохранены.
- [x] Run `go test ./internal/httpapi ./db -run 'TestTelegramTrial' -count=1 -timeout=5m`.
  Expected: отсутствующая активация/автоматическое provenance.
- [x] Implement минимальный source guard, новый тип решения и reservation через
  decideTrialLocked; новый audit action, без approval_card и второго worker.
- [x] Verify тот же command с -race. Expected: PASS и старые facts сохранены.
- [x] Commit `feat(subscriptions): activate ordinary telegram trial atomically`.

### Task 2: HTTP-контракт и capability

**Files:** docs/api/openapi.yaml, wire/generated, httpapi/api.go,
accounts_mapping.go, mini_app.go, subscriptions_mapping.go; тесты HTTP.
**Interfaces:** consumes T1; produces POST /trials/activate JSON {}, cookie/bearer,
Origin/CSRF/Idempotency-Key; optional trial_mode=activate, прежний TrialRequest.

- [x] RED: подписанный Mini App/обычная session, реальные body/status/error,
  CSRF/bearer/Origin/idempotency/body guard; старый web JSON и manual request.
- [x] Run `go test ./internal/httpapi -run 'TestTelegramTrialHTTP' -count=1 -timeout=5m`.
  Expected: endpoint/capability отсутствует, конкретный assertion RED.
- [x] Edit owning OpenAPI, generate via make -C backend generate и
  npm --prefix web run api:generate; адаптировать три account response в одном helper.
- [x] Verify focused HTTP + прежние Trial/MiniApp/identity тесты с -race.
  Expected: PASS, неизвестные JSON/query поля отклонены, web shape сохранён.
- [x] Commit `feat(api): expose source-aware telegram trial activation`.

### Task 3: Общий кабинет

**Files:** web/src/Cabinet.tsx, api/client.ts, i18n.ts; web/tests/telegram-trial.spec.ts (новый).
**Interfaces:** consumes optional T2 mode; produces общую CTA и устойчивую retry-попытку.

- [ ] RED: ru/en auto CTA/no-comment, клавиатура/busy, lost response same-key retry,
  нужды поддержки/no second activation, legacy missing-mode manual форма.
- [ ] Run `npm --prefix web run test:e2e -- telegram-trial.spec.ts`.
  Expected: отсутствующая CTA/новый endpoint RED.
- [ ] Implement через существующие form/attempt/polling/error механизмы.
- [ ] Verify focused web + web-trial/mini-app, typecheck. Expected: PASS.
- [ ] Commit `feat(web): activate telegram trial in shared cabinet`.

### Task 4: Native-путь и итоговая приёмка

**Files:** backend/tests/native_telegram_trial_test.go (новый), существующие fixture
helpers при необходимости; docs/superpowers/evidence/2026-10-08-s33-telegram-trial.md.
**Interfaces:** consumes T1–T3, current-source module graph, existing workers/TG transport.

- [ ] RED integration: signed identity → actual HTTP activation → worker → owned
  3X-UI 3.7.0, остановка Telegram/whole-graph restart, needs_review/reconcile,
  единственные grant/op/identity; браузер использует реальный backend.
- [ ] Run focused `go test ./tests -run 'TestNativeTelegramTrial' -count=1 -timeout=5m`.
  Expected: наблюдаемая цепочка и отсутствие duplicate. Если T1–T3 уже покрыли
  поведение, явно записать GREEN integration, не выдумывать RED.
- [ ] Run full Go/race with RUN_BROWSER_TESTS=1/NATIVE_DOCKER_STATE, web e2e,
  Python suite, vet, deterministic generation. Expected: PASS без actual skips.
- [ ] Record фактические counts/limits/rulings; task-done подтверждает full Go.
- [ ] Commit evidence, один fresh final reviewer; один author fix pass если нужен.
- [ ] PR/v2, exact-source CI, manual merge, actual preview tag/images/configs;
  completion #31/Project Done только после фактических receipt.
