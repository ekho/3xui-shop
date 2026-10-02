# С06: план операторского кабинета

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Bounded backend/frontend specialists, coordinator integration/commits; one fresh Astra/high whole-branch review after С03–С06.

**Goal:** React-admin обслуживает web/Telegram-origin клиентов через один backend и один движок триала.
**Architecture:** Nullable source-aware account fields, explicit UUID web actor, existing role from С05. Existing Service/queue/idempotency reused; custom React-admin providers on cookie API.
**Tech Stack:** Go/Echo/pgx/sqlc/Goose/River, React-admin5.15.4/TypeScript/Playwright.
**Spec:** [С06](../specs/2026-10-02-s06-operator-cabinet-design.md).

## Global Constraints

- Backend starts after С05 store release; frontend after С05 web release. Coordinator freezes authored API before consumers.
- One UUID identity/Grant/worker; real Telegram ID/name, nullable web credentials, no fake actor/email.
- Existing24 operations retained; CLI role grant/revoke only verified unrestricted web identity, file input.
- Pagination50, q256, name128, reason1000 Unicode code points; typed SQL/body, session role/CSRF/Origin.
- No legacy SQLite writer/production/remote publication/live Happ; external TG-only cutover waits С46 readiness.

## Review Focus

- RF1: nullable credential fields cannot reach login/password/reset as empty password or generate fake email cards.
- RF2: role revoked between read preflight and action commit cannot authorize the action.
- RF3: bot/web concurrent decisions or lost HTTP response cannot reserve two grants or impersonate actor.
- RF4: no Telegram operators/adapter cannot block web request/decision/provision/notification bookkeeping.
- RF5: TG-only client passes source-aware worker eligibility; panel uncertainty preserves identity and prevents reissue.

### Task 1: Backend identity, actors and operator endpoints

**Files:** Coordinator OpenAPI/wire/TS. Backend create migration `00009_operator_clients.sql`, queries `operators.sql`, service `operator.go`, HTTP `operator.go`, focused tests; modify registration/session/credential/provision/trial/audit methods, main CLI and generated store. If Telegram payload becomes nullable, bounded bot formatter + its focused tests must be updated by backend owner.
**Interfaces:** OperatorSession(ctx,session account UUID), SearchClients(typed q/page/perPage), OperatorClient/Card/Key, Decide/Reconsider/Reconcile as real UUID actor, CreateTelegramTrial(...true TG ID/name/locale,key), Grant/RevokeOperator(file UUID). Existing internal TG callback methods remain compatible.

- [x] Coordinator freezes exact API schemas and generates; source-aware actor and entitlement from С05 reused.
- [ ] RED migration/auth tests: existing data constraints preserved; Telegram-only credentialsNULL, incomplete web rejects; login/reset no fake identity; role self-grant/revoke race denied.
- [ ] RED trial concurrency tests: web-only cfg.Operators=[] succeeds, bot/web same request creates one Grant/Operation/job with winning actor, lost response idempotent; duplicate Telegram creation/used/panel failure/source-aware worker covered.
- [x] Implement common actor-aware transaction core, typed search/card/history/key and CLI, preserving strict target and UUID; update native/bot fixtures only for changed contracts.
- [x] GREEN focused real-PG/Redis race/HTTP/migration tests, source generation/vet, bot formatter regression; report → coordinator local commit and ownership release.

### Task 2: React-admin

**Files:** Create `web/src/Admin.tsx`, `web/tests/operator-cabinet.spec.ts`; modify package/lock, api/client.ts, main.tsx lazy mount, i18n/style, Playwright discovery; reuse Support view API helpers from С05.
**Interfaces:** AuthProvider uses existing cookie login/logout/operator session; dataProvider typed search/card actions, no local JWT. Client support service exposes shared controls/data, no second chat model.

- [x] RED unauthorized direct admin/revoke, search/page/card/key hide, true actor decisions/new Telegram-only client; support text/file/state/ban/ack and errors; ru/en/mobile/keyboard.
- [x] Pin react-admin5.15.4 and implement minimum resources/providers/actions; show deferred financial data honestly, no fake counters. Preserve normal cabinet/auth routes and CSRF after reload.
- [x] GREEN typecheck/build, focused С06+С05 and affected earlier browser checks; report → coordinator local commit.

### Task 3: Full integration and acceptance

**Files:** Focused real browser/API driver under `deploy/`, `docs/evidence/s03-acceptance.md` through `s06-acceptance.md`, progress/roadmap/runbook.

- [x] Own Docker3X-UI3.7.0/Mailpit/PG/Redis preflight and rebuild; register actual operator/customer, file-based CLI role, browser search/support/approve/new TG-only trial with adapter stopped; key identity/one Grant/native target readback and own Docker VPN.
- [x] Real dump/restore verifies support blobs/roles/actors/TG-only identity; old session/proofs revoked, fresh owner login and original VPN retained.
- [x] Complete8 С03,5 С04,7 С05,8 С06 AC matrices with exact revisions/commands; run generated no-diff, full Go-race, web suites, Python and bounded native integration once after code is stable.
- [x] One fresh read-only whole-branch Astra/high review from0e2009e to current code; required findings fixed with focused RED→GREEN, broaden regression only for new changes.
- [x] Coordinator commits final evidence/progress and verifies goal achieved. No push/merge/deploy/release or production inference.

Итоговый статус: локальная реализация и приёмка завершены; один свежий
whole-branch review и обязательный RED→GREEN fix pass пройдены. Неподтверждённые
исторические RED steps оставлены неотмеченными; Ruling и точные результаты —
в [общем evidence](../../evidence/s03-s06-progress.md#итоговое-закрытие-локальной-приёмки).
