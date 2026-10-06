# М04а: локальная приёмка панельного клиента vpn

Дата: 2026-10-06. Задача [М04 #58](https://github.com/ekho/3xui-shop/issues/58).
Ветка `feature/m04-vpn`, baseline `1a24150726f6ba46a4724649f208ab6c3b362357`
из fresh origin/v2. Проверенная реализация `88170462e9bef1f64bc87dd49485a8773ce12ae0`.
Контракт `2026-10-06-m04a-vpn-v1`; Native.

Все прежние HTTPS-вызовы панели теперь исполняет `modules/vpn`.
Platform содержит только aliases/config mapping и delegation helpers.
Тела протокольных методов побайтно совпадают с прежними после переименования
публичных symbols. Схема, API, web product, dependencies и job kinds не менялись.
Это первый последовательный PR М04; #58 остаётся открытой до подписок,
устойчивых операций/SQL и выноса подготовительных чтений из SQL Tx.

## Проверки

| Проверка | Результат |
| --- | --- |
| Direct-owner RED → GREEN | До переноса отсутствовали vpn Config/NewPanelClient/AccessTarget/ErrPanel. После переноса реальные TLS requests проверяют native UUID id вместо numeric7, allowedIPs array, limitIp3, expiry/traffic/ban и unknown-field сохранность; cancelled/unsafe origin не достигает сервера. Прежние native record и CSRF/login/TLS/no POST replay тесты перенесены в owner. |
| Legacy targets и affected race | PASS: frozen ProvisionTarget/AccessTarget exact JSON, int64 `9007199254740993`, identity/missing-membership helpers; vpn1.694s, platform61.060s. Provision/access/profile/monthly regressions сохраняют прежних потребителей. |
| Full Go race + connected browser | PASS: команда160.756s, platform158.340s, backend/tests100.421s. Модульные import boundaries проверены; никакого platform/store/wire в vpn. |
| Go vet/generation, web generation | PASS; generated drift отсутствует. DB/API/manifest/lock/web/deploy diff с baseline пуст. |
| Web typecheck/build/runtime config | PASS: 1.896s / 1.928s / 0.081s; runtime test из web. Сохранились прежние предупреждения сторонних use client/chunk size. |
| Python | PASS:105 тестов,16.912s; команда18.715s, Python3.13.16 и неизменённый poetry.lock. |
| Playwright | PASS:110/110, команда69.765s. |
| Compose config/build backend/gateway/bot | PASS:0.152s /17.456s; только собственные локальные образы. |
| HTTPS smoke | PASS:21.517s; routing, runtime config, file secrets, повторные migrations, rollback/provision-only restore. |
| Native 3X-UI3.7.0/TLS Mailpit/simulated Telegram | PASS:up7.519s,check44.005s,down1.062s. Physical stop/start compiled backend после commit сохраняет ту же Operation, один Grant, keys и panel readback; Telegram выключен в restart-проверке. Стенд остановлен. |
| Whole-branch review | Astra/high проверил `1a241507..bfb9a2f`: все13 изменённых файлов, потребители и18 закрытых результатов. Critical/Important/Minor0. Подтвердил совпадение20 transport functions и трёх структур после переименований; исправления не требуются. |

Все **18 этапов** полной матрицы exit0; сумма измеренных длительностей
**347.650s**. Полный прогон выполнен один раз через Native task-done.
Private logs не публикуются. Проверки:
[owner](../../backend/internal/modules/vpn/panel_test.go),
[legacy JSON](../../backend/internal/platform/panel_compatibility_test.go),
[native integration](../../backend/tests/native_trial_integration_test.go).
Порядок Docker — [runbook](../runbooks/m01-embedded-telegram.md).

## Решения исполнителя

Все Native rulings в порядке принятия:

| Решение | Почему | Цена ошибки |
| --- | --- | --- |
| Сохранить Native: координатор и один Astra/high final reviewer | Пользователь выбрал Native и автономные последующие документы/реализацию; промежуточные implementer/reviewer и approval-вопросы исключены. | Независимая проверка отдельных решений приходит на финальном gate. |
| М04а переносит транспорт; М04б завершает подписки/устойчивый SQL | #58 требует ограниченных последовательных PR; транспорт не зависит от SQL. | #58 остаётся открытой и С10 нельзя интегрировать до второго PR. |
| Frozen fixtures написаны до переноса, в Step1 | TDD требует новые проверки до product edits; Step4 только запускает их вместе с регрессией. | Меняется лишь порядок тестового шага. |
| Сохранить прежние чтения подготовки command под SQL Tx в М04а | Они существуют в access/monthly; вынос с сохранением ownership входит в М04б. Сетевые записи workers уже вне Tx. | М04 не может закрыться до устранения этих чтений без ослабления контроля конкурентных операций. |
| Commit после focused GREEN, task-done выполняет один full matrix перед evidence commit | Исключает повтор полного одинакового прогона ради ledger; проверки относятся к exact product commit, далее меняются документы. | Нужно различать product/evidence revisions при доставке. |

Все пункты Declined to judge рассмотрены координатором:

| Решение | Почему | Цена ошибки |
| --- | --- | --- |
| Подписки/устойчивые операции/SQL завершаются М04б | Review оценивал только ограниченный М04а; #58 остаётся открытой. | Преждевременная отметка Done скрыла бы общего владельца platform/store. |
| Чтения панели под Tx выносятся в М04б | Текущее поведение признано явно; следующий PR сохраняет ownership и atomic insert+job. | Долгое удержание транзакций при сбое панели останется до завершения второго PR. |
| С10 адаптируется после полного М04 | PR #5 сохранён на afaeacf; #17/#59 знают accounts/catalogue/VPN boundary. | Неверная адаптация может нарушить деньги или однократную выдачу. |
| М05/М06 и удаление Python остаются следующими этапами | Это полный функциональный roadmap; удаление разрешено только по С47 после покрытия. | Раннее удаление потеряет ещё не перенесённые сценарии. |
| Duplicate-guard не считать доказанным; uncertain retry запрещён | Preflight3.7.0 не подтвердил уникальность key/UUID. | Неверное разрешение повторной записи может создать второй клиент. |
| Real Telegram/providers/SMTP проверяются в разрешённой внешней приёмке | Локальный результат использует собственный Mailpit и simulated Bot API. | Особенности реальной среды могут потребовать исправлений до внешнего запуска. |
| Production/Happ/system trust исключены | Владелец исключил живой Happ и не разрешал production-переключение. | Этих доказательств нет; их нельзя выдавать за проверенный rollout. |
| Benchmark целевого окружения остаётся внешней проверкой | Целевые ресурсы пока не предоставлены; М04а не меняет password hashing. | Производительность будущего размещения не подтверждена. |
| Внешний статус М03/PR5 проверяет координатор | М03 exact merge/tag/images/CI проверены через CLI, #57 Closed/Done; remote PR5 afaeacf сохранён. | Без свежих проверок можно неверно продолжить зависимую работу. |
| CI/merge/preview М04а — собственные последующие gates | Review не доказывает доставку; проверки exact-source и эффекты публикации обязательны. | Один review не гарантирует успешную сборку или опубликованный образ. |

Отложенных Minor нет; fix pass и повторный review не требовались.

## Доставка и пределы

Локальная приёмка и review выполнены. [PR #64](https://github.com/ekho/3xui-shop/pull/64)
слит в v2: source `8efc8cccac71c44c80f3dbfa0f37cac5fc26ba2f`, merge
`65560b061438b5ceabb18d10c36502cff24954ff`; parents и tree проверены.
CI Platform `37386149473`, image builds `37386149769` и preview `37387356724`
успешны. [2.0.0-dev.15](https://github.com/ekho/3xui-shop/releases/tag/2.0.0-dev.15)
указывает на этот merge. У всех трёх GHCR images проверены source/version и
linux/amd64 + linux/arm64. Полный М04/#58 остаётся открытым до М04б. Общая авторизация последовательных PR
в v2: [решение владельца](https://github.com/ekho/3xui-shop/issues/55#issuecomment-6004574101).
CI waiver относится только к старому PR #62.

М03 доставлен через [PR #63](https://github.com/ekho/3xui-shop/pull/63),
[2.0.0-dev.13](https://github.com/ekho/3xui-shop/releases/tag/2.0.0-dev.13);
#57 Closed / Project Done. [PR #5](https://github.com/ekho/3xui-shop/pull/5)
сохраняет `afaeacf652964453ddd61883aa6da6a0d285783c` до полного М04.

Duplicate-guard 3X-UI не подтверждён; uncertain create по-прежнему не повторяется.
Production, реальные платежи/Telegram, внешний SMTP, целевой benchmark,
живой Happ и системное доверие не затронуты. SQL и финальные владельцы
подписок/операций, payments и notifications — последующие переносы;
полное удаление Python — С47.
