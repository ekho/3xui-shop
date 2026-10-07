# С18 — история заказов и денег

Дата: 2026-10-07. Владелец [#25](https://github.com/ekho/3xui-shop/issues/25).
[Общее решение](https://github.com/ekho/3xui-shop/issues/25#issuecomment-6029754500):
`2026-10-07-s18-payment-history-v1`. Документы, Native, одна свежая проверка
ветки, один проход Critical/Important и последовательное слияние разрешены
владельцем. База — origin/v2 после С17, c827f1944d543624309c93da6e55ad92871efaa7.

## Результат и смысл данных

Клиент открывает /cabinet/history; оператор — историю в карточке клиента.
Три самостоятельные страницы показывают заказы, денежные подтверждения и
архив старого бота. Метод остаётся видимым после выключения или удаления его
настроек; изменение каталога, отсутствие панели и истечение подписки не
скрывают историю. Чтение ничего не оплачивает, не восстанавливает и не выдаёт.

Alignment: [роадмап](../../roadmaps/2026-10-01-platform-roadmap.md),
[#25](https://github.com/ekho/3xui-shop/issues/25), архитектура
[модульного монолита v1](2026-10-05-modular-monolith-design.md), С10–С17:
confirmed. В старом app/bot/utils/user_card.py показаны последние три
транзакции. В app/bot/payment_gateways/_gateway.py pending меняется на
completed до вызова VPN; ошибка выдачи не превращает completed в pending.
Поэтому legacy completed не доказывает выданный доступ или фактический net.

- Заказ: UUID, purchase/renew/change_plan, метод/type, неизменяемый quote,
  payment_status, fulfillment_status, needs_review/review_reason,
  access_operation_id, created_at/expires_at. Цена в quote — цена заказа,
  отдельно от подтверждённой суммы. История не использует publicPurchase:
  checkout URL, инструкции перевода, CanPay и текущая доступность не нужны.
- Подтверждение: operation_id/order_id, метод, created_at/occurred_at,
  gross_minor, nullable net_minor, известная currency либо null + raw_currency,
  provider/operator source, funds_order, needs_review/review_reason,
  codepro/unaccepted. funds_order означает сохранённую связь funding_receipt_id,
  а не новое заключение о возможности выдачи. Все подтверждения, включая
  protected/unaccepted/late/conflicting, остаются читаемыми. Ручное подтверждение
  явно называется решением оператора. YooMoney/manual raw 643 отображается RUB;
  YooKassa — RUB, Cryptomus/Heleket — USD. Неизвестные net и комиссии не считаются.
  Для crypto только сохранённые payment_amount/payer_amount/merchant_amount/
  payer_currency, точные строки без conversion; весь provider JSON не выходит.
- Legacy: source_id как строка, исходные pending/completed/canceled/refunded,
  created_at/updated_at, nullable method/quote, fulfillment_status=unknown.
  Никакого сопоставления completed с modern paid/applied. Payment ID, Telegram
  ID, legacy user ID и packed subscription сохраняются приватно и не входят
  в DTO. Архив не становится новым заказом, receipt, refund или заданием.

## Владелец, права и API

Один Go-процесс HTTP/River/Telegram и один go.mod сохраняются. Модуль payments
читает собственные orders/receipts/legacy archive; аккаунты и роли получает
через публичные accounts операции. Чужих SQL/private пакетов нет. Каталог,
VPN и provider API для чтения истории не вызываются.

Авторинг — docs/api/openapi.yaml, генерация oapi-codegen/sqlc и web
openapi-typescript. Добавляются два read-only POST по существующему шаблону
operator client history: getPaymentHistory POST /api/v1/payment-history;
getOperatorPaymentHistory POST /api/v1/operator/clients/{id}/payment-history.
Session cookie, Origin, CSRF, private/no-store, обычные body limits сохраняются.
Idempotency-Key для чтения не нужен; query parameters и неизвестные поля
запрещены. Существующие operations/DTO/quote hash и migrations 15–21 не меняются.

PaymentHistoryInput: kind=orders|receipts|legacy, nullable before_created_at
и before_id. Курсор либо отсутствует целиком, либо имеет оба значения. Timestamp
не нулевой, год 1–9999, точность до микросекунд. ID: UUID для orders, непустой
bounded operation ID до 128 символов для receipts, положительный int64 в
десятичной строке для legacy. Сортировка created_at DESC, ID DESC; fixed limit
50, дополнительная строка определяет has_more. Последняя показанная запись
задаёт следующий курсор. Нет offset, totals, search или export через HTTP.

PaymentHistoryPage всегда содержит kind, orders[], receipts[],
legacy_transactions[] и has_more; только выбранный массив заполнен. Денежные
minor units и source IDs — строки, исключающие потери JavaScript precision.
UUID и RFC3339 даты валидируются; SQL параметризован. Новые записи между
страницами не дублируют старые; refresh начинает с первой страницы.

PaymentHistory(ctx,account,input) выводит account ID только из сессии,
перепроверяет существование/SourceEligible/restriction. Подтверждённый web
аккаунт читает только себя, включая пустую историю и expired подписку.
Revoked session — 401, restricted — 403; поздний отказ удаляет показанные
данные. OperatorPaymentHistory(ctx,actor,target,input) требует действующего
оператора и существующий target; разрешено читать restricted/Telegram-only
target. Неверный cursor — 400, отсутствие target — 404, чужой/неоператорский
доступ — 403, недоступная БД — 503. Операторские права не кешируются в DTO.

## Промежуточный legacy-архив

Миграция22 создаёт payments-owned legacy_payment_transactions: source_id
bigint PK, account_id FK, source_legacy_user_id/source_tg_id, уникальный
source_payment_id, сырой subscription, status, created_at/updated_at и
imported_at. Index account/created_at/source_id. CHECK ограничивает статус
четырьмя исходными значениями; UPDATE/DELETE запрещены. Down отказывается
при непустом архиве. Raw значения сохраняются без обрезки/нормализации.

Известный payload — девять частей
subscription:state:is_extend:is_change:user_id:devices:duration:traffic:price.
State — один из шести pay_*; flags — 0/1; user_id — 0 или исходный Telegram ID;
devices/duration положительны, traffic неотрицателен. Renew имеет приоритет
над change_plan, как в старом gateway. RUB/USD цена разбирается существующим
точным minorUnits, XTR требует целое число Stars. Никаких floats/rounding.
Неверный, старый, nonfinite, слишком точный или неизвестный payload сохраняется
полностью; method/quote остаются неизвестными, исходный статус/даты видны.

Stdlib Python exporter читает собственную остановленную SQLite-копию через
mode=ro/PRAGMA query_only, users mapping и transactions, ORDER BY source ID.
Явный --timezone обязателен для naive dates; UTC instants сохраняются до µs.
Package version=1, users[] и transactions[], все raw поля/ID/статусы/даты.
Не применяет enabled flags, не загружает приложение/секреты и не пишет SQLite.

server import-legacy-payments --dry-run|--apply читает UTF-8 stdin до 32 MiB,
reject unknown fields/trailing JSON. Положительные source/user IDs и Telegram
ID, уникальные source/payment IDs, существующая однозначная mapping по обеим
идентичностям, валидные dates и raw UTF-8 без NUL проверяются до writes.
Raw payment ID/subscription до 16 KiB: превышение отклоняется целиком, не
обрезается; исходный SQLite VARCHAR не принимается за реальную проверку длины.
Database credentials — только FILE, context limit 2 min; workers/SMTP/TG не
запускаются. Ошибки содержат стабильный code, без raw данных.

Dry-run использует read-only Tx. Apply сортирует account locks и в одной Tx
проверяет mapping через публичные accounts LookupTelegram/LookupTx/Lock,
добавляет новые записи и audit legacy_payment_history_imported system actor
один раз на затронутый аккаунт с числом записей. Точное повторение — ноль writes
и ноль audit. Любой конфликт source ID/payment ID/raw полей/аккаунта откатывает
весь apply. Аккаунты/permissions/orders/receipts/jobs не создаются и не меняются.

Архив представляет один остановленный snapshot перенесённой группы. Более
поздняя отличающаяся версия existing source ID — конфликт, не silent UPDATE.
Промежуточные собственные fixtures не заменяют identity import, полный реальный
snapshot и окончательную репетицию С46. Backup/restore сохраняет raw/archive,
funding и текущую историю; rollback не удаляет финансовые факты.

## Интерфейс и приёмка

Один PaymentHistory компонент используется /cabinet/history и карточкой
React-admin вместо payment-history placeholder. Native select вида истории,
refresh и «Показать ещё»; semantic heading/list/dl/time, labelled controls,
видимые loading/empty/error, retry, ru/en, keyboard/focus и 375px. Смена target/
kind/locale отменяет старый запрос и очищает старую страницу; поздние ответы
не попадают в новую историю. 401/403 очищает данные и передаёт отказ родителю.
BigInt money/displayPrice сохраняет точность. Неизвестные net/currency/legacy
quote и выдача обозначаются прямо. Только собственные modern orders могут
ссылаться на существующий detail; archive не получает payment/refund controls.
Текущие operator purchase actions и отдельный promocodes placeholder сохраняются.

1. Connected cookie HTTP/DB: три вида страниц; старые/new methods/action/quote,
   status оплаты отдельно от queued/applied/review; все receipt facts, nullable
   net, crypto exact strings и rawcurrency. Disabled flags/панель/provider outage
   не мешают чтению. Нет credentials/checkout/packed payload/Stars charges в DTO.
2. Self/foreign/revoked/restricted/nonoperator/target missing, unknown fields/
   query/bad cursor, 51+ tied timestamps и вставка новой строки между pages;
   прежние client audit/trial/approval readers остаются совместимыми.
3. SQLite export → dry-run/apply/replay и конфликтный whole rollback: длинный
   charge ID, unknown/malformed quote, все четыре status, mixed timezone/µs,
   mapping mismatch/missing, invalid UTF-8/oversize/trailing/unknown JSON,
   точные raw/sums/ID/dates и отсутствие order/receipt/job/native writes.
4. Rendered кабинет/оператор ru/en/375px/keyboard: amounts/status/source/unknown,
   empty/error/retry/load more, clear-on-denied и late response при смене target.
5. Собственная localhost БД/заглушки, controlled readonly restore и guarded Down;
   релевантные Go race/web/Python проверки и генерация, одна fresh Astra/high
   проверка всей ветки, один Critical/Important fix pass, current CI,
   ручной exact-source merge в v2 и actual tag/prerelease/3 OCI indexes/6 labels.

Общие потребители #11/#26/#27/#28/#29/#32/#33/#36/#39/#45/#53/#54 изучены и
получили ссылку на версию решения. Их scope/state/dependencies/parent не меняются.
Revenue/Stars/refund/recovery не выводят деньги из quote или successful issue
из legacy completed. Реальные деньги, публичный callback, production, живой
Happ/VPN, Mac trust и внешние SMTP/Telegram не входят; готовность С45–С47 открыта.
Ветки от origin/v2, без codex/, PR в v2, один активный исполнитель операций,
допущено окно обслуживания; название/домен только в конфигурации.
