# С45 — конфигурация продукта при deployment

Issue: [#47](https://github.com/ekho/3xui-shop/issues/47). Owner: `operations`.
Контракт: `2026-10-05-modular-monolith-v1`; база `ad66cfeb` (`origin/v2`).
Предшественники #6/#60/#44/#45/#46 закрыты и входят в эту базу.

## Основной путь и права

Эксплуатационный оператор сохраняет несекретные значения и пути к приватным
файлам в deployment env, проверяет точный Compose project и пересоздаёт только
затронутый backend или frontend. Доступ ОС к deployment/secret files даёт это
право; web/Telegram/Mini App роль не разрешает изменение env или чтение файлов.
Нового settings API, DB-платформы и произвольного shell endpoint нет.
Журнал оператора фиксирует target/revision/исполнителя/причину без значений
секретов по процедуре С44. Изменение env не выдаёт новую подписку или платёж.

Публичные значения (имя продукта, документы, support URL) задаются Caddy при
запуске через существующий `/config.json` с `Cache-Control: no-store`.
Одна web-сборка должна отобразить два deployment набора после recreate/reload.
`PRODUCT_NAME` — необязательный plain text до 128 UTF-8 bytes без управляющих
символов; отсутствие сохраняет прежние ru/en названия кабинета.
Версии документов должны совпадать в frontend/backend; URL не содержат
credentials. Неизвестная/невалидная конфигурация закрывает регистрацию.

## Совместимость и ошибки

- `TELEGRAM_ENABLED=false` запускает HTTP/River без `BOT_TOKEN_FILE`.
  Включение Telegram требует корректный token file и операторов. Нативный
  poller и legacy adapter не могут одновременно исполнять одну операцию.
- Go принимает только secret files: plaintext/conflict/missing/unreadable/empty
  отвергаются безопасными ошибками без значения/пути. Существующая политика
  конфликтов сохраняется. Python до #54 сохраняет env fallback **только когда
  `_FILE` не задан**; явно заданный плохой файл никогда не переключает credentials.
  Heleket/YooKassa используют тот же существующий string-secret reader.
- Trial/bonus, payments/currencies, timezone/retention, panel/subscription URLs,
  SMTP, modules и все используемые файлы сверяются в deployment inventory.
  Реферальные/промо операции остаются в Р7; legacy параметры перечисляются
  для переноса, новые Go-флаги для них не добавляются.
- Сохранённые credentials отключённых payment sales не удаляются: callbacks,
  reconcile, refunds и recurring obligations сохраняют прежних владельцев.
  Currency определяется provider и catalogue, нового глобального currency нет.
- Recreate не меняет DB, ключи MAIL/CODE, аккаунты, panel identities, jobs,
  audit и persisted maintenance. Их секретные значения нельзя перевыпускать
  при обычной смене имени/URL документов. Rollback возвращает прежние
  deployment files/image digest; schema rollback здесь отсутствует.

## Сохранённые эксплуатационные контракты

С42 — persisted maintenance с доступом/audit/replay, без drain/freeze.
С43 — согласованный PostgreSQL/attachments backup и изолированный restore;
Redis/config/secrets/certificates/external state вне пакета. Fixture selection
проверяет оба explicit inputs и запрещает fallback на чужой container.
С44 — один процесс, readiness PG+Redis+River, graceful drain, безопасные module
states и необязательная operational email. Используются их runbooks и тесты.
Миграций нет; чужая reservation39/#43 и его unmerged branch не включаются.

## Приёмка

1. Реальные serve/HTTP/River: Telegram off без token, on без token — отказ;
   restart сохраняет DB facts и readiness. Synthetic fixtures, динамические
   loopback ports, собственные ресурсы, без сообщений Telegram/живой панели.
2. Secret-file precedence и ошибки: Go и legacy Python, empty/unreadable/invalid
   file, conflict, отсутствие значений в диагностике.
3. Runtime shell + реальный Caddy HTTP: два набора public settings на одном
   неизменном web image; JSON/asset bytes, no-store, internal route isolation.
4. Browser ru/en: branding/title, документы, клавиатура, plain text, missing/
   invalid config, сохранённые registration versions; прежние screens/tests.
5. Deployment inventory покрывает все реально читаемые параметры; source,
   generation/static, native regression, независимое review и exact-head
   Platform PR/image gates перед manual merge в `v2`.

Production, внешние providers, пользовательские данные и итоговый cutover
не входят в эту приёмку. Python удаляется только #54.
