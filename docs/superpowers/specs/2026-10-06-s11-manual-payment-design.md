# С11 — ручная оплата с подтверждением оператора

Дата: 2026-10-06. Владелец [#19](https://github.com/ekho/3xui-shop/issues/19), контракт `2026-10-06-s11-manual-payment-v1`. База `origin/v2` c9c075e40f822fbe4f7d058d292f2a14ad07cc3f; #11/#17/#59 CLOSED. Документы и Native-реализация ведутся автономно по [решению владельца](https://github.com/ekho/3xui-shop/issues/55#issuecomment-6004574101).

## Результат

Клиент без Telegram выбирает ручную оплату, получает сумму и реквизиты, сообщает «Я оплатил» и видит решение в кабинете. Оператор получает устойчивую очередь заявок в React-admin, проверяет фактическое поступление и подтверждает либо отклоняет оплату. Заявление клиента и скриншот не подтверждают деньги. Одобрение сохраняет финансовый факт и задание выдачи атомарно; повтор или гонка решений не создают второй доступ.

Переиспользуем общий заказ/цену/права/3X-UI/River из С10 и payments. Отдельный механизм продаж или новая платёжная библиотека не нужны. Старые YooMoney-заказы сохраняют IDs, snapshot, body hash, replay и поведение. Python не меняется и удаляется по С47.

## Путь и состояния

1. Метод `manual` доступен только при `SHOP_PAYMENT_MANUAL_ENABLED=true` и корректном `MANUAL_CARD_DETAILS_FILE`; по умолчанию выключен. Настройки задаются при деплое. Реквизиты читаются из защищённого файла, отображаются обычным текстом только владельцу заказа и оператору и сохраняются в snapshot заказа.
2. Общий `CreatePurchaseOrder` принимает `payment_method=manual`, `payment_type=MANUAL`; остальные условия первой покупки/перехода с триала прежние. Один active-заказ на аккаунт обеспечивается прежним индексом. Сумма только из серверного каталога, заказ имеет прежнее окно оплаты 30 минут.
3. До истечения окна клиент может вызвать `ReportManualPayment`. Сохраняются время заявления и audit, оплата остаётся pending, выдача не начинается. Повтор не создаёт заявку/уведомление заново. После заявления клиентская отмена и новый заказ запрещены; заявка не освобождается автоматически по истечении окна.
4. Оператор видит очередь pending-заявок и карточку клиента. Для решения обязательна причина 1–1000 символов. Для approve дополнительно обязательна фактически проверенная сумма в копейках, точно равная snapshot. UI явно требует проверить поступление; скриншоты не принимаются за доказательство.
5. Approve возможен только для заявленного pending-заказа без спорного платежа. В одной Tx создаётся receipt источника `manual_confirmation`, фиксируются оператор/решение/время/причина, заказ становится paid/queued и добавляется прежний `PurchaseArgs`. Решение можно принять после окна, если заявление подано вовремя. В receipt время решения; время перевода не выдумывается.
6. Reject сохраняет оператора/время/причину и canceled. Клиент видит отказ и может создать новый заказ; прежняя заявка терминальна. После approve/reject противоположное решение запрещено. Replay исходного ключа возвращает прежний результат после повторной проверки прав.
7. Деньги и выдача имеют отдельные состояния. Сбой панели сохраняет paid; общий worker/recovery/readback С10 остаётся единственным механизмом выдачи. Restriction/VPN-ban и неизвестный native state не обходятся. Отзыв роли оператора после корректного подтверждения не отменяет уже сохранённый денежный факт.

Отключение метода останавливает новые заказы/инструкцию «переведите», но не удаляет существующие заявки, snapshot или возможность сообщить о сделанном переводе и принять решение. Для защищённых клиентских действий сохраняются verified web-account/restriction, session/CSRF/Origin; для операторских — текущая роль и account locks. Клиентский Report не подтверждает деньги.

## Данные и контракты

Миграция 16 расширяет `purchase_orders`: `payment_method` default yoomoney, snapshot `manual_details`, `manual_reported_at`, `manual_decision`, `manual_decided_at`, `manual_actor_id`, `manual_reason`. Manual-состояние хранится в том же заказе; отдельная таблица заявок не нужна. Constraints/immutable trigger запрещают смешанные методы, смену snapshot/заявления и изменение терминального решения. Down блокируется при наличии manual-заказов, финансовая история не удаляется.

`purchase_receipts` сохраняет прежнюю структуру. Manual operation ID имеет серверный формат `manual:<order UUID>`, источник `manual_confirmation`, gross=net=проверенная сумма, currency=643. Существующий funding check принимает его только вместе с immutable approved-решением, actor и соответствующим временем заявления/решения. Одной вставки receipt или подписанного YooMoney callback недостаточно. Подписанный YooMoney-перевод с меткой manual-заказа сохраняется как спорный receipt/needs_review, без автоматической выдачи и без потери факта.

Публичный `PurchaseOrder` получает optional `manual_payment`: state `not_reported|pending|approved|rejected`, instructions, can_report, nullable reported_at/decided_at/reason. Прежние YooMoney-ответы не получают новый обязательный ключ. PaymentMethods/enums расширяются additive; старые параметры/ответы/статусы не меняются.

Новые public operations payments:
- `ReportManualPayment(ctx, account, order, key uuid.UUID) (PurchaseOrder,error)`.
- `DecideManualPayment(ctx, actor,target,order,key uuid.UUID, in ManualPaymentDecisionInput) (PurchaseOrder,error)`; input decision approve/reject, reason, optional confirmed_amount_minor, обязательная только для approve.
- `ManualPaymentRequests(ctx, actor uuid.UUID, after *uuid.UUID) (ManualPaymentPage,error)`; items account_id/order, has_more и nullable next_cursor. По 50, ASC(reported_at,id), cursor UUID последней строки; reported_at immutable. Только текущий оператор, пустой список `[]`.

HTTP: `POST /api/v1/orders/{id}/manual-report` (`{}`, Idempotency-Key, 200); `POST /api/v1/operator/clients/{id}/orders/{order_id}/manual-decision` (200 reject/202 approve); `GET /api/v1/operator/manual-payments?after=<UUID>` (200). Стандартные 400/401/403/404/409/503 и прежние error codes. OpenAPI — авторский источник, Go/TS только штатной генерацией. HTTP преобразует DTO; SQL находится только в payments, accounts/audit вызываются через публичные методы.

## Интерфейсы и уведомление

Каталог показывает только включённые способы. Страница заказа показывает безопасный plain-text snapshot и кнопку «Я оплатил», затем ожидание/отказ/paid/applied и причину. После заявления истечение исходного окна не превращает ожидание оператора в «оплата истекла». Persisted результат доступен после reload и объявляется role=status; автоматическое чтение ограничено, ручное обновление остаётся.

React-admin получает отдельный список заявок со ссылкой на карточку клиента; существующий OperatorPurchase показывает управление заявкой. Ru/en, 375px, keyboard, labels, aria-busy/error/empty state обязательны. Решение и его причина — устойчивое уведомление внутри кабинета. Новые способы доставки клиенту без Telegram относятся к С27/С28 по принятому роадмапу, Telegram-интерфейс этих же операций — к С32. С11 не заменяет это отдельной массовой рассылкой или зависимостью оплаты от доступности Telegram.

## Приёмка

- AC01: opt-in, FILE/inline conflict/empty/invalid details, snapshot после изменения настройки, отсутствие реквизитов у другого клиента и в логах; disabled новые заказы не появляются, старые заявки доступны.
- AC02: серверная сумма/quote/replay, одна active-заявка; report/repeat без paid/receipt/выдачи, expiration до report/ожидание после report, запрет cancel/new order после report.
- AC03: approve только оператором после report и точной суммы; reject с причиной, отказ терминален и новый заказ разрешён. Неподтверждённый/restricted/client/чужой target/CSRF/Origin/malformed/oversize не дают изменения.
- AC04: approve/approve и approve/reject одновременно, разные/повторные ключи, потеря ответа — один terminal decision/receipt/job/доступ. Audit/queue failure откатывает всю Tx. Role change проверяется до replay.
- AC05: подписанный YooMoney event не подтверждает manual; конфликт сохраняет receipt и needs_review. Funding check не принимает forged/manual-only receipt; прежние YooMoney regressions сохраняются.
- AC06: общий worker выдаёт новый доступ и переход с триала с прежними IDs; panel outage/restart сохраняет paid и одну выдачу; позднее решение не выдаёт повторно, backup/restore сохраняет actor/claim/receipt и незавершённое paid.
- AC07: actual HTTP и browser CRUD/очередь/статусы/poll/reload/ошибки/keyboard/ru-en; actual локальная3X-UI3.7.0/TLS и teardown; generation и границы модулей стабильны, прежние миграции1–15/зависимости не меняются.

Native coordinator: domain/API → web → own local acceptance; один Astra/high final review, один Critical/Important fix pass с RED→GREEN без re-review, minor deferred. Exact-source required CI → manual v2 merge → ранее согласованный preview/3multiarch. Production, реальный ручной перевод, Telegram-delivery и Happ/VPN/trust исключены; С13 остаётся отдельной локальной приёмкой с заглушками по [уточнению владельца](https://github.com/ekho/3xui-shop/issues/18#issuecomment-6014628265); real provider delivery не заявляется.
