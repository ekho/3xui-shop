# С12 — первая покупка через YooKassa

Владелец: [#20](https://github.com/ekho/3xui-shop/issues/20). Контракт `2026-10-06-s12-yookassa-v1` в рамках `2026-10-05-modular-monolith-v1`. Документы и Native-реализация выполняются автономно по [поручению владельца](https://github.com/ekho/3xui-shop/issues/55#issuecomment-6004574101). Предпосылки С10/#17 и М05/#59 доставлены. С13/#18 доставлена отдельно PR73/dev.35.

## Результат и границы

Подтверждённый web-клиент выбирает включённую YooKassa, получает серверный заказ и переходит на страницу оплаты. Только результат авторизованного запроса к API YooKassa подтверждает деньги. Существующие purchase/access workers выдают новый доступ или переводят действующий триал с сохранением ID. Telegram не требуется.

Локальная приёмка использует собственную заглушку API и 3X-UI **3.7.0**. Закрытие #20 означает локальную реализацию, приёмку и доставку в v2. Настоящий магазин, фискальная настройка, публичная доставка уведомлений и production проверяются отдельно до С45–С47; чужие или реальные деньги не используются. Продление/смена тарифа, возвраты/споры, MiniApp, legacy-import и удаление Python остаются своими сценариями. Promos/referrals — Р7. Live Happ/VPN/macOS trust не меняются.

## Основной путь

1. Существующий каталог/права/серверная цена/30-minute quote → `payment_method=yookassa`, `payment_type=YOOKASSA`. Одна активная первая покупка; прежняя idempotency/body hash сохраняется.
2. В одной SQL-транзакции сохраняются заказ, provider-owned checkout с неизменными shop/test/request и задание River в очереди `payments`. Внешний HTTP не выполняется внутри SQL-транзакции. Сначала экран сообщает подготовку; повтор создания возвращает тот же заказ.
3. Worker отправляет фиксированный HTTPS POST `/v3/payments`, Basic Auth shop/token, `Idempotence-Key=order UUID`: RUB/точная decimal-сумма, `capture=true`, `save_payment_method=false`, redirect на свой `/orders/{id}`, metadata order_id. Текущие receipt-параметры сохраняются: `SHOP_EMAIL`, одна позиция, quantity=1, vat_code=1. Бренд/домен/почта задаются при деплое.
4. Provider payment ID и HTTPS confirmation URL фиксируются. Checkout доступен только своему клиенту при актуальных pending/active/expiry/method/не-review. Перед переходом UI повторно читает свой заказ и сверяет цену/ID/состояние/URL. Браузерный возврат только читает статус, никаких денег или доступа.
5. Worker периодически GET-проверяет известный payment ID. Уведомление тоже вызывает авторизованный GET; тело уведомления — только подсказка с ID. Неизвестный ID получает 200 без API-вызова/денег. Уведомление не обязано прийти для завершения покупки.
6. Только `succeeded`, `paid=true`, совпадение ID/shop/test/order/точной суммы/RUB, допустимые created/captured timestamps и отсутствие возврата дают один immutable receipt, funding ID и одно задание существующего PurchaseWorker. Его общий money guard проверяет связь provider checkout → receipt → order во всех prepare/access/reconcile путях. VPN-модуль остаётся единственным исполнителем доступа.

## Достоверность и повторы

Нативный источник webhook — официальный список IP YooKassa; HTTP берёт effective IP из существующего настроенного Echo extractor, никогда напрямую из произвольного X-Forwarded-For. В явно включённом test mode разрешены собственные private/loopback отправители. И в этом режиме money proof всё равно требует авторизованного GET. JSON ≤16 KiB, UTF-8/один документ/валидная schema/type/event/payment UUID; неизвестные поля полного provider object не превращаются в деньги.

HTTP API использует stdlib net/http, фиксированный endpoint, TLS verification, запрет redirects, 10s timeout, bounded response и обезличенные ошибки. API-клиент можно заменить stdlib transport только внутри тестов. Runtime API URL не настраивается.

Crash/timeout/500 не означает, что платежа нет. Запрос, первый attempt и ключ заморожены до POST; повтор отправляет те же bytes/key. При неизвестном ID после 24h повторный POST запрещён, заказ переходит на разбор. Known-ID GET не создаёт платёж. Изменение shop/test конфигурации после создания не переключает заказ на другой магазин. Отключение новых продаж скрывает checkout/новые заказы, но сохранённые credentials продолжают обрабатывать старые платежи.

`pending`/`waiting_for_capture` (даже paid=true) не дают funding. Provider canceled до денег закрывает заказ. Успешная поздняя/отменённая локально/неверная сумма/валюта/merchant/test/metadata/возврат сохраняется для разбора и не создаёт доступ. Повтор settled-факта ничего не повторяет; противоречие с тем же payment ID сохраняет первый факт, отмечает review и блокирует новую подготовку. Уже выданный доступ автоматически не отзывается — разрешение спора/возврата остаётся С19/С20.

`income_amount` у провайдера необязателен. Его отсутствие сохраняется как unknown/NULL, никогда как gross. Старые YooMoney/manual receipts сохраняют non-null net и их прежний proof. Появление ранее неизвестного income не считается противоречием; две известные разные суммы — противоречие. Receipt сохраняет первый факт без перезаписи. Sanitized provider observation сохраняет только финансовую привязку/статус/суммы/время, без card/customer/сырого ответа/секретов.

## Данные, API и конфигурация

- Migration17 расширяет метод и добавляет `yookassa_checkouts` (order FK, immutable shop/test/request, first attempt, unique provider ID, confirmation URL, state, sanitized observation). Nullable net только для receipt с явным YooKassa proof; immutable provider proof и прежние финансовые триггеры сохраняются. Down блокируется при provider history.
- Авторитетный JSON OpenAPI в `docs/api/openapi.yaml`: additive enum `yookassa`/`YOOKASSA`, необязательный `yookassa_checkout: {state: preparing|ready|unavailable, url: string|null}`, POST `/webhooks/yookassa` с operationId `receiveYooKassa`. Старый `checkout` YooMoney/manual DTO/input field order/hash не меняются. Никакого нового create-checkout endpoint или generic gateway abstraction.
- Runtime: прежний `SHOP_PAYMENT_YOOKASSA_ENABLED=false`, `YOOKASSA_SHOP_ID`, `SHOP_EMAIL`; token только `YOOKASSA_TOKEN_FILE`, inline/conflict/empty invalid. `YOOKASSA_TEST_MODE=false` должен совпадать с frozen mode/provider object. Даже disabled-but-configured credentials валидируются для старых уведомлений. Публичный DTO не содержит token/shop/receipt email/provider response.
- Приложение остаётся одним Go-процессом. Модуль payments владеет provider данными/worker; HTTP и app — адаптер/сборка. Queue `payments` с двумя workers отделяет provider latency от provision. Новых Go/npm/Python production dependencies нет.
- UI: ru/en, native radio/button, keyboard/focus/role=status/alert, preparing/error/retry/expired/review. Разрешён только HTTPS checkout на `yoomoney.ru` без userinfo/нестандартного порта; произвольный URL не открывается. Настоящая hosted checkout UI не имитируется как доказательство provider acceptance.

## Приёмка

| AC | Локальное доказательство |
| --- | --- |
| AC01 | Enabled/disabled, verified/owner/quote/replay/restriction, immutable provider request/key и атомарное River enqueue; прежние YooMoney/manual regressions проходят. |
| AC02 | POST/GET на stdlib stub: auth/bytes/key/amount/receipt/return; ambiguous500 и restart повторяют один provider ID; after24h не POST; config drift fail closed; API timeout/redirect/oversize/malformed не funding. |
| AC03 | Actual HTTP: forged-XFF/disallowed source/invalid JSON rejected; unknown ID200/noAPI; forged succeeded body + trusted pending GET не funding; live/test mismatch, missing income, capture/amount/currency/merchant/metadata/refund/time/late/canceled проверены. |
| AC04 | Receipt replay/conflict, nullable-net foreign-method collision, immutable SQL proof и общий prepare/access/reconcile funding guard: один receipt/job/access либо retained review без выдачи. |
| AC05 | Rendered Playwright ru/en/keyboard/preparing→ready/re-read before navigation/invalid URL/expiry/error/review/browser return not paid; YooMoney/manual UI сохраняется. |
| AC06 | Собственный Docker API stub → HTTP/River → native 3X-UI3.7.0: новый доступ, переход с триала без смены ID, replay/restart и paid-pending backup/restore. Source/logs/environment/evidence levels записаны. Никакой реальной PSP доставки не заявлено. |
| AC07 | Один fresh Astra/high whole-branch review; один RED→GREEN fix pass при Critical/Important, minors deferred; exact-source CI, ручной PR→v2, parents/source-equal tree и actual preview/tag/images. Только после этого #20 Done. |

## Первичные источники и решение

Проверены 2026-10-06: [API interaction](https://yookassa.ru/developers/using-api/interaction-format), [webhooks и IP](https://yookassa.ru/developers/using-api/webhooks), [redirect payment](https://yookassa.ru/developers/payment-acceptance/integration-scenarios/smart-payment), [официальный OpenAPI](https://yookassa.ru/developers/api/yookassa-openapi-specification.yaml) (SHA256 `3e411baebe816b880c6438b9fcc75d1606082b830425b4afc96f4acf4f7ece56`). Legacy receipt/flags прочитаны в app/bot/payment_gateways/yookassa.py. Из факта сохранения параметров не следует юридическая/фискальная production готовность.

Рассмотрены synchronous POST (зависимость создания заказа от PSP outage), provider SDK (новая зависимость без нужного преимущества) и существующий durable River + stdlib API. Выбран последний: сохраняет заказ при outage и текущий модульный контракт. Self-review: scope/права/финансовые edge cases/unknown net/API/локальная и внешняя приёмка разделены; placeholders нет. Спецификация принята координатором в рамках автономного поручения, без нового запроса согласования.
