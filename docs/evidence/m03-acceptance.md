# М03: локальная приёмка catalogue

Дата: 2026-10-06. Задача: [М03 #57](https://github.com/ekho/3xui-shop/issues/57).
Ветка `feature/m03-catalogue`, baseline `c780927659f66b6ec8ae741cdddf310a7e464953`
(`v2`). Проверенная реализация: `ed52c2b1f2a07eec0a14ab5c39277c7b9fd34b34`.
Контракт: `2026-10-06-m03-catalogue-v1`; выполнение Native.

Каталог, revisions, цены, import/seed и их SQL перенесены в
`internal/modules/catalogue`. App создаёт одного accounts owner и одного
catalogue owner. Переходный platform-фасад переводит DTO и ошибки; назначение
тарифов и unlimited читают каталог через порты с прежней caller transaction.
HTTP/OpenAPI, web, schema, identifiers и зависимости сохранены.

## Проверки

| Проверка | Результат и предел доказательства |
| --- | --- |
| Прямой owner и старый persisted replay, `-race` | PASS. Create/list/revise/import/seed; цена `9007199254740993`, clock/actor/reason, nullable metadata, неизменяемая история. Прежние unsorted input/hash/JSON дают тот же результат без новой revision; другой body получает 409, restricted/revoked actor — 403. Task 1: catalogue 2.732s, platform 7.046s, app 3.301s, server 3.310s. |
| Caller transaction, boundary и composition, `-race` | PASS. Реальная PG-блокировка удерживается до caller rollback; конкурентная revision ждёт. Cancelled context не пишет revision. Отрицательные SQL fixtures запрещают SELECT/UPDATE/quoted CTE вне owner. Assign/unlimited сохраняют snapshot и порядок отказов, включая archived malformed terms. Task 2: catalogue 3.450s, app 3.470s, platform 47.697s, server 2.465s. |
| `RUN_BROWSER_TESTS=1 go -C backend test -race ./... -count=1` | PASS: 11 пакетов с тестами, 4 без тестов; 159.037s. Platform 157.828s, backend/tests 109.538s. Connected browser включён; прежние auth/support/Telegram проверки сохранены. |
| `go -C backend vet ./...`, Go/web generation | PASS; generated drift отсутствует. API, migrations, Go/web dependencies не изменены относительно baseline. Python manifest/lock также не менялись. |
| Web typecheck/build/runtime config | PASS: 1.946s / 2.000s / 0.135s. Runtime test выполнялся из `web`. Сохранились предупреждения сборщика о сторонних `use client` и размере chunks. |
| `poetry run python -m unittest discover -s tests -v` | PASS: 105 тестов, 17.230s; команда 19.153s. Python 3.13.16, зависимости существующего lock-файла. |
| `npm --prefix web run test:e2e` | PASS: 110 Playwright-проверок, 69.640s. |
| Compose config и build backend/gateway/bot | PASS: 0.190s / 17.049s. Только собственные локальные контейнеры. |
| `python3 deploy/acceptance/smoke.py` | PASS: 22.451s. HTTPS, runtime public config, public/private routing, secret files, повторные migrations, bounded rollback и provision-only restore. |
| `python3 deploy/acceptance/local.py up --reuse-images`, `check`, `down` | PASS: 7.663s / 44.133s / 1.168s. 3X-UI **3.7.0**, TLS Mailpit, HTTP/jobs/Telegram с simulated Bot API. Физический stop/start compiled backend сохраняет ту же Operation, один Grant, keys и настоящий panel readback; в restart-проверке Telegram выключен. Стенд остановлен. |
| Свежий whole-branch review | Astra/high проверил `c780927..bfbcd14`: Critical/Important/Minor 0. Исходники и logs всех 18 этапов прочитаны, переход `ed52c2b..bfbcd14` меняет только документы. Fix pass и повторное review не требовались. |

Все **18 этапов** полной локальной матрицы завершились с exit 0;
сумма измеренных длительностей — **346.532s**. Private logs не публикуются.
Исполняемые проверки:
[owner](../../backend/internal/modules/catalogue/catalogue_test.go),
[transaction ports](../../backend/internal/modules/catalogue/data_test.go),
[старый replay](../../backend/internal/platform/catalogue_replay_test.go),
[границы](../../backend/internal/app/boundaries_test.go),
[composition](../../backend/internal/app/catalogue_test.go).
Команды и пределы Docker — в [runbook](../runbooks/m01-embedded-telegram.md).
Go fixtures получают пути к собственным DB/Redis через
`TEST_DATABASE_URL_FILE` / `TEST_REDIS_URL_FILE`, без секретов в аргументах.

Первый полный прогон завершился ошибкой окружения: Poetry выбрал Python 3.14
без `aiogram`. Выбрана существующая среда Python 3.13 и установлены зависимости
неизменённого lock-файла; import проверен до повторного прогона. В private-команде
runtime-config исправлена рабочая папка на `web`. Повтор разрешён C07 после
изменения входных условий. Ранние RED/fixture failures Task 1/2 сохранены в
Native ledger: исправлялись тестовые consent/sub_id seeds, без изменения продукта.

## Решения исполнителя

Все Native rulings приведены в порядке принятия.

| Решение | Причина | Цена ошибки |
| --- | --- | --- |
| Сохранить PR #5 на прежнем source и дать `LockCurrentPlan` сейчас | У существующей покупки есть raw `FOR UPDATE`; при последующей интеграции ей нужен тот же lock внутри caller Tx. | Без адаптации С10 не пройдёт совместимость с catalogue boundary. |
| Выполнить Native с одним свежим финальным reviewer | Этот порядок выбран владельцем; промежуточные implementer/reviewer не добавлялись. | Дефект отдельной задачи может быть обнаружен только в финальном review. |
| Слить PR #62 без PR CI по прямому указанию владельца | Сохранены точные source/tree, локальные проверки и review М02. Это решение относится к #62. | Дефект, выявляемый только PR CI, мог попасть в preview `v2`. |
| Вернуть metadata вместе с `ErrInvalidTerms`, archived unlimited пропускать до decode refusal | Сохраняет прежний порядок availability guards и работу valid unlimited при повреждённой archived строке. | Иной failure code или блокировка корректной выдачи unlimited. |
| Закрыть Task 3 через полный прогон до записи evidence | Результаты нельзя честно записать до проверки; после неё меняются только документы. | Документация может неверно описать факты; исполняемые доказательства привязаны к `ed52c2b`. |
| Совместимость полной покупки PR #5 оставить для интеграции С10 после М04 | Ревьюер её не оценивал: исходный PR не включён в эту ветку; адаптация ports записана в #17/#59. | Без адаптации raw SQL и account calls покупка не пройдёт новую module boundary. |
| Real providers, внешний SMTP и production проверять отдельно | Ревьюер их не оценивал; текущая приёмка использует собственный локальный стенд и simulated Telegram. | Условия внешних сервисов и production могут потребовать исправлений перед развёртыванием. |
| М04–М06 и удаление Python оставить следующими этапами | Ревьюер не оценивал завершённость всей архитектуры; М03 закрывает только catalogue. | Ошибочное признание всей миграции завершённой скроет оставшуюся функциональность и связи platform. |

Отложенных Minor нет. Все три пункта `Declined to judge` финального review
рассмотрены и отражены в последних трёх решениях таблицы.

## Доставка и границы

Локальная реализация, приёмка и свежий whole-branch review выполнены.
PR CI пока ожидается. М03 не слит в `v2`, production не менялся.
М02 отдельно доставлен через [PR #62](https://github.com/ekho/3xui-shop/pull/62);
[2.0.0-dev.9](https://github.com/ekho/3xui-shop/releases/tag/2.0.0-dev.9)
опубликован с проверенными source/version manifests amd64/arm64.

[PR #5](https://github.com/ekho/3xui-shop/pull/5) остаётся на
`afaeacf652964453ddd61883aa6da6a0d285783c`: адаптация accounts/catalogue ports
и повторная проверка покупки выполняются при интеграции С10 после М04.
Полный перенос subscriptions/vpn, payments и notifications — М04–М06;
удаление Python — С47. Эти результаты не входят в приёмку М03.

3X-UI preflight не гарантирует отказ повторным key/UUID, поэтому
`PANEL_DUPLICATE_GUARD_VERIFIED=false` и uncertain create не повторяется автоматически.
Happ и системное доверие не менялись. Real Telegram/payment providers, внешний
SMTP, production hardware benchmark и production acceptance здесь не проверены.
