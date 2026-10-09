# С42 — локальная приёмка обслуживания

Дата: 2026-10-10. Задача: [#44](https://github.com/ekho/3xui-shop/issues/44).
Контракт: `2026-10-05-modular-monolith-v1`, владелец `operations`.
[Спецификация](../superpowers/specs/2026-10-09-s42-maintenance-design.md) ·
[План](../superpowers/plans/2026-10-09-s42-maintenance.md) ·
[Процедура оператора](../runbooks/s42-maintenance.md).

## Ревизии и проверки

База реализации: `9305fa5b6a5dec44648aa9a57bb1501164e455f6` из `origin/v2`.
Backend product revision: `192d09d6e1e08d80656e61761a2fb82eb08f588b`.
Frontend/native product revision: `d7d8fbeada59e7c3af0344b259953d8b35caf5ea`.
Последние test-only fixtures: `1b2e4105615a39850d137a206de48e50335db20f`.
Последующие изменения evidence не меняют проверенный код. Итоговый HEAD и
результаты CI устанавливаются по PR в `v2`; локальные проверки не заменяют CI.

| Проверка | Фактический результат |
| --- | --- |
| Go с race и browser | Исходный полный `make test-integration TEST_TIMEOUT=60m`: 14 пакетов PASS, 3 пакета FAIL, 5 failed cases, 2596.086s. Все пять адресно перепроверены ниже; исходный полный запуск не объявляется PASS. |
| Go vet | `go vet ./...` PASS, 5.511s |
| Каноническая генерация | `make -C backend generate`, `npm --prefix web run api:generate`, отсутствие diff в generated Go/SQL/TS — PASS |
| Frontend typecheck/build | `npm run typecheck` и `npm run build -- --mode test` PASS на d7d8fbe; full browser suite повторяет typecheck/build |
| Полный browser suite | `env -u NO_COLOR E2E_PORT=41744 npm run test:e2e`: 551/551 PASS, 305.621s на d7d8fbe (typecheck/build в startup) |
| Повтор native maintenance с итоговым web/dist | `RUN_BROWSER_TESTS=1 go test ./tests -run '^TestNativeMaintenance' -count=1 -race -timeout=5m`: PASS, 22.160s на d7d8fbe |
| Legacy Python | `poetry run python -m unittest discover -s tests -v`: 112/112 PASS, 30.651s |
| Names/runtime config/whitespace | `python3 deploy/acceptance/check_names.py`, `node web/scripts/runtime-config.test.mjs`, `git diff --check` — PASS |

Полный Go, vet и Python запускались на `7ff64a1f5c0955dc74a593d1481e60a441d05e86`. `git diff` до1b2e410 подтверждает, что в Go/Python/API границе изменились только два test fixtures: `server_pool_test.go` и `web_trial_integration_test.go`. Product Go/Python, canonical API, generated TS и native browser script не менялись. Последняя UI-правка проверена свежим native browser и полным551 web; прежний полный549 web сохраняется только как результат прежней ревизии.

## Подтверждённые границы

| Критерий | Проверка и наблюдение |
| --- | --- |
| Единый долговечный режим | `TestMaintenanceDefaultAndSharedAdmission`, `TestMaintenanceConcurrentRevisionAndSharedInstances` и native flow: default off, второй экземпляр, конкурентные операторы, состояние после рестарта. |
| Права и безопасный API | `TestMaintenanceHTTPAuthorityReplayAndAdmission`: operator cookie, Origin/CSRF, отказ клиенту, revoked role и key/revision conflicts. Canonical API и source review подтверждают валидацию входа, cookie-only operator route без Mini grant и публичный ответ только enabled/revision/changed_at. |
| Replay и audit | Тот же HTTP test и native flow: принятый body/key даёт сохранённый результат; конфликт другого body/версии; no-op без ложного transition audit; старый enable replay после disable не включает режим повторно. Состояние, command journal и audit пишутся в одной транзакции (source review); отдельного fault-injection rollback test не добавлялось. |
| Новые и уже принятые операции | Maintenance HTTP/admission tests: новые trial/purchase и Stars invoice/resume блокируются, существующий purchase replay/готовая invoice доступны, Stars cancel доступен; ошибка чтения режима запрещает новые действия. Caller transaction используется для admission при MaxConns=1; полные purchase/resume при таком pool size этим не подтверждаются. |
| Деньги и выдача продолжаются | `TestNativeMaintenanceFlow`: до включения принят заказ Stars; во время режима реальный TLS HTTP/Go Telegram/River обрабатывают pre-checkout/paid, поддержку и решение оператора; после рестарта duplicate не создаёт второй receipt/order/access/trial и сохраняет access identity. |
| Web, Mini App, Go-бот | Native flow/browser используют реальное общее API и подписанный Mini bearer. Web/Mini показывают один режим и запрещают новые trial/purchase; бот отвечает ru/en с cabinet/support. |
| Ошибки, язык и доступность UI | Browser maintenance cases: unknown/read retry, lost response с прежним body/key, конфликт с обновлением, отзыв прав, ru/en, Tab/Enter, видимый focus и перенос focus на свежий статус. Два rendered regression cases сохраняют правильную подпись pending enable/disable после применённого, но потерянного ответа и смены EN→RU, с теми же body/key. |

Нативные проверки находятся в `backend/tests/native_maintenance_test.go` и
`web/tests/native-maintenance.mjs`. API в native browser не перехватывается;
подменяется только Telegram browser SDK. Остальные browser cases используют
явные endpoint fixtures, поэтому не выдаются за реальный backend.

## Review и исправления

Независимые backend/frontend/native task reviews завершены. Review исправил
admission, который мог брать второе соединение при уже открытой транзакции,
и потерю keyboard focus после успешного запроса/конфликта. Весь исходный
backend/frontend/native diff прошёл отдельное Astra/high review.
На 1ea7929 выявлена одна Important проблема pending-action label; исправление d7d8fbe получило scoped Astra/high approval без новых дефектов. Дополнительные fixture changes ниже не меняют product source. Готовность PR требует отдельного результата полного CI на окончательном HEAD.

Первый полный web прогон: 545/549; новая публичная GET ошибочно учитывалась
notice fixture как mutation. Исправлен только fixture, исходные проверки
body/count/CSRF/bearer сохранены: 53/53, затем полный 549/549.
Pending-label regressions: RED2/2 до правки, GREEN10/10 maintenance после неё; body/key сохранены. Ранее восстановлен Mini GET: реальный signed Mini запрос первоначально получал unknown из-за отсутствия public maintenance в allowlist, после исправления native browser прошёл.
Прерванные и частичные прогоны не считаются PASS.

## Пять ошибок полного Go и адресные повторы

| Failed check | Диагноз и фактический повтор |
| --- | --- |
| `TestServerPoolRegistry` | `Down()` откатывал новую38 вместо server-pool guard35/36. RED воспроизведён; fixture теперь делает `DownTo(ctx,34)`, сохраняя запрет удаления истории. Тот же race case GREEN, 2.289s. |
| `TestReminderSchedulerKeepsHTTP` | В полном запуске `reads=0,writes=0`; неизменённый case с race прошёл1+3 повтора (4.283s и8.274s). Истечение50ms до panel read под parallel pressure остаётся гипотезой; точное место задержки не установлено. Scheduler, timeout и assertion не менялись. |
| `TestAccountSecurityAccountSecurityFlow`, `TestWebTrialFlowAndFailures` | Go helper запускает `poetry run python`; отсутствовал наш `POETRY_VIRTUALENVS_PATH`, выбран Python3.14 без aiogram. Readiness actual child: own Python3.13.16/aiogram3.22.0. Оба case с race после передачи окружения (WebTrialFlow также запускает browser) прошли в адресном трёхтестовом запуске; третий дошёл до отдельного restore fixture failure. |
| `TestWebTrialBackupRestore` | После исправления Python обнаружена жёсткая привязка55491/default Compose. Fixture принимает explicit `TEST_POSTGRES_COMPOSE_FILE`, сверяет published loopback endpoint с тестовым URL и сохраняет container-ID/0600 dump/UUID DB/no-owner/no-privileges проверки. Дальше воспроизведён прежний latest-Down дефект; целевой `DownTo(ctx,34)` сохраняет guard35/36. Итоговый отдельный race case PASS,49.173s. |

Исходный Go command завершился exit2, без `WARNING: DATA RACE`; адресный
трёхтестовый повтор — exit1 только из-за restore isolation, затем restore — exit0.
Сохраняются исходные14 PASS пакетов и прошедшие cases неизменённого кода.
По указанию родительского координатора долгий набор повторно не запускался:
исправлены fixture/окружение и перепроверены failed checks. Это прозрачная
составная локальная проверка, а не новый green single-run `go test ./...`.
Полный GitHub CI после восстановления #98 обязателен.

Для адресных consumer/restore команд дополнительно передавались
`POETRY_VIRTUALENVS_PATH=/tmp/3xui-s42-022d/venvs` и
`TEST_POSTGRES_COMPOSE_FILE=/tmp/3xui-s42-022d/compose.test.yml`.
Runtime lookup по умолчанию сохраняет существующий CI Compose fixture;
новое поле относится только к тесту, не к product configuration.

## Окружение, доставка и ограничения

Собственный Compose project `cabinet-s42-022d-test`, loopback PostgreSQL55444 /
Redis56344 и browser41744; отдельные UUID базы и Redis namespaces. URL читаются
из файлов режима0600, значения не включены в source/evidence. Общий
`cabinet-native`, чужие worktrees и данные не менялись. Python3.13, Node24,
Go1.27; новые зависимости не добавлялись.

На первом checkpoint локальной приёмки доставка была заблокирована внешней задачей [#98](https://github.com/ekho/3xui-shop/issues/98): Docker Hub429; owning cache-fix [PR99](https://github.com/ekho/3xui-shop/pull/99) ещё не подтвердил recovery. Этот неуспешный CI не повторяли. Следующий этап интеграции и его результаты записаны ниже. Готовность PR требует всех обязательных checks на его окончательном HEAD; merge и закрытие #44 остаются у родительского координатора.
Миграция `00038_maintenance.sql` сохраняет номер; родительский координатор
интегрирует/применяет `00037_server_management.sql` из PR96/#42 перед38.
Проверка чистой базы в этой ветке не подтверждает upgrade с38 до позднее
добавленной37. Сборка всей объединённой ветки и порядок rollout принадлежат
интеграции родительской сессии. Merge/Done/закрытие #44 не выполнялись здесь.

Контролируемая приёмка использует синтетические аккаунты и платежные события,
external Bot API/panel/payment transports — stubs. Локальный PostgreSQL dump/restore проверен; Docker 3X-UI,
SMTP, реальные Telegram/payment providers/VPN, production/deploy не проверялись
и не разрешены этой приёмкой. Обязательный CI содержит container gates; их PASS на этой ветке ещё не подтверждён.
Режим не является drain, freeze БД или процедурой backup: уже принятое действие,
River/webhooks/recovery и работающий VPN продолжаются. Legacy Python maintenance
не объединён с Go; это граница С47.

Reminder race case с50ms контекстом в полном локальном запуске показал
reads0/writes0 и затем прошёл четыре focused повтора. Причина остаётся недоказанной;
это сохраняется как test-environment limitation, не объявляется product fix.

Существующее полное purchase/Stars resume с MaxConns=1 может возвращать
SERVICE_UNAVAILABLE после успешного admission из-за более позднего pool
re-entry при удерживаемом AccessOwner. Эта независимая ранее существовавшая
граница не исправлена и не объявляется проверенной одним admission regression.


После проверок собственный Compose остановлен, его test volumes удалены.
Приватные логи и recipe/Poetry окружение сохраняются для продолжения после #98;
рабочая ветка и чужие ресурсы не архивируются и не удаляются.

## Интеграция проверенного CI source

По указанию координатора в существующую ветку объединён owning fix
`b320f8eef415bb48e4c1cebdb12409b143eb9501` из PR99. SHA проверен после SSH fetch.
Его полный [push Platform run 38001638382](https://github.com/ekho/3xui-shop/actions/runs/38001638382)
и все три [Image jobs 38001644520](https://github.com/ekho/3xui-shop/actions/runs/38001644520)
имеют SUCCESS на этом source. Это основание возобновить CI нашей ветки,
а не подтверждение её собственного окончательного CI или merge PR99.

Объединение с прежним local HEAD `b741dcb7107f84b0b6a74571d4c5d4162ff85a03`
прошло без content conflicts. `.github` совпадает с b320f8e без дополнительных
правок. Incoming source содержит уже слитую #46: readiness, bounded shutdown,
River и operational SMTP reporting сохранены вместе с maintenance admission.
Миграция37 не копировалась; порядок37→38 и integration с #42 остаются у координатора.

| Свежая проверка объединённого source | Результат |
| --- | --- |
| Focused Go с race: cmd/server, app, telegram, operations, httpapi; healthcheck, shutdown/River, readiness/reporting и maintenance cases | PASS во всех пяти пакетах; wrapper80.534s, HTTP72.523s |
| Native maintenance flow/browser с race, `RUN_BROWSER_TESTS=1 go test ./tests -run '^TestNativeMaintenance' -count=1 -race -timeout=5m` | PASS21.164s, wrapper24.169s; реальное TLS API/River/Go Telegram, synthetic transports |
| `go vet ./...` | PASS2.080s |
| CI script unit tests, Python syntax, BuildKit TOML, diff whitespace | 10/10 PASS; parser и whitespace PASS; Docker cache configurator не применялся на Mac |
| Пинованный actionlint1.7.12 + shellcheck | Исходный запуск RED только на уже существующем `concurrency.queue`. Проверено, что поле неизменно от merged5bdcdbb; с точным исключением этого неподдерживаемого diagnostic остальные checks PASS. Workflow и CI gates не изменялись. |

Эти команды выполнены на том же объединённом product tree; после них изменён
только этот отчёт. Web/Python, canonical API/generated code, maintenance owner,
его composition ports и native test sources совпадают с прежним проверенным
деревом. Для изменённых process/runtime/HTTP границ выполнены свежие focused
и connected/native checks. Старый полный Go RED и пять адресных PASS сохраняются
как отдельные исторические результаты; причина scheduler0/0 по-прежнему неизвестна.
Неизменённый дорогой local suite повторно не запускался. Собственный полный
GitHub CI окончательного HEAD и независимое ревью интеграции обязательны;
точные итоговые SHA и результаты фиксируются в PR.

Restore-test overlap с #45 (`web_trial_integration_test.go` и его отдельный
`postgres_fixture_test.go`) передан координатору. Source #45 не копировался,
дополнительный adapter interface не добавлялся; endpoint/ownership/history guards
и `DownTo(ctx,34)` сохраняются.
