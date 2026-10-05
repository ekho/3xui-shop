# М04б — подписки и устойчивые операции VPN

Дата: 2026-10-06. Владелец: [М04 #58](https://github.com/ekho/3xui-shop/issues/58).
Контракт `2026-10-06-m04b-subscriptions-vpn-v1`; архитектура
`2026-10-05-modular-monolith-v1`. Документы, реализация и последовательные merge
в v2 автономны по [поручению владельца](https://github.com/ekho/3xui-shop/issues/55#issuecomment-6004574101).
Выполнение Native, один свежий Astra/high final reviewer.

## Результат

Завершить М04 после [PR #64](https://github.com/ekho/3xui-shop/pull/64):
перенести прежние заявки/решения, представление подписки и команды в
`subscriptions`, устойчивые панельные операции/исполнителей и monthly reset
в `vpn`. Platform становится совместимым фасадом для этих сценариев.
Панельный протокол М04а переиспользуется. Таблицы, миграции, HTTP API,
River kinds, UUID, assignment, VPN UUID/subID/key, JSON и правила не меняются.

Перенос обоих владельцев связан одной транзакцией выдачи. Выбран один следующий
PR с двумя задачами: сначала перенос владельцев с прежним поведением, затем
вынос сетевых чтений из SQL Tx. Альтернативы — новые repositories поверх общего
store или несколько промежуточных query-фасадов — оставили бы чужой SQL либо
добавили бы код, который пришлось бы сразу удалить.

Новых клиентских функций, платежей, общего event bus, интерфейсов с единственной
реализацией и зависимостей нет. Реализация С10/PR #5 сохраняет
`afaeacf652964453ddd61883aa6da6a0d285783c` до интеграции после полного М04;
затем следуют М05 и М06. Python main/support runtime удаляется только по С47.

## Владельцы данных

| Владелец | Данные и операции |
| --- | --- |
| subscriptions | trial_requests, trial_grants, decision_callbacks; eligibility, web/Telegram решения, пересмотр, разрешение reconciliation; отображение подписки/ключей; бизнес-правила изменения доступа и выбор тарифа |
| vpn | trial_operations, access_operations, monthly_reset_periods; persisted targets/observations, lease/session ownership, запись панели/readback, retry/needs_review, monthly scheduler |
| accounts | Прежние identity/roles/restriction/assignment/access metadata через публичные lookup/lock/authority/mutation методы; общий чистый predicate допустимого источника аккаунта |
| catalogue | Прежние CurrentPlanTx/LockCurrentPlan/UnlimitedPlansTx; снимок предложения в первом Tx и повторная проверка в финальном |

У первых двух владельцев свой `internal/queries` и sqlc `internal/store`.
SQL этих таблиц удаляется из глобального store и других модулей.
История оператора получает заявки у subscriptions, а operation metadata —
одним batch-read у vpn; JOIN чужих таблиц не остаётся. Snapshot DTO не содержит
credential/lease hashes, worker PID или сырой сгенерированный store.Row.

Idempotency/audit/доставка используют прежние таблицы и namespace. Нужные
узкие запросы временно остаются у вызывающего владельца; передача notifications
и audit_reports — М06. Это сохранение работающего исполнения, без второй копии
доменного правила или нового обработчика доставки.

## Публичные операции и сборка

Concrete `subscriptions.Service` использует accounts, catalogue и vpn.
Concrete `vpn.Service` использует accounts и River. Обратное завершение выдачи
связывает app: callback VPN → `subscriptions.RecordTrialOutcomeTx`, выполняемый
в caller Tx. Go import cycle, setter и отдельная шина событий не вводятся.
Отсутствие callback не позволяет закоммитить успешное применение/review.

- VPN `ReserveTrialTx` сохраняет снимок операции и River job в caller Tx;
  subscriptions сохраняет grant/решение/audit/карточки в той же транзакции.
- `TrialState`/`AccessState` возвращают необходимую метаинформацию и сохранённые
  target/observations, без lease полей. Getter, unresolved-check, requeue,
  batch metadata, `QueueAccessTx` и observation/read-profile методы принадлежат vpn.
- subscriptions предоставляет прежние trial/decision/reconsider/reconcile,
  `CreateAccessOperation`, `GetAccessOperation`, `ReconcileAccessOperation`,
  `Subscription`, `SubscriptionKey`, `OperatorSubscription`, `TrialHistory`,
  `CanRequestTrial` и `CardTx` с нейтральными DTO.
- Domain DTO сохраняют порядок полей/tags и nil/empty для persisted hashes и
  результатов. Platform/app явно переводят wire/Telegram DTO, модули их не импортируют.
- App создаёт владельцев и callback; Telegram actions вызывают subscriptions,
  cmd регистрирует VPN workers и scheduler. HTTP/CLI и старые worker wrappers
  делегируют. Прежние test fixtures, меняющие clock/config/queue, сохраняются
  через узкие getters; production зависимости остаются неизменяемыми.

GrantApplied, assignment, VPN applied/readback, audit и карточка остаются в
одной финальной транзакции на physical session, державшей account-access lock.
Ownership watchdog, bounded cleanup, max attempts, persisted first-started time,
unknown panel fields и запрет unsafe повторного POST сохраняются.

## Подготовка команд вне SQL Tx

Команда открывает dedicated physical session. Первый короткий Tx проверяет
actor/account, idempotency/replay, незавершённые операции и пригодность назначения.
Повтор сохранённого запроса возвращается до любого HTTP к панели. В той же
session команда получает account-access lock, читает необходимые operation/
catalogue snapshots и закрывает Tx. Панельные чтения и построение цели используют
эти снимки без открытой SQL-транзакции и без второго соединения из pool.

Перед записью второй короткий Tx на той же physical session заново проверяет
роль, account identity/restriction/profile/ban/assignment, idempotency/replay,
unresolved trial/access и использованный текущий тариф/revision/archive.
Изменившееся состояние не порождает операцию или job. Операция, River job,
audit и idempotency result сохраняются атомарно. Потеря physical session
запрещает финальный Tx; замена connection не используется.

Для read-only подготовки не нужен новый watchdog: HTTP уже ограничен timeout,
а финальный Tx обязан использовать исходную session. Рабочий watchdog перед
внешними записями остаётся прежним. Dedicated owner скрывает connection и
позволяет только Begin/TryLock/Release; бизнес-модуль не получает VPN Queries.

Monthly reset сохраняет period/timezone, waiting/deferred/skipped/elapsed,
one-hour catch-up, unique claim+job и отсутствие продления. Между короткими Tx
проверяет панель; перед очередью повторно проверяет период, eligibility,
identity и отсутствие конкурирующей операции. Неоднозначный reset требует
прежнего acknowledgement; VPN-ban после native resetTraffic восстанавливается.

## Совместимость и приёмка

1. SQL/import boundary checks отвергают доступ к чужим таблицам/реализациям.
   Прямые owner tests и frozen JSON/hash fixtures не подменяют HTTP/DB поведение.
2. Прежние web/Telegram approve, reject, support reconsider/reconcile,
   profile/key, restriction, compensation/assign/starter/reset, profile/ban,
   unlimited/monthly и history сценарии проходят regression.
3. TLS handler видит ноль idle-in-transaction соединений во время панельного
   чтения access/monthly. Потеря session и изменение прав/identity/операции
   между чтением и commit не создают новую операцию/job или панельную запись.
4. Старые hashes/results/targets/jobs декодируются и повторяются; нет второго
   grant, новых ключей либо автоматического повторения uncertain create/reset.
5. Full Go race/vet/generation и connected consumers; 105 Python, Playwright,
   web build/runtime config; Compose build/smoke; собственные Docker PG/Redis,
   **3X-UI 3.7.0**, TLS Mailpit, simulated Telegram и physical stop/start backend.
6. Один whole-branch review, exact-source PR CI, merge в v2, GHCR images и
   preliminary GitHub Release. Только после всех частей закрывается #58.

Production, реальные деньги, живой Happ/system trust, внешний SMTP и benchmark
целевого окружения исключены. Их прежние ограничения остаются в приёмке;
локальный прогон не подменяет финальный перенос.
