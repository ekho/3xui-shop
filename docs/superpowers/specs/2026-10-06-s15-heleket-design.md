# С15 — оплата через Heleket

Владелец: [#22](https://github.com/ekho/3xui-shop/issues/22).
Контракт `2026-10-06-s15-heleket-v1`, архитектура `2026-10-05-modular-monolith-v1`.
Предпосылки С10/#17 и М05/#59 доставлены. Native и автономное ведение документов,
реализации и ручных merges в v2 уже разрешены владельцем.

## Результат и границы

Подтверждённый web-аккаунт без подписки либо с действующим триалом выбирает
Heleket, видит серверную USD цену, создаёт заказ и открывает страницу оплаты.
Проверенная оплата запускает существующую однократную выдачу доступа.
Переход с триала сохраняет VPN/subscription ID и добавляет оплаченный срок.
Одобрение клиента не требуется. Только включённые методы отображаются.

Один Go-процесс HTTP/River/Telegram и один payments owner сохраняются.
Продления, refunds, MiniApp, история/импорт и удаление Python имеют свои сценарии.
Production, реальный merchant/кошелёк/перевод, публичный callback, Telegram/SMTP
и переключение живого Happ исключены. Локальный Docker и заглушка разрешены.

## Отдельный договор провайдера

Официальные [формат запросов](https://doc.heleket.com/general/request-format),
[создание](https://doc.heleket.com/methods/payments/creating-invoice),
[payment/info](https://doc.heleket.com/methods/payments/payment-information),
[webhook](https://doc.heleket.com/methods/payments/webhook),
[статусы](https://doc.heleket.com/methods/payments/payment-statuses) и
[история](https://doc.heleket.com/methods/payments/payment-history)
проверены отдельно от Cryptomus. Документация описывает контракт, а не
наблюдение реального merchant. Требуемый внешний доступ пока отсутствует.

- Fixed HTTPS `api.heleket.com`: POST `/v1/payment` и `/v1/payment/info`.
  Merchant — canonical ненулевой UUID; подпись MD5(base64 точных JSON bytes + key).
- Webhook `/webhooks/heleket`: effective source IP `31.133.220.8` и собственный
  ключ подписи. Непроверенный X-Forwarded-For не даёт доступ. Body UTF-8 ≤16KiB,
  без duplicate keys/query; ordered JSON сохраняет numbers/Unicode и PHP slash
  escaping, root sign исключён. Constant-time comparison. Подписанное тело —
  подсказка к authenticated info; оно не подтверждает деньги самостоятельно.
- Checkout допускает только HTTPS `new-pay.heleket.com` или `pay.heleket.com`,
  без userinfo, чужого port, CR/LF/NUL, длина ≤2000. Оба host приведены в
  текущих официальных примерах; Cryptomus host здесь не разрешён.
- HTTP timeout10s, no redirects, response≤64KiB, state=0, валидные уникальные
  JSON/identity. Никакого API-host override/SDK в production.
- Invoice principal USD, срок quote30min/lifetime1800,
  `is_payment_multiple=false`; frozen unique order_id/request/merchant.
  Return/success ведут в собственный заказ, callback — в собственный webhook.
  Повтор неизвестного POST до expiry использует те же bytes/order_id;
  после expiry только info. Не было попытки до expiry — POST запрещён.
  `is_refresh` отсутствует. Известный invoice всегда сверяется через info.

## Архитектура и совместимость

Два действительных провайдера позволяют переиспользовать частный алгоритм
payments: serializer, HTTP limits, frozen recovery, locks, decimal checks,
receipt/review и fulfillment. Сохраняются конкретные Cryptomus публичные
методы/worker/args; Heleket получает собственные `SyncHeleket`, `ReceiveHeleket`,
`HeleketSourceAllowed`, `HeleketArgs`, `HeleketWorker`.
Частный closed choice содержит только cryptomus/heleket. Его SQL table/hosts
не задаются окружением или клиентом. Не вводится общий gateway framework.

Heleket имеет отдельные config, `heleket_checkouts`, River kind
`heleket_payment`, receipt operation `heleket:<invoice UUID>`, proof provider
`heleket` и notification type `heleket.paid|heleket.paid_over`.
Одинаковый UUID двух провайдеров не сталкивает receipts. Ни подпись, ни host,
ни order, ни proof одного провайдера не подтверждают другой.

Migration19 только добавляет таблицу/ограничения; migrations15–18 не меняются.
Frozen columns защищены существующим immutable trigger, применимым к обоим
checkout layouts. Down блокируется при любой Heleket истории и возвращает
точные ограничения С14. Старые orders/receipts/body hashes и DTO сохраняются.

OpenAPI source `docs/api/openapi.yaml` получает method/type `heleket/HELEKET`,
optional `heleket_checkout:{state,url}`, `HeleketCheckout` и webhook operation
`receiveHeleket`. Methods maxItems5 соответствует пяти фактическим методам.
Генерация только repository commands; старые optional поля/enum значения не
переименовываются. HTTP adapter работает через публичный payments contract.

## Деньги и повтор

Только authenticated `paid|paid_over`, is_final=true, согласованные status и
payment_status, canonical invoice/order/merchant и точный положительный USD
principal могут создать receipt. `payment_amount`/`payer_amount`/`merchant_amount`
— отдельные bounded exact decimal crypto strings, payer ticker≤16; фактически
уплачено ≥положительного требуемого payer amount, merchant amount≥0.
Float/FX/предположение о USD-net запрещены. USD-net остаётся NULL; surplus
сохраняется в proof, не добавляет срок/credit. Signed webhook с paid и pending
info не создаёт receipt/job/access.

Check/process/confirm_check остаются pending; cancel/fail/system_fail без денег
закрывают заказ. Underpayment/refund/locked/unknown требуют review.
Pending даты не используются для подтверждения денег. Paid timestamps обязаны
быть RFC3339 с offset: created не ранее order−5min, updated≥created,
updated≤now+5min и ≤expiry. Honoring offset важнее общего описания UTC+3.
Первое финансовое время/proof неизменны; updated-only replay их не переписывает.

Все наблюдения используют account/order locks. Invalid/late/canceled/foreign
или противоречивые деньги сохраняют facts и review, без будущей выдачи.
Shared funding guard проверяет собственную таблицу/namespace/merchant/order,
полную сумму/валюту/finality/время/crypto adequacy на prepare/access/reconcile.
Один receipt, один PurchaseWorker и один доступ при повторе или гонке.
При disable новые заказы скрыты; retained keys позволяют завершать известные.
Merchant drift quarantines заказ; ключ не попадает в DTO/log/proof.

## Кабинет и настройки

`SHOP_PAYMENT_HELEKET_ENABLED=false`; `HELEKET_MERCHANT_ID`, только
`HELEKET_API_KEY_FILE`. Inline/FILE conflict, missing/empty/unreadable file,
bad UUID/key/flag fail closed, включая retained disabled credentials.
Key UTF-8≤512 без whitespace/control; настройки deployment-time.

Выбор Heleket переключает цену на USD и показывает точное подтверждение.
Смена периода сохраняет выбранный доступный метод той же валюты, включая
Cryptomus/Heleket. Lost create response сохраняет body/key. Preparing/ready/
unavailable/review/error различаются; доступность кнопки зависит от свежего
собственного GET с неизменной суммой/валютой/method/type, pending и expiry.
Browser return не означает payment. RU/EN, native radio/button, keyboard и
accessible labels обязательны; учётные данные и VPN links не показываются.

## Локальная приёмка

AC01 — enabled-only метод/USD/server quote; wrong pair/disabled/foreign owner
отвергаются, один durable order/job, старые hashes/методы проходят regression.
AC02 — signed exact POST/unique order/frozen bytes,500/expiry/disable/drift/restart;
API/hosts/IP/keys отделены от Cryptomus.
AC03 — paid/paid_over/finality/decimal/time/NULL-net; pending/underpay/refund/late/
foreign proof не выдают доступ; duplicate/concurrent observations однократны.
AC04 — HTTP source/signature/Unicode/slash/numbers/duplicates/tampering/unknown/
bounded errors; forged paid callback не обходит authenticated info.
AC05 — rendered RU/EN keyboard, USD choice/period/retry и fresh own safe checkout;
foreign host/provider/currency/amount/expired/review/owner errors не navigate.
AC06 — свой cabinet-c15 с pinned native3X-UI3.7.0, durable stdlib TLS/SQLite stub,
actual River info→receipt→worker→new/trial access,500/restart/replay, backup/restore
readonly proof. Проверка настоящего vendor-IP webhook не заявляется.
AC07 — affected Cryptomus native flow и full Go race/vet/generation/Python/web,
один свежий Astra/high review, один fix pass; exact-source CI/manual v2 merge
и actual preview/tag/3indexes/6labels до CLOSED/Project Done.

Внешний merchant/checkout/callback/payments и production readiness остаются
непроверенными до ресурсов и С45–С47. Локальная заглушка их не заменяет.
Self-review: права, ошибки, retry, money authority, compatibility и все AC
имеют конкретный тестовый путь. Автономная авторизация покрывает этот дизайн.
