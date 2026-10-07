# С32 — клиентский Telegram-канал в модульном монолите

Статус: выбран в пределах автономного мандата на roadmap v2.
Версия: `2026-10-07-s32-client-bot-v1`; owner #30 / telegram.
База: `f5182de4878c5a688ff218a25cf32d98b40dac6e`.
Предшественники #28 и #55 закрыты; С31/#29 доставлена в v2.

## Цель и объём

Клиент открывает общий кабинет через /start, меню Telegram и прежние
клиентские кнопки. Telegram работает в том же Go-процессе, вызывает
публичные контракты владельцев данных и доставляет клиенту уведомления
о решении/выдаче триала, результате выдачи оплаченного доступа и ответе поддержки.
Общий Mini App из С30/С31 остаётся клиентским интерфейсом. Регистрация требует
согласия с документами; /start не создаёт аккаунт или подписку.

Обычный автоматический Telegram-триал остаётся С33, Stars — С34–С36,
новые кампании/рефералы — С25/С23–С24, forum/media support-прокси — С37,
рассылки/периодические напоминания — С27/С28. Python удаляется только в С47.
В этом сценарии используются собственные локальные Bot API/платёжные fixtures.
Production, реальные Telegram-запуски, деньги, SMTP и живой Happ исключены.

## Выбранный способ

Использовать существующий poller, transport, апрув, Mini App и общие экраны.
Новое командное меню с бизнес-правилами дублировало бы кабинет; отдельный
bot-runtime нарушил бы принятый монолит. Остаются короткие команды и ссылки.

При включённом клиентском канале startup после проверки webhook получает
getMe, сверяет ID с токеном, username и наличие Main Mini App, затем задаёт
общую кнопку меню на /mini-app/cabinet. CABINET_ORIGIN уже принадлежит HTTP
конфигурации; нового домена/названия или публичного proxy Bot API нет.
Отсутствие Main Mini App — диагностируемая ошибка настройки Telegram;
HTTP и задания продолжают жить. Старые operator-only тестовые сборки
могут явно не собирать клиентский канал; production serve собирает его.

## /start и источник

Принимаются личный чат, реальный положительный actor, сообщение с ID/date.
Команды /start, /help, /support (включая адресованный текущему username вариант)
возвращают ru/en кнопки. /start принимает ровно один необязательный параметр:
до 64 ASCII A–Z/a–z/0–9/_/-. Неверный ввод получает безопасный ответ без записи.

Кнопка /start с источником — URL основного Mini App
https://t.me/<username>?startapp=<raw-source>. Telegram передаёт источник
в signed start_param; accounts сохраняет первый непустой источник после
согласия в существующем telegram_start_param. Числовая referral-метка и
кампанийная метка сохраняются буквально; применение бонуса здесь отсутствует.
Повторный вход, добавление email и linking сохраняют существующий источник.
Параметр ссылки не доказывает личность или право на бонус.

Обычные кнопки используют HTTPS web_app.url из текущего CABINET_ORIGIN:
кабинет, поддержка, инструкции, каталог, история. Никаких bearer/proof,
email, VPN-ссылок/ключей или денег в URL и Telegram-тексте.
Клиентские команды не становятся причиной операторского пересмотра:
команда отменяет незавершённый диалог; прежние wt1 callbacks сохраняются.

## Старые кнопки и инвойсы

Только личный callback от реального actor, сообщение текущего бота,
валидный callback ID и payload в пределах Telegram-лимита.
Прежние profile/show_key/download/platform_*, support/how_to_connect/
vpn_not_working, main_menu/start и subscription-навигация открывают
соответствующий защищённый экран. Ключ/QR показывается самим кабинетом.

Владелец payments разбирает текущий nine-part SubscriptionData:
prefix/state/flags/user/devices/duration/traffic/price. Целевой ID 0 либо
реальный actor; неизвестное состояние, неверные числа/flags/другой actor
отклоняются. Нулевые незавершённые параметры навигации допустимы.
pay_* не создаёт платёж, не подтверждает сумму и не выдаёт доступ.

Для точного совпадения invoice snapshot с единственной неизменяемой
legacy_payment_transactions текущего аккаунта payments возвращает source_id.
Ссылка открывает только этот факт через добавочный необязательный
legacy_source_id в существующем PaymentHistoryInput (kind=legacy, без cursor).
Сервер всегда фильтрует account_id; чужой/неизвестный ID даёт пустую страницу.
UI показывает найденную запись, пустое состояние и переход к полной истории.
При нулевой цене старой кнопки, отсутствии или неоднозначности snapshot
открывается полная legacy-история: нельзя выдумывать единственную операцию.
Непонятный исторический payload сохраняется как исходный факт при импорте.

Старые successful_payment/pre_checkout/refunded_payment остаются
UNSUPPORTED_PAYMENT, без подтверждения poll offset и без изменения денег,
до владельцев С34–С36. Это обязательная граница поэтапной передачи;
production cutover с такими событиями до их готовности запрещён.

## Клиентские уведомления

notifications владеет отдельной таблицей client_telegram_deliveries:
UUID, последовательность, account/TG/credential-version/locale, event key,
ограниченный route, state/lease/attempts/message/result. Unique(account,event)
делает повтор enqueue идемпотентным. Доменный владелец ставит событие в той
же транзакции, где фиксирует результат; отсутствующий Telegram не создаёт job.
Операторская telegram_deliveries, её FK, lease и result_hash остаются прежними.

Сообщение содержит фиксированный ru/en текст о наличии обновления и кнопку
в общий экран, без деталей обращения, идентификаторов, суммы или VPN-секретов.
Триал — решение и applied/needs_review; оплата — applied/needs_review;
поддержка — новый ответ оператора, включая ответ только с файлом.
Отправка никогда не является условием выполнения выдачи.

Отдельный client-delivery цикл того же Runtime получает lease на 60 секунд.
Перед send accounts проверяет текущий UUID/TG/version, eligibility, ban,
quarantine и документы. Порядок: Telegram guard → account → delivery.
Одна короткая ограниченная send-попытка (10 секунд) удерживает account и job
в транзакции: unlink/recovery/restriction не могут завершиться во время send.
Транзакция использует одну connection, включая MaxConns=1.
Старое/отозванное назначение становится skipped без сетевого вызова.
Просроченный/чужой lease не отправляется/не завершается.

403/400 фиксируются как terminal failed; 429 сохраняет retry_after.
Сеть/непонятный ответ оставляют pending до lease/retry: Telegram sendMessage
не предоставляет ключ идемпотентности, поэтому возможен повтор обычного
уведомления при потерянном ответе. Дубликат не меняет бизнес-операцию.
Рестарт не теряет pending; выключенный Telegram не мешает HTTP/выдаче.

Новая кнопка cn1:<job UUID> закрывает только доставленное сообщение текущего
получателя с совпадающим message ID. Старые exact close_notification и
redirect_to_download проверяют actor/личный чат/текущего бота и фактическую
кнопку в сообщении, затем выполняют только косметическое удаление/навигацию.
Неизвестные operator callbacks не становятся клиентскими действиями.

## Данные и совместимость

Миграция 00026 добавляет только клиентский outbox; Down запрещён при фактах.
Все UUID, source/legacy/payment/panel IDs, immutable payload и факты остаются.
Раньше существовавшие HTTP paths/DTO/cookies, signed Mini sessions и guards
С31 сохраняются; PaymentHistoryInput только расширяется optional полем.
SQL accounts/payments/support/subscriptions/notifications живёт у владельца.
Composition только собирает сервисы и функции; нового глобального facade нет.
Токен остаётся file-only; транспорт не логирует URL/token/raw updates.

## Приёмка

1. Собственный Bot API подтверждает startup/menu, /start raw source → signed
   Mini login/consent → первый источник/UUID; повтор не перезаписывает его.
2. Личные ru/en команды и старые callbacks ведут в нужный экран; hostile
   actor/chat/bot/payload отклонён, секреты в сообщениях/URL отсутствуют.
3. Exact и ambiguous legacy snapshot, price=0, foreign account и исторические
   огромные значения проверены; фильтр history работает с web и signed Mini,
   без CSRF/Origin/чужого владельца данные не доступны.
4. Реальные доменные решения/выдача/support reply создают ровно один job;
   rollback/replay не создаёт лишний. Owned DB/Redis, текущие миграции,
   TLS 3X-UI 3.7.0 и Mailpit; операторский апрув сохраняется.
5. Lease/unknown result/restart/403/429/отзыв binding/quarantine/ban,
   конкурентный unlink и MaxConns=1 проверены; деньги/VPN не дублируются.
6. Затронутая история в браузере: ru/en, клавиатура, screen reader,
   loading/error/retry/empty, clear/abort и переход к полной истории.
7. Module/SQL boundaries, generation, vet, backend/race, frontend и Python
   регрессии; один fresh whole-branch review и один авторский fix pass.
8. Точный source CI, ручной PR → v2, действительный prerelease и три OCI
   образа amd64/arm64. Это локальная приёмка, не production-утверждение.

## Первичные источники

- [Deep linking](https://core.telegram.org/bots/features#deep-linking):
  формат /start и ограничение payload.
- [Main Mini App](https://core.telegram.org/bots/webapps#launching-mini-apps):
  startapp → signed start_param.
- [Bot API](https://core.telegram.org/bots/api): getMe/has_main_web_app,
  MenuButtonWebApp, InlineKeyboardButton, deleteMessage и ограничения.

