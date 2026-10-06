# М06b1 — владелец устойчивой доставки Telegram

Дата: 2026-10-06. Контракт `2026-10-06-m06b1-telegram-delivery-v1`, владелец #60.
Основание: [принятая архитектура](2026-10-05-modular-monolith-design.md),
[M06](https://github.com/ekho/3xui-shop/issues/60), автономные документы/Native/
последовательные ручные merges в v2 и preview images/prerelease разрешены пользователем.

## Результат и порядок

Перенести существующий Telegram outbox в `internal/modules/notifications`.
Модуль telegram остаётся каналом Bot API; subscriptions владеет триалом и актуальной
карточкой, accounts — допуском операторов. Внешний HTTP API и поведение не меняются.

M06a доставлен PR #67: source3067bf3413f44b03ed96b918d352722c577a0eaf,
merge6cc8d031ea9185bdbfc25affd00c9fc2f873e42e, preview dev.23, все CI/image checks PASS.
Новая ветка feature/m06b-telegram-delivery начинается с этой свежей origin/v2.
M06 продолжается support → notifications (Telegram → email) → audit → removal
shared platform.Service/store. #60 остаётся OPEN до всех переносов и собственной
архитектурной приёмки. Email — отдельный M06b2: его proof/revocation/SMTP граница
имеет самостоятельную приёмку; объединение с Telegram увеличивает риск одного PR.

## Владение и сборка

Notifications единолично владеет SQL telegram_deliveries: enqueue, latest message/
state, lease и complete. SQL и sqlc/store скрыты за module/internal. Root и
subscriptions больше не содержат этого SQL. API, all15 migrations, dependencies,
DB/3X-UI IDs, sequence, raw payload, lease/result hashes и retry semantics неизменны.
Модуль не импортирует platform/store/wire/HTTP/Echo/app/private peers.

Конструктор без отдельного interface/factory:
`New(pool *pgxpool.Pool, operators func() []int64, allowed func(int64) bool,
card func(context.Context, pgx.Tx, uuid.UUID, int64) (json.RawMessage, error)) *Service`.
App передаёт accounts.OperatorAllowed и callback к subscriptions.CardTx, который
сериализует существующий neutral TelegramPayload. Late-bound callback замыкает
subscription owner, как существующие VPN outcome hooks; import cycle не возникает.
Root test constructor сохраняет dynamic config/clock. Active app.TrialBridge
обращается прямо к notifications; root HTTP facade остаётся до M06d.

Subscriptions передаёт реальный caller pgx.Tx в payload/notify/decisionResult/
reconsiderTrialLocked, включая web-operator и VPN-outcome callers. Новый transaction
для enqueue не создаётся: trial/grant/audit/callback/idem и outbox коммитятся вместе.
Audit extraction остаётся M06c; notification transport выполняется после commit.

## Публичные операции и совместимость

```go
EnqueueTelegramTx(ctx context.Context, tx pgx.Tx, request uuid.UUID, operation *uuid.UUID,
    chat int64, kind string, payload json.RawMessage, createdAt time.Time) error
LatestTelegramMessageTx(ctx context.Context, tx pgx.Tx, request uuid.UUID, chat int64) (*int64, error)
LatestTelegramStateTx(ctx context.Context, tx pgx.Tx, request uuid.UUID, chat int64) (string, error)
ClaimTelegramJobs(ctx context.Context, limit int) ([]TelegramJob, error)
CompleteTelegramJob(ctx context.Context, id uuid.UUID, leaseToken string, result json.RawMessage) error
```

Latest отсутствующего message даёт nil; state — pending. DB errors дают прежний503.
TelegramJob содержит ChatID, JobID, Kind, LeaseExpiresAt, LeaseToken, Payload с прежними
JSON tags; Payload — raw JSON существующей карточки. Empty claim — пустой slice, а не nil. TelegramSent поля ChatId/Kind/MessageId, TelegramFailed — Code/Kind, прежние
JSON order/tags; они нужны app bridge для byte-compatible outcome.

Claim принимает только limit1 (иначе400), выбирает pending/available DB-clock/
expired-or-absent lease/allowlisted chat по sequence с FOR UPDATE SKIP LOCKED.
Opaque32 bytes → URL-safe43-character token; SHA256 хранится в DB; lease ровно60s
по DB clock, attempts+1. CardTx читает актуальный статус и latest target message
в этой же Tx; ошибка card откатывает lease/attempt. Commit предшествует Bot API.

Complete сначала проверяет nonnil id и token length43 (409), затем JSON (400).
sent требует kind=sent, positive chat_id/message_id; delivery_failed — один из
edit_failed/forbidden/invalid_response/network/timeout. Lease всё ещё действителен,
оператор разрешён accounts, hash token сравнивается constant-time, sent chat совпадает.
Повтор завершённого результата требует того же state и прежнего SHA256 от
json.Marshal(raw union): ключи/unknown fields НЕ нормализуются через typed struct.
Changed raw/order/content конфликтует409, как раньше; expired replay также409.
HTTP schema validation неизменна; internal bridge сохраняет прежние outcome bytes.
Completed_at/message_id/failure_code/result_hash и сохранённые jobs не мигрируются.

## Проверка и доставка

TestNotificationsTelegramSQLBoundary: actual RED на старом SQL → GREEN; negative
SELECT/JOIN/UPDATE/INSERT/DELETE/quoted public fixtures. Compatibility pre-seeds
old completed result_hash из wire union, включая нетипичный key order/extra field;
прямой owner и facade допускают точный повтор, отклоняют changed result/invalid
enum/chat/token, не меняют attempts/completed_at/message. Сравнить neutral Sent/
Failed JSON со старым wire для app bridge; payload null/optional fields сохраняются.

Composition использует существующий bridgeFixture: claim/bridge complete/decision
показывают latest message/current state через public Tx ports. Caller rollback
не оставляет enqueue; контролируемый outbox INSERT failure откатывает trial/audit/
idem; card failure откатывает lease/attempt. Существующие lease60/expiry/reclaim/
parallel-lease/decision/reconsider/provision/lifecycle/HTTP tests сохраняются.

Полная существующая22-stage local matrix на одной committed product revision,
Go race connected consumers, Python/Playwright, native3XUI3.7.0/TLS SMTP/simulated
Bot API, signed purchase repeat/paid restore/down. Один fresh Astra/high review;
Important/Critical — один RED→GREEN fix pass + suite без re-review. Exact-source CI,
ручной SHA-guarded merge в v2, preview/tag/три multiarch GHCR images.
Production/real provider/Happ/VPN/macOS trust исключены. Python retirement — С47;
новые рассылки, кампании, Telegram topics/relay — отдельные сценарии.

