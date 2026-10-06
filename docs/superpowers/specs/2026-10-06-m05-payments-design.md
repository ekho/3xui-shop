# М05 — владелец денег и YooMoney

Версия `2026-10-06-m05-payments-v1`, владелец #59. Основание:
[модульный монолит](2026-10-05-modular-monolith-design.md), доставленный М04
и С10/PR #5. Свежая база `origin/v2`:
`c57c5e77f0368ac449ae4fbbb7581389cf108f74`; ветка `feature/m05-payments`.

## Результат и границы

Существующие purchase orders, receipts, funding, YooMoney и preparation worker
переходят из `platform` в `internal/modules/payments`. HTTP/River/Telegram
продолжают работать одним Go-процессом. Сохраняются все правила С10, публичный
HTTP API, миграции, форматы quote/result/targets и ожидающие задания.

М05 переносит работающий сценарий, без новых провайдеров, продления/смены тарифа,
Stars, возвратов или переключения кошелька. Support/notifications/audit и
удаление общего `platform/store` остаются М06; удаление Python runtime — С47.
Production, настоящие деньги, URL кошелька и живой Happ/настройки Mac исключены.

## Владение и публичные операции

Payments единолично читает/меняет `purchase_orders` и `purchase_receipts`.
Существующий raw SQL остаётся в его файлах; private sqlc store содержит только
необходимые shared idempotency/audit запросы, как у текущих владельцев. Schema
не меняется. Запись существующего audit внутри reconcile остаётся в caller Tx;
М06 отдельно определит audit port для всех producers. Это не новый audit модуль.

```go
func New(pool *pgxpool.Pool, authority *accounts.Service,
    catalogueOwner *catalogue.Service, accessOwner *vpn.Service,
    queue func() *river.Client[pgx.Tx], config func() Config,
    now func() time.Time) *Service
```

`Config` содержит CabinetOrigin, PanelID, YooMoneyWalletID (string),
YooMoneyEnabled (bool), YooMoneyNotificationSecret ([]byte). Panel client
создаёт существующий `vpn.PanelClient()`. Production callbacks неизменяемы;
test composition сохраняет нынешние динамические cfg/queue/clock.

Экспортируются существующие операции с нейтральными типами payments:

- PaymentMethods(ctx, account) → (PaymentMethods, error).
- CreatePurchaseOrder(ctx, account, key, PurchaseOrderInput) → (PurchaseOrder, error).
- PurchaseOrder(ctx, account, id) → (PurchaseOrder, error).
- CurrentPurchaseOrder(ctx, account) → (CurrentPurchaseOrder, error).
- CancelPurchaseOrder(ctx, account, id, key) → (PurchaseOrder, error).
- OperatorPurchaseOrder(ctx, actor, target) → (CurrentPurchaseOrder, error).
- ReconcilePurchaseOrder(ctx, actor, target, id, key, PurchaseReconcileInput)
  → (PurchaseOrder, error).
- ReceiveYooMoney(ctx, url.Values) → error; входные поля уже разобраны HTTP
  адаптером с прежними UTF-8/size/duplicate guards.
- FulfillPurchase(ctx, orderID) → error.
- CheckPurchaseAccess(ctx, tx, orderID, accountID, operationID) → (string, error).
- RecordPurchaseAccessTx(ctx, tx, operationID, status, reason) → error.

Здесь ctx — context.Context; все ID — uuid.UUID; tx — pgx.Tx.
Публичные DTO повторяют прежние поля и JSON tags; enum значения представлены
string. Порядок полей, null/empty arrays и вложенных объектов сохраняется:
JSON участвует в сохранённых SHA-256 body hashes и replay results. Domain
не импортирует `wire`, `platform`, `httpapi`, Echo или private peer packages.
HTTP facade переводит поля явно, без нового serializer/interface/event bus.

## Другие владельцы и атомарность

Accounts предоставляет Lookup/LookupTx/Lock, RequireOperator/LockOperatorPair.
Payments не читает account/role SQL. Nullable поля Snapshot сравниваются по
значению и наличию, а не по адресам Go pointers: два чтения одного аккаунта
не должны приводить к ложному `account_changed`.

Catalogue.LockCurrentPlan удерживает текущий тариф до caller commit/rollback.
VPN OpenAccessOwner/TryLock/Begin/Release сохраняет ту же physical session;
SQL Tx не охватывает panel HTTP. После чтения панели account eligibility
проверяется повторно. VPN UnresolvedTx/QueueAccessTx владеет access SQL и job.
Новый payments не пишет panel state напрямую.

`vpn.PurchaseHooks` С10 сохраняет точные сигнатуры. App composition связывает
Check/Outcome непосредственно с payments; отсутствие hooks запрещает native
write/commit. Receipt + paid/funding + River job остаются одной транзакцией;
access/assignment/profile + payment outcome — caller Tx VPN. Ошибка outcome
откатывает все локальные изменения. Funding ID не заменяется при повторе;
receipt dispute сохраняется отдельно от успешного fulfillment recovery.

PurchaseWorker переносится в payments. Persisted kind `purchase_fulfillment`,
args `{"order_id": UUID}`, queue `provision`, timeout 2m5s и retry правило
сохраняются. Server регистрирует owner worker напрямую. `platform` оставляет
только временные HTTP/test facades; копии денежных правил и лишние worker
aliases удаляются. Private signature vector test переносится с implementation;
root/app интеграционные fixtures подписывают notices через testkit signer;
официальный vector проверяет оба signer, runtime его не импортирует.
Два теста, вызывающих private purchaseReview, задают то же состояние SQL fixture.

## Проверка и доставка

Сначала фактический RED на ownership boundary и контракт/production composition.
Тесты сравнивают прежние wire JSON/hash/result с neutral DTO и повторяют заранее
записанные до переноса order/reconcile records. Сохраняются С10 recovery, expired
trial, disabled-unexpired checkout, actor revoke, funding mismatch, nil hook,
atomic outcome и lost physical owner tests; они проходят через новые facades.

Полная матрица включает generation/no drift, API/migrations/dependency equality
с базой, Go vet/race с реальными PostgreSQL/Redis и browser-потребителями,
TypeScript/build/runtime config, весь Playwright и Python regression, сборку
контейнеров, Native Go + TLS SMTP/HTTPS/3X-UI **3.7.0**, purchase callback repeat,
process restart и paid backup/restore через существующие helpers.

После Native — один свежий Astra/high whole-branch review; один RED→GREEN fix
pass для Critical/Important, без re-review. Все rulings/declines/minors сохраняются.
Затем exact-source CI, ручной SHA-guarded merge в v2, проверка preliminary release
и трёх GHCR images (digest/repository/revision/version/amd64/arm64). #59 закрывается
по собственным критериям; следующие limited М06 планы выполняются последовательно.

Документы и реализация ведутся автономно по ранее принятому решению владельца;
Native coordinator и последовательные manual merges сохраняются. Новый scope
или production не подразумеваются этим разрешением.
