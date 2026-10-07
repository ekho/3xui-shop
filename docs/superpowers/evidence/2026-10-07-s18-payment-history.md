# С18 — локальная приёмка истории платежей

Владелец [#25](https://github.com/ekho/3xui-shop/issues/25), контракт
[2026-10-07-s18-payment-history-v1](https://github.com/ekho/3xui-shop/issues/25#issuecomment-6029754500).
База c827f1944d543624309c93da6e55ad92871efaa7, ветка feature/s18-payment-history,
цель PR — v2. Спецификация и план находятся в соседних каталогах superpowers.

Task1 реализована на 5532a14d3ef367cd6dcf1222a7391aa70560c7d1,
Task2 — на 6a23fadf7b45772936b1bed9fcb714781c8fbd44. Task3 добавляет этот
отчёт и исправляет две существующие тестовые зависимости новой истории и
миграции22. Рабочие миграции15–21, прежние операции/DTO/quote hash сохранены.
Повторный полный Go-прогон прошёл на Task3. Одна свежая проверка ветки
3204e0e нашла два Important; один авторский fix pass завершён с actual
RED→GREEN и итоговым full Go/browser PASS. Source CI и доставка пока pending.

## Что проверено

Connected HTTP/DB проверяет три account-scoped страницы, текущие cookie,
Origin/CSRF, отозванные сессии/роли, restricted и Telegram-only target.
История читается при выключенных платёжных методах; snapshot orders,
receipts, access, jobs и audit до/после чтения совпадает. Заказ показывает
quote и раздельные payment/fulfillment состояния; receipt — сохранённые
gross/net, currency/review/protection и funding link. Неизвестные суммы и
валюта не вычисляются, crypto decimal-строки не округляются. Checkout,
credentials, charge ID и packed legacy payload отсутствуют в DTO.

Keyset pagination проверена на 51 tied timestamp, с новой строкой между
страницами. Legacy source IDs и количества больше 2^53 проходят как строки.
Импорт сохраняет четыре исходных статуса, длинные raw ID, неизвестный
payload и UTC до микросекунд. Dry-run не пишет; exact replay не пишет и
не создаёт audit. Конфликт source/payment/identity атомарно откатывает apply.
Строгий CLI отвергает неверный UTF-8, lossy Unicode, лишние поля, trailing
JSON и превышение лимита. SQLite export оставляет исходные байты неизменными.

В реальном браузере один компонент работает в кабинете и карточке оператора:
ru/en, ширина375px, клавиатура и видимый focus, точные BigInt суммы,
ручной операторский источник, неизвестные данные, empty/error/retry.
Failed append сохраняет страницу и курсор; refresh начинает заново.
Held ответы прежних kind/locale/target не смешиваются с текущими данными.
401/403 удаляют показанную историю или всю операторскую карточку.
Прежние operator purchase/trial/support действия сохранены.

## Фактические проверки

FILE-backed PostgreSQL/Redis — собственные localhost fixtures. Переменная
RUN_BROWSER_TESTS=1 включает connected browser проверки в Go suite.
Команды запускаются из backend/web либо корня согласно таблице; длительности
включают запуск через run-check, а не только время отдельных тестов.

| Проверка | Результат | Время |
| --- | --- | --- |
| Task1 Go: cmd/server, payments, httpapi; PaymentHistory/LegacyPayment/strict CLI; -race | PASS | 9.428s |
| Task1 python3 -m unittest tests.test_legacy_payment_export -v | 2/2 PASS | 0.179s |
| Task2 npm run typecheck | PASS | 2.360s |
| Task2 rendered payment-history + operator-cabinet | 22/22 PASS | 20.250s |
| make generate | PASS, generated files без drift | 1.785s |
| npm run api:generate | PASS, generated files без drift | 0.613s |
| make vet | PASS | 1.051s |
| poetry run python -m unittest discover -s tests -v | 107/107 PASS | 14.014s |
| npm run build | PASS | 2.851s |
| npm run test:e2e, итог после fixture correction | 295/295 PASS | 123.796s |
| python3 deploy/acceptance/check_names.py | PASS | 0.285s |
| node scripts/runtime-config.test.mjs | PASS | 0.122s |
| Go TestPlanChangeActionMigration -count=1 -race -v -timeout=5m | обе ветви PASS | 8.455s |
| Task3 go test ./... -count=1 -race -v -timeout=20m | 803 PASS,343 root tests,13 packages,0 failures/skips | 666.727s |
| History/import/manual cross-method после обоих review fixes, -race | PASS | 15.022s |
| make vet после review fixes | PASS | 1.028s |
| Итоговый go test ./... -count=1 -race -v -timeout=20m | 805 PASS,345 root tests,13 packages,0 failures/skips | 670.172s |

Actual RED: новый HTTP route сначала вернул404 вместо200; новая web route
не показала заголовок истории в ru/en (2 failed,27.106s). RED импортера,
архивной таблицы, строгого CLI, exporter и wire precision сохранены отдельно.
Первый web запуск остановился на неправильной test fixture typing; это не
выдаётся за поведенческий RED. Затем поведение наблюдалось до реализации.

Первый полный web прогон294/295 выявил старый default `{}` в mock истории
account-restrictions; explicit typed empty page исправляет зависимость,
исходные restriction assertions сохранены. Первый полный Go прогон663.750s
выявил единственный failed subtest `TestPlanChangeActionMigration/retained_pending_change`:
одношаговый Down теперь откатывал пустую migration22 вместо проверяемой21.
Обе ветви старого теста используют DownTo20, как существующий renewal test;
рабочие миграции не менялись. Focused проверка прошла. Повтор полного Go прошёл и
обоснован именно изменённым тестовым входом; web/Python без изменений не
повторяются. Исходные failed logs сохранены.

Task3 source-bound verifier сверяет575 tracked inputs, hash фактических
полных logs, completed/exit0, counts и restore proofs, вместо повторения
неизменных дорогих suites при task-done. Изменился только исторический Go test
после web/Python: их реальные inputs не менялись. Task3 input manifest SHA-256:
e88bf105fc2fb7527cad17898aeacf6370c48a958a4090bfe937a09591e02cd4.
Go log SHA-256: e2fecc8c1a219327247000161bce33f09b8a104cec1aff147f57dce8363a07e7.

## Одна свежая проверка и один проход исправлений

Fresh gpt-6-astra/high проверил всю ветку c827f19..3204e0e read-only.
Critical и Minor не найдены; два Important подтверждены координатором
по реальному эффекту. Повторного reviewer нет.

1. История receipt брала payment_method из заказа. Подписанный YooMoney
   callback к manual order сохраняется с payment_method_mismatch, поэтому
   прежний reader показывал неверный источник. Новый connected HTTP test
   TestPaymentHistoryCrossMethodReceipt получил actual RED7.278s: YooMoney
   receipt назван manual. Общая SELECT-проекция теперь берёт известный provider
   из сохранённого provider_data; manual_confirmation обозначает manual,
   остальные записи нынешнего writer без provider_data принадлежат YooMoney.
   Клиент/оператор видят правильный источник, исходный order остаётся manual;
   p2p/card/неизвестный notification type сохранены для review. Existing HTTP
   fixture также фиксирует YooKassa receipt на YooMoney order. Writers/API shape
   и миграции не меняются.
2. Прежний import test менял входные ID и останавливался до сравнения владельца
   архива. TestLegacyPaymentMovedIdentity действительно переносит ту же пару
   Telegram/legacy identity на второй существующий аккаунт, вставляет новую
   строку перед старой и требует IMPORT_SOURCE_CONFLICT/full snapshot rollback
   для apply/dry-run. Существующая защита прошла baseline8.243s; временное
   удаление только account!=id дало actual mutation RED7.270s. Исходные байты
   восстановлены сразу; продуктовый importer не менялся. Новый test сохраняет
   прежнего владельца/raw, проверяет отсутствие новой строки/audit/money/jobs.

Все целевые history/import/manual-cross-method проверки после обоих
исправлений PASS15.022s; актуальный make vet PASS1.028s. Первый receipt запуск
остановился на test-only secret type (string вместо []byte) и не считается
поведенческим RED. Полный Go/browser suite на трёх изменённых Go files прошёл:
805 PASS,345 root tests,13 packages,0 failures/skips,670.172s.
Web/Python и CLI/migration/restore inputs не менялись: их предыдущие реальные
успешные результаты сохраняются с source/log hash proof. Финальный verifier
фактически прошёл: current575 inputs, complete/exit0 и full log hashes,
оба RED→GREEN, восстановленные исходные bytes импортера, own stopped restore.
Он не заменяет actual full Go command.
Final input manifest SHA-256: 2d619ef939e67d0921779fc161cf8bdd03c87096a436ab086a11f7a006061017.
Final Go log SHA-256: 6412604907d38bf752356ae231875135d81110a71d2172daffb3bdfb6e8a4a40.

Deferred minors: none.

## Контролируемое восстановление

Собственная SQLite-копия содержит4 записи, все исходные статусы, raw charge
ID около8.4KiB, неизвестный Unicode payload, exact количество9007199254740993
и UTC с микросекундами. Actual server migrate/import-legacy-payments
dry-run/apply/replay запускались без serve/workers/SMTP/Telegram/providers.

Снимок: archive4, orders1, receipts1, jobs1, audit1, access0. Существующий
order/receipt/funding link/known net/job сохранены точно; extra jobs или
финансовые записи не появились. pg_dump -Fc и pg_restore --single-transaction
в отдельную БД сохранили полный snapshot. Restored database имеет
default_transaction_read_only=on; реальный UPDATE отклонён read-only guard.
На исходной БД UPDATE/DELETE архива запрещены immutable trigger.

Actual Down SQL migration22 в транзакции отвергает непустой архив без потери
данных; для отдельной пустой БД удаляет архив и trigger function. Это proof
самого Down SQL, а не полного Goose Down runner/version decrement.
Все три собственные source/restored/empty базы удалены после проверки;
общие PostgreSQL/Redis контейнеры остаются для следующих локальных тестов.
Первая seed попытка с null YooMoney net была корректно отвергнута существующим
constraint; исправлена только fixture. Её отдельные базы также удалены.

- SQLite SHA-256: bbfb588dfd3e202925815225c7823e4f0bf22b8a772b7ddf24c5f426c25fa09e.
- Dump SHA-256: 838fede517df40da58ff56ae2825a2ea8a4e76c104538b61fef935cac7aa7da0.
- Snapshot SHA-256: cdc8318cd6e2da64b64120887c0ca62675aad6d6f4b79584f4e5932d90e5c90f.

## Решения исполнителя

1. SHA-256 unique index с native hash-equality CHECK и полным сравнением raw
   полей: text B-tree не вмещает разрешённый16KiB ID. Цена ошибки — безопасный
   отказ импорта при неизменном источнике.
2. No-job proof сравнивает существующий before snapshot: регистрация уже
   создаёт mail_delivery. Dry-run Inserted — предсказанное число новых строк,
   фактических writes нет. Цена ошибки — пропущенное дополнительное задание;
   exact before/after counts это проверяют.
3. Legacy quantities в wire — decimal strings, domain — int64: значения
   могут быть больше2^53. Цена — будущий consumer должен разбирать строку для
   арифметики; существующий PurchaseQuote не менялся.
4. Task2 plan использует реальный style.css, lang prop и Playwright testMatch.
   Цена ошибки — дополнительные fixture проверки; dependency/store не добавлены.
5. Verbose Go command заменяет Makefile wrapper с теми же count/race/timeout
   flags для per-test evidence; web build/suite идут до Go browser checks.
   Цена ошибки — возможное будущее расхождение wrapper; текущий Makefile сверён.
6. Старая account-restrictions fixture возвращает typed empty history вместо
   default `{}`. Цена ошибки — mock может скрыть будущий drift пустой страницы;
   собственные history checks фиксируют её контракт.
7. Старый plan-change migration test привязан к DownTo20, предшественнику21,
   вместо «последней» миграции. Цена — явная зависимость исторического теста
   от номера проверяемой миграции; исходные data-loss assertions сохранены.
8. Реальные платежи/public callbacks/production/внешние SMTP/TG/Happ/VPN/Mac
   trust, которые reviewer отложил, исключены принятой локальной границей.
   С13/С45–С47 остаются открытой внешней приёмкой. Цена — поведение реальных
   provider/deployment неизвестно до соответствующих проверок.
9. Полный реальный snapshot и эксплуатационная длительность остаются С46.
   Точные собственные fixtures доказывают сохранность, а не объём production.
   Цена — большой импорт может быть отвергнут лимитом/timeout до репетиции;
   источник сохраняется.
10. Source CI/merge/prerelease/tag/OCI labels — следующие gates координатора,
    их не выводят из локальной проверки. Цена ошибки — неверный/непроверенный
    артефакт; требуется actual SHA/parents/tree/tag/digest proof.
11. Уже показанные разрешённые данные очищаются при полученном401/403 без
    добавления polling/socket. Сервер проверяет каждый новый protected request.
    Цена — старый экран остаётся до следующего запроса; новый доступ не даётся.

## Границы и доставка

Новых dependencies, UI/store/framework и финансовых обработчиков нет.
Все проверки используют собственные localhost данные и заглушки. Реальная
оплата YooMoney С13, публичные callbacks, production, внешние SMTP/Telegram,
живой Happ/VPN и Mac trust остаются за пределами этой приёмки. Совместимость
будущих потребителей #11/#26/#27/#28/#29/#32/#33/#36/#39/#45/#53/#54 привязана
к общему решению; их scope/status/dependencies/parent не изменялись.

Workflow C01–C07/C10 проверяет записанные факты, не даёт разрешение и не
подменяет реальные тесты. Managed workflow-state/finalization helper поддерживает
GitLab и неприменим к этому GitHub repository; используются обычные публичные
checkpoint в #25 и действительные GitHub gates, без выдуманного GitLab owner.
После Task3 проведена одна fresh Astra/high whole-branch review; единственный
Important fix pass завершён, Minor отсутствуют, без re-review. Source CI, manual exact-SHA merge в v2,
actual prerelease/tag/три OCI indexes и шесть platform labels пока pending.
