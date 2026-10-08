# С35: Stars с автопродлением

Контракт `2026-10-08-s35-stars-recurring-v1`; владелец `payments`, задача
[#33](https://github.com/ekho/3xui-shop/issues/33),
[общая запись решения](https://github.com/ekho/3xui-shop/issues/33#issuecomment-6051821522).
Основа `origin/v2` — `27ae2dec9aadfe03bada575a416d1f1afdf3c383` после С34/PR86/dev.68.
Документы и Native-реализация разрешены автономным мандатом
[6004574101](https://github.com/ekho/3xui-shop/issues/55#issuecomment-6004574101).

## Результат

Клиент явно выбирает автопродление первой 30-дневной покупки в подписанном
Mini App. Подтверждённые последующие платежи продлевают тот же доступ один раз.
Кабинет показывает оплаченный период, состояние Telegram и состояние команды
отмены/возобновления. Отмена сохраняет оплаченный VPN; переход на другой способ
оплаты требует подтверждённой отмены всех известных автосписаний аккаунта.

Завершаются Stars-ветки продления/смены тарифа С16/С17, native refund С20,
identity/billing guards С31 и lapse-факты для С27. Новые уведомления, браузерный
переход С36, реальный импорт С46 и переключение С47 сохраняют своих владельцев.
Два Minor С34 — различимые SDK hints и Mini-only invoice OpenAPI — исправляются
в затронутых экранах/контракте здесь.

## Владелец и совместимость

Один Go-процесс HTTP/River/Telegram; существующие PostgreSQL, Redis, React,
React-admin, 3X-UI **3.7.0**. Новый процесс, worker, зависимость, реестр событий
или универсальный платёжный слой не добавляются. Telegram передаёт проверенные
платёжные факты публичному payments owner; root связывает modules типизированными
функциями. Чужой SQL/private-пакеты из модулей не используются.

UUID аккаунта, panel key, VPN UUID/sub ID, история, оплаченные quote/target,
старые поля/порядок JSON/body hashes и cookie/bearer правила сохраняются.
`stars_recurring?: boolean` добавляется **в конец** Input/Quote с `omitempty`;
отсутствие/false сохраняет прежние байты и разовую покупку. Пять внешних методов
сохраняют валюты, настройки и денежные доказательства. Unknown legacy billing
с `LegacyUserID` остаётся закрытым до С46; старые snapshots/payload/charge не
превращаются в выдуманный новый платёж или новую выдачу.

Название, домен и публичные документы задаются runtime-конфигурацией.
Авторизация распространяется на локальные проверки, PR/v2 и согласованный
предварительный релиз; production и переключение живого Happ исключены.

## Первый платёж и защита от повторного старта

1. Только signed Mini: `purchase`, 30 дней, XTR, `0 < amount <= 10000` и явный
   `stars_recurring=true`. Для renew/change_plan, других периодов и внешних
   методов true недопустим. Native invoice передаёт `subscription_period=2592000`.
2. Счёт сохраняет исходные bot/payer/payload/quote. Pre-checkout проверяет те же
   live account/plan/history/amount/binding правила, что С34. Для recurring
   сохраняется один native query ID под текущим account/order lock: повтор того
   же запроса допустим; другой query не начинает вторую подписку. Неудачный
   зарезервированный pending-заказ можно отменить и создать заново. SDK callback
   и таймер не освобождают reservation и не доказывают оплату.
3. Только доверенный SuccessfulPayment с `is_recurring=true`,
   `is_first_recurring=true` и корректным будущим expiration создаёт canonical
   billing identity и funding. Amount/currency/root/bot/payer/frozen terms
   должны совпасть. Expiration находится после payment date и не дальше
   30 дней + 5 минут от неё; VPN target использует прежнее правило выдачи.
4. Exact replay `(bot, charge)` ничего не выдаёт повторно. Конфликт, лишний
   first charge, неверный/поздний/отключённый/заблокированный платёж сохраняет
   receipt/review, не обходит выдачу. Каждая настоящая дополнительная first
   recurring charge сохраняется как **отдельная** identity для native cancel;
   нельзя потерять её только потому, что canonical identity уже существует.

Telegram разрешает несколько subscriptions одного payer. Reservation уменьшает
повторные старты; сохранение всех реальных first charges защищает и при гонке
или неожиданном provider-событии. Неизвестные/неоднозначные факты закрывают
автоматическую выдачу и переход оплаты, сохраняя возможность точного refund.

## Последующие циклы и денежное доказательство

Исходный payload всегда указывает на invoice root, а каждый допустимый следующий
charge получает отдельный существующий `renew` order, receipt и access operation.
Не изменяются применённый первый заказ и его финансовое доказательство.
Cycle хранит root, canonical first receipt, свой receipt/expiration и точную
предыдущую applied access operation. Root+expiration уникальны для выдачи.

Снимок 30 дней/цены/устройств/трафика/profile берётся из исходного согласованного
quote, а не из изменившегося каталога. Current applied source должен принадлежать
этой цепочке; same-plan переназначение оператором также меняет source и закрывает
старую recurrence. Новый expiration больше предыдущего; charge date не раньше
предыдущего оплаченного конца более чем на 5 минут. Перерыв допустим, пропущенные
периоды не выдаются. `max(VPN expiry, preparation now)+30 дней` и прежние правила
reset/renewal сохраняют уже оплаченный доступ.

Денежный receipt и provider paid_until отделены от VPN expiry: например, первый
платёж может добавиться к неистёкшему trial. Новый настоящий charge за уже
известный период остаётся деньгами для review, без дополнительной выдачи.
Отсутствующий первый proof, pending/partial доступ, ambiguity дополнительных
subscriptions, ban/refund/изменённый source не превращаются в слепое продление.

Общие funding и live policy проверяются при checkout, queue, preparation,
reconcile и каждой финальной VPN-записи. Не только HTTP-экран защищает деньги.

## Данные

Additive migration `00029_stars_recurring.sql`:

- `stars_checkouts`: immutable period 0/2592000, set-once pre_checkout_id;
  исходные identity/payload/invoice URL сохраняют прежнюю защиту.
- `stars_subscriptions`: immutable first receipt/root/bot/payer/first charge,
  canonical flag (не больше одной canonical/root); mutable provider state,
  actual paid_until, текущий cycle order, desired/latest control и native proof.
  Primary key — first receipt, чтобы сохранять дополнительные реальные старты.
- `stars_subscription_cycles`: immutable order/root/canonical receipt/свой
  receipt/expiration/previous applied source; уникальные receipt и root+expiration.
- `stars_subscription_controls`: immutable identity/action/key/reason/source/
  actor intent и retained result `pending|uncertain|confirmed|rejected`.
  Внешний ответ проверяется до confirmed; delete/перезапись provenance запрещены.

Используется существующая idempotency для команд и provider update replay.
Downgrade запрещён при сохранённых recurring invoice/money/control фактах.
Операционная запись `uncertain` не заменяет provider proof; timestamps/state
не переписывают original receipt. Все значения charge/bot/payer остаются внутри
защищённых владельцев/БД, не публикуются в ответах, логах или issue.

## Отмена, возобновление, lapse

Свой клиент может запросить cancel через verified cookie либо signed Mini,
в том числе при недоступном Telegram. Команда с confirmed=true и Idempotency-Key
сначала сохраняется. Она охватывает все известные subscriptions аккаунта.
Существующий per-account AccessOwner сериализует native control/денежные
refund/выдачу; HTTP выполняется вне SQL-транзакции. Intent и доступная authority
проверяются перед вызовом, result сохраняется даже после разрыва браузера.

Только actual `editUserStarSubscription(is_canceled=true) -> True` подтверждает
bot-cancel. Оплаченный доступ не сокращается. 400/false/невалидный ответ/потеря
ответа оставляют незавершённое либо отклонённое действие и закрытый billing guard.
Повторяется лишь тот же актуальный idempotent setter; поздний старый resume
не отменяет более новый required cancel. Native refund payout по-прежнему не
повторяется вслепую после неопределённого исхода.

Resume разрешён только canonical identity текущего finite applied source,
с актуальной binding/согласием, без ban/quarantine/refund/неразобранных денег/
pending внешней оплаты. Успешный `is_canceled=false` означает **resume_allowed**:
клиенту ещё нужно включить подписку в Telegram. Лишь genuine provider active
update/payment меняет provider state; возвращённый True сам по себе не active.

Trusted `Update.subscription` принимает `active|canceled|failed`, проверяет
bot/payer/root и дедуплицируется долговечно. Native poll обрабатывает события
последовательно. Числовой update_id не считается вечно монотонным: Telegram
может выбрать случайный ID после недели без событий. Replay не меняет состояние;
active event не переопределяет подтверждённый bot-cancel/актуальный cancel intent.

В том же процессе запускается immediate reconciliation и один stdlib timer
каждые **15 минут**. `paid_until < now <= paid_until+24h` — grace;
после **24 часов** без новой доказанной оплаты — lapse и required cancel.
Grace/lapse ничего не оплачивают и не выдают. Сбой provider остаётся внутри
цикла и не останавливает HTTP; shutdown прерывает timer/native request.

Restriction, VPN ban, unlimited, операторские plan/profile/starter изменения
и quarantine recovery помечают required cancel **в той же owner transaction**
через typed root callbacks. Captured original payer/charge сохраняются после
ClearTelegramIdentity. Recovery может закончиться при недоступном Telegram;
переход к внешней оплате остаётся закрытым до native cancellation proof.

Для С27 provider/control/paid_until/period_phase являются источником Stars/lapse
policy; собственные пороги и доставка уведомлений остаются #37. Истёкший grace,
user cancel, disabled config и failed state сами не доказывают bot-cancel.

## Продление, смена тарифа и смена способа оплаты

После доказанного cancel всех известных recurrences signed Mini может сделать
разовое Stars `renew|change_plan` через существующие action/quote/source правила.
Новая recurring смена тарифа не создаётся; first purchase guard С34 после starter
clearing сохраняется. `subscriptions.CurrentPlanSourceTx` принимает eligible
native Telegram source, а payments проверяет конкретный billing/method.

Verified web может использовать все пять включённых внешних методов для того же
UUID даже с текущей Telegram binding, если authoritative guard доказал отсутствие
неотменённых автосписаний. Pending/uncertain native control и unknown legacy
закрывают create/funding/final access и unlink. Пока есть pending external order,
resume закрыт; после оплаты/операторского переназначения старая цепочка не
возобновляется. Отсутствие Telegram не превращает unknown billing в safe billing.
Переход во внешний браузер и самостоятельный login — следующий С36, не этот UI.

## HTTP и UI

- `GET /api/v1/stars-subscription`: own auth; safe `state`, nullable `order_id`,
  `provider_state`, `control_state`, `paid_until`, `period_phase`,
  `can_cancel`, `can_resume`, `external_billing_blocked`, `needs_review`.
  None и legacy_unknown различаются; чужие/секретные provider данные не выдаются.
- `POST /api/v1/stars-subscription/control`: own cookie+CSRF либо signed Mini,
  `{action: cancel|resume, confirmed: true}`, Idempotency-Key. Ответ показывает
  actual state, включая pending/uncertain; принятие команды не подтверждает cancel.
- Existing order/renewal/plan-change APIs расширяются совместимо;
  stars-invoice OpenAPI документирует только miniAppBearer, как runtime С34.

Catalogue показывает unchecked выбор автопродления только для допустимой первой
30-дневной Mini покупки и явную стоимость каждого 30-дневного периода. Cabinet
показывает paid period, actual provider/control state и confirm cancel/resume;
PurchaseOrder различает SDK canceled/failed/pending как подсказки. Только server
paid/access state определяет результат. Используются существующие компоненты,
ru/en, формы/кнопки, keyboard/aria-live, abort/session guards, пустые/error/retry
состояния. Inline внешняя оплата в Mini не появляется.

## Приёмка

1. Migration/hash/provenance/downgrade; legacy snapshots и разовая С34 неизменны.
2. Signed first invoice/native period/reservation/first grant/replay; subsequent
   frozen cycle/one grant/source/ID/reset; duplicate period/extra first/conflict/
   rollback/disabled/ban/invalid facts сохраняют деньги и безопасное состояние.
3. Actual native True/False/400/lost reply/старый intent/restart, права/CSRF/
   foreign account/replay; paid доступ после cancel сохраняется; resume_allowed
   не выдаётся за active. Additional first cancellation не теряет binding.
4. Early refund without recurring metadata, child refund/pending/partial/applied
   preservation; exact proof и AccessOwner; uncertainty не повторяет выплату.
5. Все пять external paths, one-time Stars renew/change, unlink/recovery,
   restriction/ban/same-plan/starter изменения; нет двойной выдачи/безопасного
   handoff по недоказанным facts. Actual timer/grace/lapse/context stop.
6. Real authenticated HTTP/browser/current whole Go composition, TLS3X-UI3.7.0,
   native repeat/restart и обе локали/keyboard. Owned fixtures, без real money,
   публичного callback, живого Telegram или переключения Happ.
7. Native: одно fresh Astra/high whole-branch review; один author
   Critical/Important RED→GREEN pass при необходимости, Minor записаны,
   no re-review. Свои full checks/exact PR CI/guarded manual v2 merge/actual
   prerelease/tag/3multiarch images подтверждены перед #33 Done.

## Проверенные источники

[createInvoiceLink](https://core.telegram.org/bots/api#createinvoicelink),
[SuccessfulPayment](https://core.telegram.org/bots/api#successfulpayment),
[RefundedPayment](https://core.telegram.org/bots/api#refundedpayment),
[editUserStarSubscription](https://core.telegram.org/bots/api#edituserstarsubscription),
[BotSubscriptionUpdated](https://core.telegram.org/bots/api#botsubscriptionupdated),
[Update](https://core.telegram.org/bots/api#update),
[Star subscriptions](https://core.telegram.org/api/subscriptions).
Проверены 2026-10-08, Bot API10.3; Update.subscription добавлен в10.2.
Старый Python profile/expiry/payment код — источник прежних пользовательских
сценариев, а не authority для выдуманного cancel после400 или active после resume.
