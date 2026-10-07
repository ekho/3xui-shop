# С19 — Локальная приёмка восстановления выдачи

Owner [#26](https://github.com/ekho/3xui-shop/issues/26), canonical `2026-10-07-s19-fulfillment-recovery-v1`. [Спецификация](../specs/2026-10-07-s19-fulfillment-recovery-design.md), [Native-план](../plans/2026-10-07-s19-fulfillment-recovery.md). База origin/v2 `5246923c6a7fe92a1f29693d401fde292368d63d`; реализация проверена на `872a9eb1c09efa5ba403950e3e12d713b04b8593`. Последующие изменения этого отчёта/плана не меняют runtime inputs. Ревью и доставка пока pending; локальная приёмка не означает production-готовность.

## Изменение

Оператор может проверить неизвестный результат reset без нового согласия на сброс трафика. Отмена, повторное открытие формы или обновлённая операция требуют нового явного согласия для destructive retry. Подготовка оплаченного заказа отменяет старый запрос при смене карточки и не публикует его access ID; неизменённый запрос после потерянного ответа сохраняет Idempotency-Key, другой order/operation/input получает новый.

Действующие payments/subscriptions/VPN/River уже сохраняют деньги, target и задания. Новых Go product writers, API/DTO, migrations или dependencies нет. Добавлены оплаченный lost-create regression и opt-in локальная проверка двух остановок скомпилированного Go-процесса. Default YooMoney fixture выключен; файлы локальных секретов создаются с режимом 0600 и не ротируются при resume.

## Матрица проверенных сценариев

| Сценарий | Доказательство и результат |
| --- | --- |
| Safe ambiguous-reset verification | Новые rendered assertions сначала RED: confirm был disabled без checkbox. GREEN: EN/RU, 375px, клавиатура; отправляется `acknowledge_reset_cost=false`. |
| Fresh destructive consent | RED: cancel/reopen сохранял checkbox. GREEN: после cancel и нового operation state согласие сбрасывается; explicit true и неизменённый lost request проверены отдельно. |
| Поздний preparation response другой карточки | RED: старый запрос не отменялся. GREEN: фактический browser request отменён; нет access lookup для старого клиента. Прежние role/late-read/key assertions сохранены. |
| Оплаченный create выполнен, ответ потерян | `TestRegressionPurchaseLostCreateReply`: один Add в TLS panel fixture, readback того же UUID/SubID/абсолютного expiry/device/traffic target, одна receipt/access operation. Повтор worker не создаёт второй эффект. |
| Проверка силы lost-create теста | Рабочий baseline GREEN. Удаление только readback после Add дало RED новой assertion. VPN worker восстановлен байт в байт; никаких Go product changes в итоговом diff. |
| Повтор webhook и другой реальный receipt | Текущие purchase/YooMoney regression checks: duplicate не добавляет деньги/доступ, второй реальный receipt сохраняется отдельно, funding/target/access остаются одни. |
| Timeout/частичный reset/явное повторное согласие | Текущие access reconciliation и purchase ambiguous-reset checks: counters readback, needs_review и расходование explicit consent сохраняются. |
| Expired pending, cancel и поздняя оплата | Текущие purchase/YooMoney funding guards: actual payment time, cancel/order races и спорные receipts проверены; recovery не отменяет financial review. |
| Два конкурентных действия и отозванные права | Текущие owner-lock/reconcile guards и rendered denial checks: права проверяются для нового действия; уже принятые paid jobs продолжаются после отзыва роли. |
| Stop после receipt/job commit | Реальный compiled backend остановлен при paid order без access; после запуска того же image River создал один сохранённый target/access job. |
| Stop после target/job commit | Второй stop и запуск того же image; applied подтверждён real 3X-UI 3.7.0 readback. Funding/access/target/identity сохранены. |
| Duplicate до и после restart/applied | Подписанное собственным fixture уведомление не меняет receipt/operation/job counts и target. Три actual StartedAt, один image; одна receipt/access operation и по одному purchase/access job. |

## Выполненные команды

Все записи завершены с exit 0 на неизменённых inputs. Приватный verifier повторно сверяет текущие файлы, input manifest, log SHA-256 и runtime inputs; дорогие suites не повторяются только ради оформления task-done.

| Проверка | Результат |
| --- | --- |
| `npm run typecheck`; focused purchase/subscription-operations Playwright | PASS; 40/40, 29.669 s |
| `python3 -m unittest tests.test_paid_recovery_acceptance -v` | RED→GREEN 3/3, 0.538 s; signature vector/0600/default-off и оба compose consumers |
| Go focused `Regression(Purchase\|YooMoney\|AccessConcurrent\|AccessReconcile)` с `-race` | PASS, 47.432 s package time |
| `LOCAL_RUNTIME=native LOCAL_YOOMONEY_FIXTURE_ENABLED=true python3 deploy/acceptance/local.py up` | PASS, 33.260 s; собственный Docker-проект |
| Та же конфигурация, `python3 deploy/acceptance/local.py paid-recovery` | PASS, 9.225 s; actual paid process restarts/readback |
| `make generate`; `npm run api:generate`; `make vet` | PASS; generated drift отсутствует |
| `RUN_BROWSER_TESTS=1 go test ./... -count=1 -race -timeout=20m -json` | PASS, 806 test/subtest passes, 346 top-level tests, 13 tested packages, 680.144 s. 0 failures/0 skipped tests; 12 пакетов без test files. Flags совпадают с `make test-integration`, `-json` меняет только вывод. |
| `npm run build`; `node scripts/runtime-config.test.mjs` | PASS, 3.867 s / 0.107 s |
| Полная `npm run test:e2e` | PASS, 299/299, 128.008 s |
| `poetry run python -m unittest discover -s tests -v` | PASS, 110/110, 16.998 s |
| `python3 deploy/acceptance/check_names.py`; `git diff --check` | PASS |

Manifest SHA-256 backend `e41345ab6d3cc052e8edd94f6d713fd816b66adbfea73e1cff67a605684ded0a`; web `5b732004e8a58b09d2f1f80d17795c71bf1d9a56b455c2cb68d15672ee87ba16`; Python/deploy `2c2fb74e694df28bf1af39cc9d76cb4e15f300292c49fbad72d1112f6e8563cc`. Go JSON-log SHA-256 `1034c7b919e84c1096f5c0601980e8b75c8d932ae5b565bf6c14dbc83f02eedb`; web full log `92cf3cc1f10b01093c18be1ac0b496f178d161741484c40694a8d314236d470d`; native paid log `90d40a903126abb720550f7021c990f1a5de3012109f3d2e00aa832380ec7e40`. Полные source-bound записи и fixture proof остаются приватными; секреты, персональные данные и VPN-ключи не публикуются.

## Среда и ограничения

Собственный localhost Docker-проект: PostgreSQL/Redis, TLS Mailpit, compiled single Go backend, Telegram disabled и 3X-UI **3.7.0**, pinned digest `sha256:3b3131f1876e6bf35063a9ec4dd1c594e4525180bfc2e1c477dcc8a3c9550ca1`. Собственные кошелёк-заглушка и notification secret; реальному провайдеру checkout/платёж не отправлялись. Собственный стек остановлен после proof, чужие контейнеры сохранены.

Actual native preflight не подтверждает безопасное automatic uncertain-create retry: панель не обеспечивает нужную cross-inbound UUID uniqueness. Его прежний запрет сохраняется; оператор использует проверяемую сохранённую операцию. Нельзя выдавать это доказательство за проверку внешних финансовых сервисов.

C13 external acceptance остаётся OPEN: нет публичного callback/реального YooMoney перевода. Production, внешний SMTP/Telegram, живой Happ/VPN и Mac trust не затронуты. Refund/disputed-funding решения принадлежат С20, Stars — С34/С35, maintenance — С42, итоговая readiness — С45–С47. Нефатальные MUI/NO_COLOR warnings в web output не скрыты; tests/build exit 0.

## Native rulings и цена ошибки

1. Документы, реализация и merge выполняются без нового approval: владелец уже разрешил автономную последовательную работу и выбрал Native. Цена ошибки — корректирующая ветка/PR.
2. Использованы рабочие durable money/access workers и guards, без второго scheduler/migration/API/writer: это минимальное выполнение С19. Цена ошибки — добавить узкий guard только при обнаруженном fault-test gap.
3. Полные/дорогие checks выполняются один раз на изменённых inputs, task-done сверяет завершённые source-bound records. Цена ошибки — инвалидировать запись и выполнить затронутую проверку.
4. Полная Go integration использует Makefile flags с `-json` для точного подсчёта; семантика tests/browser flag сохранена. Цена ошибки — повторить `make test-integration`.

Final review, его rulings/deferred minors и delivery evidence будут дописаны по фактическому результату. Повторного обзора не планируется.
