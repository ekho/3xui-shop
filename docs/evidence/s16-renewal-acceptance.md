# С16 — локальная приёмка продления

Владелец [#23](https://github.com/ekho/3xui-shop/issues/23), контракт
`2026-10-07-s16-renewal-v1`; [spec](../superpowers/specs/2026-10-07-s16-renewal-design.md),
[Native plan](../superpowers/plans/2026-10-07-s16-renewal.md).
Backend checkpoint c69767b, UI c1541b5. Product inputs и результаты сохраняются
по SHA256; финальные source/review/CI/merge/preview фиксируются в PR и #23.

Использованы существующие orders/receipts/River/VPN worker. Неизменяемое
`action=renew` различает назначение заказа; физический kind остаётся purchase.
Снимок тарифа и funded target сохраняются. Новый независимый web-клиент
продлевает собственный конечный regular/euru тариф через YooMoney AC/PC/RUB.
Неизвестный Stars закрывает внешние деньги до достоверного перехода в С35.

| Проверка | Фактический результат |
| --- | --- |
| Connected renewal и старые оплаты | Все9 renewal roots и прежние purchase/YooMoney HTTP cases PASS90.699s: own hidden/frozen revision, provenance, late account/native/billing guards, expiry/exhaustion, immutable action/down, concurrent create, replay и reconcile. |
| Модульная композиция | PASS5.778s: публичные владельцы, один процесс, lifecycle. |
| Полный Go race/real browser | PASS561.176s,690 проверок в13 test packages; FAIL/SKIP/race отсутствуют. |
| Полный web | 227/227 PASS106.334s. Focused36 purchase/renewal PASS17.957s: ru/en/mobile375px/keyboard, lost response/locale frozen key/body, immediate403/404, foreign/mismatched purpose и fresh checkout. |
| Python baseline | 105 PASS19.836s; сохранился текущий legacy consumer. |
| Generators/vet/typecheck/build | PASS; actual Go/TS generation сохраняет generated inputs; current source hashes подтверждают их соответствие. Runtime config self-check и semantic-name check PASS. |
| Собственный native runtime | cabinet-c16, собственные Go/web image IDs и revision labels c1541b5, один serve-процесс; native3X-UI3.7.0, TLS Mailpit; запуск34.267s. Telegram/legacy transport выключены. |
| Native renewal | Все6 случаев PASS29.319s: active/expired/exhausted, late ban/plan/identity. Подписанные localhost callbacks → River → настоящая панель; один receipt/target/job, срок/нулевые счётчики/ID/server сохранены. Повтор не меняет результат; applied история позволяет следующий renew. Поздний конфликт сохраняет деньги для review без native write. |
| Funded target/backup/restart | PASS4.510s: target/job подготовлены до остановки исходного backend; dump восстановлен в отдельную собственную базу без workers. READ ONLY financial/identity/action/quote/hash/receipt/funding/target digest равен; auth maintenance идемпотентна. Исходный backend после запуска применил прежний target, повтор не добавил срок/reset. |

Два первых запуска нового native checker остановились из-за его входов:
KeyError can_pay (4.165s) — поле принадлежит order, current DTO содержит order;
502 после собственного panel restart (13.098s) — добавлено ожидание готовности
по существующему шаблону. Во втором запуске active/expired уже прошли.
Окончательный полный native check прошёл; product код для этих исправлений
не менялся. Все failed/completed records и диагностические logs сохранены.

| Критерий | Исполняемое доказательство |
| --- | --- |
| AC01–02, срок/повторы/restart | TestRenewalActiveExpiredExhaustedAndRepeat, ConcurrentCreate, native check/restore и retained target digests. |
| AC03, guards/provenance/деньги | UnknownBilling, LateBilling, EligibilityAndFrozenSnapshot, PlanProvenance, LateGuards; native late-ban/plan/identity и readonly money/proof digest. |
| AC04, HTTP/совместимость | HTTPBoundary, ActionMigration; полные Go/web/Python regressions, migrations15–19 не изменены. |
| AC05, интерфейс | renewal.spec.ts вместе с прежними purchase/provider/operator suites; frozen locale retry и немедленное закрытие недоступной формы. |
| AC06, restore/delivery | renewal-local.py restore; один fresh review, exact source CI/manual v2 merge и actual preview являются последующими gates, не выводятся из локальной проверки. |

Решения Native, в порядке принятия; полный ledger остаётся в private archive:

1. Сохранять ранее разрешённое автономное проектирование/исполнение без повторных
   согласований. Цена ошибки: владельцу понадобится пересмотреть локальное предложение.
2. Координатор реализует Native, один свежий reviewer проверяет всю ветку.
   Цена ошибки: нет независимого review каждой отдельной задачи.
3. Только новый web без Telegram/legacy ID доказывает независимые деньги.
   Цена ошибки: связанные аккаунты ждут достоверного Stars-перехода С35.
4. Физический kind purchase сохраняется; purpose определяется order.action.
   Цена ошибки: история должна читать action, а не угадывать назначение по kind.
5. Task1 включает не перечисленные планом YooMoney callback и ручной HTTP route.
   Они нужны для фактического funding/API. Цена ошибки: общим путям нужна регрессия.
6. Существующий markWrite закрывает unexplained future disable до physical reset.
   Цена ошибки: conservative native gate применяется и к funded first purchase.
7. Task2 включает playwright.config.ts: иначе явный testMatch исключает renewal.
   Цена ошибки: в прежней suite появляется ещё один test file.
8. Restore использует отдельную базу собственного PostgreSQL вместо второго
   контейнера: writer остановлен, копия читается READ ONLY. Цена ошибки:
   изоляция базой вместо отдельного сервиса; восстановленный worker не запускается.
9. Local task-done предшествует fresh review; delivery остаётся отдельным открытым
   gate до review/CI/merge/preview. Цена ошибки: завершение задачи реализации нельзя
   принимать за доставку всего сценария; #23 до неё остаётся открытой.

Частные log bytes/durations/source hashes, image IDs, reports и dump находятся
в `.superpowers/acceptance/c16-renewal`; credentials и VPN-идентификаторы не
публикуются. [Команды](../../deploy/purchase/README.md) используют только
собственный Docker/localhost. UI provider submit перехватывается, уведомления
синтетические. Это локальная приёмка приложения; реальные деньги YooMoney,
публичный callback, production и итоговый перенос остаются в С45–С47.
Живой Happ/VPN и доверие Mac не менялись.
