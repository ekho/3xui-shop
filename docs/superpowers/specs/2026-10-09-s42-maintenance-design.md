# С42 — Обслуживание общего backend

Дата: 2026-10-09. Owner: `operations`. Задача: [#44](https://github.com/ekho/3xui-shop/issues/44).
Основание: `2026-10-05-modular-monolith-v1` / #55. Мандат этой сессии:
реализация, локальная приёмка, review и готовый PR в `v2`; merge/закрытие/Done
выполняет головная сессия. Production, downtime, release и deploy исключены.

## Результат и основной путь

Действующий web-оператор открывает «Обслуживание», видит текущее состояние,
вводит причину, подтверждает включение либо выключение. Защищённая операция
повторно проверяет права, сохраняет состояние и audit в одной транзакции.
Кабинет, Mini App и Go-бот используют одну запись PostgreSQL, без локального
кэша. Новый экземпляр и рестарт сохраняют режим. Исходное состояние — выключено.

Это ограничение приёма новых клиентских операций, без остановки процесса,
River, Telegram или действующего VPN. Операция, прошедшая проверку до
переключения, может закончиться после него; режим не является drain/DB freeze.
Операторские операции остаются доступны с их существующими проверками прав.
Оператор на клиентском маршруте получает те же ограничения, что клиент.

## Граница чтений и изменений

| Вызов | При включённом обслуживании |
| --- | --- |
| Новый purchase/renew/change-plan через `payments.CreatePurchaseOrder` | 503 `MAINTENANCE`; уже принятый idempotent replay возвращает прежнюю операцию |
| Новая заявка через `subscriptions.CreateTrialRequest`, автоматический TG trial | 503 `MAINTENANCE`; существующие replay/выданный trial не создают новых фактов |
| Новый Stars invoice, Stars resume | 503 `MAINTENANCE`; готовая invoice/принятая control-команда сохраняет replay |
| Вход, согласие Mini App, регистрация, восстановление и защита аккаунта, logout | Доступны; существующие identity/Origin/CSRF/restriction проверки сохраняются |
| Все клиентские чтения: подписка/ключ, каталог, текущий заказ, история, identity, support | Доступны с прежней авторизацией |
| Отмена заказа/Stars, manual-report уже оплаченного заказа, поддержка, preferences/dismiss | Доступны: безопасное продолжение, помощь и остановка списаний |
| Все operator/internal операции, компенсация, reconcile, возвраты | Доступны; прежние роли, replay и audit обязательны |
| YooMoney/YooKassa/Cryptomus/Heleket webhook; Stars pre-checkout/paid/refund/subscription update | Доступны; деньги и их подтверждение не проходят admission gate |
| River fulfillment/recovery/delivery, scheduler/retention/monthly reset | Доступны, без изменения существующих lease/funding/deduplication правил |
| Go `/start`/help/client callback | Понятное сообщение ru/en о режиме с доступом к кабинету/поддержке; операторские callback и money updates сохраняются |

Проверка новых операций находится у owning modules через узкий admission port
к публичной операции `operations`, после поиска replay и перед новым эффектом.
HTTP и Telegram не читают чужие SQL. Нельзя заменить эту границу общей
блокировкой POST/бота: она потеряет уже подтверждённые события и recovery.

## Данные, API, права и повторы

* `operations.Maintenance` — отдельная операция; не заменяет lifecycle/readiness
  из параллельного PR97/#46. Новая миграция `00038_maintenance.sql`:
  00037 зарезервирована PR96/#42, на текущей базе последний номер — 00036.
* Singleton state: `enabled`, `revision`, `changed_at`. Журнал команд сохраняет
  actor, idempotency UUID, вход и результат; SQL принадлежит operations.
* `GET /api/v1/maintenance` — публичный минимальный статус, без actor/reason/PII;
  `GET /api/v1/operator/maintenance` — тот же статус после operator session.
* `POST /api/v1/operator/maintenance` с cookie session, точным Origin, CSRF и
  `Idempotency-Key`. Body: `enabled`, `expected_revision`, `reason` (1–1000
  символов, без NUL), `confirmed: true`. Ответ — status. Mini bearer не даёт
  операторских прав. Права перепроверяются под блокировкой аккаунта в транзакции.
* Смена версии — optimistic conflict 409 `MAINTENANCE_CONFLICT`; повтор того же
  actor/key/body возвращает сохранённый ответ без нового изменения/audit.
  Другой body с тем же ключом — 409 `IDEMPOTENCY_CONFLICT`.
  Старый replay после последующего переключения не включает режим повторно;
  UI перечитывает текущий статус после любого успешного ответа.
* Состояние и audit `maintenance.enabled` / `maintenance.disabled` атомарны.
  Idempotent no-op не меняет revision и не выдаёт ложный transition audit.
  Причина доступна только защищённому audit; значения не пишутся в логи.
* Ошибка чтения режима запрещает только новые клиентские операции; webhook и
  recovery не зависят от этого чтения. Данные, UUID, money proofs и ключи
  сохраняются. Redis не является источником режима.

OpenAPI авторинг — `docs/api/openapi.yaml`, Go/SQL/TS генерируются через
`make -C backend generate` / `npm --prefix web run api:generate`.
Новые endpoints/ошибка additive; существующие DTO не меняются.

## UI и приёмка

Операторский экран: загрузка, ошибка/повтор, конфликт с обновлением статуса,
причина и явное подтверждение, disable при запросе, сохранение idempotency key
при неопределённом ответе, отзыв прав. Клиентский banner отражает статус,
объясняет разрешённые продолжения и выключает новые purchase/trial/Stars-resume.
Проверять свежий статус при переходе/возврате вкладки и ограниченным polling;
серверная проверка обязательна при устаревшем UI. ru/en, keyboard, labels,
status/alert, видимый focus. Ошибка статуса не изображается как «выключено».

Проверки: auth/Origin/CSRF/revoked role, replay/conflict/concurrency/no-op,
restart/two instances, реальное чтение и blocked write web+Mini, новые против
принятых операций, native HTTP/River/Telegram с подтверждённым платежом и
duplicate/restart без второй выдачи, rendered browser сценарий ru/en/keyboard.
Изолированные synthetic PostgreSQL/Redis/Compose/loopback; sandbox/stub Bot API,
payment и panel. Общий cabinet-native и чужие fixtures не трогать.
Полные Go race/vet, canonical generation, web typecheck/build/e2e, Python
regression и текущий CI сохраняются. Локальная приёмка и PR не доказывают
реальный production/Telegram/provider/VPN или возможность безопасного backup.
