# С29 — История действий и хранение аудита

Дата: 2026-10-09. Владелец: `audit_reports`, задача [#39](https://github.com/ekho/3xui-shop/issues/39).
Версия решения: `2026-10-09-s29-audit-history-v1`. Спецификация подготовлена и проверена
агентом в рамках поручения автономно вести остальные документы и реализацию; новое
одобрение пользователя не подменяется этой записью. Реализация и приёмка ещё впереди.
Исполнение Native, один свежий финальный reviewer и один авторский проход исправлений.

## 1. Результат и выбранный подход

Оператор видит общий журнал и историю конкретного клиента в React-admin: действие,
время, подтверждённого исполнителя, клиента, причину и имеющиеся ссылки на операции.
История поддержки содержит метаданные сообщений. Деньги и исполнение доступа
показываются вместе с переходом к уже существующей карточке клиента С18/С20/С08.
Просмотр не создаёт деньги, доступ, сообщения, audit-события или задания.

Используем существующие `audit_events`, `RecordTx`, роли и карточку клиента. Добавляем
журнал старого `audit_log`, системные записи импорта/очистки и ежедневный retention.
Нативный журнал, legacy и системные записи читаются отдельными страницами: у них
разные идентификаторы и смысл. Это сохраняет старые DTO и точные исходные факты.
Единый переписанный журнал потребовал бы менять прежние идентификаторы/контракты;
внешняя система журналирования добавила бы сервис без необходимости для этого сценария.

Существующий monolith-контракт `2026-10-05-modular-monolith-v1` сохраняется.
Новые ветки от `origin/v2`, PR в `v2`. У одного процесса HTTP, задания и Telegram;
SQL каждого модуля остаётся приватным. Новых зависимостей, очереди или шины событий нет.

## 2. Источники и идентичность

- Нативная запись сохраняет существующие поля без переименования: `id`, `account_id`,
  время, action, reason, operator UUID/TG ID, `system_actor` и ссылки request/operation,
  support message/access operation, monthly period. Возврат С20 использует audit ID,
  равный refund ID; `operation_id` остаётся пространством trial operations.
- Неуказанный исполнитель показывается как неизвестный; причина отсутствует, если
  источник её не записал. `false`, `null` и пустая строка не переписываются в новые факты.
- Legacy сохраняет числовые source ID, target TG ID, actor type/ID/name, source, action,
  исходное время и приватный JSON payload. TG/source ID передаются в HTTP десятичным
  текстом, сохраняющим signed int64; они никогда не превращаются в account UUID.
- Связь legacy target с аккаунтом берётся только из неизменяемого
  `accounts.legacy_approval_snapshots.source_tg_id` через публичный порт accounts
  в той же транзакции. Текущая Telegram-привязка и actor ID не доказывают эту связь.
  Неизвестный target остаётся самостоятельной legacy-ссылкой. Последующее появление
  подтверждённой импортной связи меняет только представление, не исходную запись/hash.
- Payload старого журнала хранится приватно под retention, не возвращается в API,
  DOM, экспорт, логи или Telegram. Журнал не показывает даже обрезанные тела DM.
  Актуальные тела/вложения принадлежат support/notifications и доступны в их экранах.

Общий journal глобален для операторов. Клиентский фильтр нативного журнала использует
UUID; legacy-фильтр клиента сначала получает подтверждённый исходный TG ID у accounts.
Без такого доказательства список пуст. Поиск по отдельному legacy TG ID доступен
только на вкладке legacy; он не является поиском аккаунта.

## 3. HTTP и интерфейс

Добавочный `POST /api/v1/operator/audit/history`, operationId `readOperatorAuditHistory`.
Вход: kind `native|legacy|system`, optional `account_id`, optional `legacy_target_tg_id`,
optional paired `before_created_at` + UUID `before_id` либо legacy `before_source_id`.
`system` не принимает клиентский фильтр; TG-фильтр только legacy и несовместим с UUID.
UUID — canonical lowercase, ненулевые; даты валидные, источник/target — positive int64.
Неверные/смешанные/непарные курсоры и чужие поля дают 400.

Ответ `version=audit-history-v1`, kind, неизменённый выбранный фильтр, три массива
`native_events`, `legacy_events`, `system_events` (невыбранные пусты), `has_more`.
Нативная строка содержит `account_id` и прежний `OperatorAuditEvent` как вложенный event.
Legacy возвращает только исходные метаданные и nullable подтверждённый `account_id`.
Системная строка содержит ID/время/action и фактические counts/cutoff/retention period.
Порядок убывающий по времени и ID, page size 50, выборка 51 для `has_more`.

Требуются web session cookie, same-Origin и CSRF, действующая операторская роль.
Actor берётся из сессии. Mini App bearer и legacy adapter token не дают доступ.
Проверка роли/ограничения и чтение выполняются через caller-Tx порт accounts,
используя существующий `LockNoticeOperatorTx`; второй connection не заимствуется.
Неизвестный UUID даёт 404, отсутствие прав — 401/403, безопасная недоступность — 503.
Существующие client history/card и их DTO сохраняются без изменения.

React-admin получает раздел журнала и тот же компонент с account filter в карточке
клиента. RU/EN, semantic headings, подписанные фильтры, клавиатура, статус загрузки,
пустой список, retry и фокус ошибки. Страница/результат/курсор принадлежат текущим
kind, фильтру, языку и аккаунту; старый запрос отменяется. Отзыв прав очищает данные
по существующему операторскому механизму. Причины и имена отображаются обычным React
текстом; HTML, ссылки из payload и `innerHTML` не используются.

## 4. Импорт и сроки хранения

`server import-legacy-audit --dry-run|--apply` читает пакет version 1 из stdin,
`events` обязателен и может быть пустым. Поля строки соответствуют исходному audit_log,
payload передаётся nullable `payload_json` с точными UTF-8 JSON bytes внутри строки.
Используется текущий строгий decoder: весь пакет до 32 MiB, неизвестные поля/остаток,
некорректный UTF-8/UTF-16 и null array запрещены. Строки/идентификаторы/даты проверяются;
payload — JSON object до 64 KiB, action до 128, actor/source до 256 символов.
Время сохраняется с точностью PostgreSQL microsecond; потеря точности отклоняется.

Один пакет атомарен. Повтор source ID с теми же исходными данными идемпотентен,
с другими данными — 409; дубликаты ID в пакете запрещены. Отдельный минимальный
ledger source ID + SHA-256 хранится после retention, без исходных тел/имён/TG ID.
Повтор старого пакета не восстанавливает уже удалённые строки. Dry run не пишет ни
строки, ни ledger, ни системный факт; CLI выводит только counts и безопасные error codes,
не запускает HTTP, Telegram, River или панельные операции. Full import остаётся С46.

`AUDIT_RETENTION_DAYS` default 365, диапазон 1..3650. `AUDIT_RETENTION_TIMEZONE`
задаёт расписание; при отсутствии сохраняется совместимость с `BOT_TIMEZONE`, затем UTC.
Ежедневно после 03:30 местного времени встроенный scheduler очищает свои native/legacy/
system audit rows строго старше UTC cutoff `database_now - retention_days`.
Проверка срока и receipt основаны на времени БД. Обработка пропущенного запуска —
одна очистка текущего дня; прошлые дни не догоняются отдельными заданиями.

Одна транзакция удаляет только данные audit owner и сохраняет системный receipt
с локальным днём, UTC cutoff, параметрами и точными удалёнными counts. Уникальный
дневной ключ и сериализация операции исключают повтор очистки в тот же день после
рестарта. Изменение срока/таймзоны в течение уже обработанного дня действует со
следующего дня. Приватные legacy payload удаляются вместе со строкой; replay ledger
сохраняется. Orders/receipts/refunds, VPN proof/keys/grants, support bodies, notices,
identity/import proof и другие данные остаются в своих владельцах.

## 5. Необязательное зеркало в General

`AUDIT_MIRROR_ENABLED` default false. Включение требует отдельные
`SUPPORT_BOT_TOKEN_FILE` и отрицательный `SUPPORT_GROUP_ID`; main Telegram может
оставаться отключённым. Транспорт реализует Telegram-модуль и передаётся audit owner
через одну функцию. Он не получает входящие обновления support-бота — это С37.

Зеркало best effort: после commit audit owner атомарно помечает одну новую native/
system запись attempted, завершает SQL-транзакцию и делает один вызов Telegram.
Рестарт, timeout, 429 и неопределённый результат не повторяют эту попытку. На старте
pending старых запусков помечается пропущенным, чтобы смена адресата/включение не
рассылали старый журнал. Пропуск зеркала не теряет исходный audit факт. Частота
до одной попытки в секунду достаточна для первого выпуска; доставка не гарантируется.
Legacy payload/история не зеркалируются.

Текст содержит action, IDs, время и подтверждённые actor/target/operation references;
reason, имена, email, тела, payload и ключи не включаются. Plain text, без preview,
без `message_thread_id`, только заданная супергруппа General. ACK проверяет адресата
и message ID. Таймаут/безопасная ошибка остаются внутри scheduler и не останавливают
кабинет; безопасные health-данные процесса расширяет С44.
Основание: [Telegram Bot API sendMessage](https://core.telegram.org/bots/api#sendmessage)
и [General forum topic](https://core.telegram.org/api/forum).

## 6. Приёмка и границы доставки

1. Реальный HTTP/native UI: оператор читает все три источника, фильтр/курсор/50+1,
   same-time tie, empty/error/retry, неизвестный actor, точные большие TG/source IDs.
2. Непривилегированный/restricted/revoked actor, bearer, Origin/CSRF и invalid input
   не читают журнал. MaxConns=1 не зависает; приватные payload не попадают в ответ/DOM.
3. Native metadata соответствует подтверждённым деньгам/возврату/access/support/notice
   фактам; их IDs, статусы, суммы, keys/grants, jobs и старые DTO не меняются при чтении.
4. CLI: dry-run/apply/replay/conflict/атомарный отказ; source snapshots сохранены;
   unlink/rebind не переназначает legacy target. После prune replay не воскрешает запись.
5. Retention: before/equal/after cutoff, timezone/day/restart, точные counts, свой private
   payload удалён; чужие финансовые/панельные/identity/support/notification данные сохранены.
6. Fake Bot API: post-commit only, General metadata, no body/thread/secret, negative
   group guard, unknown/429/no retry, disabled main/support, shutdown и restart.
7. Additive migration up/down пустой БД, защищённый downgrade при сохранённой истории,
   generators, boundaries, static/build/runtime, Go/Python/web regression и native Docker.
8. Свежий финальный reviewer Astra/high, один author correction pass; exact-source CI,
   ручной source-SHA merge, annotated prerelease и три multiarch images.

Все проверки выполняются на owned local fixtures, реальном локальном TLS SMTP и
3X-UI **3.7.0**. Production, реальный support Telegram, внешняя почта, кошелёк,
публичный cutover и живой Happ не входят в эту приёмку. С13/#18 уже CLOSED для
реализации; внешняя денежная приёмка остаётся pending. С37/#40, С46/#53 и С47/#54
сохраняют свои критерии, IDs/topics и необходимость отдельно разрешённого production.
