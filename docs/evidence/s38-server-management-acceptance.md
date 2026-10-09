# С38 — локальная приёмка

Задача [#42](https://github.com/ekho/3xui-shop/issues/42), owner `vpn`,
контракт `2026-10-09-s38-server-management-v1`.
[Спецификация](../superpowers/specs/2026-10-09-s38-server-management-design.md),
[план](../superpowers/plans/2026-10-09-s38-server-management.md),
[runbook](../runbooks/s38-server-management.md).
База `9305fa5b6a5dec44648aa9a57bb1501164e455f6`; ветка
`feature/s38-server-management`. Слияние/Done/архивирование выполняет родительский
координатор, отдельно от локальной реализации.

## Проверенная граница

- Отдельное право подтверждённого web-оператора: file-only grant/revoke CLI,
  deny обычному оператору, session/Origin/CSRF и fresh private Telegram proof.
- Список/карточка/create/ping/sync/delete вызывают публичные операции `vpn`;
  совместимые добавочные DTO/OpenAPI/Go/TS и отдельная ru/en web-навигация.
- Atomic mutation/action/audit, исходный безопасный snapshot на replay,
  конфликт изменённого входа; host, provider body, пароли и VPN-ключи не в audit.
- Assigned/reserved/nonterminal trial/access targets/provider clients/offline
  запрещают delete. Общая pool lock до и после сети; tombstone/revision
  препятствуют восстановлению старым наблюдением. Нет editor или client remap.
- Миграция 37 проверяет пустой Down/Up и отказ Down при сохранённых фактах.

## Проверки

Фокусный race-прогон db/accounts/vpn/telegram/httpapi/cmd завершён PASS;
покрывает права, повторы, audit rollback, legacy encoded IDs и все delete guards.
Новые web-тесты: 8/8 PASS; соседние operator/campaign: 33 PASS. Typecheck/build,
ветка naming check, vet и Go/SQL/TS generation со сравнением SHA256 — PASS.
Python с тестовыми URL-файлами: 112/112 PASS; полный web: 549/549 PASS (4.8m).
До исправлений ревью эти результаты подтверждали существующие потребители.
После исправлений полный web: **551/551 PASS (4.6m)**; шесть целевых Go-пакетов
(db/accounts/vpn/telegram/httpapi/cmd) с race: **6/6 PASS**, включая новые primary,
concurrent delete и slow-panel regressions. Vet, naming, runtime config и
штатная generation с SHA256 drift check повторно PASS. Полный Go-прогон первой
реализации завершился FAIL: новый Telegram bridge отвергал существующий trial-only
режим без web-origin; backup/restore тест выбирал защиту отката пула через последнюю
миграцию, которой теперь стала 37. Настройка bridge ограничена существующим полным
режимом, HTTPS-проверки сохранены. Restore-тест явно выбирает версию 36, сохраняя
проверки резерва, запрета отката и всех восстановленных данных. Целевые lifecycle,
native trial и backup/restore race-тесты после исправлений PASS. Новый полный
Go-прогон и exact-source CI остаются обязательными финальными gates. Их итог,
полный текущий HEAD и статус PR фиксируются в финальной записи задачи #42,
без изменения проверяемого исходника.

Собственный fixture `deploy/server-management` использует pinned 3X-UI 3.7.0,
две панели localhost 61444/61449 и TLS CA, отдельные private DB directories,
cap-drop/read-only containers и явную непересекающуюся Docker subnet.
Свежий compiled `cmd/server`, реальные HTTP handlers и Chromium без route mocks:

```sh
TEST_DATABASE_URL_FILE=/tmp/3xui-s38-test/database-url \
TEST_REDIS_URL_FILE=/tmp/3xui-s38-test/redis-url \
SERVER_MANAGEMENT_NATIVE_STATE=/tmp/3xui-s38-native RUN_BROWSER_TESTS=1 \
go -C backend test -race ./tests \
  -run 'TestServerManagementBrowser|TestNativeServerManagement' -count=1 -timeout=10m -v
```

Первый прогон двух тестов PASS (19.354s): фактические add/list/ping/sync/delete на второй панели,
primary ping, role deny/replay/conflict/assigned409, рестарт с точным snapshot
accounts/registry/action/audit/targets/reservations/grants/roles и сохранённый
tombstone. Браузер проверяет EN keyboard/add/ping, отсутствие mutation до
подтверждения, появление назначения между подтверждениями → 409 alert, RU labels.
Исторические private paths/ресурсы сохранены; authored source использует
семантические имена. Пароли, cookies, tokens и provider response не публикуются.

## RED/GREEN и ограничения

Сначала connected-тест занятого сервера при offline provider получил 503 вместо
409; core исправлен проверкой backend facts до сети и повторной проверкой под
lock. Encoded legacy IDs и browser absence/validation/error focus получили свои
RED→GREEN проверки. Snapshot replay покрыт regression GREEN; отдельный literal
RED для этой поздней проверки не зафиксирован.

Ранний browser fixture обнаружил реальный дефект первой операции: пустой реестр
возвращал primary из конфигурации без строки в БД. Ping обновлял ноль строк и
возвращал online=false, хотя прямой PanelClient успешно читал inbound. Permanent
compiled HTTP RED до первого create подтвердил rows=0/revision=0 до и после ping.
Прежний native PASS делал create раньше, поэтому уже имел сохранённый primary.
Ревью выявило тот же риск для первого delete: успех без сохранённого tombstone.
Эти два пути, конкурентный delete с разными ключами, бюджет sync медленных панелей
и устаревшие can_delete флаги открытой web-карточки исправляются одним проходом.
После исправлений exact connected selection расширен до
`TestNativeServerManagementPrimaryRetirement|TestNativeServerManagement|TestServerManagementBrowser`:
**3/3 PASS, 35.681s**. Во всех трёх свежих `cmd/server` SHA256
`1d4fcdfde1af10698a73e9914fa7d4447a617c1462591041768125d9d005ce03`.
Первый ping до create сохраняет online observation; первый delete сохраняет
только один tombstone и action/audit, sync и restart его не оживляют, повтор после
restart возвращает исходный snapshot. Прямой provider probe использовался только
для безопасной диагностики RED; успешный основной путь проходит через реальный HTTP.
Локальные browser TLS handshake diagnostics не означают выключенную проверку CA
в PanelClient; системное доверие macOS не менялось. Fixture up/check/down PASS.

Один fresh whole-branch reviewer Astra/high: четыре Important finding, затем
один ограниченный проход исправлений и повторное ревью тем же агентом. Итог:
**Critical 0, Important 0**, ReadyForDelivery при выполнении проверок координатора.
Backend first-ping/first-delete/concurrent-key/slow-panel получили RED→GREEN;
concurrency/deadline regressions дополнительно прошли `-count=3`. Web два новых
RED→GREEN проверяют оба перехода can_delete и закрытие открытого подтверждения.
Последнее форматирование CLI test — только gofmt. Все 607 прежних Go Test entrypoints
сохранены (один расширенный CLI test переименован, существующие assertions сохранены).

Production, внешний Telegram, реальные клиенты/VPN-трафик и выпуск исключены.
Provider-only clients/offline/stale sync guards проверены core tests; отдельный
реальный browser отказ из-за provider-only клиента не заявлен. Конфигурация
panels и ручная выдача узкого права остаются ответственностью владельца окружения.
При исчерпании общего probe budget sync возвращает 503 без частичной записи;
ограничение относится и к большому числу медленных панелей. Локальный общий smoke
на 58443 не запускался: порт занят чужим `cabinet-native`; readiness образов и этот
smoke проверяет source CI в отдельном runner. Свой loopback fixture прошёл teardown.
