# С12 — локальная приёмка YooKassa

Дата: 2026-10-06. Владелец [#20](https://github.com/ekho/3xui-shop/issues/20),
контракт [2026-10-06-s12-yookassa-v1](https://github.com/ekho/3xui-shop/issues/20#issuecomment-6016738246).
[Спецификация](../superpowers/specs/2026-10-06-s12-yookassa-design.md) ·
[Native-план](../superpowers/plans/2026-10-06-s12-yookassa.md).

## Проверенные ревизии

Ветка feature/s12-yookassa создана от fresh v2 `d7b69ebd96fe09370c5197d60e1dc6e95324c3e3`.
Task1/backend/API/migration: `5d0bd4dc4561216be4be6c9ecd51d6552b48f3fc`.
Task2/web и исходная ревизия проверок Task3: `6ce3ddad390af1c4afae0ad06d96391d5ffb07da`.
В Task3 добавлены собственные fixtures и точный маршрут webhook в существующем
тестовом proxy. После финального ревью исправлена одна гонка в payments и добавлен
управляемый SQL regression test; текущая полная регрессия приведена ниже. Web/API/
dependencies не менялись. Хеши проверенных файлов и полные логи сохранены в private
`.superpowers/acceptance/c12-provider`.
Это связь проверенного содержимого с последующим commit, без заявления о запуске
тестов на ещё не существовавшей ревизии.

| Проверка | Результат | Время |
| --- | --- | --- |
| Connected provider/старые YooMoney/manual, race | 46 корневых тестов PASS, без skip | 99.292s |
| Новый и существующие purchase/manual browser suites | 45 PASS | 22.877s |
| Весь web mock matrix, включая новый YooKassa suite | 156 PASS, без skip | 89.128s |
| Python contracts/deploy/real Go consumer с protected FILE | 105 PASS, без skip | 13.988s |
| Первоначальный полный Go race, RUN_BROWSER_TESTS=1 | 12 пакетов PASS; один тест в backend/tests FAIL | 387.988s |
| Весь затронутый backend/tests после исправления proxy | 9 корневых тестов PASS, без skip | 98.681s |
| Stdlib API stub self-check | auth/key/bytes/500/recovery/status PASS | 0.605s |
| Own native HTTPS/API/River/3X-UI cohort, обычная сборка | новый клиент и переход с триала PASS | 41.509s |
| Paid-pending backup/restore | read-only restore и source recovery PASS | 12.803s |

Go-регрессия составлена из успешных неизменённых пакетов первоначального полного
запуска и повторного полного затронутого backend/tests. Первый запуск имел exit1,
а не общий PASS: TestWebTrialHTTPContractPaths отправлял новый webhook в SPA200.
В production общий middleware уже отвергал query; исправлен только точный route
тестового proxy. Всего в исходном прогоне 278 PASS и один FAIL; после исправления
все девять integration roots прошли. Source equivalence и хеши логов проверяются
исполняемым private verifier. Собственная exact-source CI остаётся отдельным gate.

Go/TypeScript generation/no generated diff, semantic names, `go vet ./...` и web
typecheck прошли. Go1.27.1; новых production dependencies нет.

## Финальное ревью и исправление

Fresh Astra/high whole-branch review `d7b69eb..a2ec5cc`: Critical0 / Important1 /
Minor0. Important принят: обработка canceled читала receipt вне общей транзакции
с succeeded. Теперь проверка receipt, отмена и отметка противоречия сериализованы
по account/order. Первый финансовый факт сохраняется; новые и уже подготовленные
операции доступа проверяют его состояние. Автоматического отзыва выданного доступа нет.

Управляемый SQL barrier воспроизвёл поведение RED5.530s; тот же тест GREEN7.126s.
Один fix pass, без повторного ревью. [Все Native/Final rulings, их стоимость ошибки
и Declined](s12-decisions.md) опубликованы; deferred Minor нет.

| Проверка исправленного содержимого | Результат | Время |
| --- | --- | --- |
| Полный Go race с RUN_BROWSER_TESTS=1 | 280 корневых тестов / 13 пакетов PASS, включая 47 финансовых; без skip | 388.103s |
| Python contracts/deploy/real Go consumer | 105 PASS | 21.249s |
| Go vet | PASS | 0.733s |
| Обычная новая Docker-сборка, new/trial/500/restart/replay | PASS | 60.797s |
| Paid-pending read-only restore и source recovery | PASS | 13.127s |

Эти проверки выполнены до commit: SHA256 затронутых Go-файлов связывает результаты
с последующим commit, actual container image IDs фиксируют обычную сборку.
156 успешных web-проверок сохранены по неизменённому web-содержимому. Первая полная
Go-попытка и диагностические failures выше остаются историей; текущий полный Go
запуск завершился с exit0. Private verifier проверяет обе группы логов и текущие хеши.

## Критерии и Review Focus

| AC | Доказательство и граница |
| --- | --- |
| AC01 | OrderAtomic/RequestAndRecovery/старые quote и owner regressions: серверная цена, один request/key/job, disabled/verified/owner/replay. Замороженная сумма9007199254740993 сохраняет точность. |
| AC02 | Connected TLS stub проверяет Basic Auth/bytes/key/receipt/return и malformed/redirect/oversize/24h/config drift. Native500 сохраняет первый POST до ошибки; после backend restart те же bytes/key дают тот же ID, после stub restart ID остаётся прежним. |
| AC03 | Actual Echo/SQL tests: effective IP/forgedXFF, malformed body, unknown200/noAPI, forged succeeded body + pending authenticated GET даёт ноль receipt/funding/jobs. FundingBoundary покрывает17 случаев; native forged notice тоже не подтверждает деньги. |
| AC04 | Immutable receipt/first known income, NULL net, foreign-method collision; поздние invalid income/refund/currency и противоречие блокируют подготовленный access. Shared prepare/access/reconcile guard не выдаёт доступ без provider proof; replay сохраняет один receipt/job/access. |
| AC05 | Новый rendered suite17 cases: ru/en/keyboard/preparing→ready, fresh own order перед переходом, lost-response key/body, шесть unsafe URLs, шесть stale-state cases, read403, error/retry/review и return not paid. Старые YooMoney/manual UI проходят в полном156 matrix. Hosted PSP UI перехвачена браузерным тестом. |
| AC06 | Own cabinet-c12: реальный HTTP и River → native 3X-UI3.7.0, новый доступ и переход с триала с прежним identity/expiry плюс30days, devices/traffic; replay один receipt/job/access, unknown income NULL. Paid-pending dump восстанавливается read-only с checkout digest; auth maintenance идемпотентна, restored writers не запускаются. Только исходный backend возобновляет выдачу одного доступа. |
| AC07 | Fresh whole-branch review, exact-source CI, ручной PR→v2 и actual merge/preview остаются отдельными gates; результаты публикуются в #20. До их завершения задача остаётся OPEN. |

Review Focus покрыт напрямую: unknown income/cross-method collision — AC04;
ambiguous POST/deadline/config drift — AC02; body vs authenticated GET/source IP —
AC03; conflicting facts vs queued fulfillment — AC04; fresh keyboard navigation — AC05.

## Стенд и диагностика

Собственный project cabinet-c12, subnet10.253.12.0/28; порты только127.0.0.1.
API `api.yookassa.ru` переопределяется только DNS собственного Docker project,
CA передаётся только контейнерам. Стенд использует pinned 3X-UI3.7.0, защищённые
FILE-секреты, stdlib TLS/SQLite stub на уже установленном pinned Python3.14 image.
Никакого доверия macOS, Telegram/legacy API/reconcile или live VPN клиента.
Native runtime/source/container image IDs и environment preflight сохранены отдельно.

Повторы первоначальных failures записаны с C07 и конкретными изменёнными inputs:
fixture catalogue требовал RUB/USD/XTR; Python consumer требовал абсолютные TEST FILE;
после restart возникал SSL EOF в первые331ms и затем503 на webhook.
Узкий readiness wait принимает только ConnectionError/TimeoutError/SSLEOFError.
Auth/certificate failures не маскируются. Webhook fixture моделирует документированную
повторную доставку только503 с пределом30s; persistent failure завершает проверку.
Диагностический cohort зафиксировал connect: connection refused во время restart;
он не засчитан как acceptance. Затем исходная сборка без диагностического кода
прошла41.509s и restore12.803s. Изменений Go для этой диагностики не сохранено.

Неудачные попытки диагностики, incomplete cold-TLS hypothesis, пустой Python stdin
через wrapper и исправление C07 pending→failed/no-proof также сохранены в ledger.
Пустой exit0 не засчитан как проверка. Все Native/Final rulings и стоимость ошибки
публикуются вместе с финальным review; исходный private export остаётся неизменным.

## Ограничения

**real_payment=false, provider_delivery=false, live_vpn_changed=false**.
Настоящий магазин, hosted checkout/публичный webhook, фискальная настройка, production
и внешние ресурсы не проверены и не изменялись. Сохранение receipt-параметров не
доказывает юридическую или фискальную готовность. Legacy import/cutover/Python removal
остаются С45–С47; продление/тариф/споры/возвраты/MiniApp — отдельными сценариями,
promos/referrals — Р7. Закрытие #20 означает собственную локальную приёмку и доставку в v2.
