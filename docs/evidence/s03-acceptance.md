# С03: локальная приёмка профиля

Реализация: backend `f710935`, UI `41a2ade`. Первый реальный browser/native run
проверял runtime с этими изменениями при checkout `0a14733`. Расширенный native
driver завершился exit0. Общий Go-race и browser regression завершены на
`5c21d94`; source профиля/панели/Connection после native snapshot `0a14733`
не изменялся. Все8 AC локально приняты. Свежий whole-branch review не выявил новых
замечаний к профилю; обязательные support fixes общей ветки закрыты на `df34ef5`.
Текущий полный web64 GREEN; core профиля/панели неизменён. Команды/ревизии — в [сводном evidence](s03-s06-progress.md#итоговое-закрытие-локальной-приёмки).

| AC | Требование | Проверенная поверхность | Статус / граница доказательства |
| --- | --- | --- | --- |
| 1 | Собственный trial, upload/download, N/bytes/expiry | Реальный HTTPS cabinet/Mailpit, fixture approve, native3.7.0 и положительный трафик через собственный Docker VPN | PASS, native snapshot `0a14733` |
| 2 | none/provisioning/needs_review/expired/banned/disabled/exhausted | Playwright С03 и `TestProfileStates`; native none/active/expired/ban/disabled/exhausted | PASS; provisioning/needs_review rendering — controlled fixtures |
| 3 | Clamp/zero/unlimited/no-expiry отличимы от unknown | TLS boundary/membership fixtures, Playwright С03; native exhausted remaining0 и zero quotas/no-expiry с явными unlimited flags | PASS, fixture + native |
| 4 | Negative/overflow/foreign/unknown отклоняются, чужой key закрыт | `TestProfileTrafficBoundary`, `TestProfileOwnership`, `TestProfileMembership`; два реальных владельца с разными native keys и reciprocal guard | PASS; malformed data проверены TLS fixtures, без порчи native БД |
| 5 | Outage сохраняет весь cache/time, первый сбой не создаёт нули | `TestProfileCache`; фактический stop/start собственной панели сохранил полный snapshot/time, stale/key409 и fresh recovery | PASS; первый сбой без cache — fixture |
| 6 | Старое наблюдение не заменяет новое; GET не пишет panel/Grant | `TestProfileObservationOrder`, `TestProfileOlderReadUsesCurrentObservation`, GET-only TLS fixture, one Grant native readback | PASS; полный race suite `5c21d94` |
| 7 | ru/en/mobile/keyboard/retry/key privacy и С01/С02 | Реальная mobile browser поверхность; Playwright С01–С06, TTL/abort tests | PASS; полный browser64 `df34ef5` и Go/Python regression |
| 8 | Настоящая native3.7.0/browser, живой Happ исключён | Расширенный `node deploy/s04/browser.mjs`: exit0, positive up/down, native identity/limits/states/outage, прежний Docker proxy восстановлен | PASS; неизменность core source до `df34ef5` проверена |

Команды: `go test ./internal/s01 -race -count=1 -run Profile` с file-based
inputs тестовой PostgreSQL/Redis; `npm --prefix web run test:e2e -- s03.spec.ts
s04.spec.ts` (10/10); `node deploy/s04/browser.mjs` (exit0).
Нативный driver не запускает Happ, не меняет системный VPN/clipboard/trust Mac.
Fixture operator101 означает тестовый backend adapter, не реальную Telegram-доставку.

Подробные redacted records первого native run хранятся локально в
`.superpowers/sdd/2026-10-02-s03-s06/s03-s04-surface-evidence.md` и его двух логах.
Публичное доказательство — воспроизводимый driver и именованные focused tests;
секреты, identities, cookies и subscription URLs в этот документ не копируются.
Финальный native log: `s03-s04-native-browser-final.log`; независимый postflight
подтвердил прежние image IDs, pinned3.7.0 digest, HTTPS/panel/proxy health и VPN
файл0600. Runtime был освобождён для С06. Неуспешные попытки и исправления только
driver сохранены отдельно, их результаты не включены в PASS.
