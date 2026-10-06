# М06c — существующий журнал действий в audit_reports

Дата: 2026-10-06. Контракт `2026-10-06-m06c-audit-v1`, владелец [#60](https://github.com/ekho/3xui-shop/issues/60).
Спецификация принята в рамках разрешённого автономного ведения документов и Native.
Fresh base origin/v2 `d23c848354ac663b953d9faa9b49b09d93e04303`, ветка `feature/m06c-audit`.
М06b2 полностью доставлен [PR #69](https://github.com/ekho/3xui-shop/pull/69)/[dev.27](https://github.com/ekho/3xui-shop/releases/tag/2.0.0-dev.27):
[checkpoint](https://github.com/ekho/3xui-shop/issues/60#issuecomment-6010525855).

## Результат и граница

Модуль `audit_reports` становится единственным владельцем runtime SQL таблицы
audit_events. Accounts, subscriptions, support, vpn и payments записывают событие
через публичный RecordTx в той же транзакции, где меняют данные/задания/idempotency.
Текущая карточка оператора и ветка history kind=audit читают публичный Page.
Новых отчётов, retention, metadata mirror, кампаний или событий не добавляется.
Legacy approval snapshots/events остаются accounts: это отдельная история импорта,
с собственным source_id cursor. Их не объединяем с audit pagination.

М06d следующим удаляет shared platform.Service/store и проверяет весь М06.
#60 остаётся OPEN/In progress. Python retirement — С47. Production, реальные
provider/Telegram requests, установленный Happ/VPN/macOS trust исключены.

## Публичный контракт

Путь `backend/internal/modules/audit_reports`, Go package `auditreports`.
Один небольшой audit.go, приватные queries/store по существующему sqlc pattern.

```go
type Event struct {
    ID, AccountID uuid.UUID
    CreatedAt time.Time
    Action string
    RequestID, OperationID, OperatorAccountID *uuid.UUID
    SupportMessageID, AccessOperationID *uuid.UUID
    OperatorTgID *int64
    Reason, MonthlyPeriod *string
    SystemActor *bool
}
func RecordTx(ctx context.Context, tx pgx.Tx, event Event) error
func New(pool *pgxpool.Pool) *Service
func (s *Service) Page(ctx context.Context, account uuid.UUID, before *time.Time, beforeID uuid.UUID) ([]Event, bool, error)
```

RecordTx не начинает/не завершает Tx, не генерирует ID/time и не подавляет ошибки.
Caller передаёт прежние UUID, время и action. SQL INSERT явно сохраняет все13
колонок; optional pointers сохраняют NULL отдельно от пустой строки/false/zero.
Ограничения FK, reason<=1000, single web/TG actor остаются в прежней схеме.
Нет нового event bus, интерфейса для одной реализации, clock/config или очереди.

Service скрывает pool и sqlc rows от потребителя; пять writers не получают новый
constructor argument и не импортируют reader implementation. Page — доверенный
внутренний read-порт: до вызова текущий consumer проверяет accounts.RequireOperator,
target UUID/existence и пару cursor через существующий operatorClientAccount/history.
Это прежняя граница прав, как у accounts.Lookup; внешнего незащищённого endpoint нет.
App создаёт конкретный reader; временный root адаптер сохраняет error/HTTP DTO.
Модули не импортируют platform/store/wire/app/HTTP или private peers.

## Сохраняемое поведение

- Весь audit SQL из account/subscription/support queries, raw vpn/payment/subscription
  writers и root queries переносится владельцу; не оставляем generated dead queries.
- Audit failure прерывает caller Tx: не фиксируются restriction/session revoke,
  message/attachment, trial/grant/job, access outcome или reconcile/idempotency частично.
  Повтор защищается существующими caller guards; RecordTx не вводит ON CONFLICT/no-op.
- Actor TG остаётся int64, в wire — строкой; web actor/reason/support/access/system/monthly
  поля, request/provision operation links и UTC/monthly caller timestamps сохраняются.
  Нет перечня actions, отбрасывающего старые записи, или изменения error mapping503.
- Page использует прежний `(created_at,id)<cursor`, `ORDER BY created_at DESC,id DESC
  LIMIT 51`; возвращает не более50 записей и more. При before=nil UUID cursor игнорируется.
  Empty list не меняет wire `[]`; card и history сохраняют role/target/cursor ошибки.
- HTTP API, все15 миграций, зависимости, row IDs/links и idempotency JSON/hash неизменны.
  Schema/maintenance/test fixtures остаются прежними исключениями SQL-boundary checker.

## Приёмка

Task1: actual Audit SQL-boundary RED на текущих пяти writers/root → GREEN с
SELECT/JOIN/UPDATE/INSERT/DELETE/quoted-public negative fixtures. Independent legacy
rows со всеми13 полями, NULL/empty/false/int64 max и одинаковым временем проверяют
owner+реальную app/card/history composition, страницы50+2 без потерь/повторов,
изоляцию другого account, caller-Tx visible/rollback/commit и constraint error.
Сохраняем OperatorHistoryStablePages, revoked/restricted roles/cursor tests,
SupportComposition/restriction audit rollback, trial/access/monthly/payment/replay.

Task2: одна committed revision, полный22-stage matrix с real PostgreSQL/Redis/race,
Python/Playwright, local Docker3X-UI3.7.0/TLS, process restart, signed localhost payment
repeat и paid restore/down; API/schema/dependency/generation comparison к fresh base.
Один fresh Astra/high whole-branch review; Important/Critical — один RED→GREEN fix
pass и зелёная suite без re-review. Все rulings/declines публикуются. Exact-source CI,
manual SHA-guarded v2 merge, source-equal tree и preview/tag/три multiarch образа.
