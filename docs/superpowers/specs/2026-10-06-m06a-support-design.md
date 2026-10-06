# М06а — владелец web-поддержки

Дата: 2026-10-06. Контракт `2026-10-06-m06a-support-v1`, владелец #60.
Основание: принятая [архитектура](2026-10-05-modular-monolith-design.md),
[M06](https://github.com/ekho/3xui-shop/issues/60) и разрешённое автономное
ведение документов/Native/последовательных ручных merges в v2.

## Результат и порядок

Перенести существующий С05 в `internal/modules/support`: переписка, вложения,
получение страниц, подтверждение получения, open/closed и независимый support-ban.
Правила и внешний API сохраняются. Клиент получает ту же поддержку без Telegram;
оператор использует прежний React-admin. Новые Telegram topics/relay относятся к С37.

M06 выполняется четырьмя ограниченными планами: support → notifications → audit →
удаление общего platform.Service/store после переноса всех потребителей. #60
остаётся открытой до последнего переноса и её собственной архитектурной приёмки.
M05 доставлен PR #66, merge b5d8eb315b02c23ee7a132d9845edbd759b3117b,
preview dev.21; С05/#10 и М04/#58 закрыты. Новая ветка feature/m06a-support
начинается с этой свежей origin/v2.

Выбран перенос поддержки первым: это одна существующая граница с готовыми тестами.
Перенос audit первым тоже возможен, но затрагивает всех владельцев сразу; общий
перенос трёх модулей объединяет независимые риски в одном review/rollback.

## Владение и сборка

Support владеет SQL `support_conversations`/`support_messages`, включая все
чтения карточки оператора. Реализация не импортирует platform, общий store,
wire, HTTP, Echo, app или private peer packages. Собственный sqlc/store скрыт
за module/internal. Схема и прежние идентификаторы/sequence не меняются.

Accounts остаётся единственным владельцем аккаунтов и ролей. Support вызывает
публичные Lookup/Lock/RequireOperator/LockOperatorPair. Ошибки ErrNotFound
преобразуются только для прежнего ветвления; ошибки роли/ограничения сохраняются.
Порядок account locks по UUID, затем conversation/idempotency прежний; отзыв роли
и бан во время заблокированной записи по-прежнему побеждают запись.

`support.New(pool *pgxpool.Pool, limiter *redis.Client, authority *accounts.Service,
rateNamespace string, now func() time.Time) *Service`; nil now означает time.Now.
App создаёт owner и передаёт его в временный platform facade. Тестовый constructor
использует существующий динамический clock. Отдельный interface/factory не нужен.
Shared audit writer пока переносится вместе с support в caller Tx; единый audit
port определяется отдельным М06с, без изменения atomicity текущей операции.

## Публичные типы и операции

Нейтральные SupportAttachment, SupportConversation, SupportMessage, SupportResult
сохраняют порядок полей, JSON tags, timestamps, null и пустые массивы старых wire DTO.
UUID — google/uuid, enum values — string. Это обязательно для сохранённого replay.

- SupportAttachment: Name, SizeBytes.
- SupportConversation: CreatedAt, CustomerReceivedSequence, Id,
  OperatorReceivedSequence, Status, SupportBanned, UpdatedAt.
- SupportMessage: Attachment, CreatedAt, Delivery, Id, Sender, Sequence, Text.
- SupportResult: Conversation, HasMore, Messages, OldestSequence.

Публичные методы сохраняют параметры существующих операций и возвращают neutral DTO:

```go
Support(ctx, actor, target, operator) (SupportResult, error)
SupportHistory(ctx, actor, target, operator, beforeSequence) (SupportResult, error)
CreateSupportMessage(ctx, actor, target, operator, key, text, name, file) (SupportMessage, bool, error)
AcknowledgeSupport(ctx, actor, target, operator, sequence) error
SetSupportState(ctx, actor, target, operator, state) error
SetSupportBan(ctx, actor, target, banned, reason) error
SupportAttachment(ctx, actor, message) (string, []byte, error)
Conversation(ctx, actor, target, operator) (*SupportConversation, error)
```

ctx — context.Context; actor/target/key/message — uuid.UUID; operator/banned — bool;
beforeSequence/sequence — int64; text/name/state/reason — string; file — []byte.
Conversation обслуживает существующую карточку оператора через тот же access check:
отсутствующая переписка даёт nil; произвольный доступ к чужим таблицам убирается.
Error содержит прежние Status, Code/Message и RetryAfter. RequireSupportOperator
в facade остаётся прямым вызовом accounts.RequireOperator для HTTP authority.

## Сохранённые правила

Текст до 4000 Unicode символов, запрет NUL; имя файла 1–128 символов, без control
characters/путей; файл до 10 MiB, переписка до 50 MiB; 30 сообщений за 15 минут.
Redis outage запрещает новую запись. Rate namespace/Lua/TTL и Retry-After прежние.
HTTP проверяет auth/role/CSRF/origin до multipart, сохраняет body cap и private
download headers. Support-ban не меняет VPN и не закрывает чтение истории.

Message, conversation timestamp/state, audit и idempotency result сохраняются в
одной Tx. Прежний principal/operation/anonymous input field order, file SHA-256,
result JSON и advisory namespace неизменны. Same-key replay не добавляет сообщение,
не обновляет timestamp и не расходует новый rate slot; changed body конфликтует.
Повтор ранее сохранённого ответа после support-ban сохраняет прежнее поведение;
account restriction/отозванная operator role проверяются до replay.

Страница содержит последние 50 сообщений в возрастающем sequence; 51-я запись
задаёт has_more, oldest_sequence — первый элемент страницы. Stored становится
delivered только после подтверждения противоположной стороны. Ack монотонный,
sender не подтверждает своё сообщение. Новая запись открывает closed переписку.

## Проверка и доставка

Добавить SupportSQLBoundary через существующий AST/SQL checker: actual RED на
старом SQL → GREEN после переноса, с отрицательными fixtures. App compatibility
проверяет старый JSON/hash/result из pre-seeded records, включая attachment/null/
empty arrays. App composition проверяет сообщение/карточку/вложение, ban/revoke и
откат message/conversation/audit/idem при контролируемом отказе audit INSERT.
Существующие real-PG/Redis support/concurrency/quota/HTTP/browser tests сохраняются.

Полная матрица существующего 22-stage driver: generation/API/migrations/dependencies,
Go race и connected consumers, Python/Playwright, Compose/smoke/native3XUI3.7.0/TLS
SMTP, signed purchase repeat/paid restore/down на одной product revision. Один fresh
Astra/high final review; Important/Critical исправляются одним RED→GREEN pass без
re-review. Exact-source CI → ручной SHA-guarded merge в v2 → preview/tag/три multiarch
GHCR images. Production, реальные деньги/provider settings, живой Happ/VPN/trust
Mac исключены. Python retirement — С47; новые отчёты/кампании/бонусы — свои сценарии.
