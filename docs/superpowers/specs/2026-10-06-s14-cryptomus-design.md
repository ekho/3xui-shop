# С14 — первая покупка через Cryptomus

Владелец [#21](https://github.com/ekho/3xui-shop/issues/21); контракт
`2026-10-06-s14-cryptomus-v1` в рамках `2026-10-05-modular-monolith-v1`.
Документы, Native и ручные PR→v2 выполняются автономно по
[поручению владельца](https://github.com/ekho/3xui-shop/issues/55#issuecomment-6004574101).
С10/#17 и М05/#59 доставлены; С12/#20 доставлена PR74/dev.37. #55 — завершённый
М01 и источник архитектуры, а не общий эпик или родитель этого сценария.

## Результат и выбор

Подтверждённый web-клиент выбирает включённый Cryptomus, видит цену в USD,
получает один invoice и переходит на его страницу. Авторизованный payment/info
подтверждает оплаченный invoice; существующие workers выдают доступ либо
переводят триал, сохраняя его ID. Клиенту не нужен Telegram.

Я выбираю небольшой stdlib-адаптер внутри payments и существующий River/
purchase/access путь. Один webhook без проверки API не обеспечивает восстановление
потерянных уведомлений и надёжную сверку денег. SDK или общий gateway framework
добавляют зависимость и ещё одну модель заказа без необходимости.

Это расширение финансового контракта, поэтому до кода нужны эта спецификация и
Native-план; отдельный новый сервис или процесс не появляется. Сохраняются
SHOP_PAYMENT_CRYPTOMUS_ENABLED=false, merchant, USD, lifetime1800 и
is_payment_multiple=false. Секрет поступает только из FILE. Реальный магазин,
кошелёк, hosted checkout, публичная доставка и production не используются.
Локальные заглушки и 3X-UI3.7.0 доказывают локальную реализацию; внешняя готовность
остаётся перед С45–С47. Live Happ/VPN/Mac trust/Telegram/SMTP не меняются.

## Путь и восстановление

1. Существующие owner/verified/restriction/plan/first-purchase правила; способ
   cryptomus/CRYPTOMUS выбирает серверную USD-цену. Старые способы выбирают RUB
   и сохраняют input field order/body hash. Quote действует30min. UI показывает
   выбранную валюту и цену перед созданием; USD не форматируется как RUB.
2. Одна SQL-транзакция сохраняет заказ, immutable merchant/request и provider
   River job. API не вызывается внутри SQL-транзакции. Экран сначала preparing.
3. До POST фиксируется first attempt. POST https://api.cryptomus.com/v1/payment
   подписывает те же замороженные bytes; order_id — UUID заказа. Return/success
   ведут на собственный /orders/{id}, callback — /webhooks/cryptomus.
   Сумма decimal без float; параметры и URL-length255 проверяются до enqueue.
4. UUID invoice и безопасный HTTPS URL на pay.cryptomus.com фиксируются один раз.
   Повтор/500/timeout/restart не меняет merchant/order_id/request и не создаёт
   другой invoice. API документирует повтор по merchant order_id. is_refresh
   не включается. После истечения локального quote неизвестный результат только
   выясняется через info; новый POST запрещён. Отменённый/истёкший заказ без
   первого attempt не создаёт invoice. Известный invoice продолжает сверяться.
5. Worker запрашивает POST /v1/payment/info с order_id, тем же merchant и ключом.
   Webhook тоже вызывает эту операцию; его деньги/статус не являются доказательством.
   Потерянное уведомление не мешает polling. Неизвестный подписанный order_id
   получает200 без API/receipt/funding. Disabled скрывает новые продажи, но
   сохранённые реквизиты позволяют завершать старые операции. Merchant drift
   переводит заказ на разбор, не переключает его на другой магазин.
6. Только проверенный final paid/paid_over, соответствующие UUID/order/ USD
   invoice amount и достаточная фактическая оплата в payer currency дают один
   immutable receipt/funding/PurchaseWorker. Подготовка/исполнение/reconcile
   проверяют общий money guard. VPN остаётся единственным владельцем записи панели.

## Деньги и противоречия

Invoice amount в USD и actual payment/merchant amount в криптовалюте — разные
величины. Receipt gross_minor хранит **наблюдённую API сумму invoice в USD**,
которая должна точно совпасть с quote; это principal invoice, не вся сумма
поступления на кошелёк. Actual payment/payer/merchant amounts и payer currency
сохраняются отдельными строковыми фактами proof; USD-net остаётся unknown/NULL.
Криптовалютная сумма не подставляется в USD-net, обменный курс не придумывается.
Излишек paid_over сохраняется в исходной валюте без дополнительного доступа,
кредита, автоматического возврата или награды; разрешение остаётся С19/С20.

USD допускает только точные minor units, включая лишние нулевые десятичные знаки.
Положительные crypto payment_amount/payer_amount сравниваются точно через stdlib
big.Rat после ограниченной проверки decimal-синтаксиса; no float/exponent/fraction.
Плановая crypto сумма, фактическая и валюта обязательны для funding. Missing/
некорректные/противоречивые сведения не дают доступ. Согласованный status и
payment_status, is_final, created/updated timestamps тоже проверяются.

check/process/confirm_check не подтверждают оплату. cancel/fail/system_fail до
денег закрывают invoice; wrong_amount/wrong_amount_waiting/locked/refund states
требуют разбора. Поздняя оплата и локальная отмена сохраняют достоверный факт,
но не funding. Уже settled receipt выше запоздалого nonterminal observation.
Повтор не меняет первый факт/время и не создаёт второго job; изменение одного
updated_at без изменения финансового факта не делает replay конфликтом.
Противоречие сохраняет первый receipt, отмечает review и блокирует новую и
подготовленную выдачу. Выданный доступ автоматически не отзывается.

Все наблюдения сериализованы теми же account/order locks, что funding: receipt
не проверяется вне этой транзакции. Provider HTTP завершён до её начала.
Immutable proof включает provider/invoice/order/merchant/status/finality,
invoice denomination и typed crypto facts; не содержит key, wallet/address,
txid, customer или сырого ответа. Неподтверждённые сведения остаются sanitized
observation и review, не превращаются в доказанную полную оплату.

## Границы и контракт

- Stdlib net/http: fixed HTTPS API, TLS verification,10s timeout/no redirects,
  response<=64KiB/UTF-8/один JSON/state=0, обезличенные ошибки. Существующий
  payments HTTP client используется повторно; runtime API URL не настраивается.
- Webhook<=16KiB, UTF-8/один JSON/no duplicate keys/query, type=payment,
  canonical UUIDs, constant-time sign comparison. Effective Echo IP только
  91.227.144.54; произвольные forwarded headers не доверяются. Production
  local-IP bypass не добавляется. MD5(base64 JSON+key) — протокол провайдера.
  При удалении sign сохраняются порядок полей/числовые лексемы и документированное
  PHP slash escaping; тестируются unicode/слэши/whitespace/tampering.
- Migration18: explicit cryptomus method/type, cryptomus_checkouts (immutable
  merchant/request/attempt/invoice/URL, state/observation), explicit typed proof
  и nullable net только для признанного provider receipt. Старые migration15–17
  и receipt/body hashes не переписываются. Down блокируется при истории.
- OpenAPI authoring source: cryptomus/CRYPTOMUS, USD payment/quote currency,
  optional cryptomus_checkout {state: preparing|ready|unavailable,url:string|null},
  POST /webhooks/cryptomus receiveCryptomus. Generated Go/TS создаются командами
  репозитория. Старые YooMoney checkout/manual/YooKassa DTO остаются прежними.
- Runtime CRYPTOMUS_MERCHANT_ID (canonical UUID), CRYPTOMUS_API_KEY_FILE,
  SHOP_PAYMENT_CRYPTOMUS_ENABLED=false; inline/conflict/empty invalid, paired
  retained credentials проверяются и при disabled. Бренд/домены — runtime.
- UI ru/en/native controls/keyboard/status/alert/error/retry. Перед переходом
  повторный owner GET/ID/price/currency/expiry/review/URL check; return read-only.
  Внешний checkout не используется браузерным тестом как реальная PSP приёмка.

## Приёмка и Review Focus

| AC | Локальное доказательство |
| --- | --- |
| AC01 | USD server quote/owner/flags/old RUB/body hashes, atomic invoice+River job, immutable bytes/merchant; restart/ambiguous500/same invoice/no refresh/expired/config drift. |
| AC02 | Connected TLS API auth/state/redirect/oversize/malformed; source/sign/duplicate/Unicode/slash/forged-XFF/unknown200; forged paid body + pending info не funding. |
| AC03 | paid/paid_over/finality/principal/crypto units/missing/underpayment/currency/time/late/canceled/AML/refund/conflict; unknown USD-net и retained first facts. |
| AC04 | Receipt/operation foreign-method collision и SQL immutability, controlled settled/cancel race, shared prepare/access/reconcile guard; one receipt/job/access or retained review. |
| AC05 | Rendered currency/provider/keyboard/ru/en/preparing/ready, stale/unsafe URL/owner failure/retry/return; старые YooMoney/manual/YooKassa UI сохраняются. |
| AC06 | Own Docker TLS API stub + actual HTTP/River/info polling → native3X-UI3.7.0 new/trial/replay/restart; paid-pending read-only backup/restore/source recovery, no concurrent restored writers. Native vendor IP delivery не заявляется. |
| AC07 | Fresh Astra/high full branch review; one Critical/Important RED→GREEN fix pass/full affected green/no re-review; all rulings/costs/Declined, exact CI/manual v2 merge/actual preview/tag/indexes/labels; then #21 Done. |

Review Focus: USD invoice vs actual crypto units/unknown net; ordered/slash signature
vs authenticated info; ambiguous creation and expiry; late/terminal observation
with shared fulfillment locks; USD display/fresh safe checkout. Referrals/promos,
renew/tariff/refunds/disputes/MiniApp/import/Python removal не входят в С14.

## Источники и self-review

Проверены2026-10-06: [invoice/idempotency](https://doc.cryptomus.com/merchant-api/payments/creating-invoice),
[info](https://doc.cryptomus.com/merchant-api/payments/payment-information),
[request auth](https://doc.cryptomus.com/merchant-api/request-format),
[statuses](https://doc.cryptomus.com/merchant-api/payments/payment-statuses),
[webhook/IP/sign](https://doc.cryptomus.com/merchant-api/payments/webhook).
Сохранённые параметры подтверждены app/bot/payment_gateways/cryptomus.py.
Local-only scope и principal/unknown-net модель — решение этой спецификации,
не заявление о проверке реального merchant. Все7 AC имеют конкретный путь;
новые зависимости/процессы/generic framework и изменения чужих fixtures исключены.
Документ self-reviewed автономно; следующая стадия — Native-план.
