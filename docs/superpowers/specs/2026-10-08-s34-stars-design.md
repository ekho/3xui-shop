# С34 — разовая оплата Telegram Stars

Владелец: #32 / `payments`. Общий контракт `2026-10-08-s34-stars-v1`.
Авторизация: последовательное автономное выполнение и PR/v2 по #55;
Native inline и один независимый Astra/high review всей ветки.

## Результат и границы

Пользователь подписанного Mini App выбирает тариф XTR, создаёт существующий
purchase order, открывает Telegram invoice и получает доступ через существующие
PurchaseWorker/AccessWorker. Единственное доказательство оплаты — серверное
`successful_payment`; callback SDK только обновляет экран. Оператор может
выполнить настоящий полный `refundStarPayment`, а `/paysupport` открывает поддержку.

С34 покрывает первую разовую покупку новых non-legacy аккаунтов, в том числе
переход с триала. С35/#33 владеет рекуррентными счетами/платежами и Stars-ветками
продления/смены тарифа; С36/#34 — внешними способами из Mini App. С45/#47 сверяет
полную конфигурацию, включая старый dev-режим 1 Star/возврат; С46/#53 импортирует
старые финансовые факты. Старый parser/history сохраняется, payload не доказывает
оплату. Неизвестные legacy/recurring money updates не подтверждаются до их владельца.

## Право покупки и transport

- `payment_method=telegram_stars`, `payment_type=STARS`, `action=purchase`;
  XTR — положительное целое число Stars, без масштабирования на 100.
- Флаг `SHOP_PAYMENT_STARS_ENABLED` по умолчанию false; Telegram runtime и
  настроенный Mini App обязательны. Существующие пять внешних способов сохраняют
  свои web/email/independentBilling guards. Cookie API не создаёт Stars-заказ.
- Mini allowlist разрешает payment-methods, создание только Stars-заказа и
  POST `/api/v1/orders/{order_id}/stars-invoice` с `{}`. Согласие, актуальная
  подписанная сессия, Origin/CSRF, ограничения аккаунта и idempotency сохраняются.
  Никакие внешние checkout URL при этом не выдаются.
- Разрешение проверяется под существующими account/access locks, с актуальной
  привязкой Telegram, без legacy ID/ban/unlimited/незавершённых операций;
  сохраняется прежний account UUID/VPN identity. При повторной покупке после
  оплаченной подписки требуется С35, а не обход покупки первой подписки.
- Root `app.NewTelegram` связывает публичный `payments.StarsGateway` с уже
  существующим приватным BotAPI client. Новый SDK, worker, процесс, registry,
  foreign SQL или payments→telegram import не вводятся.

## Invoice и pre-checkout

Сохранённый checkout связывает order UUID, первоначальный payer Telegram ID,
bot ID, payload `stars:v1:<canonical-order-uuid>` и неизменяемый quote.
`createInvoiceLink`: один XTR price, пустой provider_token, без subscription_period.
URL — вспомогательное поле; только `https://t.me/$...`, без credentials/query/fragment.
Повтор генерации использует тот же payload; лишний счёт не даёт вторую выдачу.
Бот сверяет getMe с token bot ID до начала платежных API.

Pre-checkout проверяет текущую привязку payer/bot, точные XTR/amount/payload,
pending/active/unexpired order, consent/policy и отсутствие неопределённого
refund/выдачи. Это только ограниченные DB операции; внешняя панель не вызывается.
Ответ доставляется в пределах 10 секунд: отдельный deadline 5 секунд, безопасный
ru/en отказ. Повтор заново проверяет актуальный заказ. Ошибка DB/API не выдаёт OK.

## Неизменяемые деньги и выдача

`successful_payment` проверяется в доверенном Telegram adapter и передаётся
публичному payments owner. Proof содержит bot/payer, charge IDs, payload,
currency/amount, message time, recurring flags. Граница charge IDs — 4096 bytes
для нашего транспорта, не объявляемый лимит Telegram. Receipt key:
`stars:<sha256(botID + ':' + telegram_payment_charge_id)>`, в пределах существующих
128 символов. Настоящие charge IDs сохраняются в provider_data.

Receipt и audit/queue/order изменения коммитятся до продвижения poll offset.
Replay точного charge не создаёт receipt/grant повторно; конфликт charge либо
вторая оплата одного заказа сохраняют деньги и требуют review. Неверная сумма,
поздний/canceled/disabled/restricted/binding-changed платёж не пропадает и не
выдаёт доступ автоматически. DB rollback оставляет update неподтверждённым.
Shared purchaseFundingCheck проверяет Stars proof и refund state при Fulfill,
Reconcile и окончательной VPN записи, включая восстановление после перезапуска.
Стабильные order/access/identity и существующий ownership lock предотвращают
двойную выдачу. Операторский reconcile не обходится без актуального права.

## Настоящий возврат

Используется одна Stars refund observation/attempt таблица, ключ — тот же
bot+charge receipt key. Она сохраняет pending/uncertain/confirmed proof даже
до появления положительного receipt: иначе ранний refunded_payment остановит
ordered polling перед успешным платежом. Собственный корректный ранний refund
коммитится и подтверждается; поздний receipt уже не может финансировать доступ.

Операторский endpoint POST `/api/v1/operator/clients/{id}/orders/{order_id}/stars-refund`
принимает receipt_operation_id, reason, confirm_full=true, keep_access=true.
Текущая роль/защищённый target/idempotency/account-access owner обязательны.
До network вызова коммитится реальная авторизованная попытка; pending/uncertain
блокируют funding. Повтор после неоднозначного результата не вызывает payout
вслепую: оператор видит uncertain; matching refunded_payment завершает факт.
Подтверждённый True либо совпадающий серверный refund — достаточный proof;
timeout/неизвестный API результат не объявляется успешным возвратом.

После proof заполняется существующий неизменяемый purchase_refunds ledger:
source=telegram, реальный operator nullable, integer returned_amount/currency=XTR,
reference=receipt key. Provider событие не получает вымышленного оператора.
Неприменённая выдача прекращается, применённый доступ сохраняется для отдельного
решения оператора; дни автоматически не вычитаются. Старый endpoint внешнего
подтверждения возврата не может подтверждать Stars без provider proof.
Down запрещён при сохранённых Stars money/refund/checkout фактах.

## Интерфейсы и совместимость

Catalogue/PurchaseOrder используют прежние формы, ключи попыток и server polling;
SDK openInvoice только после свежего CanPay и проверки URL. failed/canceled/pending
отображаются отдельно от оплаченного backend состояния; ru/en, клавиатура,
aria-live/busy/errors. PaymentCase показывает native refund state и реального
actor/source, без обязательной выдуманной внешней reference. VPN key скрыт.
Браузер не сохраняет bearer, invoice payload или payment credentials в storage/log.

Старые поля/order hashes/quote/receipt/legacy history сохраняются. Новые checkout,
refund-state поля optional; старый operator refund JSON остаётся прежним.
XTR и telegram source добавляются в owning OpenAPI и generated consumers.
Совместимый контракт фиксируется в #32/#55 и ссылками в #33/#34/#29/#30/#27/#47/#53/#54.

## Локальная приёмка

Owned BotAPI fixtures, TestKit PostgreSQL/Redis, текущий целый Go graph и TLS
3X-UI **3.7.0**. Signed Mini → invoice/pre-checkout → receipt → workers → panel/key;
restart/повторы/конфликт/неверный владелец/ранний refund/неопределённый возврат;
реальный browser backend и оба языка. Full Go/race, web, Python, vet, generation,
один final review и один author Critical/Important fix pass. Exact-source CI,
manual PR/v2 merge и фактические prerelease/OCI receipts до Done.
Нет настоящих денег, production/public callback/внешнего Telegram, live Happ
или изменения Mac trust. Внешняя готовность остаётся С45–С47.

Официальные источники: [Stars](https://core.telegram.org/bots/payments-stars),
[Bot API](https://core.telegram.org/bots/api#createinvoicelink),
[refund](https://core.telegram.org/bots/api#refundstarpayment),
[Mini App SDK](https://core.telegram.org/bots/webapps#initializing-mini-apps).
