# С22 — локальная приёмка промокодов

Source base: `e7a46c338420cb459cbf86fdae3bae9254ab9f86`, `origin/v2`.
Проверены изменения реализации, включённые в commit с этим документом.
Итоговый source SHA, результаты проверок этого SHA, CI и merge фиксируются
в [#48](https://github.com/ekho/3xui-shop/issues/48) и связанном PR.

Собственный Compose project с PostgreSQL/Redis, отдельные случайные test DB,
loopback ports и synthetic accounts. URL/session/CSRF находятся только в
private temporary files. Ни production, ни общие fixtures не использовались.

| Проверка | Результат и граница |
| --- | --- |
| Initial HTTP TDD | FAIL 404 на отсутствующем endpoint, затем management PASS |
| `go -C backend test -race ./db ./internal/modules/telegram ./internal/app -count=1 -timeout=4m` | PASS; полный DB suite, старые migration/rollback guards и module/SQL boundaries сохранены |
| `go -C backend test -race ./internal/httpapi -run '^TestPromocodes' -count=1 -timeout=2m -v` | PASS; lifecycle, replay/restart, changed-body/stale conflicts, current/revoked/restricted rights, CSRF/origin, audit |
| Activation race | PASS edit/delete × client/operator; account → promocode, used duration/link/history сохранены. Активационный SQL моделирует границу #49, grant не выполняется |
| Native Telegram | PASS; real accounts+bonuses+PG, synthetic Telegram update through local transport, dropped reply/restart/replay, revoked rights, one row/event/audit |
| Chromium API stubs | 4/4 PASS; RU/EN, empty/error states, keyboard/focus, uncertain retry, stale refresh, confirmed delete, used card |
| Native TLS browser | PASS `RUN_BROWSER_TESTS=1 go -C backend test ./tests -run '^TestPromocodesBrowser$' -count=1 -v`; real API/PostgreSQL/River, synthetic SMTP/account session, RU/EN CRUD/history/conflict/read-only legacy facts and client denial |
| Legacy/rollback | Positive duration beyond native limit, bigint IDs as decimal strings, nullable facts, retained used records/events and terminal deletion; empty downgrade/up succeeds, nonempty downgrade blocked |
| Static/contracts | `make -C backend generate`, web API generation, `go vet ./...`, typecheck/test-mode build and `git diff --check` PASS |
| Independent review | Read-only full diff and lock-order delta; actionable findings отсутствуют. Exact-HEAD confirmation перед merge |

Исправлена именно модель тестовой активации: обратный порядок promo → account
вызывал подтверждённый FK-deadlock при self-activation. Native owner уже берёт
account первым. Требование записано для #49 в canonical #48 comment
[2026-10-10-promocodes-lock-order-v1](https://github.com/ekho/3xui-shop/issues/48#issuecomment-6094624527).

## Ограничения

Клиентская активация и исполнение бонуса — #49; SQLite importer — #53.
Telegram transport локальный; живой Telegram и ручной screen reader не проверялись.
Реальные панели, внешние платежи и production не затрагивались. Vite сообщил
vendor directive/chunk-size warnings; build завершился успешно, оптимизация
существующего bundle не входит в С22.

Один principal Platform CI и Image(bot/backend/web) проверяют итоговый HEAD
перед manual merge. Разрешены только эффекты v2 preview (GHCR/prerelease);
production delivery и дополнительное ожидание postmerge preview отсутствуют
в DoD этой задачи. Cleanup удаляет только собственный Compose project.
