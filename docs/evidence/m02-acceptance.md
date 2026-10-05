# М02: локальная приёмка accounts

Дата: 2026-10-05. Задача: [М02 #56](https://github.com/ekho/3xui-shop/issues/56).
Ветка: `feature/m02-accounts`, baseline `896a883c96bf662dc568372046fca75ae0753d33`
(`v2`). Проверенная реализация: `856b4eab34b2aa7785de7d373bd6e5ed75c5e640`.
Решение: `2026-10-05-m02-accounts-v1`; выполнение Native.

Регистрация, credentials, сессии, роли, ограничения и legacy approval
перенесены в `internal/modules/accounts`. SQL закрыт приватным sqlc;
публичный Snapshot не содержит хеш пароля или proof. Производственный app
собирает одного владельца для HTTP, CLI и jobs. Переходные facade/DTO
сохраняют существующее поведение до следующих переносов М03–М06.

## Проверки

| Проверка | Результат и предел доказательства |
| --- | --- |
| Auth focused | PASS: прямые Register/Verify/Login, прежний ID/session, restricted refusal, Unicode и отмена при занятой Argon2 capacity. Native task run: accounts 1.427s, platform 12.568s. |
| Roles/restrictions/legacy focused, `-race` | PASS: прежний replay JSON/hash/result, actor/role locks, protected target, dry run, import conflicts и support restrictions. Native task run: accounts 4.157s, platform 24.017s. |
| SQL boundary/composition, `-race` | PASS: отрицательные SQL/import fixtures; app создаёт одного accounts owner; caller Tx сохранена. Native task run: app 6.298s, platform 142.152s, server 3.849s. |
| `RUN_BROWSER_TESTS=1 go test -race ./... -count=1` | PASS: 10 пакетов с тестами, 3 без тестов; platform 157.631s, backend/tests 106.245s. Connected browser включён. |
| `go vet ./...`, Go/web generation | PASS; generated drift отсутствует. HTTP/OpenAPI, migrations, go.mod/go.sum и web dependencies не изменены относительно baseline. |
| Web typecheck/build, runtime config | PASS. Существующие предупреждения сборщика о `use client` в сторонних пакетах сохранены. |
| Python unittest | PASS: 105 тестов, 10.541s; сохраняет legacy consumer. |
| Playwright | PASS: 110 isolated проверок, 1.1m; connected HTTP/SMTP/adapter путь дополнительно входит в Go run. |
| Compose config и rebuild backend/web/bot | PASS. Используются существующие зависимости и собственные локальные контейнеры. |
| Container smoke | PASS: HTTPS, runtime public config, public/private routing, secret files, повторные migrations, rollback и provision-only restore. |
| Native Docker с 3X-UI **3.7.0** и TLS Mailpit | PASS: HTTP/jobs/Telegram, auth SMTP, approve/reject/reconsider/replay/outage; Bot API simulated. |
| Физический stop/start compiled backend | PASS: одна Operation/Grant, прежние keys и настоящие panel readback/expiry/limits/membership. Telegram disabled; DB и данные сохранены. |
| Свежий whole-branch review | Astra/high проверил `896a883..b97ff52`: новых Critical/Important/Minor 0. Узкий race run accounts 6.580s, app 4.329s, platform 11.138s. Повторное review и fix pass не требовались. |

Полная локальная команда Task 4 завершилась с exit 0 за **341.878s**.
Исполняемые проверки:
[accounts](../../backend/internal/modules/accounts/accounts_test.go),
[migration](../../backend/internal/modules/accounts/migration_test.go),
[restriction replay](../../backend/internal/modules/accounts/restrictions_test.go),
[границы](../../backend/internal/app/boundaries_test.go),
[composition](../../backend/internal/app/accounts_test.go),
[native integration](../../backend/tests/native_trial_integration_test.go).
Окружение и команды Docker — в прежнем [runbook](../runbooks/m01-embedded-telegram.md).

Предсуществующие password hashes, сессии, mail_delivery JSON и proof encryption
проверяются migration/restore/security regression. Credential changes сохраняют
подписку. SMTP timeout/advisory-lock gates сохранены; их изменение относится к М06.

3X-UI preflight допускает повторные key/UUID, поэтому
`PANEL_DUPLICATE_GUARD_VERIFIED=false`; автоматический повтор uncertain create
по-прежнему выключен. Локальная приёмка не доказывает поведение всех vendor failures.

## Решения исполнителя

Все решения Native ledger приведены в порядке принятия.

| Решение | Причина | Цена ошибки |
| --- | --- | --- |
| М01 слить через GitHub guard по точному source SHA с проверкой parents/tree | GitLab finalization registry не обслуживает ekho/GitHub; не записывать состояние чужого проекта. | При конкурентном изменении target нужна сверка доставленного дерева. |
| Оставить PR #5 на прежнем SHA, адаптировать account calls при интеграции С10 после М04 | Решение владельца заранее записано в #56/#59; AccountByID/LockAccount переехали в accounts. | С10 не скомпилируется без адаптации owner port. |
| Опубликовать документы сначала, code commits отправить вместе после проверок | Канонические ссылки доступны до изменения shared contract; меньше промежуточных CI. | До финального push код хранится только в managed checkout. |
| Передать `CheckTelegramAvailable` и `CreateTelegram` одну caller Tx | Сохраняет identity conflict перед disabled-trial ответом, без связи accounts с trial config. | Один дополнительный узкий port нужно учитывать в М04. |
| Включить CLI catalogue/import и `LookupTx` в перевод composition | Все production entry points используют одного владельца; Telegram claim сохраняет транзакцию. | Возможная CLI/claim регрессия требует проверки; текущие проверки прошли. |
| Восстановить prerelease М01 через CLI из merge896 и трёх проверенных multiarch images | После двух отмен из-за отсутствия hosted runner бюджет одного failed-only rerun исчерпан; release job не выполнялся. | При ошибке потребуется исправление release metadata; tag/source guards проверены. Production не менялся. |
| Task 4 ledger закрывает local implementation, review/PR остаются finishing phase | Устраняет цикл между task completion и финальным review без пропуска review/delivery. | Task completion отдельно не доказывает review, CI или delivery; состояния остаются раздельными. |
| Оставить совместимость PR #5 для интеграции С10 | Ревьюер её не оценивал; открытый PR не включён в эту ветку, адаптация принята в #56/#59. | При пропуске адаптации account calls С10 не скомпилируются. |
| Оставить М03–М06 и удаление Python для следующих этапов | Ревьюер не оценивал их завершённость; М02 переносит владельца, сохраняет нынешние SMTP gates. | Ошибочное признание всей архитектуры завершённой скроет оставшуюся связь platform/notifications. |
| Унаследованный stale deliveryCode учитывать отдельно | Ревьюер не пересматривал путь М01; diff М02 его не меняет, HTTP/выдача не блокируются. | Состояние канала может оставаться устаревшим до следующей доставки; нельзя использовать его как достоверный health. |
| Не распространять local acceptance на production/Happ/real providers/внешний SMTP/benchmark | Ревьюер не оценивал их; они исключены принятой границей и текущим разрешением. | Непроверенные условия развёртывания или внешних сервисов могут потребовать исправлений до production. |
| Сохранить запрет повторного uncertain panel create | Ревьюер не оценивал все 3X-UI failures; native readback и прежние fault tests покрывают локальный сценарий. | Новая vendor failure потребует отдельной диагностики; нельзя автоматически дублировать клиента. |

М01: [PR #61](https://github.com/ekho/3xui-shop/pull/61) слит в baseline;
[2.0.0-dev.7](https://github.com/ekho/3xui-shop/releases/tag/2.0.0-dev.7)
опубликован. Версионные/source manifests всех трёх образов совпадают и содержат
linux/amd64 и linux/arm64. Workflow
[37363120914](https://github.com/ekho/3xui-shop/actions/runs/37363120914)
остался failure из-за непредоставленного runner; CLI recovery не делает этот run зелёным.
#55 закрыт только в принятой локальной границе; весь v2 и production остаются открытыми.

## Отложенные замечания и границы

Унаследованный Minor М01: успешный пустой outbox claim после ошибки сохраняет
прежний deliveryCode до следующей доставки. Это может оставить stale degraded
в Runtime.State(), но не блокирует HTTP/выдачу; State не используется web health.
М02 не меняет этот путь.

PR #5/С10 сохраняется на `afaeacf652964453ddd61883aa6da6a0d285783c`.
Его прежняя проверка не доказывает совместимость с новым accounts SQL boundary;
адаптация нужна после М04 и до М05, по записи #56/#59.

Реальный Telegram/payment provider, внешний SMTP и целевой password benchmark
остаются отдельными проверками. Happ, системное доверие и действующий VPN
не менялись. Полное удаление Python входит в С47. Новые зависимости, схема,
event bus и пустые будущие модули не добавлены.

М02: локальная реализация и fresh review завершены. Review проверил account/role
locks, старые password/session/mail форматы, replay и SQL/DTO boundaries; все пять
Review Focus разобраны. Между проверенной реализацией `856b4ea` и review HEAD
`b97ff52` менялись только документы. Исполнитель подтвердил принятые границы
каждого пункта Declined to judge; они полностью перечислены в таблице решений.

PR, финальная ревизия и exact-head CI фиксируются в
[М02 #56](https://github.com/ekho/3xui-shop/issues/56).
Слияние М02 и production отдельно. Следующий этап — М03/#57 (catalogue).
