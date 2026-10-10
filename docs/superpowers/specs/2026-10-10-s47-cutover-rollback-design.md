# С47: передача исполнения и восстановление

Дата: 2026-10-10. Задача: GitHub #54. База: `e53746c4a13d4209438012a33390ec84eb38539c`.
Контракт: `2026-10-10-cutover-v2`, владелец `operations`; архитектура
`2026-10-05-modular-monolith-v1`. Все семь Blocked-by закрыты и входят в базу.
Разрешены реализация, собственная локальная приёмка, PR/ручной merge в `v2`
и существующие preview GHCR/prerelease. Production переключение не разрешено.

## Сценарий и владение

Оператор включает существующее обслуживание, закрывая новые операции, но
сохраняя приём уже оплаченных событий. Он останавливает прежних писателей
SQLite/панели, main/support pollers и schedulers, получает законченный private
SQLite snapshot, экспортирует/проверяет пакет и выполняет С46. С43 проверяет
восстановление текущего PostgreSQL в отдельную новую БД. Один Go `serve` принимает
HTTP, Telegram и River; Caddy/web работают отдельно. Public subscription links,
VPN UUID/subId/panel key/server ID, Telegram IDs и исходные payment/charge IDs
сохраняются. Импорт не перепровижинит панель и не выдумывает money/trial history.

| Ресурс/операция | Перед передачей | После передачи | Ограничение |
| --- | --- | --- | --- |
| Legacy SQLite и её schedulers | Остановленный старый release, snapshot read-only | Только Go offline exporter читает snapshot | Старый runtime после передачи не запускается |
| PostgreSQL | Go migrations/import/backup по своим операциям | Текущая authoritative БД | Старый snapshot не восстанавливается поверх новых фактов |
| HTTP/River/Telegram/schedulers/3X-UI writes | Единственный подтверждённый владелец | Один `serve` либо recovery `reconcile` | Общая session advisory lock до запуска эффектов; потеря владения останавливает runtime |
| Callback поздних денег | Те же public пути и credentials провайдеров | `payments` проверяет proof и сохраняет receipt | Нет автоматической выдачи по архивному неподтверждённому quote |
| Web/Caddy | Отдельный ingress/static runtime | Отдельный web image | Обслуживание не отсекает money callbacks |
| Backup/recovery | С43 snapshot актуальных PostgreSQL фактов | Новая БД с полной public schema и текущими фактами | Только остановленные writers; проверка digest и идентичности |

Rollback означает остановить ingress новых команд, закончить/остановить текущих
исполнителей и запустить проверенную совместимую Go-версию с той же текущей БД.
Для DB recovery используется новый restore актуального snapshot; после backup
новые события должны быть сохранены/replayed перед открытием. Возврат к Python
или старой SQLite не является поддерживаемым rollback. Не применяется down с
финансовыми или source-history фактами. Второй `serve`/`reconcile` отказывает до
polling, jobs или panel writes; ownership удерживается до их остановки.
Трёхсекундная monitored выдержка нового владельца покрывает локальное окно
обнаружения loss. Это не HA fencing зависшего/приостановленного процесса: перед
переключением требуется подтверждённая остановка прежнего writer/supervisor.

Уточнение приёмки от родительской задачи: исходный `e53746c` не является
совместимым rollback target, потому что ACKs поздние legacy деньги без журнала.
Проверяется новая совместимая checkpoint-ревизия: `cutover-check` (read-only,
exact source commit и schema fingerprints), реальная замена binary/process,
поздний callback после rollback и явный отказ исходному artifact до остановки
текущего процесса. Два проверенных commit могут иметь одинаковую business logic;
это ограничение и их SHA закрепляются в acceptance/checkpoint record.

## Матрица потребителей до удаления

| Потребитель старого Python | Go replacement / проверяемая граница |
| --- | --- |
| `app/__main__.py`, dispatcher, root image/entrypoint/compose | `backend/cmd/server`, `internal/app/lifecycle.go`, backend image, backend/web Compose |
| Identity/approval/admin gates, user/session/Telegram registration | `accounts`, `subscriptions`, native Telegram approval/client handlers, HTTP/operator tests |
| Plan/subscription/purchase and trial issuance | `catalogue`, `subscriptions`, `vpn` public operations, native trial/purchase/access tests |
| Server pool, inbound groups, monthly reset, panel client | `vpn` server/group/access workers and schedulers; native two-panel and VPN tests |
| YooMoney/YooKassa/Cryptomus/Heleket/manual/Stars gateways | `payments` native gateways plus retained old paths, labels and packed Stars decoder; late-money tests |
| Stars auto-renew/charge controls and old callbacks | `payments`/`telegram`; immutable imports and a dedicated legacy receipt journal; no synthetic native funding |
| Promocodes/referrals/trials/rewards/invites | `bonuses`/`campaigns`, aggregate import and native referral/promo/campaign tests |
| Support proxy/topics/attachments | `support` + embedded `telegram` support runtime, native support tests |
| Notifications, audit mirror/retention and reports | `notifications`, `audit_reports`, embedded delivery, native mail/notices/audit tests |
| `WebTrialAdapter`, acceptance Python polling image and `/internal/v1/*` transport | Embedded native Telegram calls public modules; native real Bot API fixtures/restart tests; retired transport returns 404 |
| Restore/operator and downstream account/catalogue/access/subscription fixtures | Public web operator decisions and file-based role CLI; real native dump/restore/reconcile/VPN, empty configured/running Telegram operators and independently disabled native Telegram |
| Full SQLite exporter and its CLI/native rehearsal callers | Offline `server export-legacy` + existing `import-legacy`, exact package validation and read-only SQLite fixture tests |
| Small approvals/payments/campaign exporters and focused harnesses | Go exporter or dependency-free synthetic producer; preserve focused owner import assertions and full aggregate migration |
| Alembic/Poetry/gettext/py3xui/aiogram, root install and release scripts | One `backend/go.mod`, Goose, native Telegram/panel client, web locale strings, backend/web CI images |
| Historical `backfill_panel_limit_ip` | Retired historical repair, not an executor at cutover. Preserve panel limits on import; current explicit operator access operations handle proved terms. Unknown old completed quote cannot authorize an automatic repair |
| Python unit tests of removed product internals | Existing Go native/module tests plus focused exporter/late receipt/owner regression tests; test tooling remains independent of `app`/Poetry |

The detailed final inventory and exact test names belong in the acceptance record.
Python standard-library fixture/orchestration tools do not form a product runtime;
they must never import removed `app` or require old product dependencies/images.
Historical evidence is retained and marked historical where commands were retired.

## Данные, права и совместимость

Единственная объявленная новая DDL — **00044**, если требуется для доказанных
поздних legacy receipts. `payments` владеет этим журналом, unique receipt proof,
состоянием review и refund/recurring observations. YooKassa refund ID даёт
`refunded`, aggregate payment ID даёт отдельный `refund_observed`; суммы этих
наблюдений не складываются. Source history остаётся
immutable. Проверяются сохранённые signature/provider API/Telegram transport,
merchant/bot/payer/currency/amount и исходные IDs; неизвестный invoice не становится
оплаченным native order. Повтор одного proof не выдаёт доступ/бонус второй раз;
конфликт не перезаписывает первый proof. Ошибка БД не подтверждает потерянное
событие. Secret/provider payload/keys не попадают в audit, logs и delivery evidence.
Операторский offline report проверяет текущую роль и выводит только безопасные
идентификаторы/проверенные суммы и классификацию review.

Ранние legacy Stars payloads `subscription:...` и payment IDs берутся из
реального старого кода и source snapshots, а не придумываются. Известный старый
public webhook path продолжает вести к единственному Go handler. Изменение
delivery owner не требует новых panel/VPN keys или внешнего invoice.

## Приёмка

1. Полный Go export/import synthetic SQLite: strict schema/types/private paths,
   ошибки, dry-run/apply/replay, operator rights, все original IDs и unknown units.
2. Два реальных процесса против собственной БД: второй owner отказывает без
   HTTP/River/TG/panel effects; SIGTERM/restart/reconcile сохраняют одного owner.
3. Реальные HTTP/provider и Telegram fixtures в обслуживании: native и legacy
   late payment, duplicate/conflict/refund/recurring; durable receipt, audit,
   нет незаслуженного fulfillment и нет потери подтверждённых денег.
4. Cutover/rollback/restart поверх текущего PG с реальными разными artifacts:
   compatible checkpoint принят, исходный `e53746c` отклонён, после rollback
   прежний и новый поздние callbacks не теряются. Новые accounts/orders/receipts/
   grants/jobs и исходные keys остаются. Реальный С43 backup/restore новой БД,
   полный public-table digest; затем replay без перезаписи поздних фактов.
5. Все действующие Go/static/generated/security/native/browser/backup/cutover
   scopes и backend/web image gates остаются в CI. Python bot gate удаляется
   вместе с продуктом. Независимое whole-branch review после исправлений.
6. Exact-source CI, ручной merge в `v2`, cleanup собственных fixtures, Closed/Done
   и evidence родителю. Preview после merge не отдельный DoD gate.

Локальная приёмка не доказывает production конфигурацию, реальную доставку
Telegram/PSP или состояние панели пользователя; эти ресурсы не используются.
