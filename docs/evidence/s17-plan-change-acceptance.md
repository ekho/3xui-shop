# С17 — локальная приёмка смены тарифа

Владелец [#24](https://github.com/ekho/3xui-shop/issues/24), контракт
`2026-10-07-s17-plan-change-v1`; [спецификация](../superpowers/specs/2026-10-07-s17-plan-change-design.md),
[план Native](../superpowers/plans/2026-10-07-s17-plan-change.md).
Backend checkpoint `28fa6e2`, исходный web `e30089b`, свежий обзор `1461fab`.
После обзора изменены только Catalogue и четыре rendered regressions; остальные
код/API/native inputs сохранены. Точные source hashes, completed
records и log hashes хранятся в закрытом `.superpowers/acceptance/c17-plan-change`.
Этот отчёт фиксирует локальную приёмку. Точные ревизии свежего обзора,
CI, ручного слияния и предварительного релиза записываются в PR и задаче#24.

Смена использует существующие orders/receipts/River/VPN worker. Неизменяемый
source operation UUID обнаруживает повторное назначение даже того же тарифа.
Новый срок фиксируется один раз как now + оплаченные дни; остаток не переносится.
Продление сохраняет max(expiry,now) + дни. Каждый из пяти реализованных внешних
методов доступен для обоих назначений при его включении, сохраняя свои валюты
и финансовые доказательства.

| Завершённая проверка | Фактическое доказательство |
| --- | --- |
| Backend Task1 | 97 корневых / 390 результатов PASS с race detector, 362.726s. Десять связанных PlanChange roots, пять методов × два назначения, немедленные guards пяти funding paths, старые purchase/renew replay/proofs, HTTP/сессии/CSRF/immutable action/source и migration Down. |
| Web Task2 | 170/170 PASS, 51.404s; typecheck 2.038s, сборка 2.111s. RU/EN375px, клавиатура, warning, все методы/валюты, frozen locale retry, explicit reload и fresh 401/403/404/foreign/purpose/source для YooMoney, hosted и manual report. Проверены 53 web inputs и неизменность backend/API checkpoint. |
| Полный web до обзора | 281/281 PASS, 118.179s на исходных inputs; сохранён как историческое доказательство. |
| Web после исправления P2 | 285/285 PASS, 118.597s; 53 текущих web inputs совпали до и после. Four ordering cases: 2 RED/2 PASS26.305s → 4 GREEN5.330s, typecheck1.788s/build2.318s. Оба порядка ответов для sole Cryptomus/Heleket без ручного выбора валюты; точные renew method/type/price. |
| Полный Go на 1461fab | 335 корневых / 779 результатов PASS, 647.304s; `-count=1 -race -v -timeout=20m`, `RUN_BROWSER_TESTS=1` и защищённые DB/Redis files. Нет FAIL/SKIP/race. 542 входных файла совпали до и после прогона, включая Go/web, настоящее app/tests и native driver. |
| Генерация и статические проверки | Go/sqlc и TypeScript generation без изменения bytes, vet, semantic names, runtime-config self-check и whitespace PASS. Отдельно runtime-config: 5/5 rendered PASS, 5.093s. Сохранены completed records/log hashes; приватные verifiers сверяют исходные ревизии Task1/Task2/Task3 и отдельные свежие web input digests; прежние логи и manifests не переписываются. |
| Python baseline | 105/105 PASS, 18.915s с защищёнными абсолютными DB/Redis files; legacy consumer сохранён. |
| Native runtime | Только cabinet-c17/cabinet_c17, subnet10.253.17.0/28, loopback58443/59444–59447; source labels e30089b и фактические image IDs. Один backend serve; Telegram/legacy transport выключены. Закреплённая 3X-UI3.7.0 и TLS Mailpit; запуск31.708s. |
| Native смена | 7/7 PASS, 31.401s: active/expired/exhausted, regular→euru→regular, late ban/same-plan-source/identity. Подписанные localhost callbacks → River → настоящая панель. Quote/source/сохранённый now-target, IDs/server, лимиты/группы и включение проверены readback; nonzero exhausted traffic стал нулевым. Один receipt/access job, повтор не меняет target/steps/readback. Точное число reset проверяет connected Go panel counter. Поздние конфликты сохраняют paid review без native write. |
| Prepared backup/restart | PASS6.224s: после подготовки funded target исходный backend остановлен, dump восстановлен в отдельную собственную базу без workers. READ ONLY action/source/quote/money/receipt/funding/native identity/target digests совпали. Post-restore auth идемпотентна. Исходный backend применил прежний target; повтор не начисляет срок или новый reset. |

Behavioral RED сохранены: отсутствующий route/heading кабинета (14.629s) и
четыре fresh checkout/operator случая (16.181s). После изменений core GREEN
5.774s, boundary GREEN6.104s и общий Task2 прогон выше. Ранние backend RED и
ошибки только тестовых fixtures сохранены отдельно; они не считаются PASS.

| Критерий спецификации | Связанная проверка |
| --- | --- |
| AC1: тариф/срок/методы/ID | HTTPFlow, AllExternalMethods, Eligibility; native active/expired/exhausted/roundtrip. |
| AC2: live source/права/деньги | LateSource, LateGuards, FundingGuards, ConcurrentCreate; native late-ban/same-plan-source/identity. |
| AC3: replay/target/совместимость | FrozenQuote, ActionMigration, старые persisted compatibility/proofs; native prepared restore/restart. Миграции15–20 неизменны. |
| AC4: кабинет | plan-change.spec.ts и прежние renewal/purchase/manual/YooKassa/Cryptomus/Heleket suites; подтверждение warning до заказа и немедленное закрытие старой оплаты. |
| AC5: полная проверка/доставка | Полные Go/web/Python, генерация и локальный restore завершены. Свежий обзор, exact-source CI, ручное слияние в v2 и опубликованные tag/prerelease/OCI проверяются отдельно и записываются в PR и задаче#24; из локальных HTTP проверок они не выводятся. |

Решения Native и цена ошибки:

1. Сохранить разрешённые автономные документы/исполнение/слияние. Цена ошибки:
   владельцу потребуется пересмотреть конкретное предложение.
2. Координатор реализует Native; один fresh Astra/high reviewer проверяет всю
   ветку. Цена ошибки: нет независимого обзора каждой задачи реализации.
3. С17 завершает все пять методов продления в том же money/purpose boundary.
   Цена ошибки: общая матрица обоих назначений и прежние proof/hash regressions
   должны обнаружить случайное расширение допуска. Решение связано со всеми
   13 затронутыми потребителями в GitHub до изменения кода.
4. Фиксировать применённую source operation UUID, сохранив публичные PlanID
   readers. Цена ошибки: допустимое повторное назначение того же тарифа потребует
   операторского разбора уже оплаченной смены.
5. План расширен реальным api.go manual router и callbacks существующих
   kassa/crypto test fixtures. Цена ошибки: старые callers должны сохранить
   прежние defaults; vendor stubs не копируются.
6. Общая queueFundedPurchaseTx используется четырьмя реальными funding callers
   (YooMoney/manual/YooKassa/Crypto+Heleket). Валидный receipt остаётся валидным;
   paid review — состояние заказа. Цена ошибки: пять реальных stub funding
   guards и прежние деньги/jobs должны исключить ложное подтверждение/повтор.
7. Один sameOrder guard сравнивает immutable order/action/source перед тремя
   checkout/report путями. Цена ошибки: rendered foreign/purpose/source случаи
   обязаны исключить любой provider/manual report запрос.
8. Оператор показывает quote.currency вместо hardcoded RUB. Цена ошибки:
   rendered USD amount/purpose/link и прежние manual RUB decisions должны пройти.
9. С16 и С17 используют один существующий VM-local writer счётчиков. Его
   прежний stopped-panel/identity/membership guard сохранён, входные числа
   проверяются до остановки. Цена ошибки: native exhausted обязан показать
   ненулевой счётчик до выдачи и нулевой после неё; чужие panel DB не затрагиваются.
10. Restore сравнивает отдельную собственную PostgreSQL базу без её workers,
    затем выдаёт доступ только исходный backend. Цена ошибки: digest денег,
    action/source/identity и прежний expiry/target обязаны совпасть; запуск
    восстановленного writer нарушил бы правило одного исполнителя.
11. Завершение задач реализации использует сохранённые completed records и
    текущие input digests; не повторяет успешные дорогие тесты ради task-done.
    Обзор и доставка выделены в фазу после задач, чтобы task-done не объявлял
    будущие результаты завершёнными. Цена ошибки: verifier обязан отклонить
    незавершённый или устаревший отчёт;
    исторические Task1/Task2 доказательства сохраняются на исходных ревизиях.
12. Первый полный локальный Go-прогон достиг стандартного лимита10m во время
    обычного Argon2 хеширования, после721 PASS и без FAIL/SKIP/race results.
    Сохранён его отдельный лог; разрешён один прогон того же кода с явным20m
    package timeout и внешней границей25m. Параметры безопасности не ослаблялись.
    Цена ошибки: увеличенная граница может лишь позже обнаружить зависание;
    следующий эквивалентный повтор без новых фактов запрещён. CI на e30089b
    прошёл с прежним лимитом; его конфигурация не меняется.
13. После одного fresh whole-branch review допускается один проход исправления
    Critical/Important с проверкой затронутых путей; Minor откладывается,
    повторный обзор не запускается. Цена ошибки: согласованная граница не даёт
    второй независимой оценки исправлений. Отклонённые суждения и оставшиеся
    риски записываются в публичном результате обзора, даже если их нет.

Локальная приёмка использует синтетические деньги и собственный localhost.
Все provider submit в браузерных tests перехватываются; native gateway здесь
только YooMoney, остальные четыре протокола проверены connected Go stubs.
Реальные платежи/публичные callbacks/production/внешние SMTP/Telegram и живой
Happ/VPN не проверялись; Mac trust не менялся. Эти внешние границы остаются
С45–С47. Достоверный Stars-переход и Telegram/legacy billing остаются С35;
unknown billing закрыт. С17 не удаляет прежний Python-бот — это финальный перенос.

Результат одного fresh Astra/high read-only обзора 9253b42..1461fab:
Critical0, Important1 (P2), Minor0. При единственном Cryptomus/Heleket ответ
renewal offer перезаписывал уже выбранный USD на RUB, если methods/current order
приходили первыми. Координатор принял finding: существующая инициализация
сохраняет current.currency одной строкой. Четыре browser cases управляют порядком
ответов барьером и прошли actual RED→GREEN; полный web/typecheck/build выше.
Это один согласованный fix pass, повторного обзора нет. Цена этого решения:
исправление проверено затронутыми тестами; текущий CI проверяется отдельно,
без второй независимой оценки.

Исходные full Go и native HTTP results остаются на своих source revisions.
Все backend/API/legacy/native-driver bytes после исправления совпадают; повторный
дорогой Go/native прогон не требуется для изменения только выбора валюты.
Frontend native images с label e30089b исторический и исправление P2 не содержит;
текущий frontend проверен свежим full web и отдельно собирается в source CI/PR.

Координатор явно разрешил все declined-to-judge пункты обзора:

| Неоценённая граница | Решение и основание | Цена ошибки / оставшийся предел |
| --- | --- | --- |
| Реальные платежи, публичные callbacks, production, внешние SMTP/Telegram, живой Happ/VPN, Mac trust | Приняты только собственные заглушки/localhost; внешняя приёмка остаётся С45–С47. Не выводить её из локального PASS. | Реальное окружение/провайдер могут отличаться; до отдельной проверки production готовность не заявляется. |
| Полные Stars-переходы и legacy billing/import | С35 и С46 сохраняют свои критерии; unknown billing сейчас закрыт, старые proof bytes сохранены. | Реальные импортированные и recurring состояния ещё не приняты; неизвестное состояние может законно запретить оплату. |
| Удаление Python-бота | Оставить до финального переноса С47 по принятому роадмапу; встроенный Go Telegram-модуль сохраняется. | Перед переносом нужно подтвердить единственного владельца событий; преждевременное удаление потеряло бы оставшиеся сценарии. |
| Несколько одновременно активных исполнителей и обновление без окна обслуживания | Принят один владелец и допустимое окно обслуживания; С17 не расширяет этот эксплуатационный контракт. | Работа двух процессов/rolling update не доказана и требует отдельной приёмки, если появится такое требование. |
