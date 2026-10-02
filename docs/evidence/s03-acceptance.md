# С03: локальная приёмка профиля

Реализация: backend `f710935`, UI `41a2ade`. Первый реальный browser/native run
проверял runtime с этими изменениями при checkout `0a14733`. Расширенный native
driver завершился exit0; итоговый regression С03–С06 ещё выполняется, общий статус
**Pending**.

| AC | Требование | Проверенная поверхность | Что осталось |
| --- | --- | --- | --- |
| 1 | Собственный trial, upload/download, N/bytes/expiry | Реальный HTTPS cabinet/Mailpit, fixture approve, native3.7.0 и положительный трафик через собственный Docker VPN | Итоговая проверка после С06 |
| 2 | none/provisioning/needs_review/expired/banned/disabled/exhausted | Playwright С03 и `TestProfileStates`; native none/active/expired/ban/disabled/exhausted | Итоговый regression; provisioning/needs_review UI остаются controlled fixtures |
| 3 | Clamp/zero/unlimited/no-expiry отличимы от unknown | TLS boundary/membership fixtures, Playwright С03; native exhausted remaining0 и zero quotas/no-expiry с явными unlimited flags | Итоговая проверка после С06 |
| 4 | Negative/overflow/foreign/unknown отклоняются, чужой key закрыт | TLS panel fixtures `TestProfileTrafficBoundary/Ownership/Membership`; два реальных owner contexts с разными native keys и reciprocal guard | Malformed native data не вносится в живую БД панели; boundary fixtures остаются доказательством этих входов |
| 5 | Outage сохраняет весь cache/time, первый сбой не создаёт нули | `TestProfileCache`; фактический stop/start собственной панели сохранил полный snapshot/time, stale/key409 и fresh recovery | Первый сбой без cache остаётся fixture proof; итоговый regression |
| 6 | Старое наблюдение не заменяет новое; GET не пишет panel/Grant | `TestProfileObservationOrder`, `TestProfileOlderReadUsesCurrentObservation`, GET-only TLS fixture, one Grant native readback | Итоговый race regression после С06 |
| 7 | ru/en/mobile/keyboard/retry/key privacy и С01/С02 | Реальная mobile browser поверхность; Playwright С01–С05; клиентские TTL/abort tests | Итоговый общий regression |
| 8 | Настоящая native3.7.0/browser, живой Happ исключён | Расширенный `node deploy/s04/browser.mjs`: exit0, positive up/down, native identity/limits/states/outage, прежний Docker proxy восстановлен | Итоговая фиксация ревизии после С06 |

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
