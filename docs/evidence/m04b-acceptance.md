# М04б: локальная приёмка subscriptions и vpn

Дата: 2026-10-06. Владелец [М04 #58](https://github.com/ekho/3xui-shop/issues/58).
Контракт `2026-10-06-m04b-subscriptions-vpn-v2`, Native.
Ветка `feature/m04-subscriptions` от fresh v2
`65560b061438b5ceabb18d10c36502cff24954ff`.
Проверенный код `08f70c88f69964477140c5155ada4db2696a7581`.

Subscriptions владеет заявками, grants, callbacks и правилами доступа; VPN —
устойчивыми операциями, SQL, workers и monthly reset. Старые вызовы platform
делегируют владельцам. Схема, API, зависимости, job kinds, persisted JSON/hashes,
UUID, assignment и ключи сохранены. В сетевой фазе подготовки access/monthly
SQL Tx закрыта; финальный Tx использует исходную physical session и повторяет
проверки прав, аккаунта, незавершённых операций, тарифа и периода.

## Проверки поведения

- Task1 boundary/owner RED: прежний чужой SQL и отсутствие нового owner contract.
  После переноса owner/legacy replay/outcome rollback PASS; полная Go race
  проверка Task1 162.241s. Последующий Task2 проверен отдельно.
- Task2 TLS/PG RED 20.664s: открытая Tx блокировала смену роли/identity/profile,
  monthly допускал очередь после окончания периода. После изменения подготовки
  те же проверки PASS; original-session loss не создаёт replacement enqueue.
- Дополнительный RED 4.413s обнаружил ложный конфликт после обновления
  observed_at; baseline теперь содержит стабильные IDs/status/targets.
  Cached observations не определяют identity. GREEN с same-key concurrency
  15.804s; affected access/monthly/profile/provision race 76.518s.
- Конкурентный повтор одной команды возвращает одну operation/job; повтор с
  недоступной панелью обходится без HTTP. Смена catalogue revision во время HTTPS
  чтения даёт conflict без новой операции. Прежние reset acknowledgement,
  ban reapply, сроки, readback, history и frozen JSON/hash tests проходят.

## Полная матрица

Выполнена один раз через Native task-done на указанном коде. Все 18 этапов exit0.
Private logs и учётные данные не публикуются.

| Этап | Результат | Длительность команды |
| --- | --- | --- |
| names | PASS | 0.163s |
| go-generate | PASS | 0.719s |
| web-generate | PASS | 0.582s |
| generated-drift | PASS | 0.023s |
| contracts-unchanged | PASS | 0.016s |
| go-vet | PASS | 1.138s |
| web-types | PASS | 1.895s |
| web-build | PASS | 1.939s |
| runtime-config | PASS | 0.082s |
| go-race-connected | PASS | 166.811s |
| python | PASS | 20.036s |
| playwright | PASS | 69.984s |
| compose-config | PASS | 0.145s |
| compose-build | PASS | 16.708s |
| smoke | PASS | 23.558s |
| native-up | PASS | 7.766s |
| native-check | PASS | 44.272s |
| native-down | PASS | 1.148s |

Сумма этапов 356.985s.
Full Go race: platform164.807s, connected backend/tests98.810s.
Python105, Playwright110/110. Генерация не даёт drift; сравнение схемы/API/
Go dependencies с baseline пустое. Web warnings сторонних компонентов прежние.
Native: собственные Docker 3X-UI3.7.0 и TLS Mailpit, simulated Bot API;
compiled Go process остановлен после commit и перезапущен. Operation/grant/keys
и panel readback сохранились, Telegram выключен в restart-проверке.
Собственный native стенд остановлен.

## Решения исполнителя

Все Native rulings в порядке принятия; формулировки включают причину и цену ошибки.

- Ruling: preserve Native coordinator execution and one final Astra/high reviewer — accepted autonomous mandate and prior Native choice — cost if wrong: independent judgment arrives only at final review.
- Ruling: transfer owners with existing command ordering in Task1, then close SQL Tx before panel reads in Task2 — keeps mechanical transfer and concurrency change separately attributable — cost if wrong: #58 cannot close at the intermediate task commit.
- Ruling: retain one physical session across both short Tx and prefetch catalogue/operation snapshots — avoids a second pool connection or new read-interface scaffolding during network preparation — cost if wrong: snapshot changes must be caught by the final recheck.
- Task 1: Ruling: consume accounts.RequireOperator; RequireSupportOperator is the existing platform alias — public accounts method verified in operators.go — cost if wrong: adapter error mapping could drift, covered by role/revocation regressions.
- Task 1 Ruling: repair first-run fixtures to obey existing account_source/16-character subID and deferred request-operation FK; cached replay is tested on a restricted valid account — migrations are unchanged — cost if wrong: a fixture failure can hide the intended rollback/replay assertion.
- Task 1 Ruling: adapt two legacy row inspections to test-only raw reads after deleting global generated owner queries; worker timeout/retry facade delegates with an empty VPN worker because those policies need no Service — existing nil-Service backoff test retained — cost if wrong: fixture row mapping drift fails persisted-field assertions.
- Task 1 Ruling: retain existing shared audit/idempotency/delivery queries until M06; owner APIs return stdlib snapshots without leases, with explicit wire field mapping — spec requires persisted compatibility — cost if wrong: full wire/replay/UI regressions must catch a missed field.
- Task 1 Ruling: complete frozen callback fixture in the transfer verification alongside create replay; owner constructor absence was the observed initial RED, this fixture adds no behavior — cost if wrong: a passing compatibility fixture alone cannot prove ownership, covered separately by real SQL boundary RED/GREEN.
- Task 2 Ruling: session idempotency lock in the existing namespace is retained across preparation, acquired before account row locks; release all locks belonging to the dedicated session — concurrent same-key replay otherwise becomes busy, and waiting on idem after taking row locks deadlocks the final phase — cost if wrong: changed SQL wait order must preserve auth/replay/error behavior, covered by concurrent same-key and different-body regressions. Contract v2 clarifies this internal order; public DTO/ports/schema and roadmap remain compatible.
- Task 2 Ruling: final plan guard uses existing catalogue.LockCurrentPlan, with captured used metadata/terms compared — preserves selected revision until commit without HTTP under lock — cost if wrong: a catalogue change during preparation must return conflict, not queue stale desired access.
- Task 2 Ruling: compare only stable baseline operation IDs/status/targets, excluding mutable cached observations — only these fields define confirmed access, observed_at refresh is not an identity change — cost if wrong: an omitted eligibility field could permit a stale operation; role/account/unresolved/plan guards remain separately checked.

## Ревью, доставка и пределы

Fresh whole-branch Astra/high review готовится. CI/merge/preview М04б ещё не
подтверждены, #58 остаётся открытой. Локальная проверка не подменяет эти gates.
Авторизация последовательных PR в v2 и preliminary releases действует;
CI waiver старого PR62 не относится к этой ветке.

Production, реальные платежи/Telegram, внешний SMTP, target benchmark и живой
Happ/system trust не проверялись в М04б. Duplicate-guard 3X-UI3.7.0 не подтверждён,
автоматический uncertain-create retry по-прежнему запрещён. После полного М04
следует адаптация С10/PR5 afaeacf, затем М05 и М06; Python удаляется по С47.
