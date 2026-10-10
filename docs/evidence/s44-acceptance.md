# С44: локальная приёмка процесса и оповещений

Дата: 2026-10-09. Задача: [#46](https://github.com/ekho/3xui-shop/issues/46).
Ветка `feature/s44-process-operations`, база `9305fa5b6a5dec44648aa9a57bb1501164e455f6`.
Контракт `2026-10-09-s44-v1`; [spec](../superpowers/specs/2026-10-09-s44-process-operations-design.md),
[plan](../superpowers/plans/2026-10-09-s44-process-operations.md),
[операторская процедура](../runbooks/s44-process-operations.md).
Точный HEAD, результаты CI и передача координатору фиксируются в PR.
Merge, закрытие issue/Project Done, release и production сюда не входят.

## Среда

Собственный Compose project `cabinet-process-l51zh3e2`, подсеть
`172.30.146.0/28` проверена на отсутствие пересечений. Из-за исчерпанного
автоматического Docker address pool подсеть задана явно; чужие сети не удалялись.
PostgreSQL/Redis и loopback-порты уникальны. Go testkit создаёт UUID-базы внутри
своего PostgreSQL; compiled backend использует отдельную `process_runtime`.
SMTP — pinned Mailpit с собственным self-signed CA, TLS и тестовым адресом.
HTTPS Caddy использует изменённый repository Caddyfile; frontend-образ взят из
уже собранного локального fixture, без изменений frontend-кода.

Backend собран из актуального дерева после review fixes:
OCI image index/runtime image ID
`sha256:bc6a263bd5469146258ea1afea4425f3480e766bab231ca1c426404178ffdec0`;
build config digest
`sha256:26b42de8a7da6ab9446fbdc122435d5595798de7b916f8d2ded6415906c033e5`.
Контейнер scratch запускает `/server` непосредственно, без shell/curl/supervisor.
`cabinet-native`, `cabinet-test`, стенд #42 и основной checkout не изменялись.

## Проверки

| Проверка | Результат и граница доказательства |
| --- | --- |
| Go race: новые operations/config/readiness/healthcheck, Telegram state/recovery/polling, native lifecycle, старый `/healthz`, forced HTTP/River cleanup | PASS по пяти затронутым пакетам, 21.147s. Фильтр перечислен ниже; это focused matrix, не полный локальный `./...`. |
| Telegram startup state/recovery отдельным race-прогоном | PASS, 4.575s. Ошибки Telegram и восстановление simulated Bot API; внешних запросов к Telegram нет. |
| Медленный настоящий River job держит PostgreSQL connection, graceful timeout → hard cancel | PASS под `-race`; connection освобождён, timeout/error наблюдаем. Дополнительно worker повторил concurrent reproducers трижды. |
| Generated contract/SQL drift, `go vet ./...`, `git diff --check`, application-name check | PASS. OpenAPI, generated transport/SQL, go.mod/go.sum и схема не изменились. |
| Base/native Compose parser + resolved JSON | PASS. Проверены обязательные 10 backend secrets, `/server healthcheck`, grace 75s, native overlays. Parser без проверки resolved secrets недостаточен. |
| Compiled `/server healthcheck`, HTTPS `/healthz` и `/readyz` через Caddy | PASS; success только `{"ok":true}`, `no-store`. Probe не требует registration credentials. |
| Остановка своего Redis → восстановление | PASS: `/readyz` 503 и probe nonzero; `/healthz` 200 и HTTP жив; после startup Redis readiness 200. |
| Остановка своего PostgreSQL с сохранённым volume → восстановление | PASS: health/readiness 503, probe nonzero; после startup PG readiness 200. |
| Настоящий TLS SMTP | PASS: письма backend ready/stopping/stopped приняты собственным fixture. Только безопасные module/state/code. |
| SMTP down + настоящая регистрация + stop/start backend | PASS: регистрация 202 и один persisted River mail job; readiness остаётся 200. Graceful stop 0.251s; state/logs HTTP/River/schedulers stopped. После restart прежний challenge/job сохранён, исходное письмо доставлено ровно один раз. |
| Повторный same-image Compose restart | PASS: readiness восстановилась, новый ready и shutdown notices, прежний единственный job не дублируется. |

Команда focused matrix (test-only URL files задаются окружением):

```sh
go -C backend test -race ./cmd/server ./internal/app ./internal/httpapi \
  ./internal/modules/operations ./internal/modules/telegram \
  -run 'Test(Readiness|Reporter|OperationsEmailConfig|Readyz|Healthcheck|RiverTimeout|ShutdownTimeout|ServeDoesNotAnnounce|SchedulerCompletionOnCancel|NativeLifecycle|Empty.*Claim|EmptyLeasedQueue|TelegramRuntimeStates|WebhookConflict|Polling|TelegramDisabled|RegistrationHTTP)' \
  -count=1 -timeout=3m
go -C backend test -race ./internal/modules/telegram \
  -run 'TestRuntimeStateTransitionsAreSafe|TestRuntimeRecoversAfterStartupOutage' -count=1
```

Полный локальный race-прогон старого дерева остановлен после review findings,
поскольку требовались исправления. Его частичный результат не считается PASS
всего проекта. Полный project matrix и image checks проверяет существующий CI
на точном PR HEAD. Локальная приёмка compiled runtime выполнена одноразовым
assert-based драйвером на собственном fixture; credentials и mailbox bodies
не включены в repository/evidence.

## Ревью и исправления

Один fresh Astra/high whole-change review дерева `e5b17b6f` выявил Critical0 /
Important3. Исправления прошли RED→GREEN:

- Явный список `operations_email` заменял inherited YAML secrets: сохранены
  прежние девять, добавлен десятый; base/native resolved JSON теперь проверены.
- Empty Claim ошибочно очищал send failure под 60s lease: ошибки claim/send
  разделены. Перманентный 429 + empty leased queue остаётся degraded; реальный
  успешный empty claim снимает только свою DB-ошибку.
- Graceful Stop timeout не отменял job и мог задержать pool.Close: добавлены
  bounded hard cancel/проверка его результата; unresponsive worker не заставляет
  процесс ждать pool.Close. HTTP drain timeout закрывает активные соединения.

Bounded follow-up того же reviewer на дереве `fcf6386b` подтвердил закрытие всех
трёх Important; повторного whole-branch review не было. Полностью unresponsive
worker и пропуск pool.Close подтверждены чтением кода; runtime-тест проверяет
настоящий job, реагирующий на отмену. Финальная общая матрица остаётся за CI.
После semantic rename тестового job повтор River regression под `-race` прошёл
за 2.545s. Первый повтор использовал прежний dynamic PG port после stop/start;
URL files обновлены из live Compose ports, код приложения не менялся.

## Ограничения

Операторский доступ к host/Compose и журнал actor/reason — защищённая процедура,
а не новые web-права. Реальный SSH/host audit и реальный SMTP требуют отдельной
внешней приёмки. Enabled Telegram проверен Go fixtures; compiled restart выполнен
с disabled Telegram. VPN/платежи/production не проверялись этим сценарием.
SMTP best effort: bounded очередь может потерять уведомление, SIGKILL/host outage
не позволяют процессу известить оператора. Внешний host/readiness контроль нужен
независимо от backend; новая система мониторинга здесь не добавлена.
