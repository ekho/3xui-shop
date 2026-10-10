# С42 Maintenance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development with the repository placement policy; bounded backend, frontend and native acceptance owners. The coordinator owns integration and publication.

**Goal:** Довести #44 до проверенного PR в `v2` с одним постоянным режимом для web/Mini/Go Telegram.

**Architecture:** Публичная операция operations владеет состоянием и переключением. Owning modules проверяют admission новых операций после replay. Деньги и recovery продолжаются. React использует canonical API.

**Tech Stack:** Go, PostgreSQL/pgx/sqlc, River, Redis, OpenAPI, React/React-admin, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-09-s42-maintenance-design.md`.

## Global Constraints

Один Go процесс/go.mod; operations не переписывает PR97 lifecycle. 00037 занята PR96: использовать 00038. Только текущий worktree, branch `feature/s42-maintenance`, base `9305fa5b6a5dec44648aa9a57bb1501164e455f6`, PR base `v2`. Никаких production/release/deploy/merge/issue close или сообщений другим чатам. Только synthetic/stub и собственные fixtures. Не урезать проверки. Conventional Commits + Co-Authored-By.

## Review Focus

* Lost reply и replay после обратного переключения не возвращают старое состояние в БД.
* Права отозваны между чтением и записью: protected action отказывает.
* UI устарел/другой процесс переключил режим: сервер сохраняет границу.
* Уже подтверждённый платёж, recurring charge, компенсация и River не блокируются.
* Ошибка хранилища не выглядит как выключенное обслуживание и не теряет money update.

### Task 1: Backend / data / contract / Telegram

Owner: backend specialist. Files: operations/maintenance и owned SQL, `00038_maintenance.sql`, `backend/sqlc.yaml`, app composition, HTTP API, owning payments/subscriptions admission, Telegram client messages, OpenAPI и generated Go/SQL/TS. Не трогать web product files.

Interfaces: `operations.Maintenance` exposes current status and protected idempotent set; status `{enabled:boolean, revision:integer, changed_at:date-time|null}`. POST input `{enabled:boolean, expected_revision:integer, reason:string, confirmed:true}`. Admission port obtains allowed/error without leaking operations SQL. Endpoints and statuses — spec.

- [ ] Red: focused owning tests for default/set/replay/stale/revoked role/no-op/multi-instance and admission/confirmed money bypass.
- [ ] Implement canonical contract first; `make -C backend generate`, `npm --prefix web run api:generate`.
- [ ] Implement singleton transaction + public audit API, auth/Origin/CSRF adapter, replay-aware module admission, Telegram ru/en.
- [ ] Green: `go -C backend test ./internal/modules/operations ./internal/httpapi ./internal/app ./internal/modules/telegram ./db -count=1`, with owned TEST URL files; no false whole-suite claim.
- [ ] Report exact tests/results and scope to coordinator; no commit/push.

### Task 2: Frontend

Owner: frontend specialist. Files: `web/src/Maintenance.tsx`, `Admin.tsx`, `main.tsx`, `api/client.ts`, `i18n.ts`, affected Cabinet/Catalogue/PurchaseOrder/StarsSubscription and focused Playwright tests/config. Generated TS belongs Task 1.

Consumes endpoints/status/input from Task 1. New requests are blocked by server, so UI is informative and refreshes mode; no client-only enforcement. Separate operator Resource with reason/confirmation/retry/conflict/permission loss.

- [ ] Red: rendered Playwright cases ru/en toggle, uncertain reply same key, conflict refresh, role loss, banner and blocked controls, keyboard/labels/alerts.
- [ ] Minimal UI using existing controls/error patterns; no new dependency.
- [ ] Green: `npm --prefix web run typecheck`, `npm --prefix web run build -- --mode test`, focused e2e then normal suite. Use a unique browser port.
- [ ] Report exact results; no commit/push.

### Task 3: Native boundary acceptance and integration

Owner: native acceptance specialist for `backend/tests/native_maintenance_test.go` and its browser fixture; coordinator for own environment/runbook/evidence/publication. Acceptance specialist changes no product code.

- [ ] Real assembled HTTP/River/Telegram with synthetic web+Mini and payment/Bot API/panel stubs, using existing native fixture APIs.
- [ ] Enable, native client state, stale client write blocked, existing payment confirmed/repeated during mode, single fulfillment through restart; operator continuation/disable; stored identity/data preserved.
- [ ] Render actual browser ru/en mode, keyboard/accessible status and operator disable; no mock-only acceptance claim.
- [ ] Coordinator runs canonical generation cleanliness, full Go race/vet, all web and Python checks, own native checks; bounded hypotheses for failures.
- [ ] Independent whole-branch Astra/high review; fix/reverify touched scope; write `docs/evidence/s42-maintenance-acceptance.md` and `docs/runbooks/s42-maintenance.md`.
- [ ] Verify SSH branch/upstream; commit/push, PR `--base v2`, attach artifact, wait for all CI on final exact HEAD, mark ready. No merge/auto-merge/close/archive/deploy.
