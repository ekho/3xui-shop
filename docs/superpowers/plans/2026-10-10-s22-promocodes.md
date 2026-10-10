# С22 — план управления промокодами

> Agentic execution: coordinator owns backend/integration/delivery, ordinary
> independent workers own web and Telegram adapters. Each reads the spec.

**Goal:** оператор безопасно создаёт, изменяет и удаляет неиспользованные
промокоды, сохраняя used records и историю.

**Architecture:** единый bonuses owner по `2026-10-10-bonuses-scaffold-v1`;
HTTP/Telegram адаптеры вызывают публичные операции. PG locks/guards, текущие
accounts rights, public audit port; без новой очереди/второго процесса.

**Tech stack:** Go/pgx/sqlc, PostgreSQL, Echo, React-admin, Playwright.

**Spec:** ../specs/2026-10-10-s22-promocodes-design.md.

## Ограничения и review focus

- #48 only; клиентская активация #49, referrals #50, import #53 отдельно.
- Миграция 00040, base v2; не менять другие worktrees/fixtures/production.
- После role revocation даже старый valid key не даёт operator доступ.
- Activation/edit/delete должны блокировать одну строку; direct SQL guard
  защищает used links и deleted state, rollback не теряет историю.
- Старый mutation response может быть stale: UI перечитывает текущую карточку.
- Сохранить исходный bigint legacy ID/nullable timestamps и activated flags.
- Generated/API/shared bonuses files интегрируются с сохранением #50.

## 1. Backend, coordinator

Files: backend/db/migrations/00040_promocodes.sql; db/promocodes_migration_test.go,
db/group_reconciliation_migration_test.go; internal/modules/bonuses/**;
internal/app/modules.go/boundaries_test.go; internal/httpapi/promocodes.go/
promocodes_test.go/api.go; docs/api/openapi.yaml; backend/sqlc.yaml и generated files.

- [x] Написать real HTTP management test; проверить FAIL 404 missing endpoint.
- [x] Ввести schema + sqlc + public owner operations по spec, strict OpenAPI
  DTO/routes и transactional audit. Generate contracts, не менять старые DTO.
- [x] Проверить lifecycle/replay/restart/rights и concurrent activation/role
  revocation на isolated DB, without panel/grant execution.
- [x] Добавить migration guard/legacy ID/link checks; старый 00039 guard
  предварительно достигает DownTo(39). Запустить весь db suite до CI.
- [x] Проверить owner import/SQL boundaries и vet/generation.

## 2. Web, отдельный worker

Ownership: web/src/AdminPromocodes.tsx/Admin.tsx/api/client.ts/i18n.ts;
web/tests/promocodes.spec.ts; web/playwright.config.ts. Coordinator generates
web/src/api/schema.gen.ts после OpenAPI изменения.

- [x] Начать с test empty/create/edit/delete/stale/uncertain/used/RU+EN;
  убедиться FAIL из-за отсутствующей страницы/поведения.
- [x] Внедрить существующий admin form/page pattern без новой зависимости.
- [x] Typecheck/build и focused Chromium test; сохранить keyboard/error focus.

## 3. Telegram, отдельный worker

Ownership: internal/modules/telegram/promocodes.go/promocodes_test.go/runtime.go;
internal/app/telegram.go, own integration test. Constructor общий не менять.

- [x] Test parser/actor/public operation boundary через локальный botapi HTTP.
- [x] Добавить minimal /promocodes,/promo,/promo_create,/promo_edit,/promo_delete
  commands и ConfigurePromocodes, wiring через Modules.Bonuses.
- [x] Проверить nonoperator/group/forward/foreign mention/replayed update;
  safe RU/EN outputs и ID-based audit без redeemable code.

## 4. Integration и доставка, coordinator

- [x] Собрать source и meaningful native fixture checks; exact revision фиксируется в PR/issue.
- [x] Независимый read-only review; findings отсутствуют, исправленная модель гонки проверена.
- [ ] Conventional Commit + Co-Authored-By, SSH push feature/**, PR в v2.
- [ ] Exact-HEAD principal Platform checks + backend/frontend/Python image checks.
  Не повторять широкие проверки без нового input/evidence.
- [ ] Fresh target/exact HEAD/gates/effects, manual merge; issue Closed,
  Project Done, удалить own fixtures, передать evidence родителю для archive.

Доставка и итоговый SHA/CI фиксируются в канонической #48 и связанном PR.
