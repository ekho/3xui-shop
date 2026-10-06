# С10 — интеграция первой покупки в модульный монолит

Версия `2026-10-06-s10-modular-integration-v1`, владелец #17. Основание:
принятый функциональный дизайн С10 в сохранённом `afaeacf652964453ddd61883aa6da6a0d285783c`
и [архитектурный контракт](2026-10-05-modular-monolith-design.md).
М04 доставлен в `v2` через PR #64/#65; С10 предшествует М05.

## Результат и границы

Сохраняются все функциональные требования прежнего С10: первая покупка YooMoney,
подписанные денежные факты, точная цена, неизменяемый funding ID, отдельные
оплата/выдача, повторы, поздние платежи и переход с триала без смены идентификаторов.
Сохраняются миграция 15, публичный API, UI, очередь и абсолютные panel targets.
HTTP/River/Telegram выполняются одним Go-процессом; Telegram не требуется для оплаты.

Новый branch `feature/s10-modular-integration` создаётся от свежей `origin/v2`
`26d4b96733881d89b0538e479c2facdca232f419`. В него включается сохранённый source PR #5
обычным merge, без перезаписи истории. После проверки remote `feature/s10-first-purchase`
продвигается fast-forward до результата; PR #5 остаётся тем же PR с base `v2`.
Старый checkout и его незавершённые документы не изменяются.

## Владение и взаимодействие

До отдельного М05 `platform` временно владеет purchase orders/receipts/YooMoney.
Он использует существующие account facades, вызывающие public accounts lookup/lock;
catalogue `LockCurrentPlan` удерживает тариф до commit. SQL чужих accounts/catalogue/
trial/access таблиц в purchase коде не восстанавливается. VPN владеет access rows,
lease, queue, readback и изменением параметров аккаунта через accounts.

`vpn.AccessWrite` и `AccessState` получают nullable `PurchaseOrderID`;
`QueueAccessTx` записывает его вместе с прежними полями и River job в caller Tx.
`RequeueAccess` сохраняет NULL executor для purchase; оператор нужен для запроса
сверки, но его последующий отзыв не отменяет подтверждённые деньги.

В `vpn.New` добавляется явный аргумент `PurchaseHooks` с двумя функциями:

```go
Check func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, uuid.UUID) (reason string, err error)
Outcome func(context.Context, pgx.Tx, uuid.UUID, string, string) error
```

Аргументы Check после Tx: orderID, accountID, operationID; Outcome: operationID,
status, reason. Это функции приложения без event bus, setter, внешнего API или нового interface.
`Check` проверяет связанный paid order и прежний funding predicate; пустая причина
и nil error означают разрешение. До write и в final Tx проверка повторяется;
отсутствующая функция или ошибка запрещает выдачу. `Outcome` обновляет состояния
`queued`, `needs_review`, `applied`, сохраняет review по отдельным receipts.
Изменение access и payment outcome выполняется одним caller Tx, включая review.
Ошибка или отсутствие callback не допускает commit access/assignment/profile.

Production composition задаёт immutable функции с обращением к созданному
payment service. Test constructor сохраняет существующие динамические cfg/clock/queue.
М05 перенесёт implementations hooks в payments без изменения VPN-контракта.

Подготовка покупки использует `vpn.OpenAccessOwner`/`TryLock`/`Begin`/`Release`.
Accounts читаются через существующие facades; unresolved operations — через
`vpn.UnresolvedTx`. SQL Tx не охватывает HTTP; финальный Begin использует ту же
physical session и не заменяет потерянное соединение. Quote/receipt/job и
access/order/job сохраняют прежнюю атомарность.

## Проверка и доставка

Повторно выполняются сохранённые money/quote/checkout/trial/recovery tests и
локальный сценарий с Docker 3X-UI **3.7.0**, TLS SMTP, HTTPS и browser.
Дополнительные checks подтверждают rollback payment outcome вместе с access,
assignment/profile, fail-closed при отсутствующем hook и отсутствие SQL Tx
во время panel reads. Реальное прекращение backend и backup/restore paid-заказа
проверяются существующими helpers на собственных fixtures.

Один свежий Astra/high reviewer проверяет всю ветку после Native-реализации;
затем обязательный CI exact source, разрешённый merge в `v2`, три GHCR образа
и preliminary release с проверкой tag/source/version/architectures.
Настоящие деньги, URL кошелька, production, живой Happ и настройки Mac исключены.
Полная внешняя приёмка YooMoney остаётся С13; удаление Python runtime — С47.
