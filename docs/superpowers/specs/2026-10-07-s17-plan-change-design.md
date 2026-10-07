# С17 — клиент меняет тариф

Дата: 2026-10-07. Владелец сценария [#24](https://github.com/ekho/3xui-shop/issues/24).
Автономные документы, Native и последовательное выполнение/слияние разрешены
владельцем. [Общий контракт](https://github.com/ekho/3xui-shop/issues/24#issuecomment-6027840665):
`2026-10-07-s17-plan-change-v1`. База — свежая origin/v2 после С16/PR77.

## Результат и правила

Клиент выбирает видимый конечный тариф, оплачивает его включённым внешним
способом и получает новый период/устройства/трафик/профиль. Старый срок
заменяется, остаток дней не переносится и не пересчитывается. Подготовка
неизменяемого target фиксирует момент один раз: now + period_days * 86400000.
Повтор платежа, задания, восстановления или потерянного ответа не выбирает
новый now, не добавляет дни и не повторяет сброс. UUID, sub_id, panel_key и
назначенный сервер сохраняются; трафик сбрасывается один раз.

Alignment — [роадмап](../../roadmaps/2026-10-01-platform-roadmap.md),
[#24](https://github.com/ekho/3xui-shop/issues/24),
[архитектура v1](2026-10-05-modular-monolith-design.md),
[С09](2026-10-02-s09-catalogue-design.md),
[С16 v2](2026-10-07-s16-renewal-design.md): confirmed.
`app/bot/services/vpn.py:change_subscription/update_client` использует
replace_duration=True и текущий момент; `subscription_handler.py` предлагает
все purchasable планы, включая текущий. RU/EN payment confirmation предупреждает
об отсутствии перерасчёта. Эти правила сохраняются; новой экономики нет.

- Подтверждённый unrestricted web-клиент имеет собственную применённую выдачу
  конечного regular/euru тарифа. Trial-only использует С10. Banned/unlimited,
  чужой сервер, отсутствующий клиент, несогласованные UUID/группы и неизвестное
  отключение отказываются. Истёкший или доказанно исчерпавший положительный
  лимит доступ можно включить. Новая оплата не отменяет поздний ban/restriction.
- Цель — текущая revision видимого неархивного regular/euru плана с положительной
  ценой нужного периода/валюты. Тот же видимый тариф допустим, как в боте.
  Собственный исходный hidden/archived тариф не мешает переходу на другой
  действующий видимый; hidden/archived/unlimited новая цель недоступна.
- Исходный выбор определяется применённой access operation. Фиксируется её
  UUID, не только plan UUID: повторное назначение того же тарифа тоже меняет
  источник. Компенсация/reset/ban сохраняют выбор; starter_trial/возврат с
  unlimited обнуляют. Отсутствие истории не доказывает оплаченный тариф.
- Заказ фиксирует цену/валюту/лимиты/профиль/revision и источник. Поздняя правка
  или архивация каталога не меняет оплаченный снимок. Поздняя смена назначения,
  даже на тот же plan UUID, прекращает checkout/выдачу; полученные деньги
  остаются needs_review. Оператор не повторяет подготовку, пока источник неверен.
- Один активный заказ и существующие account/access locks сохраняются.
  Применённые платежи не мешают renew/change_plan; unresolved paid/review/access
  блокируют. Read текущего заказа сохраняет приоритет review → unresolved paid
  → active pending → applied history. Деньги и выдача остаются разными состояниями.

## Общий платёжный путь и модули

Используем существующие orders/receipts, неизменяемый quote/funding, River
purchase_fulfillment и VPN AccessWorker. Физический kind остаётся purchase;
action различает purchase, renew, change_plan. Нового исполнителя/таблицы/SDK
нет. subscriptions владеет eligibility; VPN — provenance/target/native writes;
payments — заказом, proof и всеми денежными/checkout/recovery guards.

С17 также завершает оставшуюся после первого YooMoney выпуска С16 поддержку
методов продления. Для renew/change_plan работают уже доставленные YooMoney
AC/PC, manual, YooKassa в RUB; Cryptomus/Heleket в USD. Используются действующие
enabled flags; выключенный метод не включается. Provider identity, подпись,
authenticated status, exact principal, manual operator proof и unknown-net
правила не меняются. Расчёт renew остаётся max(expiry,now)+days.

До С35 независимый внешний billing доказан только у новых web-аккаунтов без
Telegram/legacy ID. Остальные, включая созданный раньше заказ, закрываются
EXTERNAL_BILLING_UNVERIFIED. Нет фиктивного inactive default. С35/#33 принимает
реальные Stars-переходы, reciprocal guards и отсутствие двойного списания;
С31/#29 и импорт/#53 сохраняют реальные billing facts. Их работа не выполнена
этой задачей. Затронуты #17–#23/#25–#27/#29/#33/#53; собственные состояния,
приёмка и зависимости этих задач сохраняются.

## API и совместимость

Авторинг — docs/api/openapi.yaml; oapi-codegen/sqlc/openapi-typescript.
GET /api/v1/subscription/plan-change возвращает PlanChangeContext:
current_plan_id и source_access_operation_id. Session/401, restriction/403,
PLAN_CHANGE_NOT_ELIGIBLE или billing unknown/409, dependency/503.
Каталог видимых предложений читается существующим GET catalogue.

POST orders добавляет action=change_plan и source_access_operation_id.
Поле обязательно и не может быть nil UUID только для change_plan; для остальных
действий отсутствует. Оно сохраняется в quote с omitempty. Сервер проверяет
живой источник при preflight/final create, checkout, всех пяти funding paths,
preparation/reconcile и каждой физической записи/final Tx. Аккаунт удерживается
одним владельцем во время чтения панели; сеть не выполняется внутри SQL Tx.
Origin/CSRF/body limit/Idempotency-Key/foreign-order guards сохраняются.

Миграция21 расширяет action CHECK, сохраняя trigger неизменяемости; Down
отказывается при любой change_plan истории. Миграции15–20 не редактируются.
Старые purchase/renew JSON bytes/body hashes/quotes/receipts/targets/job kinds
сохраняются: новое nullable Go pointer поле опускается, а старые публичные
PlanID чтения остаются совместимыми. Backup сохраняет action/source/proofs/IDs;
старое приложение после change_plan требует совместимого читателя, историю
нельзя удалить ради rollback.

## Кабинет и проверяемая приёмка

/cabinet/change-plan переиспользует Catalogue с одним action mode.
Контекст источника и каталог читаются до выбора; данные после потерянного POST
сохраняются вместе с key/body при повторе и смене локали. Обновление предложений
явно отменяет попытку и требует нового подтверждения. Перед заказом видимо:
«Оставшиеся дни не переносятся. Новый срок начинается при смене тарифа;
счётчики трафика сбрасываются». Заказ/оператор показывают «Смена тарифа».
Fresh GET перед оплатой закрывает форму при запрете/foreign/purpose mismatch.
RU/EN, 375px, native radio/select, keyboard/focus/labels, empty/error/retry
обязательны; VPN-ключей, credentials или Stars charge в DTO нет.

1. Connected HTTP/модули/DB/Redis: active/expired/exhausted смена, видимый тот же
   план, regular↔euru, собственный hidden/archived источник; новый срок от now,
   quote limits/profile, один reset и постоянные IDs/server. Все пять методов
   для обоих действий; неверный proof не выдаёт доступ. Старые покупки сохраняются.
2. Stale/foreign/cleared source и повторное назначение того же UUID, late ban/
   restriction/billing/native identity/unknown disable, pending/review conflicts;
   неизменяемые деньги, source/quote/action, один target и безопасный reconcile.
3. Потерянный ответ/concurrent key/callback/jobs, restored prepared target и
   native readback/restart не начисляют повторно. Migration Down guard сохраняет
   историю, old purchase/renew byte/hash replay сохраняется.
4. Rendered RU/EN/mobile/keyboard: предупреждение, все методы/валюты, frozen
   source/key/body, locale/reload, errors/disabled methods/current order и fresh
   GET 403/404/foreign/purpose/source mismatch до перехода к провайдеру.
5. Собственный localhost cabinet-c17, native3X-UI3.7.0 и TLS Mailpit, подписанные
   синтетические события; READ ONLY restore без restored writer. Полные Go race/
   web/Python проверки, один fresh Astra/high review и один fix pass, current CI,
   manual exact-source merge→v2 и actual preview/tag/3indexes/6labels до Done.

Реальные деньги, публичный callback, production, живой Happ/VPN, Mac trust и
внешние SMTP/Telegram не входят; внешняя готовность остаётся С45–С47.
Новые ветки от origin/v2, без codex/, PR только v2. Один Go-процесс и один
активный владелец операций; допущено окно обслуживания.
